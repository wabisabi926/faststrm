package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/internal/service/task"
)

type cleanupFakeManaged struct {
	verified map[string]bool
	owned    map[string]bool
}

func (f *cleanupFakeManaged) Record(context.Context, strm.ManagedInput) error { return nil }
func (f *cleanupFakeManaged) VerifyFile(_ context.Context, p, _ string) (bool, error) {
	return f.verified[filepath.Clean(p)], nil
}
func (f *cleanupFakeManaged) OwnedBy(_ context.Context, p, _ string) (bool, error) {
	return f.owned[filepath.Clean(p)], nil
}
func (f *cleanupFakeManaged) MarkDeleted(context.Context, string) error { return nil }
func (f *cleanupFakeManaged) CloudRef(context.Context, string) (*strm.ManagedCloudRef, error) {
	return nil, nil
}
func (f *cleanupFakeManaged) Bootstrap(context.Context, []string, []string) (int, int, error) {
	return 0, 0, nil
}

func TestCleanupTaskStrms_OnlyOwnedManagedAndKeepsTrash(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(dir, "owned.strm")
	foreign := filepath.Join(dir, "foreign.strm")
	unmanaged := filepath.Join(dir, "unmanaged.strm")
	trashFile := filepath.Join(root, strmTrashDirForTest(), "batch", "old.strm")
	if err := os.MkdirAll(filepath.Dir(trashFile), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{owned, foreign, unmanaged, trashFile} {
		if err := os.WriteFile(p, []byte("http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fake := &cleanupFakeManaged{
		verified: map[string]bool{filepath.Clean(owned): true, filepath.Clean(foreign): true},
		owned:    map[string]bool{filepath.Clean(owned): true},
	}
	removed, skipped, err := cleanupTaskStrms(context.Background(), task.Task{ID: "t1", TargetPath: root}, fake, false)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d want 1 (skipped=%d)", removed, skipped)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("owned file should be moved to trash")
	}
	for _, p := range []string{foreign, unmanaged, trashFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("must be preserved: %s (%v)", p, err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".faststrm-trash", "*", "电影", "owned.strm"))
	if len(matches) != 1 {
		t.Fatalf("trashed owned file not found: %v", matches)
	}
}

func TestCleanupTaskStrms_RefusesFilesystemRoot(t *testing.T) {
	if _, _, err := cleanupTaskStrms(context.Background(), task.Task{ID: "t1", TargetPath: string(filepath.Separator)}, nil, false); err == nil {
		t.Fatal("filesystem root must be refused")
	}
}

func strmTrashDirForTest() string { return ".faststrm-trash" }

func TestValidateTaskTargetRejectsSharedPath(t *testing.T) {
	dir := t.TempDir()
	t1 := task.Task{ID: "a", TargetPath: dir}
	t2 := task.Task{ID: "b", TargetPath: dir}
	code, msg := validateTaskTarget(t2, []task.Task{t1})
	if code != http.StatusConflict || msg == "" {
		t.Fatalf("want 409 conflict, got code=%d msg=%q", code, msg)
	}
	// 更新自身不应冲突
	if code, _ := validateTaskTarget(t1, []task.Task{t1}); code != 0 {
		t.Fatalf("updating self should pass, got %d", code)
	}
}
