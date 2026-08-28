package notify

import (
	"strings"
	"testing"
)

// TestFormatMessage_FullWidthColon 验证 Metadata 使用全角冒号（：）而非半角（:）
func TestFormatMessage_FullWidthColon(t *testing.T) {
	out := FormatMessage("标题", "内容", map[string]string{"键": "值"})
	if !strings.Contains(out, "<b>键：</b> 值") {
		t.Errorf("期望全角冒号，实际输出: %s", out)
	}
	if strings.Contains(out, "<b>键:</b>") {
		t.Errorf("不应出现半角冒号，实际输出: %s", out)
	}
}

// TestFormatMessage_MetadataSortedByPriority 验证 Metadata 按 metadataKeyOrder 业务优先级排序
// 顺序：账号(0) < 类型(1) < 大小(12) < 时间(21)，而非 UTF-8 字节序（大小<时间<类型<账号）
func TestFormatMessage_MetadataSortedByPriority(t *testing.T) {
	out := FormatMessage("T", "C", map[string]string{
		"账号": "acc",
		"大小": "1GB",
		"类型": "movie",
		"时间": "2026-01-01",
	})
	idxAcc := strings.Index(out, "账号：")
	idxSize := strings.Index(out, "大小：")
	idxTime := strings.Index(out, "时间：")
	idxType := strings.Index(out, "类型：")
	// 业务优先级：账号 < 类型 < 大小 < 时间
	if !(idxAcc < idxType && idxType < idxSize && idxSize < idxTime) {
		t.Errorf("Metadata 未按业务优先级排序，acc=%d type=%d size=%d time=%d\n%s",
			idxAcc, idxType, idxSize, idxTime, out)
	}
}

// TestFormatMessage_UnknownKeyFallback 验证未列入 metadataKeyOrder 的 key 按 UTF-8 序追加在末尾
func TestFormatMessage_UnknownKeyFallback(t *testing.T) {
	out := FormatMessage("T", "C", map[string]string{
		"账号":    "acc", // 已列入，优先级 0
		"自定义字段": "val", // 未列入，走 fallback
	})
	idxAcc := strings.Index(out, "账号：")
	idxCustom := strings.Index(out, "自定义字段：")
	if idxAcc == -1 || idxCustom == -1 {
		t.Fatalf("字段缺失, acc=%d custom=%d\n%s", idxAcc, idxCustom, out)
	}
	// 已列入的 key 应排在未列入的 key 前面
	if !(idxAcc < idxCustom) {
		t.Errorf("已列入 key 应在未列入 key 前，acc=%d custom=%d\n%s", idxAcc, idxCustom, out)
	}
}

// TestFormatMessage_EmptyContentSkipped 验证 content 为空时不输出 content 行
func TestFormatMessage_EmptyContentSkipped(t *testing.T) {
	out := FormatMessage("标题", "", map[string]string{"k": "v"})
	if !strings.HasPrefix(out, "<b>标题</b>\n<b>k：</b> v\n\n") {
		t.Errorf("空 content 时输出格式不符: %q", out)
	}
}

// TestFormatMessage_EmptyMetadataValueSkipped 验证值为空的 metadata 不输出
func TestFormatMessage_EmptyMetadataValueSkipped(t *testing.T) {
	out := FormatMessage("T", "C", map[string]string{
		"有值": "ok",
		"空值": "",
	})
	if strings.Contains(out, "空值") {
		t.Errorf("空值 metadata 应被跳过，实际: %s", out)
	}
	if !strings.Contains(out, "有值：") {
		t.Errorf("期望包含有值，实际: %s", out)
	}
}

// TestFormatMessage_NoMetadata 验证无 metadata 时只有标题和内容
func TestFormatMessage_NoMetadata(t *testing.T) {
	out := FormatMessage("标题", "内容", nil)
	expected := "<b>标题</b>\n内容\n"
	if out != expected {
		t.Errorf("期望 %q, 实际 %q", expected, out)
	}
}
