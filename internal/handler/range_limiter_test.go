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
