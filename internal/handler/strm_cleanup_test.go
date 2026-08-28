package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/store"
)

func TestScanMappingFromCache_EmptyEntry(t *testing.T) {
	m := MappingScanRequest{
		Account:   "test",
		CloudPath: "/test",
		LocalPath: "/tmp/test",
	}
	result := scanMappingFromCache(m, nil)
	if result.Error == "" {
		t.Error("expected error for nil entry")
	}

	result2 := scanMappingFromCache(m, &store.StrmCacheEntry{})
	if result2.Error == "" {
		t.Error("expected error for empty entry")
	}
}

func TestScanMappingFromCache_WithMatch(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	os.MkdirAll(localPath, 0o755)

	// Create some local .strm files
	os.WriteFile(filepath.Join(localPath, "movie1.strm"), []byte("url1"), 0o644)
	os.WriteFile(filepath.Join(localPath, "movie2.strm"), []byte("url2"), 0o644)
	// Create a stale file (exists locally but not in cache)
	os.WriteFile(filepath.Join(localPath, "old_movie.strm"), []byte("url3"), 0o644)

	// Cache has movie1 and movie2, but NOT old_movie
	entry := &store.StrmCacheEntry{
		UUID:   "test-uuid",
		TaskID: "task-001",
		LocalPaths: []string{
			filepath.Join(localPath, "movie1.strm"),
			filepath.Join(localPath, "movie2.strm"),
		},
	}

	m := MappingScanRequest{
		Account:   "test_account",
		CloudPath: "电影",
		LocalPath: localPath,
	}

	result := scanMappingFromCache(m, entry)

	// Should have found 3 local STRMs
	if result.LocalStrmCount != 3 {
		t.Errorf("LocalStrmCount: got %d, want 3", result.LocalStrmCount)
	}
	// Cache has 2 entries
	if result.RemoteFileCount != 2 {
		t.Errorf("RemoteFileCount: got %d, want 2", result.RemoteFileCount)
	}
	// Should detect 1 stale (old_movie.strm not in cache)
	if len(result.StaleStrms) != 1 {
		t.Fatalf("StaleStrms count: got %d, want 1", len(result.StaleStrms))
	}
	if result.StaleStrms[0].RelPath != "old_movie.strm" {
		t.Errorf("StaleStrm RelPath: got %s, want old_movie.strm", result.StaleStrms[0].RelPath)
	}
	// No missing STRMs since cache entries all exist locally
	if len(result.MissingStrms) != 0 {
		t.Errorf("MissingStrms count: got %d, want 0", len(result.MissingStrms))
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestScanMappingFromCache_MissingStrms(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	os.MkdirAll(localPath, 0o755)

	// Only create movie1 locally
	os.WriteFile(filepath.Join(localPath, "movie1.strm"), []byte("url1"), 0o644)

	// Cache has movie1 and movie2 (movie2 is missing locally)
	entry := &store.StrmCacheEntry{
		UUID: "test-uuid-2",
		LocalPaths: []string{
			filepath.Join(localPath, "movie1.strm"),
			filepath.Join(localPath, "movie2.strm"),
		},
	}

	m := MappingScanRequest{
		Account:   "test_account",
		LocalPath: localPath,
	}

	result := scanMappingFromCache(m, entry)

	if result.LocalStrmCount != 1 {
		t.Errorf("LocalStrmCount: got %d, want 1", result.LocalStrmCount)
	}
	if result.RemoteFileCount != 2 {
		t.Errorf("RemoteFileCount: got %d, want 2", result.RemoteFileCount)
	}
	// movie2 is missing (in cache but not on disk)
	if len(result.MissingStrms) != 1 {
		t.Fatalf("MissingStrms count: got %d, want 1", len(result.MissingStrms))
	}
	if result.MissingStrms[0].RelPath != "movie2.strm" {
		t.Errorf("MissingStrm RelPath: got %s, want movie2.strm", result.MissingStrms[0].RelPath)
	}
}

func TestScanMappingFromCache_Subdirs(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	os.MkdirAll(filepath.Join(localPath, "subdir1"), 0o755)

	// Create nested STRM file
	os.WriteFile(filepath.Join(localPath, "subdir1", "nested.strm"), []byte("url"), 0o644)

	entry := &store.StrmCacheEntry{
		UUID: "test-uuid-3",
		LocalPaths: []string{
			filepath.Join(localPath, "subdir1", "nested.strm"),
		},
	}

	m := MappingScanRequest{
		Account:   "test",
		LocalPath: localPath,
	}

	result := scanMappingFromCache(m, entry)

	if result.LocalStrmCount != 1 {
		t.Errorf("LocalStrmCount: got %d, want 1", result.LocalStrmCount)
	}
	if len(result.StaleStrms) != 0 {
		t.Errorf("StaleStrms: got %d, want 0", len(result.StaleStrms))
	}
	if len(result.MissingStrms) != 0 {
		t.Errorf("MissingStrms: got %d, want 0", len(result.MissingStrms))
	}
}

func TestScanMappingFromCache_NoLocalDir(t *testing.T) {
	// Local directory doesn't exist
	entry := &store.StrmCacheEntry{
		UUID: "test-uuid-4",
		LocalPaths: []string{
			"/nonexistent/movie1.strm",
			"/nonexistent/movie2.strm",
		},
	}

	m := MappingScanRequest{
		Account:   "test",
		LocalPath: "/nonexistent",
	}

	result := scanMappingFromCache(m, entry)

	if result.LocalStrmCount != 0 {
		t.Errorf("LocalStrmCount: got %d, want 0", result.LocalStrmCount)
	}
	// All cache entries are missing locally
	if len(result.MissingStrms) != 2 {
		t.Errorf("MissingStrms: got %d, want 2", len(result.MissingStrms))
	}
}

func TestListLocalStrmFiles_EmptyDir(t *testing.T) {
	root := t.TempDir()
	files := listLocalStrmFiles(root)
	if len(files) != 0 {
		t.Errorf("empty dir should return 0 files, got %d", len(files))
	}
}

func TestListLocalStrmFiles_IgnoresNonStrm(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "video.mp4"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(root, "movie.strm"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("test"), 0o644)

	files := listLocalStrmFiles(root)
	if len(files) != 1 {
		t.Errorf("should only find .strm files, got %d", len(files))
	}
	if _, ok := files["movie.strm"]; !ok {
		t.Error("movie.strm should be in results")
	}
}

func TestScanMappingFromCache_AllMatch(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	os.MkdirAll(localPath, 0o755)

	// All files match cache
	os.WriteFile(filepath.Join(localPath, "a.strm"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(localPath, "b.strm"), []byte("b"), 0o644)

	entry := &store.StrmCacheEntry{
		UUID: "test-uuid-5",
		LocalPaths: []string{
			filepath.Join(localPath, "a.strm"),
			filepath.Join(localPath, "b.strm"),
		},
	}

	m := MappingScanRequest{
		Account:   "test",
		LocalPath: localPath,
	}

	result := scanMappingFromCache(m, entry)

	if result.LocalStrmCount != 2 {
		t.Errorf("LocalStrmCount: got %d, want 2", result.LocalStrmCount)
	}
	if len(result.StaleStrms) != 0 {
		t.Errorf("StaleStrms: got %d, want 0", len(result.StaleStrms))
	}
	if len(result.MissingStrms) != 0 {
		t.Errorf("MissingStrms: got %d, want 0", len(result.MissingStrms))
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}
