package server

// ===== STRM 清理 P1 方案 E2E 测试 =====
// 覆盖本轮改动的核心契约（后端）：
//   E2E-1. 扫描/全量对账返回同一 ScanResponse 结构，带 4 个聚合字段
//   E2E-2. SQLite 接入时两模式都带 dbRecordCount + totalDbRecords 聚合
//   E2E-3. delete_and_regenerate：仅删 entries 选中条目
//   E2E-4. regenerate 返回 regeneratedPaths
//   E2E-5. delete_and_regenerate 组合同时返回 regeneratedPaths + cleanupSummary

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

type strmE2E struct {
	t       *testing.T
	Deps    handler.StrmCleanupDeps
	Root    string
	MediaA  string
	MediaB  string
	Account string
}

func buildStrmE2E(t *testing.T, withSQLite bool) *strmE2E {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, "cfg")
	dataDir := filepath.Join(root, "data")
	mediaA := filepath.Join(root, "media_a")
	mediaB := filepath.Join(root, "media_b")
	for _, d := range []string{cfgDir, dataDir, mediaA, filepath.Join(mediaA, "sub"), mediaB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	accountName := "e2e_user"
	salt := "strm_e2e_salt_pad_to_32_chars_ok"
	settings := model.DefaultSettings()
	settings.StrmPrefix = "http://127.0.0.1:8090"
	settings.Cleanup.RemoveStrm = true
	settings.Cleanup.RemoveRelatedFiles = true
	settings.Cleanup.RemoveEmptyDirs = false
	settings.LifeMonitor.Enabled = true
	settings.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: accountName, CloudPath: "/电影", LocalPath: mediaA},
		{Account: accountName, CloudPath: "/电视剧", LocalPath: mediaB},
	}
	settingsStore := store.NewSettingsStore(salt, cfgDir)
	if err := settingsStore.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	accountStore, err := store.NewAccountStore(salt, cfgDir)
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}

	_ = os.WriteFile(filepath.Join(mediaA, "staleA1.strm"), []byte("oldA1"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaA, "sub", "staleA2.strm"), []byte("oldA2"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaB, "staleB1.strm"), []byte("oldB1"), 0o600)
	// P2 关联文件：mediaA 6 个 (3nfo/2jpg/1srt) / mediaB 4 个 (2nfo/1png/1ass) / A\sub 1 个
	_ = os.WriteFile(filepath.Join(mediaA, "goodA.nfo"), []byte("nfo"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaA, "staleA1.nfo"), []byte("nfodel"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaA, "sub", "staleA2.nfo"), []byte("nfodel2"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaA, "goodA.jpg"), []byte("jpg"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaA, "poster.jpg"), []byte("jpg2"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaA, "goodA.srt"), []byte("srt"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaB, "goodB.nfo"), []byte("nfoB"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaB, "staleB1.nfo"), []byte("nfoBdel"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaB, "fanart.png"), []byte("png"), 0o600)
	_ = os.WriteFile(filepath.Join(mediaB, "goodB.ass"), []byte("ass"), 0o600)

	var sqliteDB *sql.DB
	if withSQLite {
		sqliteDB, err = db.OpenNew(dataDir)
		if err != nil {
			t.Fatalf("db.OpenNew: %v", err)
		}
		t.Cleanup(func() { _ = sqliteDB.Close() })
		// DB file_id 列是 INTEGER，必须用纯数字字符串
		batchA := make([]db.FilePathEntry, 4)
		for i := range batchA {
			id := fmt.Sprintf("%d", 1000+i)
			batchA[i] = db.FilePathEntry{FileID: id, Path: "/电影/" + id + ".mkv", FileName: id + ".mkv", PickCode: "pickA" + id, UpdateTime: 1_700_000_000}
		}
		batchB := make([]db.FilePathEntry, 6)
		for i := range batchB {
			id := fmt.Sprintf("%d", 2000+i)
			batchB[i] = db.FilePathEntry{FileID: id, Path: "/电视剧/" + id + ".mp4", FileName: id + ".mp4", PickCode: "pickB" + id, UpdateTime: 1_700_000_000}
		}
		if err := db.UpsertFilePathEntryBatch(sqliteDB, accountName, batchA); err != nil {
			t.Fatalf("upsert batchA: %v", err)
		}
		if err := db.UpsertFilePathEntryBatch(sqliteDB, accountName, batchB); err != nil {
			t.Fatalf("upsert batchB: %v", err)
		}
	}
	deps := handler.StrmCleanupDeps{
		SettingsStore: settingsStore,
		AccountStore:  accountStore,
		SQLiteDB:      sqliteDB,
		ClientFactory: func(name string) (*client115.Client, error) { return client115.NewClient(""), nil },
		Interaction:   nil,
	}
	return &strmE2E{t: t, Deps: deps, Root: root, MediaA: mediaA, MediaB: mediaB, Account: accountName}
}

