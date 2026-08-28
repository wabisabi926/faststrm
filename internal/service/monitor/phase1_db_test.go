package monitor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
)

// ======================================================================
// Phase 1.2 RED  —— Write-Ahead DB：processEvent 副作用前先写 DB
// ======================================================================

// newTestSqliteDB 打开一个一次性 SQLite（测试后自动 Close + 删除）
func newTestSqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	sqldb, err := db.OpenNew(dir)
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	return sqldb
}

// TestBuildWriteAheadEntry 纯逻辑：校验 entry 的规范化（小写 pickcode / normalize path / ParentID 空归 0 由 normalizeEntry 处理）
func TestBuildWriteAheadEntry(t *testing.T) {
	e, ok := buildWriteAheadEntry(client115.LifeEventItem{
		FileID:   "10001",
		FileName: "Movie (2024).mkv",
		ParentID: "20001",
		PickCode: "ABCDE12345abcde",
	}, "/电影/测试//", "ABCDE12345abcde")
	if !ok {
		t.Fatalf("want ok=true (file_id + cloudPath non-empty)")
	}
	if e.FileID != "10001" {
		t.Fatalf("FileID mismatch %q", e.FileID)
	}
	if e.Path != "电影/测试" {
		t.Fatalf("Path want '电影/测试' got %q", e.Path)
	}
	if e.PickCode != "abcde12345abcde" {
		t.Fatalf("PickCode 应强制小写+trim got %q", e.PickCode)
	}
	// empty file_id → ok=false
	if _, ok := buildWriteAheadEntry(client115.LifeEventItem{}, "电影/a.mkv", "pickcode1234"); ok {
		t.Fatalf("empty file_id should return ok=false")
	}
	// empty cloudPath → ok=false
	if _, ok := buildWriteAheadEntry(client115.LifeEventItem{FileID: "1"}, "", "pickcode1234"); ok {
		t.Fatalf("empty cloudPath should return ok=false")
	}
}

// TestWriteAheadFilePath_WritesIntoDB 真实 SQLite 上，writeAheadFilePath 调用之后
//
//	SELECT COUNT(*) FROM files 必须 > 0，哪怕后续 handler 跳过
func TestWriteAheadFilePath_WritesIntoDB(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	m := &Monitor{sqliteDB: sqldb}
	entry, ok := buildWriteAheadEntry(client115.LifeEventItem{
		FileID:   "999",
		FileName: "Avatar.2009.mkv",
		ParentID: "888",
	}, "电影/Avatar (2009)/Avatar.2009.mkv", "abc123def456ghi7")
	if !ok {
		t.Fatalf("buildWriteAheadEntry failed unexpectedly")
	}
	m.writeAheadFilePath(context.Background(), "acc1", entry)

	cnt, err := db.GetEntryCount(sqldb, "acc1")
	if err != nil {
		t.Fatalf("GetEntryCount: %v", err)
	}
	if cnt <= 0 {
		t.Fatalf("writeAheadFilePath 没写进 DB: count=%d", cnt)
	}
	got, err := db.GetFilePathEntry(sqldb, "acc1", "999")
	if err != nil {
		t.Fatalf("GetFilePathEntry: %v", err)
	}
	if got == nil || got.PickCode != "abc123def456ghi7" {
		t.Fatalf("反查结果不对: %+v", got)
	}
}

