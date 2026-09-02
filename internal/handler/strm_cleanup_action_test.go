package handler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// 测试夹具：构造一份 ScanSummary，含 staleStrms + missingStrms
// staleStrms 指向 localPath 下已存在的 .strm 文件（待删除）
// missingStrms 指向 localPath 下不存在的 .strm（待生成）
func buildTestScanSummary(t *testing.T) (string, *ScanResponse) {
	t.Helper()
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	stale1 := filepath.Join(localPath, "stale_movie1.strm")
	stale2 := filepath.Join(localPath, "sub", "stale_movie2.strm")
	_ = os.MkdirAll(filepath.Dir(stale2), 0o755)
	_ = os.WriteFile(stale1, []byte("old_url_1"), 0o600)
	_ = os.WriteFile(stale2, []byte("old_url_2"), 0o600)

	missingRel1 := "missing_movie1.mkv"
	missingRel2 := "sub/missing_movie2.mp4"

	scan := &ScanResponse{
		Mappings: []MappingScanResult{
			{
				Account:   "test_account",
				CloudPath: "/test",
				LocalPath: localPath,
				StaleStrms: []StaleStrm{
					{RelPath: "stale_movie1.strm"},
					{RelPath: "sub/stale_movie2.strm"},
				},
				MissingStrms: []MissingStrm{
					{RelPath: missingRel1, PickCode: "pickcode1234567890"},
					{RelPath: missingRel2, PickCode: "pickcode9876543210"},
				},
			},
		},
	}

	return localPath, scan
}

// 验证 delete_all 修复：从 ScanSummary.Mappings.StaleStrms 真正删除文件
func TestExecuteCleanup_DeleteAll_FromScanSummary(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Cleanup: model.CleanupSettings{
			RemoveStrm:         true,
			RemoveEmptyDirs:    false,
			RemoveRelatedFiles: false,
		},
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{}, // 前端 delete_all 传空数组（修复前 bug 触发条件）
		Action:      "delete_all",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.DeletedAllCount != 2 {
		t.Errorf("DeletedAllCount = %d, want 2", resp.DeletedAllCount)
	}
	if resp.DeletedCount != 2 {
		t.Errorf("DeletedCount = %d, want 2", resp.DeletedCount)
	}
	if resp.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0; errors: %v", resp.FailedCount, resp.Errors)
	}

	if _, err := os.Stat(filepath.Join(localPath, "stale_movie1.strm")); !os.IsNotExist(err) {
		t.Errorf("stale_movie1.strm 仍存在，删除未生效")
	}
	if _, err := os.Stat(filepath.Join(localPath, "sub", "stale_movie2.strm")); !os.IsNotExist(err) {
		t.Errorf("sub/stale_movie2.strm 仍存在，删除未生效")
	}
}

// 验证 delete_all 的 DryRun 模式：计数但实际不删
func TestExecuteCleanup_DeleteAll_DryRun(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Cleanup: model.CleanupSettings{
			RemoveStrm: true,
		},
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{},
		Action:      "delete_all",
		ScanSummary: scan,
		DryRun:      true,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.DeletedAllCount != 2 {
		t.Errorf("DryRun DeletedAllCount = %d, want 2", resp.DeletedAllCount)
	}
	if _, err := os.Stat(filepath.Join(localPath, "stale_movie1.strm")); err != nil {
		t.Errorf("DryRun 模式下 stale_movie1.strm 不应被删除: %v", err)
	}
}

// 验证 regenerate 修复：从 ScanSummary.Mappings.MissingStrms 生成 STRM
func TestExecuteCleanup_Regenerate_FromScanSummary(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Strm: model.StrmSettings{
			StrmUrlTemplate: "", // 走 fallback 默认拼接
		},
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{},
		Action:      "regenerate",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.RegeneratedCount != 2 {
		t.Errorf("RegeneratedCount = %d, want 2", resp.RegeneratedCount)
	}
	if resp.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0; errors: %v", resp.FailedCount, resp.Errors)
	}

	expectedStrm1 := filepath.Join(localPath, "missing_movie1.strm")
	expectedStrm2 := filepath.Join(localPath, "sub", "missing_movie2.strm")
	for _, p := range []string{expectedStrm1, expectedStrm2} {
		content, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("STRM 未生成: %s: %v", p, err)
			continue
		}
		s := string(content)
		if !strings.Contains(s, "/api/strm?account=test_account&pickcode=") {
			t.Errorf("STRM 内容 %q 不含期望的 /api/strm?account=...&pickcode=...", s)
		}
	}
}

