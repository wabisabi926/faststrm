package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadTasks_DisablesPathCleanupOnce 路径级清理默认关闭的一次性迁移：
// 存量任务里的 removeExtraFiles=true 被关掉；标记文件存在后，用户主动重新开启不会被改回去。
func TestReadTasks_DisablesPathCleanupOnce(t *testing.T) {
	dir := t.TempDir()
	raw := `[{"id":"t1","account":"a","originPath":"/云","targetPath":"/本地","removeExtraFiles":true}]`
	if err := os.WriteFile(filepath.Join(dir, "tasks.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewTasksStore(dir)

	tasks, err := s.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].RemoveExtraFiles {
		t.Fatalf("迁移后应关闭路径级清理, got %+v", tasks)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cleanup-path-disabled")); err != nil {
		t.Fatalf("应写入迁移标记文件: %v", err)
	}

	// 用户主动重新开启 → 不得被再次改回去
	tasks[0].RemoveExtraFiles = true
	if err := s.SaveTasks(tasks); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	again, err := s.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if !again[0].RemoveExtraFiles {
		t.Fatal("标记文件存在时不应重复迁移，用户的主动开启被覆盖了")
	}
}