func (f *strmE2E) buildFakeScanSummary() *handler.ScanResponse {
	return &handler.ScanResponse{
		Mappings: []handler.MappingScanResult{
			{
				Account: f.Account, CloudPath: "/电影", LocalPath: f.MediaA,
				RemoteFileCount: 10, LocalStrmCount: 2,
				StaleStrms:   []handler.StaleStrm{{RelPath: "staleA1.strm", Size: 5}, {RelPath: "sub/staleA2.strm", Size: 5}},
				MissingStrms: []handler.MissingStrm{{RelPath: "goodA.mkv", PickCode: "pickcodeAAA0000001"}, {RelPath: "sub/goodA2.mp4", PickCode: "pickcodeAAA0000002"}},
			},
			{
				Account: f.Account, CloudPath: "/电视剧", LocalPath: f.MediaB,
				RemoteFileCount: 20, LocalStrmCount: 1,
				StaleStrms:   []handler.StaleStrm{{RelPath: "staleB1.strm", Size: 5}},
				MissingStrms: []handler.MissingStrm{{RelPath: "goodB.mkv", PickCode: "pickcodeBBB0000001"}},
			},
		},
	}
}

// E2E-1 & E2E-2
func TestE2E_StrmCleanup_ScanVsReconcile_UnifiedResponse(t *testing.T) {
	f := buildStrmE2E(t, true)
	_, scanRaw := doReq(handler.HandleStrmCleanupScanPOST(f.Deps), "POST", "/api/strmCleanup/scan", map[string]any{"useSettingsDefaults": true}, "")
	_, recRaw := doReq(handler.HandleStrmCleanupScanPOST(f.Deps), "POST", "/api/strmCleanup/scan", map[string]any{"useSettingsDefaults": true, "action": "reconcile"}, "")
	scanMap := decodeAsMap(t, scanRaw)
	recMap := decodeAsMap(t, recRaw)
	if _, ok := scanMap["results"]; ok {
		t.Error("scan 响应不应含 results 字段（旧 ReconcileScanResponse 残留）")
	}
	if _, ok := recMap["results"]; ok {
		t.Error("reconcile 响应不应含 results 字段，已合并为统一 ScanResponse")
	}
	if scanMap["mappings"] == nil {
		t.Fatalf("scan 缺 mappings 字段: %s", scanRaw)
	}
	if recMap["mappings"] == nil {
		t.Fatalf("reconcile 缺 mappings 字段: %s", recRaw)
	}
	for name, m := range map[string]map[string]any{"scan": scanMap, "reconcile": recMap} {
		for _, k := range []string{"totalRemoteFiles", "totalLocalStrms", "totalStale", "totalMissing"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%s 响应缺少聚合字段 %s", name, k)
			}
		}
		if _, ok := m["totalDbRecords"]; !ok {
			t.Errorf("%s (with SQLite) 必须带 totalDbRecords；响应=%s", name, func() string { b, _ := json.Marshal(m); return string(b) }())
		}
	}
	// dbRecordCount per mapping：电影=4 电视剧=6
	msgs := []struct {
		Cloud string
		Want  int
	}{{"/电影", 4}, {"/电视剧", 6}}
	for _, src := range []struct {
		N string
		M map[string]any
	}{{"scan", scanMap}, {"reconcile", recMap}} {
		mapsAny, _ := src.M["mappings"].([]any)
		if len(mapsAny) != 2 {
			t.Errorf("%s mappings 长度=%d, want 2", src.N, len(mapsAny))
			continue
		}
		for _, want := range msgs {
			for _, anyMap := range mapsAny {
				mi, _ := anyMap.(map[string]any)
				if mi["cloudPath"] != want.Cloud {
					continue
				}
				cnt, _ := mi["dbRecordCount"].(float64)
				if int(cnt) != want.Want {
					t.Errorf("%s mapping[%s] dbRecordCount=%v, want %d", src.N, want.Cloud, cnt, want.Want)
				}
			}
		}
	}
	// totalDbRecords = 10
	for name, m := range map[string]map[string]any{"scan": scanMap, "reconcile": recMap} {
		c, _ := m["totalDbRecords"].(float64)
		if int(c) != 10 {
			t.Errorf("%s totalDbRecords=%v, want 10", name, c)
		}
	}
}

