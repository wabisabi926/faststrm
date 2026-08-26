package monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 通知辅助函数 ====================

// tryDispatchNotification 尝试通过新的 Dispatch 接口发送结构化通知
func (m *Monitor) tryDispatchNotification(ctx context.Context, n *notify.Notification) bool {
	if dispatcher, ok := m.notifier.(notify.NotificationDispatcher); ok {
		if err := dispatcher.Dispatch(ctx, n); err != nil {
			logger.S().Warnf("[Monitor] Dispatch 通知发送失败: %v", err)
		}
		return true
	}
	return false
}

// ==================== P2-8 通知合并（对齐参考项目 _schedule_notification） ====================

// notifyEntry 单条通知记录
type notifyEntry struct {
	kind      string // create / delete / move / rename
	account   string
	cloudPath string
	localPath string
	kindLabel string
	size      int64
}

// NotifyMerger 通知合并器：60秒窗口内累积通知，到期后合并发送一条摘要
// 对齐参考项目 _schedule_notification: Timer 60s 延迟，合并 strm_count + mediainfo_count
type NotifyMerger struct {
	notifier  Notifier
	mu        sync.Mutex
	entries   []notifyEntry
	timer     *time.Timer
	windowSec time.Duration
}

// NewNotifyMerger 创建通知合并器
func NewNotifyMerger(notifier Notifier) *NotifyMerger {
	return &NotifyMerger{
		notifier:  notifier,
		windowSec: 60 * time.Second,
	}
}

// Add 将一条通知加入合并队列，启动/重置60秒定时器
func (nm *NotifyMerger) Add(entry notifyEntry) {
	if nm == nil || nm.notifier == nil {
		return
	}
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.entries = append(nm.entries, entry)
	// 首次添加时启动定时器；已有定时器则保持不变（不推迟，保证60秒内必发）
	if nm.timer == nil {
		nm.timer = time.AfterFunc(nm.windowSec, nm.flush)
	}
}

// flush 合并发送所有累积的通知（定时器回调）
func (nm *NotifyMerger) flush() {
	nm.mu.Lock()
	entries := nm.entries
	nm.entries = nil
	nm.timer = nil
	nm.mu.Unlock()

	if len(entries) == 0 {
		return
	}

	// 按类型统计
	counts := map[string]int{"create": 0, "delete": 0, "move": 0, "rename": 0}
	for _, e := range entries {
		counts[e.kind]++
	}

	// 构建摘要消息
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>STRM 操作摘要</b>（%d 条事件）:\n", len(entries)))
	if counts["create"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0✅ 生成: %d\n", counts["create"]))
	}
	if counts["delete"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0🗑️ 删除: %d\n", counts["delete"]))
	}
	if counts["move"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0📁 移动: %d\n", counts["move"]))
	}
	if counts["rename"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0✏️ 重命名: %d\n", counts["rename"]))
	}
	// 列出最多5条明细
	maxDetail := 5
	for i, e := range entries {
		if i >= maxDetail {
			sb.WriteString(fmt.Sprintf("\u00a0\u00a0...及其他 %d 条\n", len(entries)-maxDetail))
			break
		}
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0· %s: %s\n", e.kind, e.cloudPath))
	}

	if err := nm.notifier.Notify(context.Background(), sb.String()); err != nil {
		logger.S().Warnf("[Monitor] 合并通知发送失败: %v", err)
	}
}

// ==================== STRM 通知（统一 Notification 对象 + 富文本 HTML 格式） ====================

// notifyCreate 发送创建通知
// 发送策略二选一（避免一条事件发两条）：
//   · 实现了 NotificationDispatcher（新路径）：富文本卡片 + 按钮，单独发送
//   · 回退到单纯 Notifier：进入合并器汇总成摘要消息后再推送
func (m *Monitor) notifyCreate(ctx context.Context, account, cloudPath, kindLabel, localPath string, size int64) {
	if m.notifier == nil {
		return
	}
	// 仅错误模式：正常操作不发通知（错误仍由 notifyPollError/notifyEventBatchError 推送）
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildCreateNotification(notify.STRMCreateInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
		FileSize:  size,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器，后续合并器 flush 时统一推送摘要（不再单独发一条，避免重复）
	m.notifyMerger.Add(notifyEntry{kind: "create", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel, size: size})
}

// notifyDelete 发送删除通知
func (m *Monitor) notifyDelete(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildDeleteNotification(notify.STRMDeleteInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器
	m.notifyMerger.Add(notifyEntry{kind: "delete", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel})
}

// notifyMove 发送移动通知
func (m *Monitor) notifyMove(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildMoveNotification(notify.STRMMoveInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器
	m.notifyMerger.Add(notifyEntry{kind: "move", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel})
}

// notifyRename 发送重命名通知
func (m *Monitor) notifyRename(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildRenameNotification(notify.STRMRenameInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器
	m.notifyMerger.Add(notifyEntry{kind: "rename", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel})
}
