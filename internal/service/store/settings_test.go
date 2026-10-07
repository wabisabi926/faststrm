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
