package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

type Dispatcher struct {
	tg       *TelegramBot
	webhook  *WebhookSender
	enabled  bool
	botToken string
	chatID   string
	batcher  *NotificationBatcher
}

func NewDispatcher(tg *TelegramBot) *Dispatcher {
	d := &Dispatcher{tg: tg}
	if tg != nil {
		d.botToken = tg.botToken
		d.chatID = tg.chatID
		d.enabled = tg.botToken != "" && tg.chatID != ""
	}
	// 注册合并去抖发送器：最终走 d.dispatchRaw（绕过 Enqueue 递归）
	d.batcher = NewNotificationBatcher(func(ctx context.Context, n *Notification) error {
		return d.dispatchRaw(ctx, n)
	}, 0, 0)
	return d
}

// Stop 关闭合并 timer 并 flush 剩余通知（服务关闭时调用）
func (d *Dispatcher) Stop() {
	if d.batcher != nil {
		d.batcher.Stop()
	}
}

func (d *Dispatcher) SetEnabled(enabled bool) {
	d.enabled = enabled
}

func (d *Dispatcher) SetWebhook(url string) {
	d.webhook = NewWebhookSender(url)
}

func (d *Dispatcher) isConfigured() bool {
	return d.tg != nil && d.enabled && d.botToken != "" && d.chatID != ""
}

func (d *Dispatcher) Notify(ctx context.Context, message string) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured (missing botToken or chatID), skipping notification")
		return nil
	}
	if err := d.tg.SendNotification(ctx, message); err != nil {
		logger.S().Errorf("Failed to send Telegram notification: %v", err)
		return err
	}
	d.sendWebhookText(ctx, message)
	return nil
}

func (d *Dispatcher) NotifyWithPhoto(ctx context.Context, caption, photoURL string) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping photo send")
		return nil
	}
	if err := d.tg.SendPhoto(ctx, d.tg.chatID, caption, photoURL); err != nil {
		logger.S().Errorf("Failed to send Telegram photo: %v", err)
		return err
	}
	d.sendWebhookText(ctx, caption)
	return nil
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
	return d.dispatchRaw(ctx, n)
}

// dispatchRaw 真正发送 Notification 到底层（TG + Webhook）。
// 供 Batcher flush 回调使用，避免再走 Enqueue 造成递归。
func (d *Dispatcher) dispatchRaw(ctx context.Context, n *Notification) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping dispatchRaw")
		return nil
	}

	if n.ImageFile != "" || n.ImageURL != "" {
		if err := d.tg.SendPhoto(ctx, d.chatID, n.Content, n.ImageURL); err != nil {
			logger.S().Warnf("Dispatch photo failed, fallback to text: %v", err)
			d.sendWebhookNotification(ctx, n)
			return d.tg.SendNotification(ctx, n.Content)
		}
		d.sendWebhookNotification(ctx, n)
		return nil
	}

	buttons := d.buildInlineKeyboard(n)
	if len(buttons) > 0 {
		if err := d.tg.SendMessageWithButtons(ctx, d.chatID, n.Content, buttons); err != nil {
			logger.S().Warnf("Dispatch with buttons failed, fallback to text: %v", err)
			d.sendWebhookNotification(ctx, n)
			return d.tg.SendNotification(ctx, n.Content)
		}
		d.sendWebhookNotification(ctx, n)
		return nil
	}

	d.sendWebhookNotification(ctx, n)
	return d.tg.SendNotification(ctx, n.Content)
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
	return fmt.Sprintf("🎬 任务: %s\n📊 状态: %s\n📝 %s", taskName, status, detail)
}

func FormatDownloadCompleteMessage(taskName string, totalFiles, downloaded int, durationMs int64) string {
	return fmt.Sprintf("✅ 下载完成\n🎬 任务: %s\n📁 文件数: %d/%d\n⏱ 耗时: %s",
		taskName, downloaded, totalFiles, formatDuration(durationMs))
}

func FormatErrorMessage(taskName, errMsg string) string {
	return fmt.Sprintf("❌ 错误\n🎬 任务: %s\n📝 %s", taskName, errMsg)
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