package notify

import (
	"strings"
	"testing"
)

// 群/超级群的 chat_id 是负数，旧实现过滤非数字字符会把 -100… 变成正数，消息必然发错。
func TestStringToInt64_NegativeChatID(t *testing.T) {
	cases := map[string]int64{
		"-1001234567890": -1001234567890,
		"123456789":      123456789,
		" -100 ":         -100,
		"":               0,
		"abc":            0,
	}
	for in, want := range cases {
		if got := stringToInt64(in); got != want {
			t.Errorf("stringToInt64(%q) = %d, want %d", in, got, want)
		}
	}
}

// 片名/路径里的 & < > 不转义会让 Telegram 返回 400 can't parse entities。
func TestEscapeHTML(t *testing.T) {
	if got := EscapeHTML("Fast & Furious <4K>"); got != "Fast &amp; Furious &lt;4K&gt;" {
		t.Fatalf("EscapeHTML = %q", got)
	}
	if got := EscapeHTML("普通片名.mkv"); got != "普通片名.mkv" {
		t.Fatalf("无特殊字符不应改变: %q", got)
	}
}

// 通知内联按钮（refresh_emby: / detail_strm:）必须被识别为菜单回调，
// 否则 HandleCallbackQuery 会落到 default 只回一句「已处理」，按钮等于失效。
func TestIsMenuCallbackData(t *testing.T) {
	known := []string{
		"menu_main", "mon_start:acc", "task_list", "emby_lib:movie",
		"strm_sync", "strm_cleanup", "task_exec:t1", "mon_event:create",
		"refresh_emby:movie", "detail_strm:0123456789abcdef",
	}
	for _, d := range known {
		if !isMenuCallbackData(d) {
			t.Errorf("%q 应交给 RouteMenuCallback", d)
		}
	}
	for _, d := range []string{"cleanup_confirm|123|y", "unknown", ""} {
		if isMenuCallbackData(d) {
			t.Errorf("%q 不应被当成菜单回调", d)
		}
	}
}

// detail_strm 的 callback_data 必须远小于 Telegram 64 字节上限，且能换回原路径。
func TestRegisterDetailPath_ShortAndResolvable(t *testing.T) {
	path := "/电影/美国狙击手 335部UHD原盘中字 央视国语次时代/台/粤音轨 国配简繁/简繁英特效四字幕.mkv"
	id := RegisterDetailPath(path)
	if len(id) != detailIDLen {
		t.Fatalf("短 ID 长度 = %d, want %d", len(id), detailIDLen)
	}
	if cb := "detail_strm:" + id; len(cb) > 64 {
		t.Fatalf("callback_data 超限: %d 字节", len(cb))
	}
	if got, ok := LookupDetailPath(id); !ok || got != path {
		t.Fatalf("LookupDetailPath = (%q,%v), want (%q,true)", got, ok, path)
	}
	// 确定性：同一路径重复注册得到同一 ID（同一条通知重发也稳定）
	if again := RegisterDetailPath(path); again != id {
		t.Fatalf("ID 不稳定: %q != %q", again, id)
	}
	if RegisterDetailPath("") != "" {
		t.Fatal("空路径应返回空 ID")
	}
}

// FormatMessage 负责渲染所有 TG 通知，用户内容必须转义后才能进 HTML parse mode。
func TestFormatMessage_EscapesUserContent(t *testing.T) {
	out := FormatMessage("标题", "Fast & Furious <4K>", map[string]string{
		"云端路径": "/电影/A&B.mkv",
	})
	if strings.Contains(out, "&Furious") || strings.Contains(out, "<4K>") {
		t.Fatalf("正文未转义: %s", out)
	}
	if !strings.Contains(out, "&amp;") || !strings.Contains(out, "&lt;4K&gt;") {
		t.Fatalf("正文缺少转义实体: %s", out)
	}
	if !strings.Contains(out, "A&amp;B") {
		t.Fatalf("metadata 值未转义: %s", out)
	}
	// 标题里的 <b> 是渲染标签，不能被转义
	if !strings.HasPrefix(out, "<b>标题</b>") {
		t.Fatalf("标题标签被破坏: %s", out)
	}
}
