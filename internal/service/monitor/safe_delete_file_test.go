package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func TestTrashManagedStrmFileMovesManagedAndRelated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	managedStrm := filepath.Join(dir, "movie.strm")
	related := filepath.Join(dir, "movie.zh.srt")
	unmanagedStrm := filepath.Join(dir, "manual.strm")
	for _, p := range []string{managedStrm, related, unmanagedStrm} {
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

	res, err := m.trashManagedStrmFile(context.Background(), "alice", managedStrm, "/电影/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved || res.RelatedMoved != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(managedStrm); !os.IsNotExist(err) {
		t.Fatalf("managed strm should be moved, err=%v", err)
	}
	if _, err := os.Stat(related); !os.IsNotExist(err) {
		t.Fatalf("related asset should be moved, err=%v", err)
	}
	if _, err := os.Stat(unmanagedStrm); err != nil {
		t.Fatalf("unmanaged strm must be kept: %v", err)
	}

	res, err = m.trashManagedStrmFile(context.Background(), "alice", unmanagedStrm, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved || res.SkipReason == "" {
		t.Fatalf("unmanaged strm should be skipped: %+v", res)
	}
}

func TestTrashManagedStrmFileSkipsCloudPathMismatch(t *testing.T) {
	root := t.TempDir()
	strmPath := filepath.Join(root, "movie.strm")
	if err := os.WriteFile(strmPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{
		managed: fakeManaged{managedPath: strmPath},
		settingsFn: func() model.LifeMonitorSettings {
			return model.LifeMonitorSettings{
				PathMappings: []model.MonitorPathMapping{{Account: "alice", CloudPath: "/电影", LocalPath: root}},
			}
		},
	}

	res, err := m.trashManagedStrmFile(context.Background(), "alice", strmPath, "/其他/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved || res.SkipReason == "" {
		t.Fatalf("cloud path mismatch should skip: %+v", res)
	}
	if _, err := os.Stat(strmPath); err != nil {
		t.Fatalf("file must be kept: %v", err)
	}
}
