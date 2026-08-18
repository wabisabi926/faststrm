// Package rate 限流体系：令牌桶 + 并发数限制 + 按账号注册
// 对齐 frontend/src/lib/rateLimiter.ts
package rate

import (
	"context"
	"sync"
	"time"
)

// Limiter 令牌桶限流器（线程安全）
type Limiter struct {
	mu             sync.Mutex
	tokens         float64
	maxTokens      float64
	refillRate     float64 // 每秒补充的令牌数
	lastRefillTime time.Time
	waitQueue      []chan struct{}
}

// NewLimiter 创建令牌桶限流器
// maxTokensPerMinute: 每分钟最大令牌数（如 60 = 每秒 1 次）
func NewLimiter(maxTokensPerMinute int) *Limiter {
	if maxTokensPerMinute <= 0 {
		maxTokensPerMinute = 60
	}
	return &Limiter{
		tokens:         float64(maxTokensPerMinute),
		maxTokens:      float64(maxTokensPerMinute),
		refillRate:     float64(maxTokensPerMinute) / 60.0,
		lastRefillTime: time.Now(),
	}
}

func (l *Limiter) refillLocked() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefillTime).Seconds()
	if elapsed > 0 {
		l.tokens = min(l.maxTokens, l.tokens+elapsed*l.refillRate)
		l.lastRefillTime = now
	}
}

// Acquire 获取一个令牌，若不足则阻塞等待直到获取成功或 ctx 取消
func (l *Limiter) Acquire(ctx context.Context) error {
	l.mu.Lock()
	l.refillLocked()
	if l.tokens >= 1 {
		l.tokens -= 1
		l.mu.Unlock()
		return nil
	}
	// 需要等待
	ch := make(chan struct{})
	l.waitQueue = append(l.waitQueue, ch)
	l.mu.Unlock()

	// 启动后台检查协程（若尚未启动则懒启动）
	go l.scheduleRefill()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		// ctx 取消时从等待队列中移除（懒清理：被消费时发现已关闭就跳过）
		l.mu.Lock()
		for i, w := range l.waitQueue {
			if w == ch {
				l.waitQueue = append(l.waitQueue[:i], l.waitQueue[i+1:]...)
				break
			}
		}
		l.mu.Unlock()
		close(ch)
		return ctx.Err()
	}
}

// AvailableTokens 获取当前可用令牌数
func (l *Limiter) AvailableTokens() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refillLocked()
	return int(l.tokens)
}

func (l *Limiter) scheduleRefill() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		<-ticker.C
		l.mu.Lock()
		l.refillLocked()
		// 消费等待队列
		for l.tokens >= 1 && len(l.waitQueue) > 0 {
			l.tokens -= 1
			ch := l.waitQueue[0]
			l.waitQueue = l.waitQueue[1:]
			select {
			case ch <- struct{}{}:
				// 成功唤醒
			default:
				// channel 已关闭（ctx 取消），跳过
			}
		}
		hasWaiters := len(l.waitQueue) > 0
		l.mu.Unlock()
		if !hasWaiters {
			return
		}
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
