package strmutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureNoSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "sub", "file.strm")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureNoSymlinkComponents(root, inside); err != nil {
		t.Fatalf("normal path rejected: %v", err)
	}
	if err := EnsureNoSymlinkComponents(root, filepath.Join(root, "new", "file.strm")); err != nil {
		t.Fatalf("new path rejected: %v", err)
	}
	if err := EnsureNoSymlinkComponents(root, filepath.Join(root, "..", "outside.strm")); err == nil {
		t.Fatal("outside path should be rejected")
	}

	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := EnsureNoSymlinkComponents(root, filepath.Join(link, "file.strm")); err == nil {
		t.Fatal("symlink parent must be rejected")
	}
}
