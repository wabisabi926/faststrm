package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

type Dispatcher struct {
	tg       *TelegramBot
	webhook  *WebhookSender
	enabled  bool
	botToken string
	chatID   string
	batcher  *NotificationBatcher
	// 串行发送队列：单 worker 保证通知顺序 + TG 限流友好
	sendCh   chan sendTask
	sendStop chan struct{}
	sendOnce sync.Once
}

// sendTask 串行队列中的单条发送任务
type sendTask struct {
	ctx      context.Context
	n        *Notification
	plain    string // 纯文本通知（非 Notification 时使用）
	photo    string // 图片 URL/路径（纯文本通知时可为空）
	caption  string // 图片 caption
}

func NewDispatcher(tg *TelegramBot) *Dispatcher {
	d := &Dispatcher{
		tg:       tg,
		sendCh:   make(chan sendTask, 256),
		sendStop: make(chan struct{}),
	}
	if tg != nil {
		d.botToken = tg.BotToken()
		d.chatID = tg.ChatID()
		d.enabled = d.botToken != "" && d.chatID != ""
	}
	// 注册合并去抖发送器：最终走 d.enqueueSend（绕过 Enqueue 递归）
	d.batcher = NewNotificationBatcher(func(ctx context.Context, n *Notification) error {
		return d.enqueueSend(ctx, n)
	}, 0, 0)
	d.startSendWorker()
	return d
}

// startSendWorker 启动串行发送 worker goroutine
func (d *Dispatcher) startSendWorker() {
	go func() {
		for {
			select {
			case <-d.sendStop:
				// 排空剩余队列
				for {
					select {
					case t := <-d.sendCh:
						d.executeSend(t)
					default:
						return
					}
				}
			case t := <-d.sendCh:
				d.executeSend(t)
			}
		}
	}()
}

// executeSend 真正执行单条发送任务
func (d *Dispatcher) executeSend(t sendTask) {
	if t.n != nil {
		d.dispatchRawDirect(t.ctx, t.n)
	} else if t.photo != "" {
		d.dispatchPhotoDirect(t.ctx, t.caption, t.photo)
	} else if t.plain != "" {
		d.dispatchTextDirect(t.ctx, t.plain)
	}
}

// enqueueSend 将 Notification 入串行发送队列
func (d *Dispatcher) enqueueSend(ctx context.Context, n *Notification) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping dispatchRaw")
		return nil
	}
	select {
	case d.sendCh <- sendTask{ctx: context.Background(), n: n}:
	default:
		// 队列满时降级为直接发送（不丢失通知）
		logger.S().Warnf("[Dispatcher] send queue full (256), falling back to direct send")
		d.dispatchRawDirect(ctx, n)
	}
	return nil
}

// Stop 关闭合并 timer 和串行发送队列，flush 剩余通知（服务关闭时调用）
func (d *Dispatcher) Stop() {
	if d.batcher != nil {
		d.batcher.Stop()
	}
	d.sendOnce.Do(func() {
		close(d.sendStop)
	})
}

func (d *Dispatcher) SetEnabled(enabled bool) {
	d.enabled = enabled
}

func (d *Dispatcher) SetWebhook(url string) {
	d.webhook = NewWebhookSender(url)
}

// ApplySettings 热更新 Telegram 配置（通过 Web UI 保存配置后调用，避免重启服务）
// 1) 更新内部 TelegramBot 的 Token/ChatID（如果 tg 还没创建则懒创建）
// 2) 同步 enabled / webhook / dispatcher 内部缓存字段
// 3) 如果启用了 WebhookURL，创建 WebhookSender；否则清空
func (d *Dispatcher) ApplySettings(tg model.TelegramSettings) {
	if d.tg == nil && tg.BotToken != "" {
		d.tg = NewTelegramBot(tg.BotToken, tg.ChatID)
	}
	if d.tg != nil {
		d.tg.UpdateCredentials(tg.BotToken, tg.ChatID)
	}
	d.botToken = tg.BotToken
	d.chatID = tg.ChatID
	d.enabled = tg.Enabled && tg.BotToken != "" && tg.ChatID != ""
	if tg.WebhookURL != "" {
		d.webhook = NewWebhookSender(tg.WebhookURL)
	} else {
		d.webhook = nil
	}
}

