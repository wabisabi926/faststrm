package store

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestStrmCacheStore_SaveAndGet(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := NewStrmCacheStore(cfgDir)

	entry := &StrmCacheEntry{
		UUID:       "test-uuid-001",
		TaskID:     "task-001",
		Target:     "D:\\视频",
		Account:    "test_account",
		RelPaths:   []string{"movie1.strm", "movie2.strm"},
		LocalPaths: []string{"D:\\视频\\movie1.strm", "D:\\视频\\movie2.strm"},
	}

	if err := s.Save(entry); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got := s.Get("test-uuid-001")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.UUID != "test-uuid-001" {
		t.Errorf("UUID mismatch: got %s", got.UUID)
	}
	if got.TaskID != "task-001" {
		t.Errorf("TaskID mismatch: got %s", got.TaskID)
	}
	if len(got.LocalPaths) != 2 {
		t.Errorf("LocalPaths len: got %d, want 2", len(got.LocalPaths))
	}

	s2 := NewStrmCacheStore(cfgDir)
	got2 := s2.Get("test-uuid-001")
	if got2 == nil {
		t.Fatal("Get from new instance returned nil - persistence failed")
	}
	if got2.TaskID != "task-001" {
		t.Errorf("Persisted TaskID mismatch: got %s", got2.TaskID)
	}
}

func TestStrmCacheStore_Overwrite(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	_ = s.Save(&StrmCacheEntry{
		UUID:       "uuid-ow",
		TaskID:     "task-1",
		RelPaths:   []string{"a.strm"},
		LocalPaths: []string{"D:\\a.strm"},
	})

	_ = s.Save(&StrmCacheEntry{
		UUID:       "uuid-ow",
		TaskID:     "task-2",
		RelPaths:   []string{"b.strm", "c.strm"},
		LocalPaths: []string{"D:\\b.strm", "D:\\c.strm"},
	})

	got := s.Get("uuid-ow")
	if got == nil {
		t.Fatal("Get nil after overwrite")
	}
	if got.TaskID != "task-2" {
		t.Errorf("Overwrite failed: TaskID=%s, want task-2", got.TaskID)
	}
	if len(got.LocalPaths) != 2 {
		t.Errorf("Overwrite LocalPaths len: got %d, want 2", len(got.LocalPaths))
	}
}

func TestStrmCacheStore_LatestByTaskID(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	entry1 := &StrmCacheEntry{
		UUID:       "uuid-old",
		TaskID:     "task-shared",
		Account:    "acc1",
		LocalPaths: []string{"D:\\old.strm"},
	}
	_ = s.Save(entry1)
	time.Sleep(5 * time.Millisecond)

	entry2 := &StrmCacheEntry{
		UUID:       "uuid-new",
		TaskID:     "task-shared",
		Account:    "acc1",
		LocalPaths: []string{"D:\\new.strm"},
	}
	_ = s.Save(entry2)

	latest := s.LatestByTaskID("task-shared")
	if latest == nil {
		t.Fatal("LatestByTaskID returned nil")
	}
	if latest.UUID != "uuid-new" {
		t.Errorf("Latest UUID: got %s, want uuid-new", latest.UUID)
	}

	if s.LatestByTaskID("") != nil {
		t.Error("LatestByTaskID with empty task should return nil")
	}
	if s.LatestByTaskID("nonexistent") != nil {
		t.Error("LatestByTaskID with nonexistent task should return nil")
	}
}

func TestStrmCacheStore_Delete(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	_ = s.Save(&StrmCacheEntry{UUID: "uuid-del", TaskID: "t1"})
	if s.Get("uuid-del") == nil {
		t.Fatal("pre-delete Get nil")
	}

	_ = s.Delete("uuid-del")
	if s.Get("uuid-del") != nil {
		t.Error("post-delete Get should return nil")
	}

	s2 := NewStrmCacheStore(cfgDir)
	if s2.Get("uuid-del") != nil {
		t.Error("deleted entry persisted incorrectly")
	}
}

