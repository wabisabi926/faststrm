package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
//
//	· 实现了 NotificationDispatcher（新路径）：富文本卡片 + 按钮，单独发送
//	· 回退到单纯 Notifier：进入合并器汇总成摘要消息后再推送
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

// ==================== 文件删除批量聚合通知（避免整季删除每集一条刷屏） ====================

// deleteNotifyCollector 单次轮询批次内的文件删除收集器
// oncePoll 串行执行，每轮 begin；批次结束时按父目录聚合 flush，避免每集一条
type deleteNotifyCollector struct {
	mu      sync.Mutex
	entries []notifyEntry
	active  bool // 是否处于 oncePoll 批次中（非批次时 collectFileDelete 退化为立即单条通知）
}

// begin 开始一个新批次：清空并置 active（oncePoll 每轮开始调用）
func (c *deleteNotifyCollector) begin() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = nil
	c.active = true
	c.mu.Unlock()
}

// add 加入一条文件删除记录
func (c *deleteNotifyCollector) add(e notifyEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = append(c.entries, e)
	c.mu.Unlock()
}

// isActive 是否处于批次中
func (c *deleteNotifyCollector) isActive() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// finish 结束批次并取出累积条目（调用方负责发送）
func (c *deleteNotifyCollector) finish() []notifyEntry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.entries
	c.entries = nil
	c.active = false
	return entries
}

// collectFileDelete 收集文件删除通知（按父目录聚合，由 oncePoll 批次结束时 flush）
// 文件夹删除不收集（本就是一条）；非批次上下文（直接调用/测试）退化为立即单条通知
func (m *Monitor) collectFileDelete(ctx context.Context, account, cloudPath, localPath string) {
	m.mu.Lock()
	accMon, ok := m.accounts[account]
	m.mu.Unlock()
	if !ok || !accMon.delCollector.isActive() {
		m.notifyDelete(ctx, account, cloudPath, "文件", localPath)
		return
	}
	accMon.delCollector.add(notifyEntry{
		kind:      "delete",
		account:   account,
		cloudPath: cloudPath,
		localPath: localPath,
		kindLabel: "文件",
	})
}

