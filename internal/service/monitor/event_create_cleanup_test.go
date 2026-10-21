package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
)

func TestCreateStrmForSingleFileCleansOldPathAfterUpsert(t *testing.T) {
	root := t.TempDir()
	sqldb, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	oldStrm := filepath.Join(root, "old.strm")
	newStrm := filepath.Join(root, "new.strm")
	if err := os.WriteFile(oldStrm, []byte("http://old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFilePathEntry(sqldb, "alice", db.FilePathEntry{
		FileID: "100", Path: "电影/old.mkv", FileName: "old.mkv", PickCode: "abcdefghij1234567",
	}); err != nil {
		t.Fatal(err)
	}

	m := &Monitor{
		sqliteDB: sqldb,
		managed:  fakeManaged{managedPath: oldStrm, cloudPath: "电影/old.mkv"},
		settingsFn: func() model.LifeMonitorSettings {
			return model.LifeMonitorSettings{
				StrmPrefix:     "http://192.168.1.10:8090",
				StrmExtensions: []string{"mkv"},
				PathMappings: []model.MonitorPathMapping{
					{Account: "alice", CloudPath: "电影", LocalPath: root},
				},
			}
		},
	}

	_, err = m.createStrmForSingleFile(context.Background(), "alice", singleFileCreateInput{
		CloudPath: "电影/new.mkv",
		FileName:  "new.mkv",
		PickCode:  "abcdefghij1234567",
		FileID:    "100",
		FileSize:  1024,
	}, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newStrm); err != nil {
		t.Fatalf("new strm missing: %v", err)
	}
	if _, err := os.Stat(oldStrm); !os.IsNotExist(err) {
		t.Fatalf("old strm should be moved to trash, err=%v", err)
	}
}