func TestStrmCacheStore_ListTaskRecent(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	for i := 0; i < 5; i++ {
		_ = s.Save(&StrmCacheEntry{
			UUID:   "uuid-" + string(rune('a'+i)),
			TaskID: "t1",
		})
		time.Sleep(2 * time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		_ = s.Save(&StrmCacheEntry{
			UUID:   "uuid-t2-" + string(rune('a'+i)),
			TaskID: "t2",
		})
		time.Sleep(2 * time.Millisecond)
	}

	recent := s.ListTaskRecent("t1", 3)
	if len(recent) != 3 {
		t.Fatalf("ListTaskRecent len: got %d, want 3", len(recent))
	}
	for i := 1; i < len(recent); i++ {
		if recent[i].CreatedAt > recent[i-1].CreatedAt {
			t.Errorf("sort order wrong at %d", i)
		}
	}

	recent2 := s.ListTaskRecent("t2", 10)
	if len(recent2) != 2 {
		t.Errorf("ListTaskRecent t2 len: got %d, want 2", len(recent2))
	}

	if len(s.ListTaskRecent("", 10)) != 0 {
		t.Error("empty task should return empty list")
	}
}

func TestStrmCacheStore_EdgeCases(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	if err := s.Save(nil); err != nil {
		t.Errorf("Save(nil) should not error: %v", err)
	}
	if err := s.Save(&StrmCacheEntry{UUID: ""}); err != nil {
		t.Errorf("Save empty UUID should not error: %v", err)
	}
	if s.Get("") != nil {
		t.Error("Get with empty UUID should return nil")
	}

	removed, _ := s.CleanupExpired(0)
	if removed != 0 {
		t.Errorf("maxAge=0 should remove nothing, got %d", removed)
	}

	s2 := NewStrmCacheStore(filepath.Join(root, "nonexistent"))
	removed3, err := s2.CleanupExpired(1000)
	if err != nil {
		t.Errorf("CleanupExpired on missing dir: %v", err)
	}
	if removed3 != 0 {
		t.Errorf("empty store should remove 0, got %d", removed3)
	}
}

func TestStrmCacheStore_ConcurrentAccess(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	done := make(chan bool, 10)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			_ = s.Save(&StrmCacheEntry{
				UUID:       "uuid-c-" + string(rune('0'+idx)),
				TaskID:     "task-concurrent",
				LocalPaths: []string{"D:\\concurrent.strm"},
			})
			done <- true
		}(i)
	}
	for i := 0; i < 5; i++ {
		go func() {
			s.LatestByTaskID("task-concurrent")
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	list := s.ListTaskRecent("task-concurrent", 10)
	if len(list) != 5 {
		t.Errorf("concurrent save count: got %d, want 5", len(list))
	}
}

func TestFullPathSet(t *testing.T) {
	entry := &StrmCacheEntry{
		LocalPaths: []string{"D:\\a.strm", "D:\\b.strm", "", "D:\\c.strm"},
	}
	set := FullPathSet(entry)
	if len(set) != 3 {
		t.Errorf("FullPathSet len: got %d, want 3", len(set))
	}
	if _, ok := set["D:\\a.strm"]; !ok {
		t.Error("missing D:\\a.strm in set")
	}
	if _, ok := set[""]; ok {
		t.Error("empty string should not be in set")
	}
}

func TestStrmCacheStore_SavePathConsistency(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	s := NewStrmCacheStore(cfgDir)

	entry := &StrmCacheEntry{
		UUID:       "uuid-sort",
		TaskID:     "t1",
		RelPaths:   []string{"z.strm", "a.strm", "m.strm"},
		LocalPaths: []string{"D:\\z.strm", "D:\\a.strm", "D:\\m.strm"},
	}
	_ = s.Save(entry)
	got := s.Get("uuid-sort")
	if got == nil {
		t.Fatal("Get failed")
	}

	if len(got.RelPaths) != 3 || len(got.LocalPaths) != 3 {
		t.Errorf("round-trip len mismatch: rel=%d local=%d", len(got.RelPaths), len(got.LocalPaths))
	}

	sort.Strings(got.RelPaths)
	if got.RelPaths[0] != "a.strm" || got.RelPaths[1] != "m.strm" || got.RelPaths[2] != "z.strm" {
		t.Errorf("sort failed: %v", got.RelPaths)
	}
}
