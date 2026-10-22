package handler

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// rangeAcquireTimeout 同账号 Range 名额占满时的最长等待时间。
// 不能让播放请求无限等待，否则客户端/ffmpeg 会一直转圈。
const rangeAcquireTimeout = 10 * time.Second

// accountRangeLimiter 限制同一账号同时向 CDN 发起的 Range 请求数。
//
// 为什么要独立于“代理流并发”：一路代理流还会再开 N 个分片请求，
// 8 路流 × 2 分片就是 16+ 个 CDN 连接，容易触发 115 风控。
// 这里按账号共享一个池，和 LitePan 的 account_range_limiter 同思路。
type accountRangeLimiter struct {
	mu       sync.Mutex
	accounts map[string]chan struct{}
}

func newAccountRangeLimiter() *accountRangeLimiter {
	return &accountRangeLimiter{accounts: make(map[string]chan struct{})}
}

func (l *accountRangeLimiter) acquire(ctx context.Context, account string, limit int) (func(), error) {
	if l == nil || account == "" || limit <= 0 {
		return func() {}, nil
	}
	if limit > 8 {
		limit = 8
	}
	l.mu.Lock()
	sem := l.accounts[account]
	if sem == nil {
		sem = make(chan struct{}, limit)
		l.accounts[account] = sem
	}
	l.mu.Unlock()

	timer := time.NewTimer(rangeAcquireTimeout)
	defer timer.Stop()
	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-sem }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("同账号 Range 并发名额已满（上限 %d）", limit)
	}
}

var sharedAccountRangeLimiter = newAccountRangeLimiter()

// releaseReadCloser 在响应体关闭时归还账号 Range 名额。
type releaseReadCloser struct {
	body    io.ReadCloser
	release func()
	once    sync.Once
}

func (r *releaseReadCloser) Read(p []byte) (int, error) { return r.body.Read(p) }

func (r *releaseReadCloser) Close() error {
	err := r.body.Close()
	r.once.Do(r.release)
	return err
}
