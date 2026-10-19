// Package notify 提供 Telegram Bot API 客户端（基于 go-telegram-bot-api/v5）、
// 长轮询管理器、命令处理器及统一通知分发器。
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/net/proxy"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 兼容旧类型（外部仍在使用） ====================

type BotInfo = tgbotapi.User
type User = tgbotapi.User
type Chat = tgbotapi.Chat
type Message = tgbotapi.Message
type CallbackQuery = tgbotapi.CallbackQuery
type Update = tgbotapi.Update
type WebhookInfo = tgbotapi.WebhookInfo

// BotCommand 保持与旧 API 同名字段（commands.go 用）
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// InlineKeyboardButton 保持旧结构（menu.go / commands.go 直接用）
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// ==================== TelegramBot 客户端（tgbotapi 封装） ====================

// TelegramBot Telegram Bot 封装（内部持有 *tgbotapi.BotAPI）
type TelegramBot struct {
	mu       sync.RWMutex
	token    string
	chatID   string
	proxyURL string
	client   *tgbotapi.BotAPI
	hasProxy bool
}

// NewTelegramBot 创建无代理的 TelegramBot
func NewTelegramBot(botToken, chatID string) *TelegramBot {
	return newBot(botToken, chatID, "")
}

// NewTelegramBotWithProxy 创建带 HTTP/SOCKS5 代理的 TelegramBot
func NewTelegramBotWithProxy(botToken, chatID, proxyURL string) (*TelegramBot, error) {
	return newBotWithProxy(botToken, chatID, proxyURL)
}

// CreateBotFromSettings 从配置创建 TelegramBot（自动读取代理设置）
func CreateBotFromSettings(settings model.TelegramSettings) (*TelegramBot, error) {
	if settings.ProxyURL != "" {
		return NewTelegramBotWithProxy(settings.BotToken, settings.ChatID, settings.ProxyURL)
	}
	return NewTelegramBot(settings.BotToken, settings.ChatID), nil
}

func newBot(botToken, chatID, proxyURL string) *TelegramBot {
	b, err := newBotWithProxy(botToken, chatID, proxyURL)
	if err != nil {
		// 理论上无代理时不会报错（仅当 token 空），兜底返回空壳
		logger.S().Errorf("[Telegram] newBot failed: %v", err)
		return &TelegramBot{token: botToken, chatID: chatID, proxyURL: proxyURL}
	}
	return b
}

func newBotWithProxy(botToken, chatID, proxyURL string) (*TelegramBot, error) {
	if botToken == "" {
		return nil, fmt.Errorf("telegram bot token is empty")
	}
	var apiBot *tgbotapi.BotAPI
	var err error
	hasProxy := proxyURL != ""

	if hasProxy {
		client, cerr := buildProxyClient(proxyURL)
		if cerr != nil {
			return nil, fmt.Errorf("build proxy client: %w", cerr)
		}
		apiBot, err = tgbotapi.NewBotAPIWithClient(botToken, tgbotapi.APIEndpoint, client)
	} else {
		// 直连分支：给底层拨号加短超时（8s），避免 Telegram 不可达时 getMe/setMyCommands
		// 在主协程长时间阻塞，拖住 Web 服务启动（http://localhost:8090 迟迟打不开）。
		// 连接一旦建立，长轮询（getUpdates timeout=60）不受 DialContext 超时影响。
		dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
		client := &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{DialContext: dialer.DialContext},
		}
		apiBot, err = tgbotapi.NewBotAPIWithClient(botToken, tgbotapi.APIEndpoint, client)
	}
	if err != nil {
		return nil, fmt.Errorf("init telegram bot api: %w", err)
	}
	return &TelegramBot{
		token:    botToken,
		chatID:   chatID,
		proxyURL: proxyURL,
		client:   apiBot,
		hasProxy: hasProxy,
	}, nil
}

// buildProxyClient 构建支持 HTTP(S) / SOCKS5 的 http.Client
func buildProxyClient(proxyURL string) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyURL(u),
			},
		}, nil
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pass}
		}
		dialer, derr := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if derr != nil {
			return nil, fmt.Errorf("socks5 dialer: %w", derr)
		}
		return &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				Dial: dialer.Dial,
			},
		}, nil
	}
	return nil, fmt.Errorf("unsupported proxy scheme: %s", u.Scheme)
}