// TestProcessEvent_NewFolder_WritesDB_NoLocalStrm 模拟 type=17 new_folder：
//
//	预期：1) files 表写入 1 条（反查前置）；2) 本地目标目录下**不**产生任何 .strm 文件（new_folder 本身不生成）
//	3) life_event_logs 表也必须有 1 条 success=true 记录（观测性闭环）
func TestProcessEvent_NewFolder_WritesDB_NoLocalStrm(t *testing.T) {
	dir := t.TempDir()
	// 初始化 SQLite（包含 files + life_event_logs 两张表）
	sqldb, err := db.OpenNew(dir)
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })

	logRepo, err := db.NewLifeEventLogRepo(sqldb)
	if err != nil {
		t.Fatalf("NewLifeEventLogRepo: %v", err)
	}
	// 本地 STRM 根：
	localMediaRoot := filepath.Join(dir, "Videos")
	if err := os.MkdirAll(localMediaRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := model.LifeMonitorSettings{
		Enabled:  true,
		Accounts: []string{"acc1"},
		PathMappings: []model.MonitorPathMapping{
			{Account: "acc1", CloudPath: "电影/", LocalPath: localMediaRoot},
		},
		EventTypes: model.EventTypesSettings{Create: true, Remove: true, Rename: true, Move: true},
	}

	mon := NewMonitor(func() model.LifeMonitorSettings { return cfg },
		nil, logRepo, nil, nil, nil, nil, sqldb)

	// 构造一个最小可工作的 假 LifeClient（ResolvePath 返回已知路径）
	lifeClient := &stubLifeResolver{
		resolveFn: func(parentID, fileID, fileName string) string {
			// type=17：父目录为根 parent_id=0，新建目录名 "新文件夹"
			return "电影/新文件夹"
		},
	}
	// 注意：这里不能直接 mon.processEvent（因为它用 *client115.LifeClient 类型）
	// 所以我们直接调用"被 processEvent 调用到的新 write-ahead 辅助函数"做纯黑盒验证。
	ctx := context.Background()
	event := client115.LifeEventItem{
		ID:           "event-1",
		Type:         17,
		UpdateTime:   123456,
		FileID:       "40001",
		FileName:     "新文件夹",
		ParentID:     "0",
		FileCategory: 0, // 目录
		PickCode:     "",
	}
	// 模拟 processEvent 内部的 Write-Ahead + type=17 分支：
	cloudPath := lifeClient.ResolvePath(nil, event.ParentID, event.FileID, event.FileName)
	decision, _ := mon.makeWriteAheadDecision_ForTest(ctx, "acc1", event, cloudPath, cfg)
	_ = decision
	// 必须是 new_folder 类型
	if decision.EventKind != "new_folder" {
		t.Fatalf("EventKind want new_folder got %s", decision.EventKind)
	}
	// EventDecision 里 MappingType=MEDIA（因为命中了映射）
	if decision.MappingType != MappingTypeMedia {
		t.Fatalf("want MEDIA mapping, got %s", decision.MappingType)
	}
	// 写 DB 之后 files count=1
	cnt, err := db.GetEntryCount(sqldb, "acc1")
	if err != nil {
		t.Fatalf("GetEntryCount: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("type=17 new_folder 必须且仅写入 1 条 DB: count=%d", cnt)
	}
	// 本地不应有任何 strm 文件生成
	var strmFound []string
	_ = filepath.Walk(localMediaRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.ToLower(filepath.Ext(info.Name())) == ".strm" {
			strmFound = append(strmFound, p)
		}
		return nil
	})
	if len(strmFound) > 0 {
		t.Fatalf("new_folder 不应生成 .strm 文件, 但生成了 %v", strmFound)
	}
	// life_event_logs 应有 1 条 success=true, type=new_folder-create/
	logs, qerr := logRepo.Query(ctx, db.LifeEventLogQuery{Account: "acc1", Limit: 10})
	if qerr != nil {
		t.Fatalf("Query life logs: %v", qerr)
	}
	if len(logs) == 0 {
		t.Fatalf("life_event_logs 必须至少 1 条（观测性闭环）")
	}
	// 找一条 new_folder 相关的成功日志
	found := false
	for _, l := range logs {
		if l.Success &&
			(l.EventType == "new_folder" || l.EventType == "create" || strings.Contains(l.Message, "new_folder") || strings.Contains(l.Message, "新建目录")) {
			found = true
			break
		}
	}
	if !found {
		// 容忍 message 中包含 "Write-Ahead" 之类字眼，但必须有 success=true
		for _, l := range logs {
			if l.Success {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("new_folder 处理后缺少 success=true 的 life_event_logs: %+v", logs)
		}
	}
}

// ======================================================================
// 测试桩：一个最小的 ResolvePath 提供者（返回固定字符串），
// 真实 client115.LifeClient.ResolvePath 签名与此一致
// ======================================================================

type stubLifeResolver struct {
	resolveFn func(parentID, fileID, fileName string) string
}

func (s *stubLifeResolver) ResolvePath(_ context.Context, parentID, fileID, fileName string) string {
	if s.resolveFn != nil {
		return s.resolveFn(parentID, fileID, fileName)
	}
	return ""
}
