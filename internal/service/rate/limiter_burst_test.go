package rate

import (
	"context"
	"testing"
	"time"
)

// resetRegistry 把全局 Registry 恢复到测试可控状态
func resetRegistry(t *testing.T, l Limits) *Registry {
	t.Helper()
	reg := GetRegistry()
	reg.Clear()
	reg.Configure(l)
	t.Cleanup(func() {
		reg.Clear()
		reg.Configure(Limits{
			API115QPM:    RegistryDefaultAPI115QPM,
			PlaybackQPM:  RegistryDefaultPlaybackQPM,
			LifeQPM:      RegistryDefaultLifeQPM,
			GlobalCapQPM: RegistryDefaultGlobalCapQPM,
		})
	})
	return reg
}

func TestNewLimiterBurst(t *testing.T) {
	l := NewLimiterBurst(120, 4)
	if got := l.AvailableTokens(); got != 4 {
		t.Fatalf("burst tokens = %d, want 4", got)
	}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := l.Acquire(ctx); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	// 第 5 次应阻塞（桶已空）
	waitCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	if err := l.Acquire(waitCtx); err == nil {
		t.Fatal("limiter should be exhausted after burst consumed")
	}
}

func TestNewLimiterBurstDefaults(t *testing.T) {
	// burst<=0 时按 qpm/4 推导
	if got := NewLimiterBurst(120, 0).AvailableTokens(); got != 30 {
		t.Fatalf("default burst = %d, want 30", got)
	}
	// burst 超过 qpm 时收敛到 qpm
	if got := NewLimiterBurst(10, 999).AvailableTokens(); got != 10 {
		t.Fatalf("clamped burst = %d, want 10", got)
	}
	// qpm 非法时回落 60
	if got := NewLimiterBurst(0, 0).AvailableTokens(); got != 15 {
		t.Fatalf("fallback burst = %d, want 15", got)
	}
}

// TestPlaybackNotStarvedByScan 是本轮改造的核心回归用例：
// 扫描桶被打满时，播放取直链必须仍能立即拿到令牌。
func TestPlaybackNotStarvedByScan(t *testing.T) {
	reg := resetRegistry(t, Limits{API115QPM: 1, PlaybackQPM: 120, LifeQPM: 60, GlobalCapQPM: 300})
	ctx := context.Background()
	key := AccountKeyFromCookie("UID=10086;CID=x")

	// 打满扫描桶（QPM=1 → burst=1）
	if err := reg.Acquire(ctx, key, TypeAPI115); err != nil {
		t.Fatalf("first scan acquire: %v", err)
	}
	// 扫描桶已空：再取应超时
	scanCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := reg.Acquire(scanCtx, key, TypeAPI115); err == nil {
		t.Fatal("scan bucket should be exhausted")
	}

	// 播放桶独立：应立即拿到
	start := time.Now()
	if err := reg.Acquire(ctx, key, TypePlayback); err != nil {
		t.Fatalf("playback acquire: %v", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("playback acquire took %v, want immediate", d)
	}
}

// TestAccountIsolation 验证不同账号不共享配额
func TestAccountIsolation(t *testing.T) {
	reg := resetRegistry(t, Limits{API115QPM: 1, PlaybackQPM: 120, LifeQPM: 60, GlobalCapQPM: 300})
	ctx := context.Background()

	a := AccountKeyFromCookie("UID=aaa;CID=x")
	b := AccountKeyFromCookie("UID=bbb;CID=x")
	if a == b {
		t.Fatalf("account keys should differ: %q", a)
	}

	if err := reg.Acquire(ctx, a, TypeAPI115); err != nil {
		t.Fatalf("account a first acquire: %v", err)
	}
	// a 的桶已空，b 仍应有令牌
	start := time.Now()
	if err := reg.Acquire(ctx, b, TypeAPI115); err != nil {
		t.Fatalf("account b acquire: %v", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("account b acquire took %v, want immediate", d)
	}
}

// TestGlobalCap 验证全进程兜底上限生效
func TestGlobalCap(t *testing.T) {
	reg := resetRegistry(t, Limits{API115QPM: 60, PlaybackQPM: 60, LifeQPM: 60, GlobalCapQPM: 5})
	ctx := context.Background()

	// 兜底桶 burst = 5/5 = 1，先消耗完
	used := 0
	for i := 0; i < 10; i++ {
		// 用不同账号绕过分桶限制，只考察全局桶
		key := AccountKeyFromCookie("UID=" + string(rune('a'+i)) + ";")
		c, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		err := reg.Acquire(c, key, TypeAPI115)
		cancel()
		if err != nil {
			break
		}
		used++
	}
	if used == 0 || used >= 10 {
		t.Fatalf("global cap not effective, allowed=%d", used)
	}
}

// TestConfigureDisablesGlobalCap 验证负数/0 语义
func TestConfigureDisablesGlobalCap(t *testing.T) {
	reg := resetRegistry(t, Limits{GlobalCapQPM: 0})
	if lim := reg.globalCapLimiter(); lim != nil {
		t.Fatal("global cap should be disabled when GlobalCapQPM=0")
	}
	reg.Configure(Limits{GlobalCapQPM: 100})
	if lim := reg.globalCapLimiter(); lim == nil {
		t.Fatal("global cap should be enabled after Configure")
	}
}