// UpdateCredentials 热更新凭据（Token / ChatID / 代理变更时重建底层 client）
func (b *TelegramBot) UpdateCredentials(botToken, chatID, proxyURL string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	needRebuild := false
	if botToken != "" && botToken != b.token {
		b.token = botToken
		needRebuild = true
	}
	// 代理变更同样必须重建：否则保存设置后仍走旧代理（或仍直连）
	if proxyURL != b.proxyURL {
		b.proxyURL = proxyURL
		b.hasProxy = proxyURL != ""
		needRebuild = true
	}
	if chatID != "" {
		b.chatID = chatID
	}
	if needRebuild {
		b.client = nil // 触发下次懒加载
	}
}

// apply 如果 client 为 nil，懒加载重建
func (b *TelegramBot) ensureClient() error {
	b.mu.RLock()
	ok := b.client != nil
	b.mu.RUnlock()
	if ok {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		return nil
	}
	nb, err := newBotWithProxy(b.token, b.chatID, b.proxyURL)
	if err != nil {
		return err
	}
	b.client = nb.client
	return nil
}

// ChatID 返回默认通知目标 ChatID
func (b *TelegramBot) ChatID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.chatID
}

// ProxyURL 返回当前代理 URL（空字符串表示无代理）
func (b *TelegramBot) ProxyURL() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.proxyURL
}

// BotToken 返回当前 Bot Token（dispatcher 用）
func (b *TelegramBot) BotToken() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.token
}

// Underlying 暴露底层 tgbotapi（polling / StartListening 用）
func (b *TelegramBot) Underlying() (*tgbotapi.BotAPI, error) {
	if err := b.ensureClient(); err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.client, nil
}

// ==================== 基础接口（与外部 API 签名保持一致） ====================

func (b *TelegramBot) GetMe(ctx context.Context) (*tgbotapi.User, error) {
	c, err := b.Underlying()
	if err != nil {
		return nil, err
	}
	u, err := c.GetMe()
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteWebhook 清除 webhook（轮询启动前必须调用，避免冲突）
func (b *TelegramBot) DeleteWebhook(ctx context.Context) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	_, err = c.Request(tgbotapi.DeleteWebhookConfig{})
	return err
}

