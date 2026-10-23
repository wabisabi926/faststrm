package monitor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/strm"
)

func newManagedMigrationMonitor(t *testing.T, root string) (*Monitor, *sql.DB) {
	t.Helper()
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cfg := model.LifeMonitorSettings{
		PathMappings: []model.MonitorPathMapping{{Account: "alice", CloudPath: "/电影", LocalPath: root}},
	}
	return &Monitor{
		sqliteDB:   database,
		managed:    strm.NewManagedRecorder(database),
		settingsFn: func() model.LifeMonitorSettings { return cfg },
	}, database
}

func TestFolderRenameMigratesManagedStrm(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "http://192.168.1.10:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs&file_name=movie.mkv"
	oldStrm := filepath.Join(oldDir, "movie.strm")
	if err := os.WriteFile(oldStrm, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, h := newManagedMigrationMonitor(t, root)
	ctx := context.Background()
	if err := m.managed.Record(ctx, strm.ManagedInput{
		LocalPath: oldStrm, Account: "alice", CloudPath: "/电影/old/movie.mkv",
		Pickcode: "d18nbyd6w552vx2zs", FileID: "9002", Content: content, Source: "monitor",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolderEntry(h, "alice", db.FilePathEntry{
		FileID: "9001", Path: "/电影/old", FileName: "old", UpdateTime: 1,
	}); err != nil {
		t.Fatal(err)
	}

	event := client115.LifeEventItem{FileID: "9001", FileName: "new", FileCategory: 0}
	mapping := &pathMapping{cloudPath: "/电影", localPath: filepath.Join(root, "new"), relativePath: "new"}
	if err := m.handleRenameEvent(ctx, "alice", event, mapping, "/电影/new", nil); err != nil {
		t.Fatalf("folder rename failed: %v", err)
	}

	newStrm := filepath.Join(root, "new", "movie.strm")
	if _, err := os.Stat(newStrm); err != nil {
		t.Fatalf("strm should move with directory: %v", err)
	}
	rec, err := db.GetManagedStrm(h, newStrm)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.CloudPath != "/电影/new/movie.mkv" || rec.State != "active" {
		t.Fatalf("managed record not migrated: %+v", rec)
	}
	if oldRec, _ := db.GetManagedStrm(h, oldStrm); oldRec != nil {
		t.Fatalf("old managed record should be gone: %+v", oldRec)
	}

	res, err := m.trashManagedStrmFile(ctx, "alice", newStrm, "/电影/new/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatalf("new path must be recognized as managed: %+v", res)
	}
}

func TestFolderMoveMigratesManagedStrm(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "http://192.168.1.10:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs&file_name=movie.mkv"
	oldStrm := filepath.Join(oldDir, "movie.strm")
	if err := os.WriteFile(oldStrm, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, h := newManagedMigrationMonitor(t, root)
	ctx := context.Background()
	if err := m.managed.Record(ctx, strm.ManagedInput{
		LocalPath: oldStrm, Account: "alice", CloudPath: "/电影/old/movie.mkv",
		Pickcode: "d18nbyd6w552vx2zs", FileID: "9004", Content: content, Source: "monitor",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolderEntry(h, "alice", db.FilePathEntry{
		FileID: "9003", Path: "/电影/old", FileName: "old", UpdateTime: 1,
	}); err != nil {
		t.Fatal(err)
	}

	event := client115.LifeEventItem{FileID: "9003", FileName: "new", FileCategory: 0}
	mapping := &pathMapping{cloudPath: "/电影", localPath: filepath.Join(root, "new"), relativePath: "new"}
	if err := m.handleMoveEvent(ctx, "alice", event, mapping, "/电影/new", nil); err != nil {
		t.Fatalf("folder move failed: %v", err)
	}

	newStrm := filepath.Join(root, "new", "movie.strm")
	rec, err := db.GetManagedStrm(h, newStrm)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.CloudPath != "/电影/new/movie.mkv" || rec.State != "active" {
		t.Fatalf("managed record not migrated after move: %+v", rec)
	}
	if oldRec, _ := db.GetManagedStrm(h, oldStrm); oldRec != nil {
		t.Fatalf("old managed record should be gone: %+v", oldRec)
	}

	res, err := m.trashManagedStrmFile(ctx, "alice", newStrm, "/电影/new/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatalf("new path must be recognized as managed after move: %+v", res)
	}
}

func TestFileRenameMigratesManagedStrm(t *testing.T) {
	root := t.TempDir()
	oldStrm := filepath.Join(root, "old.strm")
	content := "http://192.168.1.10:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs&file_name=old.mkv"
	if err := os.WriteFile(oldStrm, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, h := newManagedMigrationMonitor(t, root)
	m.settingsFn = func() model.LifeMonitorSettings {
		return model.LifeMonitorSettings{
			StrmPrefix:   "http://192.168.1.10:8090",
			PathMappings: []model.MonitorPathMapping{{Account: "alice", CloudPath: "/电影", LocalPath: root}},
		}
	}
	ctx := context.Background()
	if err := m.managed.Record(ctx, strm.ManagedInput{
		LocalPath: oldStrm, Account: "alice", CloudPath: "/电影/old.mkv",
		Pickcode: "d18nbyd6w552vx2zs", FileID: "9002", Content: content, Source: "monitor",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFilePathEntry(h, "alice", db.FilePathEntry{
		FileID: "9002", Path: "/电影/old.mkv", FileName: "old.mkv", PickCode: "d18nbyd6w552vx2zs", UpdateTime: 1,
	}); err != nil {
		t.Fatal(err)
	}

	event := client115.LifeEventItem{
		FileID: "9002", FileName: "new.mkv", FileCategory: 1, PickCode: "d18nbyd6w552vx2zs",
	}
	mapping := &pathMapping{cloudPath: "/电影", localPath: filepath.Join(root, "new.mkv"), relativePath: "new.mkv"}
	if err := m.handleRenameEvent(ctx, "alice", event, mapping, "/电影/new.mkv", nil); err != nil {
		t.Fatalf("file rename failed: %v", err)
	}

	newStrm := filepath.Join(root, "new.strm")
	rec, err := db.GetManagedStrm(h, newStrm)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.CloudPath != "/电影/new.mkv" || rec.Pickcode != "d18nbyd6w552vx2zs" {
		t.Fatalf("managed record not migrated: %+v", rec)
	}
	if oldRec, _ := db.GetManagedStrm(h, oldStrm); oldRec != nil {
		t.Fatalf("old managed record should be gone: %+v", oldRec)
	}

	res, err := m.trashManagedStrmFile(ctx, "alice", newStrm, "/电影/new.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatalf("new path must be recognized as managed: %+v", res)
	}
}