// 验证 regenerate DryRun 模式：计数但不写文件
func TestExecuteCleanup_Regenerate_DryRun(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{},
		Action:      "regenerate",
		ScanSummary: scan,
		DryRun:      true,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.RegeneratedCount != 2 {
		t.Errorf("DryRun RegeneratedCount = %d, want 2", resp.RegeneratedCount)
	}
	if _, err := os.Stat(filepath.Join(localPath, "missing_movie1.strm")); !os.IsNotExist(err) {
		t.Errorf("DryRun 模式下 missing_movie1.strm 不应被生成")
	}
}

// 验证 delete_and_regenerate 组合操作：同时删除失效 + 生成漏项 + 填充 cleanupSummary
func TestExecuteCleanup_DeleteAndRegenerate(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Cleanup: model.CleanupSettings{
			RemoveStrm:         true,
			RemoveEmptyDirs:    false,
			RemoveRelatedFiles: false,
		},
	}

	body := ExecuteRequest{
		Entries: []ExecuteEntry{
			{
				LocalPath:     localPath,
				StaleRelPaths: []string{"stale_movie1.strm", "sub/stale_movie2.strm"},
			},
		},
		Action:      "delete_and_regenerate",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	// 修复后 delete_and_regenerate 走 deleteSelectedStale（仅删选中），不走 delete_all 分支
	if resp.DeletedAllCount != 0 {
		t.Errorf("DeletedAllCount = %d, want 0（不走 delete_all 分支）", resp.DeletedAllCount)
	}
	if resp.DeletedCount != 2 {
		t.Errorf("DeletedCount = %d, want 2（选中 2 个全删）", resp.DeletedCount)
	}
	if _, err := os.Stat(filepath.Join(localPath, "stale_movie1.strm")); !os.IsNotExist(err) {
		t.Errorf("stale_movie1.strm 应被删除")
	}

	if resp.RegeneratedCount != 2 {
		t.Errorf("RegeneratedCount = %d, want 2", resp.RegeneratedCount)
	}
	if _, err := os.Stat(filepath.Join(localPath, "missing_movie1.strm")); err != nil {
		t.Errorf("missing_movie1.strm 应被生成: %v", err)
	}

	if resp.CleanupSummary == nil {
		t.Fatal("CleanupSummary 为 nil，期望非空")
	}
	if resp.CleanupSummary.Deleted != 2 {
		t.Errorf("CleanupSummary.Deleted = %d, want 2", resp.CleanupSummary.Deleted)
	}
	if resp.CleanupSummary.Regenerated != 2 {
		t.Errorf("CleanupSummary.Regenerated = %d, want 2", resp.CleanupSummary.Regenerated)
	}
	if resp.CleanupSummary.Failed != 0 {
		t.Errorf("CleanupSummary.Failed = %d, want 0", resp.CleanupSummary.Failed)
	}
}

// 验证 ScanSummary 为空时不会 panic
func TestExecuteCleanup_DeleteAll_NilScanSummary(t *testing.T) {
	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Cleanup: model.CleanupSettings{
			RemoveStrm: true,
		},
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{},
		Action:      "delete_all",
		ScanSummary: nil,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.DeletedAllCount != 0 {
		t.Errorf("DeletedAllCount = %d, want 0", resp.DeletedAllCount)
	}
	if resp.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", resp.FailedCount)
	}
}

// 验证 buildStrmForMissing：使用 StrmUrlTemplate 模板时优先渲染
func TestBuildStrmForMissing_WithTemplate(t *testing.T) {
	m := MappingScanResult{
		Account:   "my_account",
		LocalPath: "/data/media",
	}
	miss := MissingStrm{
		RelPath:  "movie.mkv",
		PickCode: "pc1234567890abcdef",
	}
	settings := &model.Settings{
		StrmPrefix: "http://example.com",
		Strm: model.StrmSettings{
			StrmUrlTemplate: "{prefix}/api/fs/get?a={account}&p={pickcode}&n={filename}",
		},
	}

	strmPath, content, err := buildStrmForMissing(m, miss, settings)
	if err != nil {
		t.Fatalf("buildStrmForMissing failed: %v", err)
	}

	wantPath := filepath.Join("/data/media", "movie.strm")
	if strmPath != wantPath {
		t.Errorf("strmPath = %q, want %q", strmPath, wantPath)
	}

	if !strings.Contains(content, "/api/fs/get") {
		t.Errorf("content %q 不含模板渲染的 /api/fs/get", content)
	}
	if !strings.Contains(content, "a=my_account") {
		t.Errorf("content %q 不含 a=my_account", content)
	}
	if !strings.Contains(content, "p=pc1234567890abcdef") {
		t.Errorf("content %q 不含 p=pc...", content)
	}
}