// GetWebhookInfo 保留接口（没内部调用，但外部可能用）
func (b *TelegramBot) GetWebhookInfo(ctx context.Context) (*tgbotapi.WebhookInfo, error) {
	c, err := b.Underlying()
	if err != nil {
		return nil, err
	}
	info, err := c.GetWebhookInfo()
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// SetWebhook 设置 webhook（外部 handler 调用）
// secretToken 非空时用原始 API 调用补传 secret_token：tgbotapi v5.5.1 的
// WebhookConfig 没有该字段，若只走标准请求，Telegram 不会带上
// X-Telegram-Bot-Api-Secret-Token 头，入站校验会把所有更新拒掉（401）。
func (b *TelegramBot) SetWebhook(ctx context.Context, webhookURL, secretToken string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	cfg, err := tgbotapi.NewWebhook(webhookURL)
	if err != nil {
		return err
	}
	if secretToken == "" {
		_, err = c.Request(cfg)
		return err
	}

	form := url.Values{}
	form.Set("url", webhookURL)
	form.Set("secret_token", secretToken)
	endpoint := "https://api.telegram.org/bot" + b.BotToken() + "/setWebhook"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build setWebhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("setWebhook request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if uerr := json.Unmarshal(body, &out); uerr != nil {
		return fmt.Errorf("setWebhook parse: %w", uerr)
	}
	if !out.OK {
		return fmt.Errorf("setWebhook failed: %s", out.Description)
	}
	return nil
}

// GetUpdates 兼容调用方（polling.go 会被重写，但保留兼容接口）
func (b *TelegramBot) GetUpdates(ctx context.Context, offset int64, limit int, timeout int) ([]tgbotapi.Update, error) {
	c, err := b.Underlying()
	if err != nil {
		return nil, err
	}
	cfg := tgbotapi.NewUpdate(int(offset))
	if limit > 0 {
		cfg.Limit = limit
	}
	if timeout > 0 {
		cfg.Timeout = timeout
	}
	updates, err := c.GetUpdates(cfg)
	if err != nil {
		return nil, err
	}
	out := make([]tgbotapi.Update, len(updates))
	copy(out, updates)
	return out, nil
}

// SendMessage 发送文本（外部 context 忽略，走库内部超时）
func (b *TelegramBot) SendMessage(ctx context.Context, chatID, text, parseMode string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	// 4096 rune 保护（超限分段发送）
	chunks := splitTextByRunes(text, 4096)
	// 多段消息可能把 HTML 标签切断（<b> 跨段）→ 400 can't parse entities。
	// 这种情况下统一降级为纯文本，宁可丢样式也不丢消息。
	plain := len(chunks) > 1
	for _, chunk := range chunks {
		if plain {
			chunk = stripHTMLTags(chunk)
		}
		msg := tgbotapi.NewMessage(stringToInt64(chatID), chunk)
		if parseMode != "" && !plain {
			msg.ParseMode = parseMode
		}
		if e := b.sendWithRetry429(c, msg); e != nil {
			return fmt.Errorf("send message: %w", e)
		}
	}
	return nil
}

// SendNotification 便捷封装：默认 ChatID + HTML
func (b *TelegramBot) SendNotification(ctx context.Context, text string) error {
	if err := b.ensureClient(); err != nil {
		return err
	}
	cid := b.ChatID()
	if cid == "" {
		return fmt.Errorf("chat id is empty")
	}
	return b.SendNotificationWithRetry(text)
}

// SendMessageReturningID 发送单条消息并返回 messageID（供后续编辑实现进度回执）。
// 与分段发送的 SendMessage 不同：进度消息短且需要拿到 ID，不分段、不带重试，
// 失败时调用方直接放弃本次进度监听即可。
func (b *TelegramBot) SendMessageReturningID(ctx context.Context, chatID, text, parseMode string) (int64, error) {
	c, err := b.Underlying()
	if err != nil {
		return 0, err
	}
	msg := tgbotapi.NewMessage(stringToInt64(chatID), text)
	if parseMode != "" {
		msg.ParseMode = parseMode
	}
	sent, err := c.Send(msg)
	if err != nil {
		return 0, err
	}
	return int64(sent.MessageID), nil
}

// SendMessageWithRetry 带指数退避重试（QMS 同款逻辑）
func (b *TelegramBot) SendMessageWithRetry(text string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	maxRetries := 2
	if b.hasProxy {
		maxRetries = 3
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt*attempt) * time.Second
			// 逐次重试降为 Debug：网络不通时避免刷屏，最终失败由调用方（sendGuard）节流提示
			logger.S().Debugf("[Telegram] 重试第 %d 次（等待 %v）: %v", attempt, wait, lastErr)
			time.Sleep(wait)
		}
		sendErr := b.sendWithClient(c, text)
		if sendErr == nil {
			if attempt > 0 {
				logger.S().Debugf("[Telegram] 重试发送成功（第 %d 次）", attempt)
			}
			return nil
		}
		lastErr = sendErr
		if !isTimeoutError(sendErr) {
			break // 非超时类错误不重试（避免重复通知）
		}
	}
	return fmt.Errorf("telegram send retries exhausted: %w", lastErr)
}

// SendNotificationWithRetry 便捷（默认 chatID）
func (b *TelegramBot) SendNotificationWithRetry(text string) error {
	if err := b.ensureClient(); err != nil {
		return err
	}
	if b.ChatID() == "" {
		return fmt.Errorf("chat id is empty")
	}
	return b.SendMessageWithRetry(text)
}

// sendWithClient 按 4096 rune 分段 Send
func (b *TelegramBot) sendWithClient(c *tgbotapi.BotAPI, text string) error {
	cid := stringToInt64(b.ChatID())
	chunks := splitTextByRunes(text, 4096)
	// 多段时降级纯文本：分段可能切断 HTML 标签，导致整条消息 400
	plain := len(chunks) > 1
	for _, chunk := range chunks {
		if plain {
			chunk = stripHTMLTags(chunk)
		}
		msg := tgbotapi.NewMessage(cid, chunk)
		if !plain {
			msg.ParseMode = "HTML"
		}
		if err := b.sendWithRetry429(c, msg); err != nil {
			return err
		}
	}
	return nil
}

