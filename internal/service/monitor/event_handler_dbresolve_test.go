package monitor

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
)

// ======================================================================
// P0-2 回归测试：按 fileID 反查 DB 缓存路径（方案 B）
// 覆盖「文件事件 parent_id=0」时，只要该文件之前经文件夹递归/事件落盘，
// resolveCloudPathFromDB 即从 files 表取回完整云路径，而非裸文件名。
// ======================================================================

// TestResolveCloudPathFromDB_MultiSeg 目标场景：文件事件 parent_id=0，
// 但该文件路径已由文件夹递归写进 files 表（多段完整路径）→ 应命中 DB 缓存 (path, srcDB)。
func TestResolveCloudPathFromDB_MultiSeg(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFilePathEntry(sqldb, "acc1", db.FilePathEntry{
		FileID:   "40001",
		Path:     "电影/动作/Movie.mkv",
		FileName: "Movie.mkv",
		ParentID: "0",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	ev := client115.LifeEventItem{FileID: "40001", FileName: "Movie.mkv", ParentID: "0", FileCategory: 1}
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc1", CloudPath: "电影/", LocalPath: "/tmp/movies"},
	}}

	path, src := m.resolveCloudPathFromDB("acc1", ev, cfg)
	if path != "电影/动作/Movie.mkv" {
		t.Fatalf("path want 电影/动作/Movie.mkv got %q", path)
	}
	if src != srcDB {
		t.Fatalf("src want srcDB(%q) got %q", srcDB, src)
	}
}

// TestResolveCloudPathFromDB_NoEntry 文件从未落盘 → 应返回 ("","")，交给 API 解析。
func TestResolveCloudPathFromDB_NoEntry(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	m := &Monitor{sqliteDB: sqldb}
	ev := client115.LifeEventItem{FileID: "99999", FileName: "Ghost.mkv", ParentID: "0", FileCategory: 1}
	cfg := model.LifeMonitorSettings{}

	path, src := m.resolveCloudPathFromDB("acc1", ev, cfg)
	if path != "" || src != "" {
		t.Fatalf("want ('','') got (%q,%q)", path, src)
	}
}

// TestResolveCloudPathFromDB_SingleSegRejected 单段脏数据且非映射前缀 → 丢弃，强制走 API。
func TestResolveCloudPathFromDB_SingleSegRejected(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFilePathEntry(sqldb, "acc1", db.FilePathEntry{
		FileID: "40002", Path: "Movie.mkv", FileName: "Movie.mkv", ParentID: "0",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	ev := client115.LifeEventItem{FileID: "40002", FileName: "Movie.mkv", ParentID: "0", FileCategory: 1}

	path, src := m.resolveCloudPathFromDB("acc1", ev, model.LifeMonitorSettings{})
	if path != "" || src != srcDBRejected {
		t.Fatalf("want ('',srcDBRejected) got (%q,%q)", path, src)
	}
}

// TestResolveCloudPathFromDB_SingleSegMatchesPrefix 单段路径恰等于映射前缀（new_folder）→ 可信。
func TestResolveCloudPathFromDB_SingleSegMatchesPrefix(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFolderEntry(sqldb, "acc1", db.FilePathEntry{
		FileID: "40003", Path: "新文件夹", FileName: "新文件夹", ParentID: "0",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	ev := client115.LifeEventItem{FileID: "40003", FileName: "新文件夹", ParentID: "0", FileCategory: 0}
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc1", CloudPath: "新文件夹/", LocalPath: "/tmp/new"},
	}}

	path, src := m.resolveCloudPathFromDB("acc1", ev, cfg)
	if path != "新文件夹" {
		t.Fatalf("path want 新文件夹 got %q", path)
	}
	if src != srcDB {
		t.Fatalf("src want srcDB got %q", src)
	}
}

// TestResolveCloudPathFromDB_AccountMismatch 映射 acc 与事件 acc 不同则不借该前缀可信。
func TestResolveCloudPathFromDB_AccountMismatch(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFolderEntry(sqldb, "acc1", db.FilePathEntry{
		FileID: "40004", Path: "电影", FileName: "电影", ParentID: "0",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	ev := client115.LifeEventItem{FileID: "40004", FileName: "电影", ParentID: "0", FileCategory: 0}
	// 前缀电影只属于 acc2，不应让 acc1 信任该单段
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc2", CloudPath: "电影/", LocalPath: "/tmp/movies"},
	}}

	path, src := m.resolveCloudPathFromDB("acc1", ev, cfg)
	if path != "" || src != srcDBRejected {
		t.Fatalf("want ('',srcDBRejected) got (%q,%q)", path, src)
	}
}
