package strmutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPurgeAllTrash 清空回收站：删除所有批次目录，但不碰回收站之外的文件。
func TestPurgeAllTrash(t *testing.T) {
	root := t.TempDir()
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(root, "电影", TrashDirName, "batch1", "a.strm"))
	mk(filepath.Join(root, "电影", TrashDirName, "batch2", "b.strm"))
	mk(filepath.Join(root, "电影", "keep.strm"))

	n := PurgeAllTrash(root)
	if n != 2 {
		t.Fatalf("应清空 2 个批次，got %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, "电影", TrashDirName, "batch1")); err == nil {
		t.Fatal("batch1 应被删除")
	}
	if _, err := os.Stat(filepath.Join(root, "电影", "keep.strm")); err != nil {
		t.Fatal("回收站外的文件不应被删除")
	}
	if got := PurgeAllTrash(""); got != 0 {
		t.Fatalf("空 root 应返回 0，got %d", got)
	}
}
