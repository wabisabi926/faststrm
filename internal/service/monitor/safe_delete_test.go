package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/strm"
)

type fakeManaged struct {
	managedPath  string
	managedPaths []string
	// cloudPath 托管记录里的云端路径；空则用默认值（兼容既有测试）
	cloudPath string
}

func (f fakeManaged) isManaged(localPath string) bool {
	if f.managedPath != "" && localPath == f.managedPath {
		return true
	}
	for _, p := range f.managedPaths {
		if p == localPath {
			return true
		}
	}
	return false
}

func (f fakeManaged) Record(context.Context, strm.ManagedInput) error { return nil }
func (f fakeManaged) VerifyFile(context.Context, string, string) (bool, error) {
	return true, nil
}
func (f fakeManaged) MarkDeleted(context.Context, string) error { return nil }
func (f fakeManaged) Bootstrap(context.Context, []string, []string) (int, int, error) {
	return 0, 0, nil
}
func (f fakeManaged) CloudRef(_ context.Context, localPath string) (*strm.ManagedCloudRef, error) {
	if f.isManaged(localPath) {
		cp := f.cloudPath
		if cp == "" {
			cp = "/电影/movie.mkv"
		}
		return &strm.ManagedCloudRef{Account: "alice", CloudPath: cp, Pickcode: "d18nbyd6w552vx2zs"}, nil
	}
	return nil, nil
}

func TestDeleteManagedStrmTreeOnlyMovesManagedStrm(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	managedStrm := filepath.Join(dir, "managed.strm")
	unmanagedStrm := filepath.Join(dir, "unmanaged.strm")
	media := filepath.Join(dir, "movie.mkv")
	for _, p := range []string{managedStrm, unmanagedStrm, media} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := &Monitor{
		managed: fakeManaged{managedPath: managedStrm},
		settingsFn: func() model.LifeMonitorSettings {
			return model.LifeMonitorSettings{
				PathMappings: []model.MonitorPathMapping{{Account: "alice", CloudPath: "/电影", LocalPath: root}},
			}
		},
	}

	moved, err := m.deleteManagedStrmTree(context.Background(), "alice", dir)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("want 1 managed strm moved, got %d", moved)
	}
	if _, err := os.Stat(managedStrm); !os.IsNotExist(err) {
		t.Fatalf("managed strm should be moved, err=%v", err)
	}
	if _, err := os.Stat(unmanagedStrm); err != nil {
		t.Fatalf("unmanaged strm must be kept: %v", err)
	}
	if _, err := os.Stat(media); err != nil {
		t.Fatalf("real media must be kept: %v", err)
	}
}
