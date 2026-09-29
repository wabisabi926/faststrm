package monitor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
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

// ======================================================================
// P0-1 回归：按事件 parent_id 反查 folders 表
// ======================================================================

// TestResolveCloudDirPathByParentID_Hit 父目录 cid 已入库 → 直接命中目录路径
func TestResolveCloudDirPathByParentID_Hit(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFolderEntry(sqldb, "acc1", db.FilePathEntry{
		FileID:   "3523080744391935333",
		Path:     "挂载/测试",
		FileName: "测试",
		ParentID: "0",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}

	if got := m.resolveCloudDirPathByParentID("acc1", "3523080744391935333"); got != "挂载/测试" {
		t.Fatalf("want 挂载/测试 got %q", got)
	}
}

// TestResolveCloudDirPathByParentID_Miss 空/0/未入库的 parent_id → 返回空，交给后续兜底
func TestResolveCloudDirPathByParentID_Miss(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	m := &Monitor{sqliteDB: sqldb}

	for _, pid := range []string{"", "  ", "0", "999999"} {
		if got := m.resolveCloudDirPathByParentID("acc1", pid); got != "" {
			t.Fatalf("parentID=%q want '' got %q", pid, got)
		}
	}
}

// ======================================================================
// P0-2 回归：映射云端前缀反查 cid（绕开 /files/medialist 祖先链）
// 复现 CoreELEC 现场：medialist 对「挂载/测试」返回 state=true 但 ancestors 为空，
// 导致新增文件退化成裸文件名 → no_path_mapping 跳过。前缀反查走 /files/getid 兜底。
// ======================================================================

// getidRT 假传输：固定返回 body，并统计请求次数（验证前缀 cid 有缓存）
type getidRT struct {
	mu    sync.Mutex
	calls int
	body  string
}

func (r *getidRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (r *getidRT) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newLifeClientWithGetID(body string) (*client115.LifeClient, *getidRT) {
	rt := &getidRT{body: body}
	lc := client115.NewLifeClient("test-cookie")
	lc.FsClient().HTTP = &http.Client{Transport: rt}
	return lc, rt
}

// TestResolveCloudDirPathByMappingPrefix_Hit 前缀「挂载/测试」→ cid 命中事件 parent_id
func TestResolveCloudDirPathByMappingPrefix_Hit(t *testing.T) {
	lc, rt := newLifeClientWithGetID(`{"state":true,"id":"3523080744391935333"}`)
	m := &Monitor{}
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc1", CloudPath: "挂载/测试/", LocalPath: "/storage/videos/faststrm/测试"},
	}}

	got := m.resolveCloudDirPathByMappingPrefix(context.Background(), "acc1", "3523080744391935333", cfg, lc)
	if got != "挂载/测试" {
		t.Fatalf("want 挂载/测试 got %q", got)
	}
	// 第二次应命中缓存，不再打接口
	if again := m.resolveCloudDirPathByMappingPrefix(context.Background(), "acc1", "3523080744391935333", cfg, lc); again != "挂载/测试" {
		t.Fatalf("缓存后 want 挂载/测试 got %q", again)
	}
	if n := rt.callCount(); n != 1 {
		t.Fatalf("前缀 cid 应缓存，实际请求 /files/getid %d 次", n)
	}
}

// TestResolveCloudDirPathByMappingPrefix_CidMismatch 前缀存在但 cid 不等于 parent_id → 不误命中
func TestResolveCloudDirPathByMappingPrefix_CidMismatch(t *testing.T) {
	lc, _ := newLifeClientWithGetID(`{"state":true,"id":"111111"}`)
	m := &Monitor{}
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc1", CloudPath: "挂载/测试", LocalPath: "/tmp/x"},
	}}

	if got := m.resolveCloudDirPathByMappingPrefix(context.Background(), "acc1", "222222", cfg, lc); got != "" {
		t.Fatalf("cid 不匹配应返回空, got %q", got)
	}
}

// TestResolveCloudDirPathByMappingPrefix_AccountMismatch 映射属别的账号 → 不参与反查
func TestResolveCloudDirPathByMappingPrefix_AccountMismatch(t *testing.T) {
	lc, rt := newLifeClientWithGetID(`{"state":true,"id":"3523080744391935333"}`)
	m := &Monitor{}
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc2", CloudPath: "挂载/测试", LocalPath: "/tmp/x"},
	}}

	if got := m.resolveCloudDirPathByMappingPrefix(context.Background(), "acc1", "3523080744391935333", cfg, lc); got != "" {
		t.Fatalf("账号不匹配应返回空, got %q", got)
	}
	if n := rt.callCount(); n != 0 {
		t.Fatalf("账号不匹配不应打接口, 实际 %d 次", n)
	}
}

// TestResolveCloudDirPathByMappingPrefix_EmptyParentID parent_id 为空/0 → 不打接口
func TestResolveCloudDirPathByMappingPrefix_EmptyParentID(t *testing.T) {
	lc, rt := newLifeClientWithGetID(`{"state":true,"id":"1"}`)
	m := &Monitor{}
	cfg := model.LifeMonitorSettings{PathMappings: []model.MonitorPathMapping{
		{Account: "acc1", CloudPath: "挂载/测试", LocalPath: "/tmp/x"},
	}}

	for _, pid := range []string{"", "0"} {
		if got := m.resolveCloudDirPathByMappingPrefix(context.Background(), "acc1", pid, cfg, lc); got != "" {
			t.Fatalf("parentID=%q 应返回空, got %q", pid, got)
		}
	}
	if n := rt.callCount(); n != 0 {
		t.Fatalf("parent_id 非法不应打接口, 实际 %d 次", n)
	}
}