// sendWithRetry429 发送单条消息；遇到 429（限流）按 retry_after 等待后重试。
// Telegram 对单聊约 1 条/秒、全局 30 条/秒，批量通知时 429 很常见。
func (b *TelegramBot) sendWithRetry429(c *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		_, err := c.Send(msg)
		if err == nil {
			return nil
		}
		lastErr = err
		wait := telegramRetryAfter(err)
		if wait <= 0 {
			return err
		}
		logger.S().Warnf("[Telegram] 触发限流，等待 %ds 后重试（第 %d 次）", wait, attempt+1)
		time.Sleep(time.Duration(wait) * time.Second)
	}
	return lastErr
}

// telegramRetryAfter 解析 429 的 retry_after（秒）；非限流错误返回 0。
func telegramRetryAfter(err error) int {
	if err == nil {
		return 0
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "429") && !strings.Contains(s, "too many requests") {
		return 0
	}
	if i := strings.Index(s, "retry after "); i >= 0 {
		n := 0
		for _, c := range s[i+len("retry after "):] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		if n > 0 {
			return n
		}
	}
	return 3
}

// stripHTMLTags 去掉 HTML 标签并还原实体（多段消息降级纯文本时使用）。
var htmlTagRe = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

func stripHTMLTags(s string) string {
	if !strings.Contains(s, "<") {
		return html.UnescapeString(s)
	}
	return html.UnescapeString(htmlTagRe.ReplaceAllString(s, ""))
}

// SendPhoto URL 或本地路径发送图片；caption 1024 rune 截断保护；失败返回 err
func (b *TelegramBot) SendPhoto(ctx context.Context, chatID, caption, photoURLOrPath string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	if caption != "" {
		caption = truncateRunes(caption, 1024)
	}
	var photoCfg tgbotapi.PhotoConfig
	id := stringToInt64(chatID)
	if strings.HasPrefix(strings.ToLower(photoURLOrPath), "http://") ||
		strings.HasPrefix(strings.ToLower(photoURLOrPath), "https://") {
		photoCfg = tgbotapi.NewPhoto(id, tgbotapi.FileURL(photoURLOrPath))
	} else {
		photoCfg = tgbotapi.NewPhoto(id, tgbotapi.FilePath(photoURLOrPath))
	}
	if caption != "" {
		photoCfg.Caption = caption
		photoCfg.ParseMode = "HTML"
	}
	_, err = c.Send(photoCfg)
	if err != nil {
		return fmt.Errorf("send photo: %w", err)
	}
	return nil
}

// SendPhotoWithRetry 带指数退避重试（对齐 SendMessageWithRetry）
func (b *TelegramBot) SendPhotoWithRetry(ctx context.Context, chatID, caption, photoURLOrPath string) error {
	maxRetries := 2
	if b.hasProxy {
		maxRetries = 3
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt*attempt) * time.Second
			logger.S().Warnf("[Telegram] 图片重试第 %d 次（等待 %v）: %v", attempt, wait, lastErr)
			time.Sleep(wait)
		}
		sendErr := b.SendPhoto(ctx, chatID, caption, photoURLOrPath)
		if sendErr == nil {
			if attempt > 0 {
				logger.S().Infof("[Telegram] 图片重试发送成功（第 %d 次）", attempt)
			}
			return nil
		}
		lastErr = sendErr
		if !isTimeoutError(sendErr) {
			break // 非超时类错误不重试
		}
	}
	return fmt.Errorf("telegram send photo retries exhausted: %w", lastErr)
}

// SendPhotoFromFile 兼容旧签名（内部使用 SendPhotoWithRetry）
func (b *TelegramBot) SendPhotoFromFile(ctx context.Context, chatID, caption, filePath string) error {
	return b.SendPhotoWithRetry(ctx, chatID, caption, filePath)
}