func (d *Dispatcher) isConfigured() bool {
	return d.tg != nil && d.enabled && d.botToken != "" && d.chatID != ""
}

func (d *Dispatcher) Notify(ctx context.Context, message string) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured (missing botToken or chatID), skipping notification")
		return nil
	}
	// 入串行发送队列
	select {
	case d.sendCh <- sendTask{ctx: context.Background(), plain: message}:
	default:
		// 队列满时降级为直接发送
		d.dispatchTextDirect(ctx, message)
	}
	return nil
}

func (d *Dispatcher) NotifyWithPhoto(ctx context.Context, caption, photoURL string) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping photo send")
		return nil
	}
	// 入串行发送队列（图片下载和发送在 worker 中处理）
	select {
	case d.sendCh <- sendTask{ctx: context.Background(), caption: caption, photo: photoURL}:
	default:
		// 队列满时降级为直接发送
		d.dispatchPhotoDirect(ctx, caption, photoURL)
	}
	return nil
}

// dispatchTextDirect 直接发送纯文本（绕过队列，用于队列满时降级）
func (d *Dispatcher) dispatchTextDirect(ctx context.Context, message string) {
	if err := d.tg.SendNotification(ctx, message); err != nil {
		logger.S().Errorf("Failed to send Telegram notification: %v", err)
	}
	d.sendWebhookText(ctx, message)
}

// dispatchPhotoDirect 直接发送图片（绕过队列，用于队列满时降级）
func (d *Dispatcher) dispatchPhotoDirect(ctx context.Context, caption, photoURL string) {
	// 先下载图片到本地临时文件，再 multipart 上传到 Telegram（对齐 qmediasync）
	// 原因：绝大多数 Emby 部署在内网（192.168.x.x / 127.0.0.1 / 局域网域名），
	// Telegram 官方服务器无法主动抓取 URL，会导致 sendPhoto 失败或返回空图。
	tmpPath, err := downloadImageToTemp(ctx, photoURL)
	if err != nil {
		logger.S().Warnf("下载 Emby 图片失败，降级为纯文本通知: %v", err)
		d.dispatchTextDirect(ctx, caption)
		return
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if err := d.tg.SendPhotoFromFile(ctx, d.chatID, caption, tmpPath); err != nil {
		logger.S().Errorf("SendPhotoFromFile 失败，降级为纯文本通知: %v", err)
		d.dispatchTextDirect(ctx, caption)
		return
	}
	d.sendWebhookText(ctx, caption)
}

// downloadImageToTemp 把 URL 图片下载到系统临时目录，返回临时文件路径（调用方负责 os.Remove）
// 会自动根据 URL 判断后缀（.jpg/.png/无后缀都走 .jpg 默认，Telegram sendPhoto 实际看内容不看扩展名）
func downloadImageToTemp(ctx context.Context, imgURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpCli := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	// 根据 URL 扩展名选择临时文件后缀，兜底 .jpg
	ext := filepath.Ext(imgURL)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		// 合法后缀直接用
	default:
		ext = ".jpg"
	}
	f, err := os.CreateTemp("", "emby-photo-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("copy body: %w", err)
	}
	return f.Name(), nil
}

func (d *Dispatcher) NotifyTaskStatus(ctx context.Context, taskName, status, detail string) error {
	return d.Notify(ctx, FormatTaskStatusMessage(taskName, status, detail))
}

func (d *Dispatcher) NotifyDownloadComplete(ctx context.Context, taskName string, totalFiles, downloaded int, durationMs int64) error {
	return d.Notify(ctx, FormatDownloadCompleteMessage(taskName, totalFiles, downloaded, durationMs))
}

func (d *Dispatcher) NotifyError(ctx context.Context, taskName, errMsg string) error {
	return d.Notify(ctx, FormatErrorMessage(taskName, errMsg))
}

func (d *Dispatcher) Dispatch(ctx context.Context, n *Notification) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping dispatch")
		return nil
	}
	// 先走 60s 合并去抖：STRM 类通知合并，非 STRM 立即发送
	if d.batcher != nil && d.batcher.Enqueue(ctx, n) {
		return nil
	}
	return d.enqueueSend(ctx, n)
}

