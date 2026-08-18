// Package notify 的 dispatcher 子模块：统一通知分发器。
// 对齐 frontend/src/lib/emby/notifierSender.ts 中"先校验 Telegram 配置，再裸发"的发送模式。
package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// Dispatcher 统一通知分发器，路由到 Telegram 和（可选）外部 webhook
type Dispatcher struct {
	tg         *TelegramBot
	webhookURL string
	enabled    bool
	botToken   string
	chatID     string
}

// NewDispatcher 创建 Dispatcher
func NewDispatcher(tg *TelegramBot) *Dispatcher {
	d := &Dispatcher{tg: tg}
	if tg != nil {
		d.botToken = tg.botToken
		d.chatID = tg.chatID
		// 默认按"配置完整即启用"判断，可通过 SetEnabled 覆盖
		d.enabled = tg.botToken != "" && tg.chatID != ""
	}
	return d
}

// SetEnabled 显式设置 Telegram 通知是否启用
func (d *Dispatcher) SetEnabled(enabled bool) {
	d.enabled = enabled
}

// SetWebhook 设置可选的 webhook URL（用于在 Telegram 之外做额外分发）
func (d *Dispatcher) SetWebhook(url string) {
	d.webhookURL = url
}

// isConfigured 检查 Telegram 是否配置完整（enabled + botToken + chatID 同时满足）
func (d *Dispatcher) isConfigured() bool {
	return d.tg != nil && d.enabled && d.botToken != "" && d.chatID != ""
}

// Notify 发送纯文本通知到 Telegram；未配置时静默跳过（对齐 TS 行为）
func (d *Dispatcher) Notify(ctx context.Context, message string) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured (missing botToken or chatID), skipping notification")
		return nil
	}
	if err := d.tg.SendNotification(ctx, message); err != nil {
		logger.S().Errorf("Failed to send Telegram notification: %v", err)
		return err
	}
	return nil
}

// NotifyWithPhoto 发送带图片（URL）的通知到 Telegram
func (d *Dispatcher) NotifyWithPhoto(ctx context.Context, caption, photoURL string) error {
	if !d.isConfigured() {
		logger.S().Debug("Telegram not configured, skipping photo send")
		return nil
	}
	if err := d.tg.SendPhoto(ctx, d.tg.chatID, caption, photoURL); err != nil {
		logger.S().Errorf("Failed to send Telegram photo: %v", err)
		return err
	}
	return nil
}

// NotifyTaskStatus 格式化并发送任务状态消息
// 格式: 🎬 任务: {name}\n📊 状态: {status}\n📝 {detail}
func (d *Dispatcher) NotifyTaskStatus(ctx context.Context, taskName, status, detail string) error {
	return d.Notify(ctx, FormatTaskStatusMessage(taskName, status, detail))
}

// NotifyDownloadComplete 格式化并发送下载完成消息
// 格式: ✅ 下载完成\n🎬 任务: {name}\n📁 文件数: {downloaded}/{total}\n⏱ 耗时: {duration}
func (d *Dispatcher) NotifyDownloadComplete(ctx context.Context, taskName string, totalFiles, downloaded int, durationMs int64) error {
	return d.Notify(ctx, FormatDownloadCompleteMessage(taskName, totalFiles, downloaded, durationMs))
}

// NotifyError 格式化并发送错误消息
// 格式: ❌ 错误\n🎬 任务: {name}\n📝 {errMsg}
func (d *Dispatcher) NotifyError(ctx context.Context, taskName, errMsg string) error {
	return d.Notify(ctx, FormatErrorMessage(taskName, errMsg))
}

// FormatTaskStatusMessage 格式化任务状态消息
// 格式: 🎬 任务: {name}\n📊 状态: {status}\n📝 {detail}
func FormatTaskStatusMessage(taskName, status, detail string) string {
	return fmt.Sprintf("🎬 任务: %s\n📊 状态: %s\n📝 %s", taskName, status, detail)
}

// FormatDownloadCompleteMessage 格式化下载完成消息
// 格式: ✅ 下载完成\n🎬 任务: {name}\n📁 文件数: {downloaded}/{total}\n⏱ 耗时: {duration}
func FormatDownloadCompleteMessage(taskName string, totalFiles, downloaded int, durationMs int64) string {
	return fmt.Sprintf("✅ 下载完成\n🎬 任务: %s\n📁 文件数: %d/%d\n⏱ 耗时: %s",
		taskName, downloaded, totalFiles, formatDuration(durationMs))
}

// FormatErrorMessage 格式化错误消息
// 格式: ❌ 错误\n🎬 任务: {name}\n📝 {errMsg}
func FormatErrorMessage(taskName, errMsg string) string {
	return fmt.Sprintf("❌ 错误\n🎬 任务: %s\n📝 %s", taskName, errMsg)
}

// formatDuration 将毫秒耗时格式化为可读字符串
// <1s 显示 ms；<1min 显示 "x.xs"；否则使用 Go Duration 字符串（如 "1m30s"、"1h2m3s"）
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
