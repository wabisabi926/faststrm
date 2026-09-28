package model

import (
	"encoding/json"
	"testing"
)

// TestLifeMonitorDefaultSettings 测试 DefaultSettings 中 LifeMonitor 的新增字段默认值
func TestLifeMonitorDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	lm := s.LifeMonitor

	// P0-5 默认值
	if lm.TransferStallTimeoutMinutes != 30 {
		t.Fatalf("TransferStallTimeoutMinutes default: expected 30, got %d", lm.TransferStallTimeoutMinutes)
	}
	if lm.TransferWaitMode != "skip" {
		t.Fatalf("TransferWaitMode default: expected skip, got %s", lm.TransferWaitMode)
	}

	// P0-6 默认值
	if !lm.RenameAutoRelatedFiles {
		t.Fatalf("RenameAutoRelatedFiles default should be true")
	}
	if !lm.MoveLocalMoveRelatedFiles {
		t.Fatalf("MoveLocalMoveRelatedFiles default should be true")
	}

	// P0-7 默认值
	if lm.MoveMediaMode != "local_move" {
		t.Fatalf("MoveMediaMode default: expected local_move, got %s", lm.MoveMediaMode)
	}
	if lm.MoveMediaKeepOldStrm {
		t.Fatalf("MoveMediaKeepOldStrm default should be false")
	}
	if !lm.MoveMediaCreateNewStrm {
		t.Fatalf("MoveMediaCreateNewStrm default should be true")
	}
	if !lm.MoveOutRemoveLocalStrm {
		t.Fatalf("MoveOutRemoveLocalStrm default should be true")
	}
}