// dispatchRawDirect 真正发送 Notification 到底层（TG + Webhook）。
// 由串行 worker 调用，避免并发发送造成 TG 限流。
func (d *Dispatcher) dispatchRawDirect(ctx context.Context, n *Notification) {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping dispatchRaw")
		return
	}

	if n.ImageFile != "" || n.ImageURL != "" {
		// 优先本地文件（ImageFile），否则 ImageURL 下载到临时文件再上传
		var finalPath string
		var needCleanup bool
		if n.ImageFile != "" {
			finalPath = n.ImageFile
		} else {
			p, err := downloadImageToTemp(ctx, n.ImageURL)
			if err != nil {
				logger.S().Warnf("Dispatch photo: 下载图片失败，降级纯文本: %v", err)
				d.sendWebhookNotification(ctx, n)
				_ = d.tg.SendNotification(ctx, n.Content)
				return
			}
			finalPath = p
			needCleanup = true
		}
		if needCleanup {
			defer func() { _ = os.Remove(finalPath) }()
		}
		if err := d.tg.SendPhotoFromFile(ctx, d.chatID, n.Content, finalPath); err != nil {
			logger.S().Warnf("Dispatch photo: SendPhotoFromFile 失败，降级纯文本: %v", err)
			d.sendWebhookNotification(ctx, n)
			_ = d.tg.SendNotification(ctx, n.Content)
			return
		}
		d.sendWebhookNotification(ctx, n)
		return
	}

	buttons := d.buildInlineKeyboard(n)
	if len(buttons) > 0 {
		if err := d.tg.SendMessageWithButtons(ctx, d.chatID, n.Content, buttons); err != nil {
			logger.S().Warnf("Dispatch with buttons failed, fallback to text: %v", err)
			d.sendWebhookNotification(ctx, n)
			_ = d.tg.SendNotification(ctx, n.Content)
			return
		}
		d.sendWebhookNotification(ctx, n)
		return
	}

	d.sendWebhookNotification(ctx, n)
	_ = d.tg.SendNotification(ctx, n.Content)
}

func (d *Dispatcher) sendWebhookText(ctx context.Context, message string) {
	if d.webhook == nil {
		logger.S().Debug("Webhook not configured, skipping webhook notification")
		return
	}
	if err := d.webhook.Send(ctx, message); err != nil {
		logger.S().Warnf("Webhook notification failed (non-critical): %v", err)
	}
}

func (d *Dispatcher) sendWebhookNotification(ctx context.Context, n *Notification) {
	if d.webhook == nil {
		return
	}
	if err := d.webhook.SendNotification(ctx, n); err != nil {
		logger.S().Warnf("Webhook notification failed (non-critical): %v", err)
	}
}

func (d *Dispatcher) buildInlineKeyboard(n *Notification) [][]InlineKeyboardButton {
	if n == nil || n.Metadata == nil {
		return nil
	}
	switch n.Type {
	case TypeSTRMCreate, TypeSTRMDelete, TypeSTRMMove, TypeSTRMRename:
		kind := n.Metadata["kind"]
		if kind == "movie" || kind == "tv" || kind == "series" {
			return [][]InlineKeyboardButton{
				{
					{Text: "🔄 刷新 Emby 媒体库", CallbackData: "refresh_emby:" + kind},
					{Text: "🔍 查看详情", CallbackData: "detail_strm:" + n.Metadata["cloud_path"]},
				},
			}
		}
	}
	return nil
}

func FormatTaskStatusMessage(taskName, status, detail string) string {
	return FormatMessage("🎬 任务状态", taskName, map[string]string{
		"状态": status,
		"详情": detail,
	})
}

func FormatDownloadCompleteMessage(taskName string, totalFiles, downloaded int, durationMs int64) string {
	return FormatMessage("✅ 任务完成", taskName, map[string]string{
		"文件数": fmt.Sprintf("%d / %d", downloaded, totalFiles),
		"耗时":  formatDuration(durationMs),
	})
}

func FormatErrorMessage(taskName, errMsg string) string {
	return FormatMessage("❌ 任务错误", taskName, map[string]string{
		"错误": errMsg,
	})
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		return fmt.Sprintf("%.1fs", secs)
	}
	d := time.Duration(ms) * time.Millisecond
	return d.String()
}