// SetMyCommands 设置 Bot 菜单命令
func (b *TelegramBot) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	cmds := make([]tgbotapi.BotCommand, len(commands))
	for i, v := range commands {
		cmds[i] = tgbotapi.BotCommand{Command: v.Command, Description: v.Description}
	}
	cfg := tgbotapi.NewSetMyCommands(cmds...)
	_, err = c.Request(cfg)
	if err != nil {
		return err
	}
	logger.S().Info("[Telegram] Bot menu set successfully")
	return nil
}

// DeleteMyCommands 删除 Bot 菜单命令
func (b *TelegramBot) DeleteMyCommands(ctx context.Context) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	_, err = c.Request(tgbotapi.DeleteMyCommandsConfig{})
	return err
}

// AnswerCallbackQuery 回答回调查询
func (b *TelegramBot) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	cfg := tgbotapi.NewCallback(callbackID, text)
	_, err = c.Request(cfg)
	return err
}

// EditMessageText 编辑消息文本
func (b *TelegramBot) EditMessageText(ctx context.Context, chatID string, messageID int64, text, parseMode string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	text = truncateRunes(text, 4096)
	cfg := tgbotapi.NewEditMessageText(stringToInt64(chatID), int(messageID), text)
	if parseMode != "" {
		cfg.ParseMode = parseMode
	}
	_, err = c.Send(cfg)
	return ignoreEditNoop(err)
}

// EditMessageTextWithButtons 编辑消息文本+按钮
func (b *TelegramBot) EditMessageTextWithButtons(ctx context.Context, chatID string, messageID int64, text string, buttons [][]InlineKeyboardButton) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	text = truncateRunes(text, 4096)
	cfg := tgbotapi.NewEditMessageText(stringToInt64(chatID), int(messageID), text)
	cfg.ParseMode = "HTML"
	cfg.ReplyMarkup = toTGInlineMarkup(buttons)
	_, err = c.Send(cfg)
	return ignoreEditNoop(err)
}

// EditMessageReplyMarkup 仅编辑按钮
func (b *TelegramBot) EditMessageReplyMarkup(ctx context.Context, chatID string, messageID int64, buttons [][]InlineKeyboardButton) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	cfg := tgbotapi.NewEditMessageReplyMarkup(stringToInt64(chatID), int(messageID), *toTGInlineMarkup(buttons))
	_, err = c.Send(cfg)
	return ignoreEditNoop(err)
}

// ignoreEditNoop 忽略「内容未变化」这类无意义错误：
// 用户连点同一个菜单时 Telegram 会返回 400 message is not modified，
// 当成失败会污染日志，且让调用方误以为操作没生效。
func ignoreEditNoop(err error) error {
	if err == nil {
		return nil
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "message is not modified") {
		return nil
	}
	return err
}

// SendMessageWithButtons 发送文本+按钮
func (b *TelegramBot) SendMessageWithButtons(ctx context.Context, chatID string, text string, buttons [][]InlineKeyboardButton) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	// 仅第一段带按钮（保持与旧实现一致）
	chunks := splitTextByRunes(text, 4096)
	plain := len(chunks) > 1
	for i, chunk := range chunks {
		if plain {
			chunk = stripHTMLTags(chunk)
		}
		msg := tgbotapi.NewMessage(stringToInt64(chatID), chunk)
		if !plain {
			msg.ParseMode = "HTML"
		}
		if i == 0 {
			msg.ReplyMarkup = toTGInlineMarkup(buttons)
		}
		if e := b.sendWithRetry429(c, msg); e != nil {
			return fmt.Errorf("send message with buttons: %w", e)
		}
	}
	return nil
}

// ==================== formatMessage 统一渲染（QMS 同款 1:1） ====================

// metadataKeyOrder 定义 metadata 字段按业务重要性的展示顺序
// 身份类（账号/类型/状态）在前 → 路径类 → 度量类 → 描述类 → 时间类在后
// 未列入此表的 key 按 UTF-8 字节序追加在末尾，保证输出稳定
var metadataKeyOrder = []string{
	"账号", "类型", "状态", "详情",
	"云端路径", "原路径", "本地路径", "新路径",
	"入库季集", "评分", "主演", "入库时间",
	"大小", "文件数", "耗时",
	"播放进度", "时长",
	"删除时间", "错误", "简介", "时间",
}