// 验证 buildStrmForMissing：无模板时走 fallback 拼接
func TestBuildStrmForMissing_Fallback(t *testing.T) {
	m := MappingScanResult{
		Account:   "user",
		LocalPath: "/data/media",
	}
	miss := MissingStrm{
		RelPath:  "video.mp4",
		PickCode: "pc1234567890",
	}
	settings := &model.Settings{
		StrmPrefix: "http://localhost:8090",
		Strm: model.StrmSettings{
			StrmUrlTemplate: "",
		},
	}

	strmPath, content, err := buildStrmForMissing(m, miss, settings)
	if err != nil {
		t.Fatalf("buildStrmForMissing failed: %v", err)
	}

	if strmPath != filepath.Join("/data/media", "video.strm") {
		t.Errorf("strmPath = %q", strmPath)
	}

	expected := "http://localhost:8090/api/strm?account=user&pickcode=pc1234567890"
	if content != expected {
		t.Errorf("content = %q, want %q", content, expected)
	}
}

// 验证 writeStrmFileAtomic：原子写入（tmp + rename）
func TestWriteStrmFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.strm")
	content := "http://example.com/api/strm?account=test&pickcode=pc"

	if err := writeStrmFileAtomic(path, content); err != nil {
		t.Fatalf("writeStrmFileAtomic failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != content {
		t.Errorf("content = %q, want %q", string(got), content)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp 文件仍存在，rename 未生效")
	}
}

// 验证 PickCode 为空时跳过生成并计入 failed
func TestExecuteCleanup_Regenerate_EmptyPickCode(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	_ = os.MkdirAll(localPath, 0o755)

	scan := &ScanResponse{
		Mappings: []MappingScanResult{
			{
				Account:   "test_account",
				LocalPath: localPath,
				MissingStrms: []MissingStrm{
					{RelPath: "no_pickcode.mkv", PickCode: ""},
				},
			},
		},
	}

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{},
		Action:      "regenerate",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.RegeneratedCount != 0 {
		t.Errorf("RegeneratedCount = %d, want 0", resp.RegeneratedCount)
	}
	if resp.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", resp.FailedCount)
	}
	if len(resp.Errors) != 1 {
		t.Errorf("Errors len = %d, want 1", len(resp.Errors))
	}
}

