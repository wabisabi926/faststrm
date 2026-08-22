package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

const (
	// DefaultCoalesceWindow 默认合并窗口
	DefaultCoalesceWindow = 60 * time.Second
	// DefaultCoalesceMaxPerBucket 单桶最大条目数，超过立即 flush
	DefaultCoalesceMaxPerBucket = 20
)

// ==================== 桶分类键 ====================

type bucketKey struct {
	account string
	nType   NotificationType
}

// ==================== bucket：单类待合并条目 ====================

type coalescedEntry struct {
	timestamp time.Time
	content   string
	metadata  map[string]string
}

type bucket struct {
	entries []coalescedEntry
}

// ==================== NotificationBatcher 合并调度器 ====================

// NotificationBatcher 把短时间内的 STRM 通知合并为一条，避免 Telegram 刷屏
//   - 60 秒窗口内，同一 (account, type) 的多条通知合并为一条
//   - 超过 20 条同桶立即 flush
//   - 非 STRM 类型通知（错误/任务状态等）直接透传不合并
type NotificationBatcher struct {
	mu       sync.Mutex
	buckets  map[bucketKey]*bucket
	timer    *time.Timer
	window   time.Duration
	maxPer   int
	sender   func(ctx context.Context, n *Notification) error
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewNotificationBatcher 创建合并器。sender 是最终真正发送 Notification 的回调。
// window <= 0 用 DefaultCoalesceWindow (60s)；maxPer <= 0 用 DefaultCoalesceMaxPerBucket (20)。
func NewNotificationBatcher(
	sender func(ctx context.Context, n *Notification) error,
	window time.Duration,
	maxPer int,
) *NotificationBatcher {
	if window <= 0 {
		window = DefaultCoalesceWindow
	}
	if maxPer <= 0 {
		maxPer = DefaultCoalesceMaxPerBucket
	}
	b := &NotificationBatcher{
		buckets: make(map[bucketKey]*bucket),
		window:  window,
		maxPer:  maxPer,
		sender:  sender,
		stopCh:  make(chan struct{}),
	}
	b.timer = time.AfterFunc(window, func() { b.flushAll(context.Background()) })
	return b
}

// Enqueue 入队通知：STRM 类型按 (account, type) 合并；其他类型立即发送。
// 返回 true 表示已合并入队，false 表示立即发送（或调度器已停止退回 sender）。
func (b *NotificationBatcher) Enqueue(ctx context.Context, n *Notification) bool {
	if b == nil || n == nil {
		return false
	}
	// 非 STRM 类型立即发送（错误/任务状态等高频且用户期待即时到达）
	if !isCoalescable(n) {
		return false
	}
	select {
	case <-b.stopCh:
		return false
	default:
	}
	account := ""
	if n.Metadata != nil {
		account = n.Metadata["account"]
	}
	key := bucketKey{account: account, nType: n.Type}

	b.mu.Lock()
	bk, ok := b.buckets[key]
	if !ok {
		bk = &bucket{}
		b.buckets[key] = bk
	}
	bk.entries = append(bk.entries, coalescedEntry{
		timestamp: time.Now(),
		content:   n.Content,
		metadata:  n.Metadata,
	})
	exceeded := len(bk.entries) >= b.maxPer
	b.mu.Unlock()

	if exceeded {
		b.flushBucket(ctx, key)
	}
	return true
}

// Stop 停止后台 flush timer（进程退出或 server shutdown 时调用）
func (b *NotificationBatcher) Stop() {
	if b == nil {
		return
	}
	b.stopOnce.Do(func() {
		close(b.stopCh)
		if b.timer != nil {
			b.timer.Stop()
		}
		// 最后尽力 flush 剩余条目（不 ctx cancel）
		b.flushAll(context.Background())
	})
}

// ==================== 内部：flush ====================

func (b *NotificationBatcher) flushAll(ctx context.Context) {
	b.mu.Lock()
	var keys []bucketKey
	for k := range b.buckets {
		keys = append(keys, k)
	}
	b.mu.Unlock()
	for _, k := range keys {
		b.flushBucket(ctx, k)
	}
	// 下一轮 timer
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Reset(b.window)
	}
	b.mu.Unlock()
}

func (b *NotificationBatcher) flushBucket(ctx context.Context, key bucketKey) {
	b.mu.Lock()
	bk, ok := b.buckets[key]
	if !ok || len(bk.entries) == 0 {
		b.mu.Unlock()
		return
	}
	entries := make([]coalescedEntry, len(bk.entries))
	copy(entries, bk.entries)
	delete(b.buckets, key)
	b.mu.Unlock()

	if b.sender == nil {
		return
	}
	merged := mergeEntries(entries, key)
	if merged == nil {
		return
	}
	if err := b.sender(ctx, merged); err != nil {
		logger.S().Warnf("[NotifyBatcher] flush 发送失败 account=%s type=%s n=%d: %v",
			key.account, key.nType, len(entries), err)
	}
}

// isCoalescable 哪些通知类型会被合并（STRM 批量事件最适合合并）
func isCoalescable(n *Notification) bool {
	switch n.Type {
	case TypeSTRMCreate, TypeSTRMDelete, TypeSTRMMove, TypeSTRMRename:
		return true
	}
	return false
}

// mergeEntries 把同一桶的多条 Notification 合成一条聚合消息
func mergeEntries(entries []coalescedEntry, key bucketKey) *Notification {
	if len(entries) == 0 {
		return nil
	}
	// 单条保持原样发送（避免改格式）
	if len(entries) == 1 {
		return &Notification{
			Type:     key.nType,
			Content:  entries[0].content,
			Metadata: entries[0].metadata,
		}
	}
	// 提取 account / kind（取第一条）
	account := key.account
	kind := ""
	for _, e := range entries {
		if e.metadata != nil && e.metadata["kind"] != "" {
			kind = e.metadata["kind"]
			break
		}
	}
	typeLabel := notificationTypeLabel(key.nType)
	var sb strings.Builder
	fmt.Fprintf(&sb, "📦 批量%s通知 (%d条)\n", typeLabel, len(entries))
	if account != "" {
		fmt.Fprintf(&sb, "👤 账号: %s\n", account)
	}
	// 取每条的首行（避免消息过长），并按时间排序
	sorted := make([]coalescedEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].timestamp.Before(sorted[j].timestamp) })
	truncated := false
	preview := sorted
	maxPreview := 10
	if len(preview) > maxPreview {
		preview = preview[:maxPreview]
		truncated = true
	}
	for i, e := range preview {
		firstLine := firstNonEmptyLine(e.content)
		// 截断过长的单行
		if len(firstLine) > 80 {
			firstLine = firstLine[:80] + "..."
		}
		fmt.Fprintf(&sb, "%d. %s\n", i+1, firstLine)
	}
	if truncated {
		fmt.Fprintf(&sb, "...（另有 %d 条省略）\n", len(sorted)-maxPreview)
	}
	meta := map[string]string{}
	if account != "" {
		meta["account"] = account
	}
	if kind != "" {
		meta["kind"] = kind
	}
	return &Notification{
		Type:     key.nType,
		Content:  strings.TrimRight(sb.String(), "\n"),
		Metadata: meta,
	}
}

func notificationTypeLabel(t NotificationType) string {
	switch t {
	case TypeSTRMCreate:
		return "创建"
	case TypeSTRMDelete:
		return "删除"
	case TypeSTRMMove:
		return "移动"
	case TypeSTRMRename:
		return "重命名"
	}
	return "事件"
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return s
}
