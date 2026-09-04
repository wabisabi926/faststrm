package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	ctx     context.Context
	n       *Notification
	plain   string // 纯文本通知（非 Notification 时使用）
	photo   string // 图片 URL/路径（纯文本通知时可为空）
	caption string // 图片 caption
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
// 1) 更新内部 TelegramBot 的 Token/ChatID/代理（如果代理变更则重建 Bot）
// 2) 同步 enabled / webhook / dispatcher 内部缓存字段
// 3) 如果启用了 WebhookURL，创建 WebhookSender；否则清空
func (d *Dispatcher) ApplySettings(tg model.TelegramSettings) {
	// 如果代理配置变更，需要重建 Bot
	if d.tg == nil && tg.BotToken != "" {
		if bot, err := CreateBotFromSettings(tg); err == nil {
			d.tg = bot
		}
	} else if d.tg != nil {
		// 检查代理是否变更
		if d.tg.ProxyURL() != tg.ProxyURL {
			if bot, err := CreateBotFromSettings(tg); err == nil {
				d.tg = bot
			} else {
				logger.S().Errorf("[Telegram] 重建 Bot 失败: %v", err)
			}
		} else {
			d.tg.UpdateCredentials(tg.BotToken, tg.ChatID)
		}
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
// 对齐 QMS sendNewItemNotification：若 photo 参数已是本地路径 → 直接上传；若是 URL → 先下载到临时文件
// 若 photo 是受控 Emby 临时文件（fs_emby_ 前缀），发送完成后自动清理，避免 notifier 层与 worker 竞争删除
func (d *Dispatcher) dispatchPhotoDirect(ctx context.Context, caption, photo string) {
	var (
		finalPath  string
		needRemove bool
	)
	if isLocalFilePath(photo) {
		finalPath = photo
		// 受控临时图：Dispatcher 发送完删除，避免 notifier 层与 worker 的竞态
		if looksLikeEmbyTempImage(finalPath) {
			needRemove = true
		}
	} else {
		p, err := downloadImageToTemp(ctx, photo)
		if err != nil {
			logger.S().Warnf("下载 Emby 图片失败，降级为纯文本通知: %v", err)
			d.dispatchTextDirect(ctx, caption)
			return
		}
		finalPath = p
		needRemove = true
	}
	if needRemove {
		defer func() { _ = safeRemoveEmbyTempImage(finalPath) }()
	}

	if err := d.tg.SendPhotoFromFile(ctx, d.chatID, caption, finalPath); err != nil {
		// 严格对齐 QMS handlers.go L104-107：SendPhoto 失败不再发纯文本兜底，直接记录错误返回
		logger.S().Errorf("SendPhotoFromFile 失败，按 QMS 行为不再降级纯文本: %v", err)
		d.sendWebhookText(ctx, caption)
		return
	}
	d.sendWebhookText(ctx, caption)
}

// isLocalFilePath 判断字符串是否是本地文件路径（而非 http(s):// URL 或其他 scheme）
func isLocalFilePath(s string) bool {
	if s == "" {
		return false
	}
	// http/https/ftp/data/... 等显式 scheme：非本地
	if idx := strings.Index(s, "://"); idx > 0 {
		return false
	}
	// 路径绝对或相对，只要能 stat 到普通文件→算本地；Windows 盘符( C:\ )或 Unix / 或相对路径都 ok
	info, err := os.Stat(s)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// looksLikeEmbyTempImage 文件名匹配 notifier.createEmbyTempImagePath 产生的受控前缀
func looksLikeEmbyTempImage(path string) bool {
	name := filepath.Base(path)
	return strings.HasPrefix(name, "fs_emby_") && strings.HasSuffix(name, ".jpg")
}

// safeRemoveEmbyTempImage 只允许删除 TempDir 下且以 fs_emby_ 开头的 jpg，避免误删
func safeRemoveEmbyTempImage(path string) error {
	if !looksLikeEmbyTempImage(path) {
		return fmt.Errorf("拒绝删除非受控 Emby 临时图: %s", path)
	}
	tempDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if filepath.Dir(abs) != tempDir {
		return fmt.Errorf("拒绝删除 TempDir 外的文件: %s", path)
	}
	return os.Remove(abs)
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
		// 优先级：ImageFile(已明确本地) > ImageURL（可能是本地路径，也可能是 http URL）
		var (
			finalPath  string
			needRemove bool
		)
		if n.ImageFile != "" {
			finalPath = n.ImageFile
			if looksLikeEmbyTempImage(finalPath) {
				needRemove = true
			}
		} else if isLocalFilePath(n.ImageURL) {
			finalPath = n.ImageURL
			if looksLikeEmbyTempImage(finalPath) {
				needRemove = true
			}
		} else {
			p, err := downloadImageToTemp(ctx, n.ImageURL)
			if err != nil {
				logger.S().Warnf("Dispatch photo: 下载图片失败，降级纯文本: %v", err)
				d.sendWebhookNotification(ctx, n)
				_ = d.tg.SendNotification(ctx, n.Content)
				return
			}
			finalPath = p
			needRemove = true
		}
		if needRemove {
			defer func() { _ = safeRemoveEmbyTempImage(finalPath) }()
		}
		if err := d.tg.SendPhotoFromFile(ctx, d.chatID, n.Content, finalPath); err != nil {
			// 严格对齐 QMS：图片发送失败不再降级纯文本，只记录错误
			logger.S().Warnf("Dispatch photo: SendPhotoFromFile 失败，按 QMS 行为不降级纯文本: %v", err)
			d.sendWebhookNotification(ctx, n)
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
