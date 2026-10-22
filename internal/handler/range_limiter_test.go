package handler

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestAccountRangeLimiterWaitAndRelease(t *testing.T) {
	l := newAccountRangeLimiter()
	release, err := l.acquire(context.Background(), "acc", 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := l.acquire(ctx, "acc", 1); err == nil {
		t.Fatal("second acquire should time out while first slot is held")
	}
	release()
	release2, err := l.acquire(context.Background(), "acc", 1)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

func TestReleaseReadCloserReleasesOnClose(t *testing.T) {
	released := false
	body := &releaseReadCloser{
		body:    io.NopCloser(strings.NewReader("x")),
		release: func() { released = true },
	}
	_, _ = io.ReadAll(body)
	_ = body.Close()
	if !released {
		t.Fatal("release should run on Close")
	}
}

func TestAccountRangeLimiter_NewLimitUsesFreshPool(t *testing.T) {
	l := newAccountRangeLimiter()

	release1, err := l.acquire(context.Background(), "acc", 1)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// 同一账号同一上限：名额已满，ctx 取消后应立即返回。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.acquire(ctx, "acc", 1); err == nil {
		t.Fatal("same limit should wait for a free slot")
	}

	// 修改上限后必须使用新容量的池，不能被首次请求的 limit 固化。
	release2, err := l.acquire(context.Background(), "acc", 2)
	if err != nil {
		t.Fatalf("new limit should get a fresh pool: %v", err)
	}

	release1()
	release2()

	release3, err := l.acquire(context.Background(), "acc", 1)
	if err != nil {
		t.Fatalf("released slot should be reusable: %v", err)
	}
	release3()
}

func TestAccountRangeLimiter_ReleaseIsIdempotent(t *testing.T) {
	l := newAccountRangeLimiter()
	release, err := l.acquire(context.Background(), "acc", 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release2, err := l.acquire(ctx, "acc", 1)
	if err != nil {
		t.Fatalf("double release must not drain the semaphore: %v", err)
	}
	release2()
}