func TestE2E_StrmCleanup_Scan_NoSQLite_NoTotalDbRecords(t *testing.T) {
	f := buildStrmE2E(t, false)
	_, raw := doReq(handler.HandleStrmCleanupScanPOST(f.Deps), "POST", "/api/strmCleanup/scan", map[string]any{"useSettingsDefaults": true}, "")
	m := decodeAsMap(t, raw)
	if _, ok := m["totalDbRecords"]; ok {
		t.Errorf("未开 SQLite 时 totalDbRecords 应 omitempty: %s", raw)
	}
	mapsAny, _ := m["mappings"].([]any)
	for i, a := range mapsAny {
		mi, _ := a.(map[string]any)
		if _, ok := mi["dbRecordCount"]; ok {
			t.Errorf("mapping[%d] dbRecordCount 不应出现: %v", i, mi["dbRecordCount"])
		}
	}
}

// E2E-3 组合按钮仅删选中
func TestE2E_StrmCleanup_DeleteAndRegenerate_OnlySelectedEntries(t *testing.T) {
	f := buildStrmE2E(t, true)
	scan := f.buildFakeScanSummary()
	body := handler.ExecuteRequest{
		Entries: []handler.ExecuteEntry{
			{LocalPath: f.MediaA, StaleRelPaths: []string{"staleA1.strm"}},
			{LocalPath: f.MediaB, StaleRelPaths: []string{"staleB1.strm"}},
		},
		Action: "delete_and_regenerate", ScanSummary: scan, DryRun: false,
	}
	_, raw := doReq(handler.HandleStrmCleanupExecutePOST(f.Deps), "POST", "/api/strmCleanup/execute", body, "")
	resp := decodeAsMap(t, raw)
	dAll, _ := resp["deletedAllCount"].(float64)
	dCnt, _ := resp["deletedCount"].(float64)
	if int(dAll) != 0 {
		t.Errorf("deletedAllCount=%v, want 0（不走 delete_all 分支）", dAll)
	}
	if int(dCnt) != 2 {
		t.Errorf("deletedCount=%v, want 2（仅删选中）", dCnt)
	}
	if _, err := os.Stat(filepath.Join(f.MediaA, "staleA1.strm")); !os.IsNotExist(err) {
		t.Error("staleA1.strm 应被删（选中）")
	}
	if _, err := os.Stat(filepath.Join(f.MediaB, "staleB1.strm")); !os.IsNotExist(err) {
		t.Error("staleB1.strm 应被删（选中）")
	}
	if _, err := os.Stat(filepath.Join(f.MediaA, "sub", "staleA2.strm")); err != nil {
		t.Errorf("sub/staleA2.strm 未选中不应删: %v", err)
	}
}

// E2E-4 regenerate 返回 regeneratedPaths
func TestE2E_StrmCleanup_Regenerate_ReturnsRegeneratedPaths(t *testing.T) {
	f := buildStrmE2E(t, true)
	scan := f.buildFakeScanSummary()
	body := handler.ExecuteRequest{Entries: []handler.ExecuteEntry{}, Action: "regenerate", ScanSummary: scan, DryRun: false}
	_, raw := doReq(handler.HandleStrmCleanupExecutePOST(f.Deps), "POST", "/api/strmCleanup/execute", body, "")
	var resp handler.ExecuteResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ExecuteResponse: %v (raw=%s)", err, raw)
	}
	if resp.RegeneratedCount != 3 {
		t.Errorf("RegeneratedCount=%d, want 3", resp.RegeneratedCount)
	}
	// 后端 regeneratedPaths 返回 missingStrm 原始 relPath（相对路径 + 原扩展名），供前端直接过滤
	want := map[string]bool{"goodA.mkv": false, "sub/goodA2.mp4": false, "goodB.mkv": false}
	if len(resp.RegeneratedPaths) != len(want) {
		t.Errorf("regeneratedPaths len=%d want %d; paths=%v", len(resp.RegeneratedPaths), len(want), resp.RegeneratedPaths)
	}
	for _, p := range resp.RegeneratedPaths {
		if _, ok := want[p]; !ok {
			t.Errorf("未知 regenerated path: %s", p)
		} else {
			want[p] = true
		}
	}
	for p := range want {
		if !want[p] {
			t.Errorf("缺少 regenerated path %s", p)
		}
	}
	// 磁盘上的文件（扩展名被改为 .strm）应当被创建
	diskFiles := []string{
		filepath.Join(f.MediaA, "goodA.strm"),
		filepath.Join(f.MediaA, "sub", "goodA2.strm"),
		filepath.Join(f.MediaB, "goodB.strm"),
	}
	for _, p := range diskFiles {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("regenerated %s 磁盘不存在: %v", p, err)
		}
	}
}

