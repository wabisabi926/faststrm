package monitor

import (
	"testing"
	"time"
)

// ======================================================================
// EventDeduplicator 单元测试（P3：事件去重器）
// 直接结构体构造，避免 NewEventDeduplicator 启动 cleanupLoop goroutine 泄漏
// ======================================================================

func newTestDedup(ttl time.Duration) *EventDeduplicator {
	return &EventDeduplicator{
		seen: make(map[string]int64),
		ttl:  ttl,
	}
}

// TestEventDeduplicator_BasicMarkAndCheck 标记前不重复、标记后重复；
// 同一 fileID 但 eventType / parentID 变化时不视为重复
func TestEventDeduplicator_BasicMarkAndCheck(t *testing.T) {
	d := newTestDedup(time.Hour)
	if d.IsDuplicate("100", "create", "50") {
		t.Fatal("未标记前不应判定重复")
	}
	d.MarkProcessed("100", "create", "50")
	if !d.IsDuplicate("100", "create", "50") {
		t.Fatal("标记后应判定重复")
	}
	if d.IsDuplicate("100", "remove", "50") {
		t.Fatal("不同 eventType 不应判定重复")
	}
	if d.IsDuplicate("100", "create", "60") {
		t.Fatal("不同 parentID 不应判定重复")
	}
}

// TestEventDeduplicator_TTLExpiry TTL 窗口过后不再判定重复
func TestEventDeduplicator_TTLExpiry(t *testing.T) {
	d := newTestDedup(20 * time.Millisecond)
	d.MarkProcessed("100", "create", "50")
	if !d.IsDuplicate("100", "create", "50") {
		t.Fatal("TTL 窗口内应判定重复")
	}
	time.Sleep(60 * time.Millisecond)
	if d.IsDuplicate("100", "create", "50") {
		t.Fatal("TTL 过期后不应判定重复")
	}
}

// TestEventDeduplicator_EmptyFileID fileID 为空时既不判定重复也不写入 seen
func TestEventDeduplicator_EmptyFileID(t *testing.T) {
	d := newTestDedup(time.Hour)
	if d.IsDuplicate("", "create", "50") {
		t.Fatal("fileID 为空不应判定重复")
	}
	d.MarkProcessed("", "create", "50")
	if d.Stats() != 0 {
		t.Fatalf("fileID 为空不应写入 seen: stats=%d", d.Stats())
	}
}

// TestEventDeduplicator_NilReceiver nil receiver 上调用不应 panic
func TestEventDeduplicator_NilReceiver(t *testing.T) {
	var d *EventDeduplicator
	if d.IsDuplicate("100", "create", "50") {
		t.Fatal("nil receiver IsDuplicate 应返回 false")
	}
	d.MarkProcessed("100", "create", "50")
	if d.Stats() != 0 {
		t.Fatalf("nil receiver Stats 应为 0")
	}
}

// TestEventDeduplicator_Stats 统计已记录键数量
func TestEventDeduplicator_Stats(t *testing.T) {
	d := newTestDedup(time.Hour)
	d.MarkProcessed("100", "create", "50")
	d.MarkProcessed("101", "create", "50")
	if got := d.Stats(); got != 2 {
		t.Fatalf("Stats want 2 got %d", got)
	}
}
