package monitor

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// TestStatus_CopiesAllFields 回归：Status() 必须把内部状态的全部对外字段拷贝出来，
// 否则接口返回 fromId/fromTime 等恒为 0（前端游标与退避状态显示错误）。
func TestStatus_CopiesAllFields(t *testing.T) {
	m := &Monitor{
		settingsFn: func() model.LifeMonitorSettings {
			return model.LifeMonitorSettings{Accounts: []string{"acc1", "acc2"}}
		},
		accounts: map[string]*AccountMonitor{
			"acc1": {
				Account:             "acc1",
				Running:             true,
				LastPollTime:        111,
				EventCount:          7,
				fromID:              222,
				fromTime:            333,
				lastErr:             "boom",
				backoffUntil:        444,
				consecutiveErrors:   2,
				consecutiveFailures: 1,
				cookieMarkedInvalid: true,
			},
		},
	}

	got := m.Status()
	s, ok := got["acc1"]
	if !ok {
		t.Fatal("缺少 acc1 状态")
	}
	if s.Account != "acc1" || !s.Running || s.LastPollTime != 111 || s.EventCount != 7 || s.Error != "boom" {
		t.Fatalf("基础字段未拷贝: %+v", s)
	}
	if s.FromID != 222 || s.FromTime != 333 {
		t.Fatalf("游标字段未拷贝: fromID=%d fromTime=%d", s.FromID, s.FromTime)
	}
	if s.BackoffUntil != 444 || s.ConsecutiveErrors != 2 || s.ConsecutiveFailures != 1 || !s.CookieMarkedInvalid {
		t.Fatalf("异常/退避字段未拷贝: %+v", s)
	}

	// 未启动的账号：只返回 Account + Running=false，其余零值
	s2, ok := got["acc2"]
	if !ok || s2.Running || s2.FromID != 0 || s2.EventCount != 0 {
		t.Fatalf("未启动账号状态错误: %+v", s2)
	}
}