// 验证 delete_and_regenerate 仅删 entries 选中条目（修复前：误删 ScanSummary 全部失效）
// 场景：扫描发现 4 个失效 STRM，用户在 StaleStrmDialog 勾选其中 2 个，点组合按钮
// 修复前：后端调用 deleteAllStaleFromScan 删除全部 4 个
// 修复后：后端调用 deleteSelectedStale 只删 entries 中的 2 个，其余 2 个仍在
func TestExecuteCleanup_DeleteAndRegenerate_OnlySelectedEntries(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "media")
	_ = os.MkdirAll(localPath, 0o755)

	stalePaths := []string{
		"stale1.strm",
		"stale2.strm",
		"stale3.strm",
		"stale4.strm",
	}
	for _, p := range stalePaths {
		_ = os.WriteFile(filepath.Join(localPath, p), []byte("old"), 0o600)
	}

	missingRels := []string{"missing1.mkv", "missing2.mp4"}

	scan := &ScanResponse{
		Mappings: []MappingScanResult{
			{
				Account:   "test_account",
				LocalPath: localPath,
				StaleStrms: []StaleStrm{
					{RelPath: "stale1.strm"},
					{RelPath: "stale2.strm"},
					{RelPath: "stale3.strm"},
					{RelPath: "stale4.strm"},
				},
				MissingStrms: []MissingStrm{
					{RelPath: missingRels[0], PickCode: "pc000000000000001"},
					{RelPath: missingRels[1], PickCode: "pc000000000000002"},
				},
			},
		},
	}

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Cleanup: model.CleanupSettings{
			RemoveStrm: true,
		},
	}

	body := ExecuteRequest{
		Entries: []ExecuteEntry{
			{
				LocalPath:     localPath,
				StaleRelPaths: []string{"stale1.strm", "stale3.strm"},
			},
		},
		Action:      "delete_and_regenerate",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.DeletedCount != 2 {
		t.Errorf("DeletedCount = %d, want 2（只删选中）", resp.DeletedCount)
	}
	if resp.RegeneratedCount != 2 {
		t.Errorf("RegeneratedCount = %d, want 2", resp.RegeneratedCount)
	}
	if resp.CleanupSummary == nil || resp.CleanupSummary.Deleted != 2 {
		t.Errorf("CleanupSummary.Deleted 不正确: %+v", resp.CleanupSummary)
	}

	if _, err := os.Stat(filepath.Join(localPath, "stale1.strm")); !os.IsNotExist(err) {
		t.Errorf("stale1.strm 应被删除（在 entries 中）")
	}
	if _, err := os.Stat(filepath.Join(localPath, "stale3.strm")); !os.IsNotExist(err) {
		t.Errorf("stale3.strm 应被删除（在 entries 中）")
	}
	if _, err := os.Stat(filepath.Join(localPath, "stale2.strm")); err != nil {
		t.Errorf("stale2.strm 应保留（不在 entries 中）: %v", err)
	}
	if _, err := os.Stat(filepath.Join(localPath, "stale4.strm")); err != nil {
		t.Errorf("stale4.strm 应保留（不在 entries 中）: %v", err)
	}

	if _, err := os.Stat(filepath.Join(localPath, "missing1.strm")); err != nil {
		t.Errorf("missing1.strm 应被生成: %v", err)
	}
}

// 验证 regenerate 返回 regeneratedPaths（供前端移除已生成项）
func TestExecuteCleanup_Regenerate_ReturnsRegeneratedPaths(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
	}

	body := ExecuteRequest{
		Entries:     []ExecuteEntry{},
		Action:      "regenerate",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.RegeneratedCount != 2 {
		t.Fatalf("RegeneratedCount = %d, want 2", resp.RegeneratedCount)
	}

	wantPaths := map[string]bool{
		"missing_movie1.mkv":     true,
		"sub/missing_movie2.mp4": true,
	}
	if len(resp.RegeneratedPaths) != 2 {
		t.Fatalf("RegeneratedPaths len = %d, want 2", len(resp.RegeneratedPaths))
	}
	for _, p := range resp.RegeneratedPaths {
		if !wantPaths[p] {
			t.Errorf("RegeneratedPaths 含未期望的项: %q", p)
		}
	}

	if _, err := os.Stat(filepath.Join(localPath, "missing_movie1.strm")); err != nil {
		t.Errorf("missing_movie1.strm 未生成: %v", err)
	}
}

// 验证 delete_and_regenerate 同时返回 regeneratedPaths（组合操作也要同步漏项状态）
func TestExecuteCleanup_DeleteAndRegenerate_ReturnsRegeneratedPaths(t *testing.T) {
	localPath, scan := buildTestScanSummary(t)

	settings := &model.Settings{
		StrmPrefix: "http://127.0.0.1:8090",
		Cleanup: model.CleanupSettings{
			RemoveStrm: true,
		},
	}

	body := ExecuteRequest{
		Entries: []ExecuteEntry{
			{
				LocalPath:     localPath,
				StaleRelPaths: []string{"stale_movie1.strm", "sub/stale_movie2.strm"},
			},
		},
		Action:      "delete_and_regenerate",
		ScanSummary: scan,
		DryRun:      false,
	}

	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)

	if resp.RegeneratedCount != 2 {
		t.Fatalf("RegeneratedCount = %d, want 2", resp.RegeneratedCount)
	}
	if len(resp.RegeneratedPaths) != 2 {
		t.Errorf("RegeneratedPaths len = %d, want 2", len(resp.RegeneratedPaths))
	}

	if resp.CleanupSummary == nil {
		t.Fatal("CleanupSummary 为 nil")
	}
	if resp.CleanupSummary.Deleted != 2 {
		t.Errorf("CleanupSummary.Deleted = %d, want 2", resp.CleanupSummary.Deleted)
	}
	if resp.CleanupSummary.Regenerated != 2 {
		t.Errorf("CleanupSummary.Regenerated = %d, want 2", resp.CleanupSummary.Regenerated)
	}
}