// E2E-5 组合响应
func TestE2E_StrmCleanup_DeleteAndRegenerate_CombinedResponse(t *testing.T) {
	f := buildStrmE2E(t, true)
	scan := f.buildFakeScanSummary()
	body := handler.ExecuteRequest{
		Entries: []handler.ExecuteEntry{
			{LocalPath: f.MediaA, StaleRelPaths: []string{"staleA1.strm", "sub/staleA2.strm"}},
			{LocalPath: f.MediaB, StaleRelPaths: []string{"staleB1.strm"}},
		},
		Action: "delete_and_regenerate", ScanSummary: scan, DryRun: false,
	}
	_, raw := doReq(handler.HandleStrmCleanupExecutePOST(f.Deps), "POST", "/api/strmCleanup/execute", body, "")
	var resp handler.ExecuteResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, raw)
	}
	if resp.DeletedCount != 3 {
		t.Errorf("DeletedCount=%d want 3", resp.DeletedCount)
	}
	if resp.RegeneratedCount != 3 {
		t.Errorf("RegeneratedCount=%d want 3", resp.RegeneratedCount)
	}
	if len(resp.RegeneratedPaths) != 3 {
		t.Errorf("regeneratedPaths len=%d want 3", len(resp.RegeneratedPaths))
	}
	if resp.CleanupSummary == nil {
		t.Fatal("cleanupSummary 不应 nil")
	}
	if resp.CleanupSummary.Deleted != 3 || resp.CleanupSummary.Regenerated != 3 || resp.CleanupSummary.Failed != 0 {
		t.Errorf("cleanupSummary=%+v want {3,3,0}", resp.CleanupSummary)
	}
	_ = context.Background()
}

// 回归：delete_all 从 ScanSummary 删（前端 entries 空数组的情况下）
func TestE2E_StrmCleanup_DeleteAll_DeletesFromScanSummary(t *testing.T) {
	f := buildStrmE2E(t, true)
	scan := f.buildFakeScanSummary()
	body := handler.ExecuteRequest{Entries: []handler.ExecuteEntry{}, Action: "delete_all", ScanSummary: scan, DryRun: false}
	_, raw := doReq(handler.HandleStrmCleanupExecutePOST(f.Deps), "POST", "/api/strmCleanup/execute", body, "")
	resp := decodeAsMap(t, raw)
	dAll, _ := resp["deletedAllCount"].(float64)
	dCnt, _ := resp["deletedCount"].(float64)
	if int(dAll) != 3 || int(dCnt) != 3 {
		t.Errorf("delete_all counts=(%v,%v) want (3,3)", dAll, dCnt)
	}
	for _, p := range []string{
		filepath.Join(f.MediaA, "staleA1.strm"),
		filepath.Join(f.MediaA, "sub", "staleA2.strm"),
		filepath.Join(f.MediaB, "staleB1.strm"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("delete_all 后仍存在: %s", p)
		}
	}
}

// ========== P2：associatedFileCount + 轻量 re-scan (refreshedMappingStats) ==========

