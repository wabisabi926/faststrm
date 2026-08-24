// Package notify 提供 Telegram Bot API 客户端（基于 go-telegram-bot-api/v5）、
// 长轮询管理器、命令处理器及统一通知分发器。
package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"golang.org/x/net/proxy"
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
		apiBot, err = tgbotapi.NewBotAPI(botToken)
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

// UpdateCredentials 热更新凭据（Token / ChatID 变更时重建底层 client）
func (b *TelegramBot) UpdateCredentials(botToken, chatID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if botToken != "" && botToken != b.token {
		b.token = botToken
		b.client = nil // 触发下次懒加载
	}
	if chatID != "" {
		b.chatID = chatID
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
func (b *TelegramBot) SetWebhook(ctx context.Context, webhookURL, secretToken string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	cfg, err := tgbotapi.NewWebhook(webhookURL)
	if err != nil {
		return err
	}
	_ = secretToken // tgbotapi v5 WebhookConfig 无 SecretToken 字段，忽略
	_, err = c.Request(cfg)
	return err
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
	for i := range updates {
		out[i] = updates[i]
	}
	return out, nil
}

// SendMessage 发送文本（外部 context 忽略，走库内部超时）
func (b *TelegramBot) SendMessage(ctx context.Context, chatID, text, parseMode string) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	// 4096 rune 保护（超限分段发送）
	for _, chunk := range splitTextByRunes(text, 4096) {
		msg := tgbotapi.NewMessage(stringToInt64(chatID), chunk)
		if parseMode != "" {
			msg.ParseMode = parseMode
		}
		if _, e := c.Send(msg); e != nil {
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
			logger.S().Warnf("[Telegram] 重试第 %d 次（等待 %v）: %v", attempt, wait, lastErr)
			time.Sleep(wait)
		}
		sendErr := b.sendWithClient(c, text)
		if sendErr == nil {
			if attempt > 0 {
				logger.S().Infof("[Telegram] 重试发送成功（第 %d 次）", attempt)
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
	cid := b.ChatID()
	if cid == "" {
		return fmt.Errorf("chat id is empty")
	}
	_ = cid
	return b.SendMessageWithRetry(text)
}

// sendWithClient 按 4096 rune 分段 Send
func (b *TelegramBot) sendWithClient(c *tgbotapi.BotAPI, text string) error {
	cid := stringToInt64(b.ChatID())
	for _, chunk := range splitTextByRunes(text, 4096) {
		msg := tgbotapi.NewMessage(cid, chunk)
		msg.ParseMode = "HTML"
		if _, err := c.Send(msg); err != nil {
			return err
		}
	}
	return nil
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

// SendPhotoFromFile 兼容旧签名（内部直接走 SendPhoto）
func (b *TelegramBot) SendPhotoFromFile(ctx context.Context, chatID, caption, filePath string) error {
	return b.SendPhoto(ctx, chatID, caption, filePath)
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
	return err
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
	return err
}

// EditMessageReplyMarkup 仅编辑按钮
func (b *TelegramBot) EditMessageReplyMarkup(ctx context.Context, chatID string, messageID int64, buttons [][]InlineKeyboardButton) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	cfg := tgbotapi.NewEditMessageReplyMarkup(stringToInt64(chatID), int(messageID), *toTGInlineMarkup(buttons))
	_, err = c.Send(cfg)
	return err
}

// SendMessageWithButtons 发送文本+按钮
func (b *TelegramBot) SendMessageWithButtons(ctx context.Context, chatID string, text string, buttons [][]InlineKeyboardButton) error {
	c, err := b.Underlying()
	if err != nil {
		return err
	}
	for _, chunk := range splitTextByRunes(text, 4096) {
		msg := tgbotapi.NewMessage(stringToInt64(chatID), chunk)
		msg.ParseMode = "HTML"
		// 仅第一段带按钮
		msg.ReplyMarkup = toTGInlineMarkup(buttons)
		if _, e := c.Send(msg); e != nil {
			return fmt.Errorf("send message with buttons: %w", e)
		}
		break // 仅第一段带按钮（保持与旧实现一致）
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
	"播放进度", "观看时长", "时长",
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
		fmt.Fprintf(&sb, "%s\n", content)
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
				fmt.Fprintf(&sb, "<b>%s：</b> %s\n", k, v)
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
	var v int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		v = v*10 + int64(c-'0')
	}
	return v
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
