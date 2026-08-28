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
