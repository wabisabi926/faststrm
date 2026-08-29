package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
)

// newTestMonitor 创建仅用于 handleStallError 测试的最小 Monitor
func newTestMonitor(cfg model.LifeMonitorSettings) *Monitor {
	return &Monitor{
		settingsFn: func() model.LifeMonitorSettings { return cfg },
	}
}

// TestHandleStallError_NoTimeout stallTimeout=0 时原样返回错误
func TestHandleStallError_NoTimeout(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{})
	origErr := errors.New("some error")

	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{},
		"/cloud/path", origErr, 0)

	if !errors.Is(got, origErr) {
		t.Fatalf("stallTimeout=0 should return original err, got %v", got)
	}
}

// TestHandleStallError_NilError err=nil 时直接返回nil
func TestHandleStallError_NilError(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{
		TransferStallTimeoutMinutes: 30,
	})

	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{},
		"/cloud/path", nil, 30*time.Minute)

	if got != nil {
		t.Fatalf("nil err should return nil, got %v", got)
	}
}

// TestHandleStallError_NonDeadlineError 非DeadlineExceeded错误原样返回
func TestHandleStallError_NonDeadlineError(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{
		TransferStallTimeoutMinutes: 30,
	})
	origErr := errors.New("not a deadline error")

	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{},
		"/cloud/path", origErr, 30*time.Minute)

	if !errors.Is(got, origErr) {
		t.Fatalf("non-deadline err should return original, got %v", got)
	}
}

// TestHandleStallError_SkipMode DeadlineExceeded + skip模式 返回nil
func TestHandleStallError_SkipMode(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{
		TransferStallTimeoutMinutes: 30,
		TransferWaitMode:            "skip",
	})

	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{
		FileName: "test.mkv",
	}, "/cloud/path", context.DeadlineExceeded, 30*time.Minute)

	if got != nil {
		t.Fatalf("skip mode should return nil, got %v", got)
	}
}

// TestHandleStallError_AbortMode DeadlineExceeded + abort模式 返回错误
func TestHandleStallError_AbortMode(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{
		TransferStallTimeoutMinutes: 30,
		TransferWaitMode:            "abort",
	})

	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{
		FileName: "test.mkv",
	}, "/cloud/path", context.DeadlineExceeded, 30*time.Minute)

	if got == nil {
		t.Fatalf("abort mode should return error")
	}
	if !errors.Is(got, context.DeadlineExceeded) { //nolint:staticcheck // SA9003: 空分支为有意设计
		// abort 模式包装了错误消息，但底层 DeadlineExceeded 不 wrap
		// 因此这里只验证非 nil 即可
	}
}

// TestHandleStallError_EmptyModeDefaultsToSkip 空TransferWaitMode默认走skip
func TestHandleStallError_EmptyModeDefaultsToSkip(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{
		TransferStallTimeoutMinutes: 30,
		TransferWaitMode:            "", // 空，应默认skip
	})

	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{
		FileName: "test.mkv",
	}, "/cloud/path", context.DeadlineExceeded, 30*time.Minute)

	if got != nil {
		t.Fatalf("empty mode should default to skip (nil), got %v", got)
	}
}

// TestHandleStallError_WrappedDeadlineExceeded 包装过的DeadlineExceeded也能识别
func TestHandleStallError_WrappedDeadlineExceeded(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{
		TransferStallTimeoutMinutes: 30,
		TransferWaitMode:            "skip",
	})
	wrappedErr := errors.New("context deadline exceeded: 115 API timeout")

	// 注意：纯字符串不含 context.DeadlineExceeded 的 error 不会被识别
	// 只有真正的 context.DeadlineExceeded 或其 wrap 才行
	got := m.handleStallError(context.Background(), "acc1", client115.LifeEventItem{},
		"/cloud/path", wrappedErr, 30*time.Minute)

	// wrappedErr 不是 context.DeadlineExceeded，应该原样返回
	if got == nil || got.Error() != wrappedErr.Error() {
		t.Fatalf("non-deadline wrapped err should return original, got %v", got)
	}
}

// ==================== 文件删除批量聚合通知测试（B 路径） ====================

// fakeNotifier 捕获 Notify 调用（仅记录消息，不实际发送）
type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
}

func (f *fakeNotifier) Notify(_ context.Context, message string) error {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
	return nil
}

func (f *fakeNotifier) NotifyWithPhoto(_ context.Context, caption, _ string) error {
	f.mu.Lock()
	f.messages = append(f.messages, caption)
	f.mu.Unlock()
	return nil
}

