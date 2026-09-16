package monitor

import (
	"testing"
	"time"
)

// ======================================================================
// intentSuppressor 单元测试（P3-4：文件夹递归意图互斥）
// 直接结构体构造，避免 newIntentSuppressor 启动 cleanupLoop goroutine 泄漏
// ======================================================================

func newTestIntent(ttl time.Duration) *intentSuppressor {
	return &intentSuppressor{
		m:   make(map[string]int64),
		ttl: ttl,
	}
}

// TestIntentSuppressor_OneShotConsume Mark 后首次 Consume 命中并移除、二次未命中（一次性消费）
func TestIntentSuppressor_OneShotConsume(t *testing.T) {
	s := newTestIntent(6 * time.Hour)
	s.Mark("100001")
	if !s.Consume("100001") {
		t.Fatal("Mark 后首次 Consume 应命中")
	}
	if s.Consume("100001") {
		t.Fatal("一次性消费：二次 Consume 不应再命中")
	}
}

// TestIntentSuppressor_UnMarkedNotConsumed 未 Mark 的文件 Consume 返回 false
func TestIntentSuppressor_UnMarkedNotConsumed(t *testing.T) {
	s := newTestIntent(6 * time.Hour)
	if s.Consume("999999") {
		t.Fatal("未 Mark 的文件不应命中")
	}
	if s.Stats() != 0 {
		t.Fatalf("空实例 Stats 应为 0, got %d", s.Stats())
	}
}

// TestIntentSuppressor_EmptyFileID 空 fileID 的 Mark/Consume 均为无操作
func TestIntentSuppressor_EmptyFileID(t *testing.T) {
	s := newTestIntent(6 * time.Hour)
	s.Mark("")
	if s.Consume("") {
		t.Fatal("空 fileID 不应命中")
	}
}

// TestIntentSuppressor_StatsExcludesExpired Stats 剔除过期条目
func TestIntentSuppressor_StatsExcludesExpired(t *testing.T) {
	now := time.Now().UnixMilli()
	s := &intentSuppressor{
		m: map[string]int64{
			"new": now,
			"old": now - int64(7*time.Hour/time.Millisecond), // 超过 6h TTL
		},
		ttl: 6 * time.Hour,
	}
	if got := s.Stats(); got != 1 {
		t.Fatalf("Stats 应只统计未过期条目, want 1, got %d", got)
	}
	if !s.Consume("new") {
		t.Fatal("未过期条目应仍可消费")
	}
	if s.Consume("old") {
		t.Fatal("过期条目应已从 Stats 清理且不再可消费")
	}
}

// TestIntentSuppressor_NilReceiver nil 接收者安全返回
func TestIntentSuppressor_NilReceiver(t *testing.T) {
	var s *intentSuppressor
	s.Mark("1")      // 不应 panic
	if s.Consume("1") {
		t.Fatal("nil 接收者 Consume 应返回 false")
	}
}