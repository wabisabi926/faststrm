package journal

import "testing"

func TestJournalRingAndOrder(t *testing.T) {
	j := New(3)
	j.Add(LevelInfo, "a", "one", nil)
	j.Add(LevelWarn, "b", "two", map[string]any{"k": "v"})
	j.Add(LevelError, "c", "three", nil)
	j.Add(LevelInfo, "d", "four", nil)

	if got := j.Len(); got != 3 {
		t.Fatalf("len = %d, want 3", got)
	}
	events := j.List(10)
	if len(events) != 3 {
		t.Fatalf("events = %d", len(events))
	}
	if events[0].Message != "four" || events[2].Message != "two" {
		t.Fatalf("unexpected order: %+v", events)
	}
	if events[2].Detail["k"] != "v" {
		t.Fatalf("detail lost: %+v", events[2])
	}
}

func TestJournalListLimit(t *testing.T) {
	j := New(10)
	for i := 0; i < 5; i++ {
		j.Add(LevelInfo, "x", "m", nil)
	}
	if got := len(j.List(2)); got != 2 {
		t.Fatalf("limit = %d, want 2", got)
	}
}

func TestJournalNilSafe(t *testing.T) {
	var j *Journal
	j.Add(LevelInfo, "x", "m", nil)
	if j.Len() != 0 {
		t.Fatal("nil journal should be empty")
	}
	if j.List(1) != nil {
		t.Fatal("nil journal list should be nil")
	}
}
