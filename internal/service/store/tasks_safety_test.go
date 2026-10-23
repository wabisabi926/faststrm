package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/task"
)

func TestTasksStore_FailClosedOnCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	corrupt := `[{"id":"old","account":`
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewTasksStore(dir)
	if _, err := store.ReadTasks(); err == nil {
		t.Fatal("corrupt tasks.json should return error")
	}
	if err := store.UpsertTask(task.Task{ID: "new", Account: "a", OriginPath: "/o", TargetPath: "/t"}); err == nil {
		t.Fatal("UpsertTask must fail closed on corrupt tasks.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"old"`) {
		t.Fatalf("corrupt file must remain untouched, got %s", raw)
	}
}

func TestTasksStore_ConcurrentUpsertsDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	store := NewTasksStore(dir)

	const n = 20
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- store.UpsertTask(task.Task{
				ID:         "t" + string(rune('a'+i)),
				Account:    "acc",
				OriginPath: "/o",
				TargetPath: "/t",
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent upsert failed: %v", err)
		}
	}
	tasks, err := store.ReadTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != n {
		t.Fatalf("lost task updates: got %d want %d", len(tasks), n)
	}
}
