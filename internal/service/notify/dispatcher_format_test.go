package notify

import (
	"strings"
	"testing"
)

// TestFormatTaskStatusMessage_UsesFormatMessage 验证任务状态通知走 FormatMessage
func TestFormatTaskStatusMessage_UsesFormatMessage(t *testing.T) {
	out := FormatTaskStatusMessage("STRM 全量同步", "运行中", "进度 50%")
	if !strings.HasPrefix(out, "<b>🎬 任务状态</b>\n") {
		t.Errorf("应以 <b>🎬 任务状态</b> 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "STRM 全量同步") {
		t.Errorf("应含任务名, 实际 %s", out)
	}
	if !strings.Contains(out, "状态：") {
		t.Errorf("应含全角冒号 状态：, 实际 %s", out)
	}
	if !strings.Contains(out, "详情：") {
		t.Errorf("应含全角冒号 详情：, 实际 %s", out)
	}
}

// TestFormatDownloadCompleteMessage_UsesFormatMessage 验证任务完成通知走 FormatMessage
func TestFormatDownloadCompleteMessage_UsesFormatMessage(t *testing.T) {
	out := FormatDownloadCompleteMessage("全量同步", 100, 95, 60000)
	if !strings.HasPrefix(out, "<b>✅ 任务完成</b>\n") {
		t.Errorf("应以 <b>✅ 任务完成</b> 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "文件数：") {
		t.Errorf("应含全角冒号 文件数：, 实际 %s", out)
	}
	if !strings.Contains(out, "95 / 100") {
		t.Errorf("应含 95 / 100, 实际 %s", out)
	}
	if !strings.Contains(out, "耗时：") {
		t.Errorf("应含全角冒号 耗时：, 实际 %s", out)
	}
}

// TestFormatErrorMessage_UsesFormatMessage 验证任务错误通知走 FormatMessage
func TestFormatErrorMessage_UsesFormatMessage(t *testing.T) {
	out := FormatErrorMessage("全量同步", "Cookie 过期")
	if !strings.HasPrefix(out, "<b>❌ 任务错误</b>\n") {
		t.Errorf("应以 <b>❌ 任务错误</b> 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "错误：") {
		t.Errorf("应含全角冒号 错误：, 实际 %s", out)
	}
	if !strings.Contains(out, "Cookie 过期") {
		t.Errorf("应含错误信息, 实际 %s", out)
	}
}