// P2-2a：扫描（scan / reconcile 同构）返回 mapping.associatedFileCount 与 totalAssociatedFiles
func TestE2E_StrmCleanup_Scan_AssociatedFileCounts(t *testing.T) {
	f := buildStrmE2E(t, true)
	_, raw := doReq(handler.HandleStrmCleanupScanPOST(f.Deps), "POST", "/api/strmCleanup/scan", map[string]any{"useSettingsDefaults": true}, "")
	m := decodeAsMap(t, raw)
	totalAssoc, _ := m["totalAssociatedFiles"].(float64)
	// fixture：mediaA 6 个关联文件 (goodA.nfo+staleA1.nfo+sub/staleA2.nfo+goodA.jpg+poster.jpg+goodA.srt)
	//         mediaB 4 个 (goodB.nfo+staleB1.nfo+fanart.png+goodB.ass)
	if int(totalAssoc) != 10 {
		t.Errorf("totalAssociatedFiles=%d want 10; raw=%v", int(totalAssoc), m["totalAssociatedFiles"])
	}
	maps, _ := m["mappings"].([]any)
	if len(maps) != 2 {
		t.Fatalf("mappings len=%d want 2", len(maps))
	}
	// mapping 0 = mediaA ("电影")
	a, _ := maps[0].(map[string]any)
	aAssoc, _ := a["associatedFileCount"].(float64)
	if int(aAssoc) != 6 {
		t.Errorf("mappingA associatedFileCount=%d want 6; raw=%s", int(aAssoc), fmt.Sprint(a["associatedFileCount"]))
	}
	// mapping 1 = mediaB ("电视剧")
	b, _ := maps[1].(map[string]any)
	bAssoc, _ := b["associatedFileCount"].(float64)
	if int(bAssoc) != 4 {
		t.Errorf("mappingB associatedFileCount=%d want 4; raw=%s", int(bAssoc), fmt.Sprint(b["associatedFileCount"]))
	}
}

// P2-2b：NoSQLite 也能统计 associatedFileCount（totalDbRecords 省略，但 totalAssociatedFiles 仍然有）
func TestE2E_StrmCleanup_Scan_NoSQLite_HasAssociatedFileCounts(t *testing.T) {
	f := buildStrmE2E(t, false)
	_, raw := doReq(handler.HandleStrmCleanupScanPOST(f.Deps), "POST", "/api/strmCleanup/scan", map[string]any{"useSettingsDefaults": true}, "")
	m := decodeAsMap(t, raw)
	if _, ok := m["totalDbRecords"]; ok {
		t.Error("NoSQLite 时不应含 totalDbRecords")
	}
	totalAssoc, _ := m["totalAssociatedFiles"].(float64)
	if int(totalAssoc) != 10 {
		t.Errorf("NoSQLite totalAssociatedFiles=%d want 10", int(totalAssoc))
	}
}

// P2-1a：delete_all 执行后 refreshedMappingStats 为权威 Walk 值，且反映关联文件被删除
func TestE2E_StrmCleanup_DeleteAll_RefreshedMappingStats(t *testing.T) {
	f := buildStrmE2E(t, true)
	scan := f.buildFakeScanSummary()
	body := handler.ExecuteRequest{Entries: []handler.ExecuteEntry{}, Action: "delete_all", ScanSummary: scan, DryRun: false}
	_, raw := doReq(handler.HandleStrmCleanupExecutePOST(f.Deps), "POST", "/api/strmCleanup/execute", body, "")
	resp := decodeAsMap(t, raw)
	statsRaw, ok := resp["refreshedMappingStats"].([]any)
	if !ok || len(statsRaw) == 0 {
		t.Fatalf("refreshedMappingStats 缺失：%v", resp["refreshedMappingStats"])
	}
	byPath := make(map[string]map[string]any)
	for _, s := range statsRaw {
		st, _ := s.(map[string]any)
		lp, _ := st["localPath"].(string)
		byPath[lp] = st
	}
	// A：删除 staleA1.strm + sub/staleA2.strm → strm 0
	a, ok := byPath[f.MediaA]
	if !ok {
		t.Fatalf("stats 缺失 mediaA: %v", byPath)
	}
	aStrm, _ := a["localStrmCount"].(float64)
	if int(aStrm) != 0 {
		t.Errorf("A.localStrmCount=%d want 0", int(aStrm))
	}
	// A 的关联：goodA.nfo/goodA.jpg/poster.jpg/goodA.srt（删 staleA1.nfo + sub/staleA2.nfo）= 4
	aAssoc, _ := a["associatedFileCount"].(float64)
	if int(aAssoc) != 4 {
		t.Errorf("A.associatedFileCount=%d want 4", int(aAssoc))
	}
	// B：删除 staleB1.strm → strm 0；关联 goodB.nfo/fanart.png/goodB.ass（删 staleB1.nfo）= 3
	b, ok := byPath[f.MediaB]
	if !ok {
		t.Fatalf("stats 缺失 mediaB")
	}
	bStrm, _ := b["localStrmCount"].(float64)
	if int(bStrm) != 0 {
		t.Errorf("B.localStrmCount=%d want 0", int(bStrm))
	}
	bAssoc, _ := b["associatedFileCount"].(float64)
	if int(bAssoc) != 3 {
		t.Errorf("B.associatedFileCount=%d want 3", int(bAssoc))
	}
}

