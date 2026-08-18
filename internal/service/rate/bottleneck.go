package rate

import (
	"context"
	"sync"
)

// Bottleneck 并发数限制器（信号量模式）
// 对齐 TS 的 Bottleneck：限制同时进行中的任务数量
type Bottleneck struct {
	mu      sync.Mutex
	sem     chan struct{}
	running int
	limit   int
}

// NewBottleneck 创建并发数限制器
// limit: 最大并发数（<=0 时默认 8）
func NewBottleneck(limit int) *Bottleneck {
	if limit <= 0 {
		limit = 8
	}
	return &Bottleneck{
		sem:   make(chan struct{}, limit),
		limit: limit,
	}
}

// Enter 获取一个并发槽位，阻塞直到成功或 ctx 取消
func (b *Bottleneck) Enter(ctx context.Context) error {
	select {
	case b.sem <- struct{}{}:
		b.mu.Lock()
		b.running++
		b.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Leave 释放一个并发槽位
func (b *Bottleneck) Leave() {
	b.mu.Lock()
	if b.running > 0 {
		b.running--
	}
	b.mu.Unlock()
	<-b.sem
}

// Running 当前正在运行的任务数
func (b *Bottleneck) Running() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// Limit 并发上限
func (b *Bottleneck) Limit() int {
	return b.limit
}

// TryEnter 非阻塞尝试获取槽位，成功返回 true
func (b *Bottleneck) TryEnter() bool {
	select {
	case b.sem <- struct{}{}:
		b.mu.Lock()
		b.running++
		b.mu.Unlock()
		return true
	default:
		return false
	}
}