// flushDeleteNotifications 按目录层级聚合发送收集到的文件删除通知（统一 "删除 <路径>"，无计数无标签）
//   - 整剧删光（seriesDir 不存在）→ "删除 叶问 (2026)"
//   - 整季删光（seriesDir 在、seasonDir 不存在）→ "删除 .../Season 1"
//   - 单集（seasonDir 在、1 文件）→ "删除 .../ep03.strm"
//   - 多集没删完（seasonDir 在、多文件）→ 聚合一条，列出文件名
//   - 非季节目录同此理（parent 不存在→目录；存在→列名）
//
// 判定依据文件系统最终态：handleDeleteEvent 每删一文件即调 removeEmptyParents 清空目录，
// flush 时目录不存在 ⟺ 该级被删光（含单季剧整删），无需 Emby 元数据。
func (m *Monitor) flushDeleteNotifications(ctx context.Context, account string, c *deleteNotifyCollector) {
	if c == nil {
		return
	}
	if m.notifier == nil || m.settingsFn().NotifyOnlyOnError {
		c.finish() // 仍需复位 active
		return
	}
	entries := c.finish()
	if len(entries) == 0 {
		return
	}

	// 季节目录文件按 series 级聚合；非季节目录按 immediate parent 聚合
	seriesFiles := map[string][]notifyEntry{}
	seriesOrder := []string{}
	plainGroups := map[string][]notifyEntry{}
	plainOrder := []string{}

	for _, e := range entries {
		dir := filepath.Dir(e.localPath)
		if e.localPath == "" {
			dir = filepath.Dir(e.cloudPath)
		}
		if strings.HasPrefix(strings.ToLower(filepath.Base(dir)), "season") {
			// 季节目录：归到 series 级（seriesDir = dir 的父目录）
			seriesDir := filepath.Dir(dir)
			if _, ok := seriesFiles[seriesDir]; !ok {
				seriesOrder = append(seriesOrder, seriesDir)
			}
			seriesFiles[seriesDir] = append(seriesFiles[seriesDir], e)
		} else {
			// 非季节目录：按 immediate parent 分组
			if _, ok := plainGroups[dir]; !ok {
				plainOrder = append(plainOrder, dir)
			}
			plainGroups[dir] = append(plainGroups[dir], e)
		}
	}

	// series 级发送
	for _, seriesDir := range seriesOrder {
		files := seriesFiles[seriesDir]
		// 整剧删光：seriesDir 不存在 → "删除 seriesDir"
		if _, err := os.Stat(seriesDir); err != nil && os.IsNotExist(err) {
			m.sendDeletePathMsg(ctx, account, seriesDir)
			continue
		}
		// seriesDir 仍存在 → 按季拆分
		seasonFiles := map[string][]notifyEntry{}
		seasonOrder := []string{}
		for _, e := range files {
			sd := filepath.Dir(e.localPath)
			if e.localPath == "" {
				sd = filepath.Dir(e.cloudPath)
			}
			if _, ok := seasonFiles[sd]; !ok {
				seasonOrder = append(seasonOrder, sd)
			}
			seasonFiles[sd] = append(seasonFiles[sd], e)
		}
		for _, sd := range seasonOrder {
			items := seasonFiles[sd]
			// 整季删光：seasonDir 不存在 → "删除 seasonDir"
			if _, err := os.Stat(sd); err != nil && os.IsNotExist(err) {
				m.sendDeletePathMsg(ctx, account, sd)
				continue
			}
			if len(items) == 1 {
				// 单集：删除 <文件路径>
				m.sendDeletePathMsg(ctx, account, entryPath(items[0]))
				continue
			}
			// 多集没删完：聚合列名
			m.sendDeleteFilesMsg(ctx, account, sd, entryBasenames(items))
		}
	}

	// plain 级发送（非季节目录）
	for _, dir := range plainOrder {
		items := plainGroups[dir]
		// 整目录删光：parent 不存在 → "删除 dir"
		if _, err := os.Stat(dir); err != nil && os.IsNotExist(err) {
			m.sendDeletePathMsg(ctx, account, dir)
			continue
		}
		if len(items) == 1 {
			m.sendDeletePathMsg(ctx, account, entryPath(items[0]))
			continue
		}
		m.sendDeleteFilesMsg(ctx, account, dir, entryBasenames(items))
	}
}

// entryPath 取条目的展示路径（优先 localPath，回退 cloudPath）
func entryPath(e notifyEntry) string {
	if e.localPath != "" {
		return e.localPath
	}
	return e.cloudPath
}

// entryBasenames 取条目集合的文件名列表
func entryBasenames(items []notifyEntry) []string {
	names := make([]string, 0, len(items))
	for _, e := range items {
		p := e.localPath
		if p == "" {
			p = e.cloudPath
		}
		names = append(names, filepath.Base(p))
	}
	return names
}

// sendDeletePathMsg 发送 "删除 <路径>" 通知（整剧/整季/单集/整目录删光）
func (m *Monitor) sendDeletePathMsg(ctx context.Context, account, path string) {
	msg := fmt.Sprintf("🗑️ <b>删除</b> <code>%s</code>\n账号: <code>%s</code>", path, account)
	if err := m.notifier.Notify(ctx, msg); err != nil {
		logger.S().Warnf("[Monitor] 删除通知发送失败 account=%s path=%s: %v", account, path, err)
	}
}

// sendDeleteFilesMsg 发送聚合通知：目录仍在、部分文件被删（列出文件名，无计数）
func (m *Monitor) sendDeleteFilesMsg(ctx context.Context, account, parentDir string, names []string) {
	msg := fmt.Sprintf("🗑️ <b>删除</b> <code>%s</code>\n文件: %s\n账号: <code>%s</code>",
		parentDir, strings.Join(names, ", "), account)
	if err := m.notifier.Notify(ctx, msg); err != nil {
		logger.S().Warnf("[Monitor] 聚合删除通知发送失败 account=%s dir=%s: %v", account, parentDir, err)
	}
}
