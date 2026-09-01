package monitor

import (
	"strings"
	"testing"
)

// ================================================================
// generateStrmContent — 监控模块生成 STRM URL（方案 A 验证）
// 核心保证：生成的 URL 必须包含 /api/strm，不能残留 /api/fs/get
// ================================================================

// TestGenerateStrmContent_AlwaysUsesStrmEndpoint 方案 A 后统一 /api/strm
func TestGenerateStrmContent_AlwaysUsesStrmEndpoint(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		account    string
		pickcode   string
		fileName   string
		contain    string // 必须包含
		notContain string // 绝对不能包含
	}{
		{
			name:       "basic mkv",
			prefix:     "http://192.168.1.100:8090",
			account:    "主号",
			pickcode:   "abcdef123456789",
			fileName:   "movie.mkv",
			contain:    "/api/strm?account",
			notContain: "/api/fs/get",
		},
		{
			name:       "iso原盘",
			prefix:     "http://127.0.0.1:8090",
			account:    "acc2",
			pickcode:   "ABCDEFGHIJ7654321",
			fileName:   "Bluray.iso",
			contain:    "/api/strm?account",
			notContain: "/api/fs/get",
		},
		{
			name:       "prefix with trailing slash",
			prefix:     "http://192.168.1.100:8090/",
			account:    "acc3",
			pickcode:   "abc123def456ghi",
			fileName:   "show.mkv",
			contain:    "/api/strm?account",
			notContain: "//api/strm", // 不应有双斜杠
		},
		{
			name:       "public prefix with custom path",
			prefix:     "https://example.com:8090/strm",
			account:    "主号",
			pickcode:   "xyz789abc123456",
			fileName:   "test.m2ts",
			contain:    "/api/strm?account",
			notContain: "/api/fs/get",
		},
		{
			name:       "prefix already ends with /api/strm (edge)",
			prefix:     "http://host:8090/api/strm",
			account:    "accX",
			pickcode:   "aaa111bbb222ccc",
			fileName:   "edge.mp4",
			contain:    "/api/strm?account",
			notContain: "/api/fs/get",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := generateStrmContent(
				"/cloud/path/"+c.fileName,
				c.prefix, false,
				c.account, c.pickcode, c.fileName,
			)
			if out == "" {
				t.Fatal("expected non-empty STRM content")
			}
			if !strings.Contains(out, c.contain) {
				t.Errorf("expected %q in output, got: %s", c.contain, out)
			}
			if c.notContain != "" && strings.Contains(out, c.notContain) {
				t.Errorf("unexpected %q in output: %s", c.notContain, out)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Error("expected trailing newline")
			}
		})
	}
}

// TestGenerateStrmContent_PickcodeEmpty_ReturnsEmpty 防御：pickcode 缺失不生成
func TestGenerateStrmContent_PickcodeEmpty_ReturnsEmpty(t *testing.T) {
	out := generateStrmContent(
		"/cloud/path.mkv",
		"http://host:8090", false,
		"acc", "", "path.mkv",
	)
	if out != "" {
		t.Errorf("expected empty for missing pickcode, got: %q", out)
	}
}

// TestGenerateStrmContent_AccountURLEscaped 中文 account 正确编码
func TestGenerateStrmContent_AccountURLEscaped(t *testing.T) {
	out := generateStrmContent(
		"/test.mkv",
		"http://host:8090", false,
		"主号 A", "abcdef123456789", "test.mkv",
	)
	if !strings.Contains(out, "account=%E4%B8%BB") {
		t.Errorf("account should be URL-encoded, got: %s", out)
	}
	if !strings.Contains(out, "file_name=test.mkv") {
		t.Errorf("file_name param missing: %s", out)
	}
}

// TestGenerateStrmContent_NoEnable302_Remnant 确保 enable302 删除后函数签名/行为干净
func TestGenerateStrmContent_NoEnable302_Remnant(t *testing.T) {
	// 这是方案 A 的核心兜底：函数不应该再接受 enable302 参数
	// 也不应该生成 /api/fs/get 风格 URL（即使 account 非空也不追加到路径）
	out := generateStrmContent(
		"/some/file.mkv",
		"http://host:8090", false,
		"acc1", "abcdef123456789", "file.mkv",
	)
	// 旧 enable302=false 时 URL 形如 http://host:8090/acc1/some/file.mkv
	// 现在必须是 /api/strm query URL
	if !strings.Contains(out, "/api/strm?") {
		t.Errorf("must use /api/strm query URL, got: %s", out)
	}
	if strings.Contains(out, "/acc1/") {
		t.Errorf("old non-302 path-append style detected: %s", out)
	}
}
