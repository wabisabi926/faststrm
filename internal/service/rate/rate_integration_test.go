package rate

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 阶段 4 集成测试：令牌桶 + 并发数限流器 + Registry 联动
// 覆盖：
//  1. Limiter：桶初始满令牌；获取 N 次后 第 N+1 次阻塞/超时；令牌 refill 生效后可继续获取
//  2. Bottleneck：并发=3，4 个 goroutine 同时 Enter → 至少 1 个阻塞；Running() 观测并发数
//  3. Registry：按 account+type 维度懒创建 + 复用 + SetDefaults 对新实例生效
func TestPhase4_RateIntegration(t *testing.T) {
	t.Run("Limiter: 60 QPM bucket → 60 immediate Acquires, 61st times out, then refill works", func(t *testing.T) {
		lim := NewLimiter(60)
		ctx := context.Background()

		// 初始应有 60 令牌
		if n := lim.AvailableTokens(); n != 60 {
			t.Fatalf("initial tokens want 60, got %d", n)
		}

		// 60 次立即成功
		for i := 0; i < 60; i++ {
			if err := lim.Acquire(ctx); err != nil {
				t.Fatalf("Acquire[%d] err: %v", i, err)
			}
		}
		if n := lim.AvailableTokens(); n != 0 {
			t.Fatalf("after 60 Acquire, tokens want 0, got %d", n)
		}

		// 第 61 次：短 ctx，等待后取消成功
		shortCtx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
		defer cancel()
		if err := lim.Acquire(shortCtx); err != context.DeadlineExceeded {
			t.Fatalf("61st Acquire want DeadlineExceeded, got %v", err)
		}

		// 等约 1.1s：refillRate = 1/s → 可用 ≥ 1 token
		time.Sleep(1100 * time.Millisecond)
		if n := lim.AvailableTokens(); n < 1 {
			t.Errorf("after 1.1s want >=1 token, got %d", n)
		}
		// 再获取应成功
		if err := lim.Acquire(ctx); err != nil {
			t.Fatalf("after refill Acquire err: %v", err)
		}
	})

	t.Run("Bottleneck(3): 4 goroutines → Running never exceeds 3", func(t *testing.T) {
		bn := NewBottleneck(3)
		var maxRunning int32
		var counter int32
		var wg sync.WaitGroup

		run := func() {
			defer wg.Done()
			ctx := context.Background()
			if err := bn.Enter(ctx); err != nil {
				return
			}
			defer bn.Leave()
			cur := int32(bn.Running())
			for {
				old := atomic.LoadInt32(&maxRunning)
				if cur <= old || atomic.CompareAndSwapInt32(&maxRunning, old, cur) {
					break
				}
			}
			atomic.AddInt32(&counter, 1)
			// 占用一小段时间，让观测更稳定
			time.Sleep(40 * time.Millisecond)
		}

		for i := 0; i < 4; i++ {
			wg.Add(1)
			go run()
		}
		wg.Wait()

		if counter != 4 {
			t.Errorf("want 4 workers complete, got %d", counter)
		}
		if max := atomic.LoadInt32(&maxRunning); max > 3 {
			t.Errorf("Max Running observed %d > limit 3", max)
		}
		// TryEnter：4 个已 Leave → 现在应全空，但 TryEnter 成功后 Running=1
		if !bn.TryEnter() {
			t.Fatalf("TryEnter should succeed after all Leave")
		}
		if bn.Running() != 1 {
			t.Errorf("Running after TryEnter want 1, got %d", bn.Running())
		}
		bn.Leave()
		if bn.Running() != 0 {
			t.Errorf("Running after Leave want 0, got %d", bn.Running())
		}
	})

	t.Run("Registry: per-account-type lazy create + reuse + SetDefaults", func(t *testing.T) {
		// 每个测试独立创建一个 Registry（避免全局单例污染，虽然 GetRegistry 是全局，但这里用 &Registry{} 构造本地实例更可控）
		r := &Registry{
			limiters:           make(map[string]*Limiter),
			bottlenecks:        make(map[string]*Bottleneck),
			api115QPM:          RegistryDefaultAPI115QPM,
			downloadQPM:        RegistryDefaultDownloadQPM,
			downloadConcurrent: RegistryDefaultDownloadConcurrent,
		}

		// 相同 (account,type) → 同一个 Limiter
		l1 := r.GetLimiter("u1", TypeAPI115)
		l2 := r.GetLimiter("u1", TypeAPI115)
		if l1 != l2 {
			t.Fatalf("same (u1,api115) want same Limiter")
		}
		l3 := r.GetLimiter("u1", TypeDownload)
		if l1 == l3 {
			t.Errorf("different type should NOT be same Limiter")
		}

		// SetDefaults → 新 account 应该拿新默认 api115QPM=30
		r.SetDefaults(30, 0, 2)
		lNew := r.GetLimiter("u_new", TypeAPI115)
		if n := lNew.AvailableTokens(); n != 30 {
			t.Errorf("new api115 QPM after SetDefaults want 30, got %d", n)
		}

		// Bottleneck：相同 (u2,proxy) → 同一个；新默认 concurrent=2
		b1 := r.GetBottleneck("u2", TypeProxy, 0)
		b2 := r.GetBottleneck("u2", TypeProxy, 0)
		if b1 != b2 {
			t.Fatalf("same Bottleneck expect reuse")
		}
		if b1.Limit() != 8 {
			// TypeProxy 默认 8（registry 中写死，不读 downloadConcurrent）
			t.Errorf("Proxy bottleneck concurrent want 8, got %d", b1.Limit())
		}
		// TypeDownload 用 SetDefaults 的 downloadConcurrent=2
		bd := r.GetBottleneck("u2", TypeDownload, 0)
		if bd.Limit() != 2 {
			t.Errorf("Download bottleneck concurrent want 2 after SetDefaults, got %d", bd.Limit())
		}
		// overrideLimit 直接覆盖
		bc := r.GetBottleneck("u_override", TypeDownload, 15)
		if bc.Limit() != 15 {
			t.Errorf("overrideLimit want 15, got %d", bc.Limit())
		}

		// 并发安全：多 goroutine 抢不同 (acct,type) 不 panic
		types := []LimiterType{TypeAPI115, TypeDownload, TypeProxy}
		var pwg sync.WaitGroup
		for g := 0; g < 4; g++ {
			pwg.Add(1)
			go func(gi int) {
				defer pwg.Done()
				for i := 0; i < 200; i++ {
					acct := "acct" + string(rune('A'+(i%6)))
					tp := types[i%len(types)]
					_ = r.GetLimiter(acct, tp)
					_ = r.GetBottleneck(acct, tp, 0)
				}
			}(g)
		}
		pwg.Wait()
	})
}
