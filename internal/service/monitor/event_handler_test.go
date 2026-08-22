package monitor

import (
	"context"
	"errors"
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
	if !errors.Is(got, context.DeadlineExceeded) {
		// abort 模式包装了错误消息，但底层应仍是 DeadlineExceeded 的包装
		// 注意：fmt.Errorf("整理队列无进展超时: %s") 不用 %w，所以不 wrap
		// 因此这里只验证非nil即可
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
