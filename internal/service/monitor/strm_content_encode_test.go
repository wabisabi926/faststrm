package monitor

import (
	"strings"
	"testing"
)

// TestGenerateStrmContent_Encoding 默认分支编码规则：
// 空格 → %20（不是 '+'，router 不还原 '+'）、'/' 保留、中文百分号编码
func TestGenerateStrmContent_Encoding(t *testing.T) {
	out := generateStrmContent(
		"电影/合集/x.mkv", "http://nas:8090", "副卡", "abcdefghij1234567",
		"美国狙击手 次时代/台.mkv", "100", true,
	)
	if strings.Contains(out, "+") {
		t.Fatalf("不应出现 '+'（router 不还原，会导致回读名字带加号）: %s", out)
	}
	if !strings.Contains(out, "%20") {
		t.Fatalf("空格应编码为 %%20: %s", out)
	}
	if strings.Contains(out, "%2F") {
		t.Fatalf("'/' 不应被编码为 %%2F: %s", out)
	}
	if !strings.Contains(out, "/%E5%8F%B0.mkv") {
		t.Fatalf("'/' 应保留（其后中文照常编码）: %s", out)
	}
	if !strings.Contains(out, "account=%E5%89%AF%E5%8D%A1") {
		t.Fatalf("中文账号应百分号编码: %s", out)
	}
}

// TestGenerateStrmContent_DisableEncode 关闭编码时参数原样输出
func TestGenerateStrmContent_DisableEncode(t *testing.T) {
	out := generateStrmContent(
		"电影/x.mkv", "http://nas:8090", "副卡", "abcdefghij1234567",
		"电影 2024.mkv", "100", false,
	)
	if !strings.Contains(out, "account=副卡") || !strings.Contains(out, "file_name=电影 2024.mkv") {
		t.Fatalf("关闭编码应原样输出: %s", out)
	}
}

// TestGenerateStrmContent_MatchesTaskFormat 与 task 侧默认格式一致（参数顺序 + 编码规则）
func TestGenerateStrmContent_MatchesTaskFormat(t *testing.T) {
	out := generateStrmContent(
		"电影/x.mkv", "http://nas:8090", "acc1", "abcdefghij1234567",
		"movie.mkv", "100", true,
	)
	want := "http://nas:8090/api/strm?account=acc1&pickcode=abcdefghij1234567&file_name=movie.mkv\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}