// metadataKeyPriority 构建 key→优先级索引表（值越小越靠前）
var metadataKeyPriority = func() map[string]int {
	m := make(map[string]int, len(metadataKeyOrder))
	for i, k := range metadataKeyOrder {
		m[k] = i
	}
	return m
}()

// FormatMessage 渲染 Title/Content/Metadata 三段式
// Metadata 按 metadataKeyOrder 业务优先级排序（未列入的 key 按 UTF-8 序追加在末尾）
// 全角冒号对齐 QMS，所有通知（STRM/Emby/System）统一走此渲染层
func FormatMessage(title, content string, metadata map[string]string) string {
	var sb strings.Builder
	sb.Grow(len(title) + len(content) + len(metadata)*32 + 32)
	fmt.Fprintf(&sb, "<b>%s</b>\n", title)
	if content != "" {
		// 正文来自片名/路径等用户数据，必须转义，否则 '&' '<' '>' 会让 Telegram 400
		fmt.Fprintf(&sb, "%s\n", EscapeHTML(content))
	}

	if len(metadata) > 0 {
		keys := make([]string, 0, len(metadata))
		for k := range metadata {
			keys = append(keys, k)
		}
		// 按 metadataKeyPriority 排序；未列入的 key 用一个大索引 + UTF-8 序兜底
		fallbackIdx := len(metadataKeyOrder)
		sort.SliceStable(keys, func(i, j int) bool {
			oi, oki := metadataKeyPriority[keys[i]]
			if !oki {
				oi = fallbackIdx
			}
			oj, okj := metadataKeyPriority[keys[j]]
			if !okj {
				oj = fallbackIdx
			}
			if oi != oj {
				return oi < oj
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			if v := metadata[k]; v != "" {
				// metadata 的值都是路径/账号/大小等纯文本，统一转义保证 HTML parse 安全
				fmt.Fprintf(&sb, "<b>%s：</b> %s\n", k, EscapeHTML(v))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ==================== 内部辅助 ====================

func toTGInlineMarkup(buttons [][]InlineKeyboardButton) *tgbotapi.InlineKeyboardMarkup {
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, len(buttons))
	for i, row := range buttons {
		r := make([]tgbotapi.InlineKeyboardButton, len(row))
		for j, btn := range row {
			r[j] = tgbotapi.NewInlineKeyboardButtonData(btn.Text, btn.CallbackData)
		}
		rows[i] = r
	}
	m := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &m
}

func stringToInt64(s string) int64 {
	// 必须保留负号：Telegram 群/超级群的 chat_id 是负数（如 -1001234567890），
	// 旧实现过滤非数字字符会把群 ID 变成正数，导致群聊消息全部发送失败。
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// EscapeHTML 转义 Telegram HTML parse mode 中的特殊字符。
// 片名/路径里常见的 '&'（如 "Fast & Furious"）、'<'、'>' 不转义会让 Telegram
// 返回 400 "can't parse entities"，整条通知发送失败。
func EscapeHTML(s string) string {
	if !strings.ContainsAny(s, "&<>") {
		return s
	}
	return html.EscapeString(s)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// splitTextByRunes 按 rune 数分片；每段最多 max runes；尽量在 '\n' 边界切
func splitTextByRunes(s string, max int) []string {
	if max <= 0 {
		return []string{s}
	}
	r := []rune(s)
	if len(r) <= max {
		return []string{s}
	}
	out := make([]string, 0, (len(r)+max-1)/max)
	start := 0
	for start < len(r) {
		end := start + max
		if end > len(r) {
			end = len(r)
		} else {
			// 尝试回退到最后一个换行
			for i := end - 1; i > start+max/2; i-- {
				if r[i] == '\n' {
					end = i + 1
					break
				}
			}
		}
		out = append(out, string(r[start:end]))
		start = end
	}
	return out
}

// isTimeoutError 判断是否为超时类错误（仅对该类错误重试，避免重复通知）
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	keywords := []string{
		"timeout", "tls handshake timeout", "context deadline exceeded",
		"connection timeout", "dial timeout", "i/o timeout", "deadline exceeded",
	}
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
