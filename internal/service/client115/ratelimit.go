// Package client115 115 网盘 API 客户端
// ratelimit.go API 冷却限制器：防止频繁调用触发风控
package client115

import (
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// APIRateLimiter API 冷却限制器
// 保证同一账号的 API 调用之间至少间隔 minInterval
type APIRateLimiter struct {
	mu          sync.Mutex
	lastCall    time.Time     // 上次调用时间
	minInterval time.Duration // 最小调用间隔
	callCount   int64         // 调用计数
	blocked     int64         // 被阻塞次数
}

// NewAPIRateLimiter 创建限流器
// minInterval: 最小调用间隔，建议 >= 1s
func NewAPIRateLimiter(minInterval time.Duration) *APIRateLimiter {
	if minInterval <= 0 {
		minInterval = 1 * time.Second
	}
	return &APIRateLimiter{
		minInterval: minInterval,
	}
}

// Wait 阻塞等待直到可以调用 API
// 如果距离上次调用不足 minInterval，则 sleep 剩余时间
func (rl *APIRateLimiter) Wait() {
	if rl == nil {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastCall)

	if elapsed < rl.minInterval {
		remaining := rl.minInterval - elapsed
		rl.blocked++
		rl.mu.Unlock()
		time.Sleep(remaining)
		rl.mu.Lock()
	}

	rl.lastCall = now
	rl.callCount++
}

// TryAcquire 尝试获取调用权（非阻塞）
// 如果距离上次调用已超过 minInterval，立即返回 true 并记录时间
// 否则返回 false
func (rl *APIRateLimiter) TryAcquire() bool {
	if rl == nil {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.lastCall) >= rl.minInterval {
		rl.lastCall = now
		rl.callCount++
		return true
	}

	rl.blocked++
	return false
}

// UpdateInterval 动态调整冷却间隔
func (rl *APIRateLimiter) UpdateInterval(newInterval time.Duration) {
	if rl == nil || newInterval <= 0 {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.minInterval = newInterval
	logger.S().Debugf("[RateLimiter] 冷却间隔更新为: %v", newInterval)
}

// Stats 返回限流器统计信息
func (rl *APIRateLimiter) Stats() (callCount, blocked int64, minInterval time.Duration) {
	if rl == nil {
		return 0, 0, 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.callCount, rl.blocked, rl.minInterval
}
