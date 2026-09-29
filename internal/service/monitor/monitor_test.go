package monitor

import (
	"errors"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
)

// expectedBackoffBase 复刻 applyBackoffLocked 的基数计算（不含抖动），用于校验区间。
func expectedBackoffBase(n int) time.Duration {
	d := backoffBaseDelay
	for i := 1; i < n && d < backoffMaxDelay; i++ {
		d *= 2
	}
	if d > backoffMaxDelay {
		d = backoffMaxDelay
	}
	return d
}

// TestApplyBackoffLocked_ExponentialCapped 退避恢复（P0-2）：
// consecutiveErrors=0 不设退避；失败次数越多退避越长（指数 ×2^n），
// 退避值落在 [base, base+base/2) 抖动区间内，且不超过上限（含抖动）。
func TestApplyBackoffLocked_ExponentialCapped(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{})

	acc0 := &AccountMonitor{Account: "acc1", consecutiveErrors: 0}
	m.applyBackoffLocked(acc0)
	if acc0.backoffUntil != 0 {
		t.Fatalf("consecutiveErrors=0 不应设置退避，实际 %d", acc0.backoffUntil)
	}

	for _, n := range []int{1, 2, 3, 5, 9, 10, 20, 100} {
		now := time.Now()
		acc := &AccountMonitor{Account: "acc1", consecutiveErrors: n}
		m.applyBackoffLocked(acc)
		if acc.backoffUntil == 0 {
			t.Fatalf("consecutiveErrors=%d 应设置退避", n)
		}
		got := time.UnixMilli(acc.backoffUntil).Sub(now)
		base := expectedBackoffBase(n)
		// 退避 = base + [0, base/2)
		if got < base {
			t.Errorf("consecutiveErrors=%d: 退避 %v 小于基数 %v", n, got, base)
		}
		if upper := base + base/2; got >= upper {
			t.Errorf("consecutiveErrors=%d: 退避 %v 超过抖动上界 %v", n, got, upper)
		}
		if got > backoffMaxDelay+backoffMaxDelay/2 {
			t.Errorf("consecutiveErrors=%d: 退避 %v 超过全局上限(含抖动)", n, got)
		}
	}
}

// TestResetConsecutiveFailures_ClearsBackoff 成功/重置后应清零退避状态。
func TestResetConsecutiveFailures_ClearsBackoff(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{})
	m.accounts = map[string]*AccountMonitor{
		"acc1": {Account: "acc1", consecutiveErrors: 5, backoffUntil: time.Now().Add(time.Minute).UnixMilli()},
	}
	m.resetConsecutiveFailures("acc1")
	acc := m.accounts["acc1"]
	if acc.consecutiveErrors != 0 || acc.backoffUntil != 0 {
		t.Fatalf("resetConsecutiveFailures 应清零退避状态，实际 consecutiveErrors=%d backoffUntil=%d",
			acc.consecutiveErrors, acc.backoffUntil)
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

// TestIsRateLimitError 风控/限流关键词识别（P0-3），需区别于 cookie 失效。
func TestIsRateLimitError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"HTTP 429: 请求过于频繁", true},
		{"too many requests", true},
		{"Rate Limit exceeded", true},
		{"访问频繁，请稍后再试", true},
		{"系统繁忙", true},
		{"疑似触发风控，需验证码", true},
		{"未登录", false},
		{"cookie 已失效", false},
		{"login expired", false},
		{"connection reset by peer", false},
	}
	for _, c := range cases {
		if got := isRateLimitError(errors.New(c.msg)); got != c.want {
			t.Errorf("isRateLimitError(%q)=%v, want %v", c.msg, got, c.want)
		}
	}
	if isRateLimitError(nil) {
		t.Error("isRateLimitError(nil) 应为 false")
	}
}

// TestHandlePollError_RateLimit_NotMarkCookieInvalid 风控/限流不应误判为 cookie 失效：
// 应累计 rateLimitHits、设置冷却退避、不累加 consecutiveFailures、不标记 cookieMarkedInvalid，
// 且告警受 30min 独立节流（多次连续风控只发 1 条）。
func TestHandlePollError_RateLimit_NotMarkCookieInvalid(t *testing.T) {
	m, fn := newAggTestMonitor()
	rateErr := errors.New("HTTP 429: 请求过于频繁")

	for i := 0; i < 5; i++ {
		m.handlePollError("acc1", rateErr)
	}

	acc := m.accounts["acc1"]
	if acc.cookieMarkedInvalid {
		t.Fatalf("风控/限流不应标记 cookie 失效")
	}
	if acc.consecutiveFailures != 0 {
		t.Fatalf("风控/限流不应累加认证失败计数，实际 %d", acc.consecutiveFailures)
	}
	if acc.rateLimitHits != 5 {
		t.Fatalf("rateLimitHits 应累计为 5，实际 %d", acc.rateLimitHits)
	}
	if acc.backoffUntil == 0 {
		t.Fatalf("风控/限流应设置冷却退避")
	}
	if msgs := fn.Messages(); len(msgs) != 1 {
		t.Fatalf("风控告警应受独立节流，仅 1 条，实际 %d 条: %v", len(msgs), msgs)
	}
}

// TestApplyRateLimitCooldownLocked 冷却退避强度应高于普通退避，且不超过上限（含抖动）。
func TestApplyRateLimitCooldownLocked(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{})
	now := time.Now()
	acc := &AccountMonitor{Account: "acc1", rateLimitHits: 1}
	m.applyRateLimitCooldownLocked(acc)
	got := time.UnixMilli(acc.backoffUntil).Sub(now)
	if got < rateLimitBaseDelay || got >= rateLimitBaseDelay+rateLimitBaseDelay/2 {
		t.Fatalf("首次风控冷却应落在 [%v, %v)，实际 %v",
			rateLimitBaseDelay, rateLimitBaseDelay+rateLimitBaseDelay/2, got)
	}
	// 达到上限后含抖动不应超过上限的 1.5 倍
	acc2 := &AccountMonitor{Account: "acc1", rateLimitHits: 100}
	m.applyRateLimitCooldownLocked(acc2)
	got2 := time.UnixMilli(acc2.backoffUntil).Sub(now)
	if got2 < rateLimitMaxDelay || got2 > rateLimitMaxDelay+rateLimitMaxDelay/2 {
		t.Fatalf("多次风控冷却应封顶在 [%v, %v]，实际 %v",
			rateLimitMaxDelay, rateLimitMaxDelay+rateLimitMaxDelay/2, got2)
	}
	if got2 <= got {
		t.Fatalf("冷却应随风控次数增长，首次=%v 封顶=%v", got, got2)
	}
}

// TestResetConsecutiveFailures_ClearsRateLimit 重置应同时清零风控计数。
func TestResetConsecutiveFailures_ClearsRateLimit(t *testing.T) {
	m := newTestMonitor(model.LifeMonitorSettings{})
	m.accounts = map[string]*AccountMonitor{
		"acc1": {Account: "acc1", rateLimitHits: 4, backoffUntil: time.Now().Add(time.Minute).UnixMilli()},
	}
	m.resetConsecutiveFailures("acc1")
	if acc := m.accounts["acc1"]; acc.rateLimitHits != 0 {
		t.Fatalf("resetConsecutiveFailures 应清零 rateLimitHits，实际 %d", acc.rateLimitHits)
	}
}