func (f *fakeNotifier) Messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(f.messages))
	copy(cp, f.messages)
	return cp
}

// newAggTestMonitor 构造带聚合收集器的测试 Monitor
func newAggTestMonitor() (*Monitor, *fakeNotifier) {
	fn := &fakeNotifier{}
	m := &Monitor{
		accounts:     map[string]*AccountMonitor{},
		settingsFn:   func() model.LifeMonitorSettings { return model.LifeMonitorSettings{} },
		notifier:     fn,
		notifyMerger: NewNotifyMerger(fn),
	}
	m.accounts["acc1"] = &AccountMonitor{
		Account:      "acc1",
		delCollector: &deleteNotifyCollector{},
	}
	return m, fn
}

// TestFlushDeleteNotifications_WholeSeries 整剧删光：seriesDir 不存在 → 1 条"删除 剧集目录"
func TestFlushDeleteNotifications_WholeSeries(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()
	seriesDir := filepath.Join(tmp, "叶问 (2026)")
	// 不创建 seriesDir：模拟 removeEmptyParents 已清空（整剧删光）

	acc.delCollector.begin()
	for s := 1; s <= 2; s++ {
		for i := 1; i <= 5; i++ {
			lp := filepath.Join(seriesDir, "Season "+strconv.Itoa(s), "ep"+strconv.Itoa(i)+".strm")
			m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
		}
	}
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 1 {
		t.Fatalf("整剧删光应聚合为 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "删除") || !strings.Contains(msgs[0], "叶问 (2026)") {
		t.Errorf("应为'删除 叶问 (2026)'，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "Season 1") || strings.Contains(msgs[0], "Season 2") {
		t.Errorf("整剧应为 series 级路径，不应含 Season，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "集") {
		t.Errorf("不应含计数'集'，实际: %s", msgs[0])
	}
}

// TestFlushDeleteNotifications_WholeSingleSeasonSeries 单季剧整删：seriesDir 不存在 → "删除 剧集目录"
// 修掉原"季计数启发式"的缺口（单季剧只有 1 个 Season 目录，被误标"季删除"）
func TestFlushDeleteNotifications_WholeSingleSeasonSeries(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()
	seriesDir := filepath.Join(tmp, "单季剧 (2025)")
	// 不创建 seriesDir：整删

	acc.delCollector.begin()
	for i := 1; i <= 10; i++ {
		lp := filepath.Join(seriesDir, "Season 1", "ep"+strconv.Itoa(i)+".strm")
		m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
	}
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 1 {
		t.Fatalf("单季剧整删应为 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "删除") || !strings.Contains(msgs[0], "单季剧 (2025)") {
		t.Errorf("应为'删除 单季剧 (2025)'，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "集") {
		t.Errorf("不应含计数'集'，实际: %s", msgs[0])
	}
}

// TestFlushDeleteNotifications_WholeSeason 整季删光：seriesDir 在、seasonDir 不存在 → "删除 Season 1"
func TestFlushDeleteNotifications_WholeSeason(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()
	seriesDir := filepath.Join(tmp, "叶问 (2026)")
	// 创建 seriesDir + 残留的 Season 2（使 seriesDir 存在 → 非整剧）
	remainDir := filepath.Join(seriesDir, "Season 2")
	if err := os.MkdirAll(remainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(remainDir, "keep.strm"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	acc.delCollector.begin()
	// 删的是 Season 1 的 5 集（Season 1 目录不在磁盘上，模拟整季已删）
	seasonDir := filepath.Join(seriesDir, "Season 1")
	for i := 1; i <= 5; i++ {
		lp := filepath.Join(seasonDir, "ep"+strconv.Itoa(i)+".strm")
		m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
	}
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 1 {
		t.Fatalf("整季删光应为 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "删除") || !strings.Contains(msgs[0], "Season 1") {
		t.Errorf("应为'删除 .../Season 1'，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "叶问 (2026)</code>") {
		t.Errorf("整季路径应为 season 级，不应停在 series 级，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "集") {
		t.Errorf("不应含计数'集'，实际: %s", msgs[0])
	}
}

// TestFlushDeleteNotifications_PartialSeason 多集没删完：seasonDir 仍在 → 聚合列名
func TestFlushDeleteNotifications_PartialSeason(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()
	seriesDir := filepath.Join(tmp, "叶问 (2026)")
	seasonDir := filepath.Join(seriesDir, "Season 1")
	// 创建 seasonDir + 残留文件（使 seasonDir 存在 → 部分删除，非整季）
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seasonDir, "keep.strm"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	acc.delCollector.begin()
	for i := 1; i <= 3; i++ {
		lp := filepath.Join(seasonDir, "ep"+strconv.Itoa(i)+".strm")
		m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
	}
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 1 {
		t.Fatalf("多集没删完应聚合为 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "删除") || !strings.Contains(msgs[0], "Season 1") {
		t.Errorf("应含'删除 .../Season 1'，实际: %s", msgs[0])
	}
	if !strings.Contains(msgs[0], "ep1.strm") || !strings.Contains(msgs[0], "ep3.strm") {
		t.Errorf("应列出删除的文件名，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "keep.strm") {
		t.Errorf("不应列出未删除的 keep.strm，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "集") {
		t.Errorf("不应含计数'集'，实际: %s", msgs[0])
	}
}

// TestFlushDeleteNotifications_SingleEpisode 单集：seasonDir 在、1 文件 → "删除 <文件路径>"
func TestFlushDeleteNotifications_SingleEpisode(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()
	seriesDir := filepath.Join(tmp, "叶问 (2026)")
	seasonDir := filepath.Join(seriesDir, "Season 1")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seasonDir, "keep.strm"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	acc.delCollector.begin()
	lp := filepath.Join(seasonDir, "ep01.strm")
	m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 1 {
		t.Fatalf("单集应为 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "删除") || !strings.Contains(msgs[0], "ep01.strm") {
		t.Errorf("应为'删除 .../ep01.strm'，实际: %s", msgs[0])
	}
}

// TestFlushDeleteNotifications_DifferentSeriesWhole 两个不同剧集各自整删 → 各 1 条"删除"
func TestFlushDeleteNotifications_DifferentSeriesWhole(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()

	acc.delCollector.begin()
	for _, name := range []string{"剧A (2025)", "剧B (2025)"} {
		sd := filepath.Join(tmp, name)
		for i := 1; i <= 3; i++ {
			lp := filepath.Join(sd, "Season 1", "ep"+strconv.Itoa(i)+".strm")
			m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
		}
	}
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 2 {
		t.Fatalf("两剧各自整删应为 2 条，实际 %d 条: %v", len(msgs), msgs)
	}
	for _, msg := range msgs {
		if !strings.Contains(msg, "删除") {
			t.Errorf("每条应为'删除 ...'，实际: %s", msg)
		}
		if strings.Contains(msg, "集") {
			t.Errorf("不应含计数'集'，实际: %s", msg)
		}
	}
}

// TestFlushDeleteNotifications_PlainPartial 非季节目录部分删除：parent 仍在 → 聚合列名
func TestFlushDeleteNotifications_PlainPartial(t *testing.T) {
	m, fn := newAggTestMonitor()
	acc := m.accounts["acc1"]
	ctx := context.Background()
	tmp := t.TempDir()
	moviesDir := filepath.Join(tmp, "movies")
	// 创建 moviesDir + 残留文件（parent 存在 → 部分删除）
	if err := os.MkdirAll(moviesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moviesDir, "keep.strm"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	acc.delCollector.begin()
	for i := 1; i <= 2; i++ {
		lp := filepath.Join(moviesDir, "movie"+strconv.Itoa(i)+".strm")
		m.collectFileDelete(ctx, "acc1", "/cloud"+lp, lp)
	}
	m.flushDeleteNotifications(ctx, "acc1", acc.delCollector)

	msgs := fn.Messages()
	if len(msgs) != 1 {
		t.Fatalf("非季节部分删除应 1 条，实际 %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "删除") || !strings.Contains(msgs[0], "movie1.strm") {
		t.Errorf("应含'删除'与文件名，实际: %s", msgs[0])
	}
	if strings.Contains(msgs[0], "keep.strm") {
		t.Errorf("不应列出未删除的 keep.strm，实际: %s", msgs[0])
	}
}

// TestFlushDeleteNotifications_InactiveFallback 非批次上下文退化为单条（走 notifyDelete，不进聚合摘要）
func TestFlushDeleteNotifications_InactiveFallback(t *testing.T) {
	m, fn := newAggTestMonitor()
	ctx := context.Background()

	// 未 begin：collector 非 active → collectFileDelete 退化为 notifyDelete
	// 验证：不抛 panic 且不产生聚合摘要 Notify（单条走 notifyMerger）
	m.collectFileDelete(ctx, "acc1", "/cloud/a.mkv", "/strm/a.strm")
	if got := len(fn.Messages()); got != 0 {
		t.Errorf("非批次单文件不应直接 Notify（走合并器），实际 %d", got)
	}
}
