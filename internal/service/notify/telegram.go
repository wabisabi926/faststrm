// Package notify 提供 Telegram Bot API HTTP 客户端、长轮询管理器、命令处理器及统一通知分发器。
// 对齐 frontend/src/lib/telegram.ts 的 API 行为。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// BotInfo Telegram getMe 返回的机器人信息
type BotInfo struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// WebhookInfo Telegram getWebhookInfo 返回的 webhook 状态
type WebhookInfo struct {
	URL                 string `json:"url"`
	PendingUpdateCount int    `json:"pending_update_count"`
	LastErrorDate      int64  `json:"last_error_date"`
	LastErrorMessage   string `json:"last_error_message"`
}

// User 表示 Telegram 用户
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat 表示 Telegram 聊天
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message 表示一条 Telegram 消息
type Message struct {
	MessageID int64 `json:"message_id"`
	From      *User `json:"from,omitempty"`
	Chat      *Chat `json:"chat,omitempty"`
	Date      int64 `json:"date,omitempty"`
	Text      string `json:"text,omitempty"`
}

// CallbackQuery 表示回调查询
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from,omitempty"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update 表示一个 Telegram update
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// BotCommand 表示一个 Bot 菜单命令
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// InlineKeyboardButton 表示内联键盘按钮
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// apiResponse Telegram API 通用响应
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int            `json:"error_code,omitempty"`
	Description string         `json:"description,omitempty"`
}

// TelegramBot Telegram Bot API HTTP 客户端
type TelegramBot struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegramBot 创建 TelegramBot 实例
func NewTelegramBot(botToken, chatID string) *TelegramBot {
	return &TelegramBot{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// baseURL 返回 Telegram API 基础 URL
func (b *TelegramBot) baseURL() string {
	return "https://api.telegram.org/bot" + b.botToken
}

// doJSON 发送 JSON POST 请求并解析响应
func (b *TelegramBot) doJSON(ctx context.Context, method string, body any) (apiResponse, error) {
	var resp apiResponse
	if body == nil {
		body = map[string]any{}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return resp, fmt.Errorf("marshal request body: %w", err)
	}
	endpoint := b.baseURL() + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return resp, fmt.Errorf("build request %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return b.doRequest(req, method, &resp)
}

// doGET 发送 GET 请求并解析响应
func (b *TelegramBot) doGET(ctx context.Context, method string, query url.Values) (apiResponse, error) {
	var resp apiResponse
	endpoint := b.baseURL() + "/" + method
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return resp, fmt.Errorf("build request %s: %w", method, err)
	}
	return b.doRequest(req, method, &resp)
}

// doRequest 执行 HTTP 请求并解析 Telegram API 响应
func (b *TelegramBot) doRequest(req *http.Request, method string, resp *apiResponse) (apiResponse, error) {
	httpResp, err := b.client.Do(req)
	if err != nil {
		return *resp, fmt.Errorf("telegram api %s: %w", method, err)
	}
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return *resp, fmt.Errorf("read response %s: %w", method, err)
	}
	if err := json.Unmarshal(body, resp); err != nil {
		return *resp, fmt.Errorf("telegram api %s: invalid json: %w", method, err)
	}
	if !resp.OK {
		return *resp, fmt.Errorf("telegram api %s error: code=%d description=%s", method, resp.ErrorCode, resp.Description)
	}
	return *resp, nil
}

// GetMe 获取机器人信息（GET /getMe）
func (b *TelegramBot) GetMe(ctx context.Context) (*BotInfo, error) {
	resp, err := b.doGET(ctx, "getMe", nil)
	if err != nil {
		return nil, err
	}
	var info BotInfo
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		return nil, fmt.Errorf("getMe unmarshal: %w", err)
	}
	return &info, nil
}

// SendMessage 发送文本消息（POST /sendMessage）
func (b *TelegramBot) SendMessage(ctx context.Context, chatID, text, parseMode string) error {
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	if _, err := b.doJSON(ctx, "sendMessage", body); err != nil {
		return err
	}
	logger.S().Infof("[Telegram] Message sent to chat %s", chatID)
	return nil
}

// SendNotification 使用配置的 chatID 发送通知（便捷封装）
func (b *TelegramBot) SendNotification(ctx context.Context, text string) error {
	if b.chatID == "" {
		return fmt.Errorf("chat id is required for sending notifications")
	}
	return b.SendMessage(ctx, b.chatID, text, "HTML")
}

// SendPhoto 通过 URL 发送图片（POST /sendPhoto，photo 字段为 URL）
func (b *TelegramBot) SendPhoto(ctx context.Context, chatID, caption, photoURL string) error {
	body := map[string]any{
		"chat_id": chatID,
		"photo":   photoURL,
	}
	if caption != "" {
		body["caption"]     = caption
		body["parse_mode"]  = "HTML"
	}
	if _, err := b.doJSON(ctx, "sendPhoto", body); err != nil {
		return err
	}
	return nil
}

