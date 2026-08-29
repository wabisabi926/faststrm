package monitor

import (
	"errors"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// TestApplyBackoffLocked_Disabled_NoBackoffUntil 退避已禁用：
// 任意连续失败次数都不应设置 backoffUntil，保证轮询按配置间隔执行、STRM 生成不被延迟。
// 若未来有人恢复阶梯退避，本测试应失败以提醒行为变更。
func TestApplyBackoffLocked_Disabled_NoBackoffUntil(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{})
	// 覆盖原 v1.1.5 阶梯边界：1-2(原不退避) / 3-5(原2min) / 6-9(原10min) / >=10(原30min)
	for _, n := range []int{1, 2, 3, 5, 6, 9, 10, 20, 100} {
		acc := &AccountMonitor{Account: "acc1", consecutiveErrors: n}
		m.applyBackoffLocked(acc)
		if acc.backoffUntil != 0 {
			t.Errorf("consecutiveErrors=%d: 退避已禁用，backoffUntil 应保持 0，实际 %d", n, acc.backoffUntil)
		}
	}
}

// TestHandlePollError_CookieInvalidNotifyOnce cookie 失效通知去重：
// 连续认证失败达阈值后只发 1 条通知（cookieMarkedInvalid 去重闸）；
// 之后继续失败不重发；VerifyAccount 成功（resetConsecutiveFailures）清零后才允许下次失效再发 1 条。
//
// 注：oncePoll 成功路径不清零 cookieMarkedInvalid 的逻辑因依赖真实 115 API
// (lifeClient.PullEvents 为具体类型无法 mock) 不能直接单测；本测试覆盖 handlePollError
// 去重闸——它与 oncePoll 不清零共同保证「通知只发一次，直到验证成功」。
func TestHandlePollError_CookieInvalidNotifyOnce(t *testing.T) {
	m, fn := newAggTestMonitor()
	authErr := errors.New("未登录") // 命中 isAuthError 的 "未登录" 模式

	// 连续 3 次认证错误：第 3 次触发标记 + 发 1 条通知（阈值 consecutiveFailures>=3）
	for i := 0; i < 3; i++ {
		m.handlePollError("acc1", authErr)
	}
	acc := m.accounts["acc1"]
	if !acc.cookieMarkedInvalid {
		t.Fatalf("连续 3 次认证错误后应标记 cookieMarkedInvalid=true")
	}
	if msgs := fn.Messages(); len(msgs) != 1 {
		t.Fatalf("cookie 失效应只发 1 条通知，实际 %d 条: %v", len(msgs), msgs)
	}

	// 继续失败：去重闸生效，不应重发，cookieMarkedInvalid 保持 true（运行时不清零）
	for i := 0; i < 5; i++ {
		m.handlePollError("acc1", authErr)
	}
	if msgs := fn.Messages(); len(msgs) != 1 {
		t.Fatalf("去重闸生效后不应重发，应仍为 1 条，实际 %d 条", len(msgs))
	}
	if !acc.cookieMarkedInvalid {
		t.Fatalf("cookieMarkedInvalid 应保持 true（运行时不清零）")
	}

	// VerifyAccount 成功：resetConsecutiveFailures 清零去重闸，允许下次失效再发 1 条
	m.resetConsecutiveFailures("acc1")
	if acc.cookieMarkedInvalid {
		t.Fatalf("resetConsecutiveFailures 应清零 cookieMarkedInvalid")
	}
	// 再次连续 3 次认证错误：应再发 1 条（累计 2 条）
	for i := 0; i < 3; i++ {
		m.handlePollError("acc1", authErr)
	}
	if msgs := fn.Messages(); len(msgs) != 2 {
		t.Fatalf("验证成功后再次失效应发第 2 条通知，实际 %d 条", len(msgs))
	}
}
