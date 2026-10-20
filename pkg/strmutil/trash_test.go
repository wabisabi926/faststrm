package strmutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoveToTrash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sub", "movie.strm")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("url"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest, err := MoveToTrash(path, root, TrashSessionDir(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("original should be gone")
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "url" {
		t.Fatalf("trashed file unreadable: %v", err)
	}
	if !filepath.IsAbs(dest) || !strings.HasPrefix(dest, filepath.Join(root, TrashDirName)) {
		t.Fatalf("unexpected trash destination: %s", dest)
	}
}

func TestMoveToTrashRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.strm")
	if err := os.WriteFile(outside, []byte("url"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveToTrash(outside, root, ""); err == nil {
		t.Fatal("outside root should be rejected")
	}
}

func TestPurgeTrashTree(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub", TrashDirName, "20200101-000000")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(nested, old, old); err != nil {
		t.Fatal(err)
	}
	if n := PurgeTrashTree(root, 7*24*time.Hour); n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatal("nested old trash should be purged")
	}
}

func TestPurgeTrash(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, TrashDirName, "20200101-000000")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldDir, old, old); err != nil {
		t.Fatal(err)
	}
	freshDir := filepath.Join(root, TrashDirName, "fresh")
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if n := PurgeTrash(root, 7*24*time.Hour); n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("old trash should be purged")
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatal("fresh trash should remain")
	}
}