// TestLifeMonitorJSONRoundTrip 测试新字段的 JSON 序列化/反序列化往返
func TestLifeMonitorJSONRoundTrip(t *testing.T) {
	original := LifeMonitorSettings{
		TransferStallTimeoutMinutes: 60,
		TransferWaitMode:            "abort",
		RenameAutoRelatedFiles:      false,
		MoveLocalMoveRelatedFiles:   false,
		MoveMediaKeepOldStrm:        true,
		MoveMediaCreateNewStrm:      false,
		MoveOutRemoveLocalStrm:      false,
		MoveMediaMode:               "local_move",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded LifeMonitorSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.TransferStallTimeoutMinutes != 60 {
		t.Fatalf("TransferStallTimeoutMinutes: expected 60, got %d", decoded.TransferStallTimeoutMinutes)
	}
	if decoded.TransferWaitMode != "abort" {
		t.Fatalf("TransferWaitMode: expected abort, got %s", decoded.TransferWaitMode)
	}
	if decoded.RenameAutoRelatedFiles {
		t.Fatalf("RenameAutoRelatedFiles: expected false")
	}
	if decoded.MoveLocalMoveRelatedFiles {
		t.Fatalf("MoveLocalMoveRelatedFiles: expected false")
	}
	if !decoded.MoveMediaKeepOldStrm {
		t.Fatalf("MoveMediaKeepOldStrm: expected true")
	}
	if decoded.MoveMediaCreateNewStrm {
		t.Fatalf("MoveMediaCreateNewStrm: expected false")
	}
	if decoded.MoveOutRemoveLocalStrm {
		t.Fatalf("MoveOutRemoveLocalStrm: expected false")
	}
	if decoded.MoveMediaMode != "local_move" {
		t.Fatalf("MoveMediaMode: expected local_move, got %s", decoded.MoveMediaMode)
	}
}

// TestLifeMonitorJSONMissingFieldsDefaults 测试缺失字段时 JSON 反序列化使用零值（配合 DefaultSettings 基础）
func TestLifeMonitorJSONMissingFieldsDefaults(t *testing.T) {
	// 模拟旧版配置文件（不含新字段）
	oldJSON := `{"pollInterval":10,"removeEmptyDirs":true,"moveMediaMode":"recreate"}`

	// 先用 DefaultSettings 作为基础，再反序列化（对齐 config.Load 逻辑）
	base := DefaultSettings()
	if err := json.Unmarshal([]byte(oldJSON), &base.LifeMonitor); err != nil {
		t.Fatalf("Unmarshal old config: %v", err)
	}

	lm := base.LifeMonitor
	// 旧配置中存在的字段应被覆盖
	if lm.PollInterval != 10 {
		t.Fatalf("PollInterval: expected 10, got %d", lm.PollInterval)
	}
	if !lm.RemoveEmptyDirs {
		t.Fatalf("RemoveEmptyDirs: expected true")
	}
	if lm.MoveMediaMode != "recreate" {
		t.Fatalf("MoveMediaMode: expected recreate, got %s", lm.MoveMediaMode)
	}

	// 新字段应保留 DefaultSettings 中的默认值（不被旧JSON覆盖）
	if lm.TransferStallTimeoutMinutes != 30 {
		t.Fatalf("TransferStallTimeoutMinutes should retain default 30, got %d", lm.TransferStallTimeoutMinutes)
	}
	if lm.TransferWaitMode != "skip" {
		t.Fatalf("TransferWaitMode should retain default skip, got %s", lm.TransferWaitMode)
	}
	if !lm.RenameAutoRelatedFiles {
		t.Fatalf("RenameAutoRelatedFiles should retain default true")
	}
	if !lm.MoveLocalMoveRelatedFiles {
		t.Fatalf("MoveLocalMoveRelatedFiles should retain default true")
	}
	if !lm.MoveMediaCreateNewStrm {
		t.Fatalf("MoveMediaCreateNewStrm should retain default true")
	}
	if !lm.MoveOutRemoveLocalStrm {
		t.Fatalf("MoveOutRemoveLocalStrm should retain default true")
	}
}

// TestIsBdmvStreamPath 校验 BDMV/STREAM 原盘路径判定：
// 命中则跳过（避免一个原盘被拆成上百个 m2ts STRM），但独立的 .m2ts/.ts 不受影响。
func TestIsBdmvStreamPath(t *testing.T) {
	hit := []string{
		"电影/沙丘/BDMV/STREAM/00000.m2ts",
		"/电影/沙丘/BDMV/STREAM/00000.m2ts",
		"电影/沙丘/bdmv/stream/00000.m2ts", // 小写
		`电影\沙丘\BDMV\STREAM\00000.m2ts`,   // 反斜杠
		"电影/沙丘/BDMV/STREAM",             // STREAM 目录本身（需整目录跳过）
		"BDMV/STREAM/a.m2ts",               // 根层级
	}
	for _, p := range hit {
		if !IsBdmvStreamPath(p) {
			t.Errorf("IsBdmvStreamPath(%q) = false, want true", p)
		}
	}

	miss := []string{
		"",                                    // 空
		"电影/沙丘/沙丘.mkv",                     // 普通媒体
		"电影/沙丘/沙丘.iso",                     // ISO 原盘：不受影响
		"电影/沙丘.iso.strm",                    // ISO 的 STRM
		"电影/沙丘/BDMV/index.bdmv",             // BDMV 根下但不在 STREAM 内
		"电影/沙丘/BDMV/PLAYLIST/00001.mpls",    // 播放列表
		"电影/沙丘/CERTIFICATE/id.bdmv",          // 证书目录
		"电影/m2ts/单文件.m2ts",                  // 独立 m2ts，路径里无 BDMV/STREAM
		"电影/BDMVX/STREAM/a.m2ts",              // 段名不是 BDMV（防止子串误判）
		"电影/BDMV/STREAMX/a.m2ts",              // 段名不是 STREAM（防止子串误判）
	}
	for _, p := range miss {
		if IsBdmvStreamPath(p) {
			t.Errorf("IsBdmvStreamPath(%q) = true, want false", p)
		}
	}
}
