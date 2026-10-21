package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/strm"
)

type fakeManagedRecorder struct {
	allow map[string]bool
}

func (f *fakeManagedRecorder) Record(context.Context, strm.ManagedInput) error { return nil }
func (f *fakeManagedRecorder) VerifyFile(_ context.Context, localPath, _ string) (bool, error) {
	return f.allow[filepath.Clean(localPath)], nil
}
func (f *fakeManagedRecorder) MarkDeleted(context.Context, string) error { return nil }
func (f *fakeManagedRecorder) CloudRef(_ context.Context, localPath string) (*strm.ManagedCloudRef, error) {
	if !f.allow[filepath.Clean(localPath)] {
		return nil, nil
	}
	return &strm.ManagedCloudRef{Account: "alice", CloudPath: "/x/managed.strm", Pickcode: "d18nbyd6w552vx2zs"}, nil
}
func (f *fakeManagedRecorder) Bootstrap(context.Context, []string, []string) (int, int, error) {
	return 0, 0, nil
}

type fakeCloudAbsence struct {
	absent bool
	err    error
}

func (f fakeCloudAbsence) StillAbsent(context.Context, string, string, string) (bool, error) {
	return f.absent, f.err
}

func TestCleanupOrphanStrms_CloudRecheck(t *testing.T) {
	cases := []struct {
		name    string
		checker CloudAbsenceChecker
	}{
		{name: "cloud restored", checker: fakeCloudAbsence{absent: false}},
		{name: "checker missing", checker: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := t.TempDir()
			file := filepath.Join(target, "managed.strm")
			if err := os.WriteFile(file, []byte("http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"), 0o600); err != nil {
				t.Fatal(err)
			}
			rec := &fakeManagedRecorder{allow: map[string]bool{filepath.Clean(file): true}}
			_, deleted, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
				"t", target, map[string]string{}, map[string]struct{}{"other": {}}, nil,
				"auto_clean", model.CleanupSettings{RemoveStrm: true}, nil, rec, c.checker)
			if err != nil {
				t.Fatal(err)
			}
			if deleted != 0 {
				t.Fatalf("deleted=%d, cloud check should preserve file", deleted)
			}
			if _, err := os.Stat(file); err != nil {
				t.Fatal("file must be preserved")
			}
		})
	}
}

func TestCleanupOrphanStrms_OwnershipGuardAndTrash(t *testing.T) {
	target := t.TempDir()
	dir := filepath.Join(target, "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	managedFile := filepath.Join(dir, "managed.strm")
	foreignFile := filepath.Join(dir, "foreign.strm")
	for _, p := range []string{managedFile, foreignFile} {
		if err := os.WriteFile(p, []byte("http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rec := &fakeManagedRecorder{allow: map[string]bool{filepath.Clean(managedFile): true}}

	n, _, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, map[string]string{}, map[string]struct{}{"other": {}}, nil,
		"auto_clean", model.CleanupSettings{RemoveStrm: true}, nil, rec, fakeCloudAbsence{absent: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted=%d want 1", n)
	}
	if _, err := os.Stat(foreignFile); err != nil {
		t.Fatal("foreign file must be preserved")
	}
	if _, err := os.Stat(managedFile); !os.IsNotExist(err) {
		t.Fatal("managed file should be moved to trash")
	}
	matches, _ := filepath.Glob(filepath.Join(target, ".faststrm-trash", "*", "电影", "managed.strm"))
	if len(matches) != 1 {
		t.Fatalf("trash matches = %v", matches)
	}
}
