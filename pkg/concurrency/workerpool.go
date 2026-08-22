// Package concurrency 提供统一的 IO Worker Pool，保证批量任务的并发上限可控。
//
// 典型用法（task/executor STRM 写）：
//
//	pool := concurrency.NewPool(workers, 0)
//	for _, f := range items {
//		f := f
//		pool.Submit(func() { doWork(f) })
//	}
//	pool.Wait()
//	if firstErr := pool.FirstErr(); firstErr != nil { ... }
//
// P2-1 目标：将 executor.go 中重复的 sem+wg+goroutine 模板，
// 清理/monitor 中的批量并发任务，统一收敛到 WorkerPool。
package concurrency

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Pool 是一个并发控制池：Submit 的任务会在最多 maxWorkers 个并发 goroutine 中执行。
// 零值不可用，请用 NewPool。
type Pool struct {
	sem       chan struct{}
	wg        sync.WaitGroup
	firstErr  error
	errMu     sync.Mutex
	completed int64 // 已完成任务数（含失败），atomic
}

// Option Pool 选项（为未来扩展预留）
type Option func(*Pool)

// NewPool 创建一个 WorkerPool。
//   - maxWorkers <= 0 时默认 1（顺序执行）。
//   - options 预留，目前可传 0 个。
func NewPool(maxWorkers int, options ...Option) *Pool {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	p := &Pool{
		sem: make(chan struct{}, maxWorkers),
	}
	for _, opt := range options {
		opt(p)
	}
	return p
}

// Submit 提交一个任务。若池已满，本调用会阻塞到有 worker 空闲。
// 任务返回 error 非 nil 时会被记录（仅记录 FirstErr，不中断后续任务）。
// fn 内部 panic 会被 recover 并记录为 error（防止池整体崩溃）。
func (p *Pool) Submit(fn func() error) {
	p.wg.Add(1)
	p.sem <- struct{}{} // acquire
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }() // release
		defer atomic.AddInt64(&p.completed, 1)
		defer func() {
			if r := recover(); r != nil {
				p.setErrOnce(recoveredToErr(r))
			}
		}()
		if err := fn(); err != nil {
			p.setErrOnce(err)
		}
	}()
}

// Wait 等待所有已 Submit 的任务执行完毕。
func (p *Pool) Wait() {
	p.wg.Wait()
}

// FirstErr 返回首个遇到的错误（无错为 nil）。应在 Wait 之后调用。
func (p *Pool) FirstErr() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.firstErr
}

// Completed 返回已经结束的任务数（成功+失败），在 Wait 后通常等于 Submit 次数。
func (p *Pool) Completed() int64 {
	return atomic.LoadInt64(&p.completed)
}

func (p *Pool) setErrOnce(err error) {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if p.firstErr == nil {
		p.firstErr = err
	}
}

// recoveredToErr 把 recover() 结果转成 error（避免外部额外依赖）
func recoveredToErr(r any) error {
	switch x := r.(type) {
	case nil:
		return nil
	case error:
		return x
	default:
		return &recoveredPanic{v: r}
	}
}

type recoveredPanic struct{ v any }

func (e *recoveredPanic) Error() string { return "worker panic: " + sprintAny(e.v) }

// sprintAny 尽量不依赖额外重型工具，使用 fmt.Sprintf 输出可打印字符串
func sprintAny(v any) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fmt.Sprintf("%v", v)
}
