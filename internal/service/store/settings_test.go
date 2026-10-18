package store

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSettings 写入指定 JSON 内容到 config/settings.json
func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

// TestReadSettings_MigratesMissingEventTypes
// P0-2：旧配置 lifeMonitor 里缺 eventTypes 字段 → 反序列化后 4 个 bool 全 false，
// 会导致所有生活事件按 event_type_disabled_* 静默跳过（问题①"有些人正常有些人不行"）。
// 迁移必须补成默认全开；而字段确实存在时（哪怕用户刻意全关）必须尊重用户值。
func TestReadSettings_MigratesMissingEventTypes(t *testing.T) {
	// 场景 1：lifeMonitor 存在但没有 eventTypes → 补默认全开
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"lifeMonitor":{"pollInterval":10}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	et := s1.LifeMonitor.EventTypes
	if !et.Create || !et.Remove || !et.Rename || !et.Move {
		t.Errorf("缺 eventTypes 应迁移为默认全开, got create=%v remove=%v rename=%v move=%v",
			et.Create, et.Remove, et.Rename, et.Move)
	}

	// 场景 2：字段存在且用户全关 → 不得被覆盖
	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"lifeMonitor":{"eventTypes":{"create":false,"remove":false,"rename":false,"move":false}}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	et2 := s2.LifeMonitor.EventTypes
	if et2.Create || et2.Remove || et2.Rename || et2.Move {
		t.Errorf("用户显式全关不得被覆盖, got create=%v remove=%v rename=%v move=%v",
			et2.Create, et2.Remove, et2.Rename, et2.Move)
	}
}

// TestReadSettings_MigratesCleanupDefaults 清理默认值反转（一次性）：
// 旧配置没有 fullSyncCleanupOrphans → 开启 pickcode 级清理；用户显式设成 off 时不得覆盖。
func TestReadSettings_MigratesCleanupDefaults(t *testing.T) {
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"download":{"minFileSize":0}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got := s1.Download.FullSyncCleanupOrphans; got != "auto_clean" {
		t.Errorf("缺字段应迁移为 auto_clean, got %q", got)
	}

	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"download":{"fullSyncCleanupOrphans":"off"}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got := s2.Download.FullSyncCleanupOrphans; got != "off" {
		t.Errorf("用户显式关闭不应被覆盖, got %q", got)
	}
}

// TestReadSettings_CleanupRelatedDefaults 清理连带动作的一次性迁移：
// 老配置补齐 RemoveRelatedFiles=true 并写入标记；标记存在后尊重用户手动关闭。
func TestReadSettings_CleanupRelatedDefaults(t *testing.T) {
	// 1) 完全没有 cleanup 块 → 补默认（含连带删关联文件）
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"download":{}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if !s1.Cleanup.RemoveRelatedFiles {
		t.Error("缺 cleanup 块时应补为连带删除关联文件")
	}
	if !s1.CleanupDefaultsV2 {
		t.Error("应写入迁移标记")
	}

	// 2) 用户显式关闭 + 标记已存在 → 不得覆盖
	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"cleanupDefaultsV2":true,"cleanup":{"maxThreshold":1000,"maxRatio":0.1,"maxDirs":20,"removeRelatedFiles":false}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if s2.Cleanup.RemoveRelatedFiles {
		t.Error("标记存在时不应覆盖用户手动关闭")
	}
}