// P2-1b：DryRun=true 不触发 refreshedMappingStats（因为没有真实磁盘变更）
func TestE2E_StrmCleanup_DryRun_NoRefreshedMappingStats(t *testing.T) {
	f := buildStrmE2E(t, true)
	scan := f.buildFakeScanSummary()
	body := handler.ExecuteRequest{Entries: []handler.ExecuteEntry{}, Action: "delete_all", ScanSummary: scan, DryRun: true}
	_, raw := doReq(handler.HandleStrmCleanupExecutePOST(f.Deps), "POST", "/api/strmCleanup/execute", body, "")
	resp := decodeAsMap(t, raw)
	if stats, ok := resp["refreshedMappingStats"].([]any); ok && len(stats) > 0 {
		t.Errorf("DryRun 不应产生 refreshedMappingStats, got %d 项", len(stats))
	}
	dAll, _ := resp["deletedAllCount"].(float64)
	if int(dAll) != 3 {
		t.Errorf("dryRun deletedAllCount=%d want 3", int(dAll))
	}
	// 磁盘文件不应改变：3 个 stale strm 都还在
	for _, p := range []string{filepath.Join(f.MediaA, "staleA1.strm"), filepath.Join(f.MediaA, "sub", "staleA2.strm"), filepath.Join(f.MediaB, "staleB1.strm")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("DryRun: %s 被删除了: %v", p, err)
		}
	}
}

// ========== P3：preview 接口 + 扫描预读 Content + CacheTTL ==========

// P3-1b：POST /api/strmCleanup/preview 存在文件 / 不存在文件 / 参数错误三分支
func TestE2E_StrmCleanup_PreviewEndpoint(t *testing.T) {
	f := buildStrmE2E(t, false)
	// 正常场景
	_, raw := doReq(handler.HandleStrmCleanupPreviewPOST(f.Deps), "POST", "/api/strmCleanup/preview",
		handler.StrmPreviewRequest{LocalPath: f.MediaB, RelPath: "staleB1.strm"}, "")
	r := decodeAsMap(t, raw)
	exists, _ := r["exists"].(bool)
	if !exists {
		t.Errorf("preview exists=false want true")
	}
	size, _ := r["size"].(float64)
	if int(size) != 5 {
		t.Errorf("preview size=%d want 5", int(size))
	}
	content, _ := r["content"].(string)
	if content != "oldB1" {
		t.Errorf("preview content=%q want oldB1", content)
	}
	trunc, _ := r["truncated"].(bool)
	if trunc {
		t.Errorf("truncated=true want false")
	}
	// 不存在场景
	_, raw2 := doReq(handler.HandleStrmCleanupPreviewPOST(f.Deps), "POST", "/api/strmCleanup/preview",
		handler.StrmPreviewRequest{LocalPath: f.MediaB, RelPath: "nope.strm"}, "")
	r2 := decodeAsMap(t, raw2)
	e2, _ := r2["exists"].(bool)
	if e2 {
		t.Errorf("missing preview exists=true want false")
	}
	// 参数错误 → 400
	status, _ := doReq(handler.HandleStrmCleanupPreviewPOST(f.Deps), "POST", "/api/strmCleanup/preview",
		handler.StrmPreviewRequest{LocalPath: "", RelPath: ""}, "")
	if status != http.StatusBadRequest {
		t.Errorf("empty param status=%d want 400", status)
	}
}

// P3-2：useCache=true, cacheTTLMs=自定义 被后端接受
func TestE2E_StrmCleanup_Scan_CacheTTLMs_Accepted(t *testing.T) {
	f := buildStrmE2E(t, true)
	body := map[string]any{"useSettingsDefaults": true, "useCache": true, "cacheTTLMs": float64(1)}
	_, raw := doReq(handler.HandleStrmCleanupScanPOST(f.Deps), "POST", "/api/strmCleanup/scan", body, "")
	m := decodeAsMap(t, raw)
	total, _ := m["totalAssociatedFiles"].(float64)
	if int(total) != 10 {
		t.Errorf("cacheTTLMs accepted: totalAssociatedFiles=%d want 10", int(total))
	}
}