// SendPhotoFromFile 通过 multipart 上传本地文件发送图片
// 适配内网 Emby 海报无法被 TG 服务器直接访问的场景
func (b *TelegramBot) SendPhotoFromFile(ctx context.Context, chatID, caption, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file %s: %w", filePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("chat_id", chatID); err != nil {
		return fmt.Errorf("write chat_id field: %w", err)
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return fmt.Errorf("write caption field: %w", err)
		}
		if err := writer.WriteField("parse_mode", "HTML"); err != nil {
			return fmt.Errorf("write parse_mode field: %w", err)
		}
	}
	part, err := writer.CreateFormFile("photo", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy file content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	endpoint := b.baseURL() + "/sendPhoto"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return fmt.Errorf("build sendPhoto request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	var resp apiResponse
	if _, err := b.doRequest(req, "sendPhoto", &resp); err != nil {
		return err
	}
	return nil
}

// SetWebhook 设置 webhook（POST /setWebhook）
func (b *TelegramBot) SetWebhook(ctx context.Context, webhookURL, secretToken string) error {
	body := map[string]any{
		"url": webhookURL,
	}
	if secretToken != "" {
		body["secret_token"] = secretToken
	}
	if _, err := b.doJSON(ctx, "setWebhook", body); err != nil {
		return err
	}
	return nil
}

// DeleteWebhook 删除 webhook（POST /deleteWebhook）
func (b *TelegramBot) DeleteWebhook(ctx context.Context) error {
	if _, err := b.doJSON(ctx, "deleteWebhook", nil); err != nil {
		return err
	}
	return nil
}

// GetWebhookInfo 获取 webhook 信息（GET /getWebhookInfo）
func (b *TelegramBot) GetWebhookInfo(ctx context.Context) (*WebhookInfo, error) {
	resp, err := b.doGET(ctx, "getWebhookInfo", nil)
	if err != nil {
		return nil, err
	}
	var info WebhookInfo
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		return nil, fmt.Errorf("getWebhookInfo unmarshal: %w", err)
	}
	return &info, nil
}

// GetUpdates 长轮询获取更新（GET /getUpdates）
// timeout 为长轮询秒数，方法内部会附加 5s 缓冲作为 HTTP 客户端超时
func (b *TelegramBot) GetUpdates(ctx context.Context, offset int64, limit int, timeout int) ([]Update, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if timeout > 0 {
		q.Set("timeout", strconv.Itoa(timeout))
	}

	pollTimeout := time.Duration(timeout+5) * time.Second
	if pollTimeout < 5*time.Second {
		pollTimeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	resp, err := b.doGET(reqCtx, "getUpdates", q)
	if err != nil {
		return nil, err
	}
	var updates []Update
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return []Update{}, nil
	}
	if err := json.Unmarshal(resp.Result, &updates); err != nil {
		return nil, fmt.Errorf("getUpdates unmarshal: %w", err)
	}
	return updates, nil
}

// SetMyCommands 设置 Bot 菜单命令（POST /setMyCommands，scope=all_private_chats）
func (b *TelegramBot) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	body := map[string]any{
		"commands": commands,
		"scope":    map[string]string{"type": "all_private_chats"},
	}
	if _, err := b.doJSON(ctx, "setMyCommands", body); err != nil {
		return err
	}
	logger.S().Info("[Telegram] Bot menu commands set successfully")
	return nil
}

// DeleteMyCommands 删除 Bot 菜单命令（POST /deleteMyCommands）
func (b *TelegramBot) DeleteMyCommands(ctx context.Context) error {
	if _, err := b.doJSON(ctx, "deleteMyCommands", nil); err != nil {
		return err
	}
	return nil
}

// AnswerCallbackQuery 回答回调查询（POST /answerCallbackQuery）
func (b *TelegramBot) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	body := map[string]any{
		"callback_query_id": callbackID,
	}
	if text != "" {
		body["text"] = text
	}
	if _, err := b.doJSON(ctx, "answerCallbackQuery", body); err != nil {
		return err
	}
	return nil
}

// EditMessageText 编辑已发送消息文本（POST /editMessageText）
func (b *TelegramBot) EditMessageText(ctx context.Context, chatID string, messageID int64, text, parseMode string) error {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	if _, err := b.doJSON(ctx, "editMessageText", body); err != nil {
		return err
	}
	return nil
}

// SendMessageWithButtons 发送带内联键盘按钮的消息（POST /sendMessage，带 reply_markup）
func (b *TelegramBot) SendMessageWithButtons(ctx context.Context, chatID string, text string, buttons [][]InlineKeyboardButton) error {
	body := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
		"reply_markup": map[string]any{
			"inline_keyboard": buttons,
		},
	}
	if _, err := b.doJSON(ctx, "sendMessage", body); err != nil {
		return err
	}
	return nil
}
