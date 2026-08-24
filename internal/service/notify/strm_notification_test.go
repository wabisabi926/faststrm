package notify

import (
	"strings"
	"testing"
)

// TestBuildCreateNotification_UsesFormatMessage 验证 STRM 创建通知走 FormatMessage
func TestBuildCreateNotification_UsesFormatMessage(t *testing.T) {
	b := NewStrmNotifyBuilder()
	n := b.BuildCreateNotification(STRMCreateInput{
		Account: "acc1", Kind: "movie", CloudPath: "/cloud/a.mkv",
		LocalPath: "/local/a.strm", FileSize: 1024 * 1024 * 1024,
	})
	if n.Type != TypeSTRMCreate {
		t.Errorf("Type 期望 %s, 实际 %s", TypeSTRMCreate, n.Type)
	}
	if n.Title != "📺 STRM 已创建" {
		t.Errorf("Title 期望 📺 STRM 已创建, 实际 %s", n.Title)
	}
	if !strings.HasPrefix(n.Content, "<b>📺 STRM 已创建</b>\n") {
		t.Errorf("Content 应以 <b>📺 STRM 已创建</b> 开头, 实际 %s", n.Content)
	}
	if n.Metadata["account"] != "acc1" {
		t.Errorf("Metadata.account 期望 acc1, 实际 %s", n.Metadata["account"])
	}
	if n.Metadata["kind"] != "movie" {
		t.Errorf("Metadata.kind 期望 movie, 实际 %s", n.Metadata["kind"])
	}
	if !strings.Contains(n.Content, "账号：") {
		t.Errorf("Content 应含全角冒号 账号：, 实际 %s", n.Content)
	}
	if !strings.Contains(n.Content, "大小：") {
		t.Errorf("Content 应含大小字段（FileSize>0 时）, 实际 %s", n.Content)
	}
}

// TestBuildCreateNotification_NoFileSize 验证 FileSize=0 时不输出大小字段
func TestBuildCreateNotification_NoFileSize(t *testing.T) {
	b := NewStrmNotifyBuilder()
	n := b.BuildCreateNotification(STRMCreateInput{
		Account: "acc", Kind: "tv", CloudPath: "/c", LocalPath: "/l",
	})
	if strings.Contains(n.Content, "大小：") {
		t.Errorf("FileSize=0 时不应输出大小字段, 实际 %s", n.Content)
	}
}

// TestBuildDeleteNotification_UsesFormatMessage 验证 STRM 删除通知走 FormatMessage
func TestBuildDeleteNotification_UsesFormatMessage(t *testing.T) {
	b := NewStrmNotifyBuilder()
	n := b.BuildDeleteNotification(STRMDeleteInput{
		Account: "acc2", Kind: "movie", CloudPath: "/c", LocalPath: "/l",
	})
	if n.Type != TypeSTRMDelete {
		t.Errorf("Type 期望 %s, 实际 %s", TypeSTRMDelete, n.Type)
	}
	if n.Priority != PriorityHigh {
		t.Errorf("删除通知 Priority 期望 high, 实际 %s", n.Priority)
	}
	if !strings.HasPrefix(n.Content, "<b>🗑️ STRM 已删除</b>\n") {
		t.Errorf("Content 应以 <b>🗑️ STRM 已删除</b> 开头, 实际 %s", n.Content)
	}
}

// TestBuildMoveNotification_UsesFormatMessage 验证 STRM 移动通知走 FormatMessage
func TestBuildMoveNotification_UsesFormatMessage(t *testing.T) {
	b := NewStrmNotifyBuilder()
	n := b.BuildMoveNotification(STRMMoveInput{
		Account: "acc", Kind: "tv", CloudPath: "/c", LocalPath: "/l",
	})
	if !strings.HasPrefix(n.Content, "<b>📦 STRM 已移动</b>\n") {
		t.Errorf("Content 应以 <b>📦 STRM 已移动</b> 开头, 实际 %s", n.Content)
	}
	if n.Priority != PriorityNormal {
		t.Errorf("移动通知 Priority 期望 normal, 实际 %s", n.Priority)
	}
}

// TestBuildRenameNotification_UsesFormatMessage 验证 STRM 重命名通知走 FormatMessage
func TestBuildRenameNotification_UsesFormatMessage(t *testing.T) {
	b := NewStrmNotifyBuilder()
	n := b.BuildRenameNotification(STRMRenameInput{
		Account: "acc", Kind: "tv", CloudPath: "/c", LocalPath: "/l",
	})
	if !strings.HasPrefix(n.Content, "<b>✏️ STRM 已重命名</b>\n") {
		t.Errorf("Content 应以 <b>✏️ STRM 已重命名</b> 开头, 实际 %s", n.Content)
	}
}
