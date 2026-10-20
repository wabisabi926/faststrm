package handler

import (
	"testing"
	"time"
)

func TestLoginGuardBlocksAfterFailures(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	g := newLoginGuard()
	g.now = func() time.Time { return now }
	key := "1.2.3.4"

	for i := 0; i < loginMaxFailures; i++ {
		ok, _ := g.allow(key)
		if !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		g.fail(key)
	}
	ok, retry := g.allow(key)
	if ok {
		t.Fatal("should be blocked after max failures")
	}
	if retry <= 0 || retry > loginBlockFor {
		t.Fatalf("unexpected retry duration: %v", retry)
	}

	// 阻止窗口结束后允许一次新尝试
	now = now.Add(loginBlockFor + time.Second)
	if ok, _ := g.allow(key); !ok {
		t.Fatal("should be allowed after block expires")
	}
}

func TestLoginGuardResetsOnSuccess(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	g := newLoginGuard()
	g.now = func() time.Time { return now }
	key := "1.2.3.4"

	for i := 0; i < loginMaxFailures-1; i++ {
		g.fail(key)
	}
	g.reset(key)
	for i := 0; i < loginMaxFailures; i++ {
		ok, _ := g.allow(key)
		if !ok {
			t.Fatalf("attempt %d should be allowed after reset", i+1)
		}
		g.fail(key)
	}
}
