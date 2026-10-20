package emby

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteStrmTreeKeepsRealMedia(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "movie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	strm := filepath.Join(dir, "movie.strm")
	media := filepath.Join(dir, "movie.mkv")
	note := filepath.Join(dir, "note.txt")
	for _, p := range []string{strm, media, note} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deleteStrmTree(dir, []string{root}, SyncDeleteOptions{})

	if _, err := os.Stat(strm); !os.IsNotExist(err) {
		t.Fatalf("strm should be deleted, err=%v", err)
	}
	if _, err := os.Stat(media); err != nil {
		t.Fatalf("real media must be kept: %v", err)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("user file must be kept: %v", err)
	}
}

func TestDeleteStrmFileRejectsNonStrm(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleteStrmFile(media, []string{dir}, SyncDeleteOptions{})

	if _, err := os.Stat(media); err != nil {
		t.Fatalf("non-strm file must not be deleted: %v", err)
	}
}

func TestPathWithinRootRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if pathWithinRoot(root, filepath.Join(root, "..", "etc")) {
		t.Fatal("path traversal must be rejected")
	}
	if !pathWithinRoot(root, filepath.Join(root, "sub", "movie.strm")) {
		t.Fatal("path inside root should pass")
	}
}
