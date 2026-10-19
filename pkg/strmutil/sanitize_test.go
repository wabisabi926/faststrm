package strmutil

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFileName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "movie.mkv", "movie.mkv"},
		{"chinese", "流浪地球2.2023.mkv", "流浪地球2.2023.mkv"},
		{"colon", "movie:123.mkv", "movie：123.mkv"},
		{"fullwidth colon kept", "movie：123.mkv", "movie：123.mkv"},
		{"angle brackets", "movie<>test.mkv", "movie__test.mkv"},
		{"pipe", "movie|test.mkv", "movie_test.mkv"},
		{"question", "movie?.mkv", "movie_.mkv"},
		{"asterisk", "movie*.mkv", "movie_.mkv"},
		{"quotes", "movie\"test.mkv", "movie_test.mkv"},
		{"backslash", `movie\test.mkv`, "movie_test.mkv"},
		{"slash", "movie/test.mkv", "movie_test.mkv"},
		{"multiple slashes", "a/b/c.mkv", "a_b_c.mkv"},
		{"multiple", "a:b<c>d|e?.mkv", "a：b_c_d_e_.mkv"},
		{"control chars", "movie\x01\x1ftest.mkv", "movie__test.mkv"},
		{"trailing dot", "movie.", "movie"},
		{"trailing space", "movie ", "movie"},
		{"only dots", "..", "_"},
		{"only illegal", "???", "___"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeFileName(c.input)
			if got != c.want {
				t.Fatalf("SanitizeFileName(%q) = %q, want %q", c.input, got, c.want)
			}
			// 幂等：清理过的名字再清理一次必须不变（否则每次任务都会重写文件）
			if again := SanitizeFileName(got); again != got {
				t.Fatalf("SanitizeFileName not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestSanitizeRelPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string // 期望的路径段
	}{
		{"empty", "", nil},
		{"single", "movie.mkv", []string{"movie.mkv"}},
		{"nested", "dir/sub/movie.mkv", []string{"dir", "sub", "movie.mkv"}},
		{"colon in dir", "电影:合集/movie.mkv", []string{"电影：合集", "movie.mkv"}},
		{"illegal in dir", "a|b/c?d/movie*.mkv", []string{"a_b", "c_d", "movie_.mkv"}},
		{"traversal", "../etc/passwd", []string{"_", "etc", "passwd"}},
		{"duplicate slashes", "a//b.mkv", []string{"a", "b.mkv"}},
		{"slash in segment (already sanitized by caller)", "dir/movie-test.mkv", []string{"dir", "movie-test.mkv"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeRelPath(c.input)
			if len(c.want) == 0 {
				if got != "" {
					t.Fatalf("SanitizeRelPath(%q) = %q, want empty", c.input, got)
				}
				return
			}
			want := filepath.Join(c.want...)
			if got != want {
				t.Fatalf("SanitizeRelPath(%q) = %q, want %q", c.input, got, want)
			}
			if again := SanitizeRelPath(got); again != got {
				t.Fatalf("SanitizeRelPath not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// TestEscapeName 云端文件名拼入路径前的结构转义：'/' '\' → '_'，其余字符不动。
func TestEscapeName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "movie.mkv", "movie.mkv"},
		{"slash", "美国狙击手 次时代/台/粤音轨.mkv", "美国狙击手 次时代_台_粤音轨.mkv"},
		{"backslash", `a\b.mkv`, "a_b.mkv"},
		{"mixed", `a/b\c.mkv`, "a_b_c.mkv"},
		{"colon kept", "movie:1.mkv", "movie:1.mkv"}, // ':' 属于合法性清理（SanitizeFileName），结构转义不动它
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EscapeName(c.input)
			if got != c.want {
				t.Fatalf("EscapeName(%q) = %q, want %q", c.input, got, c.want)
			}
			// 幂等
			if again := EscapeName(got); again != got {
				t.Fatalf("EscapeName not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// TestSanitizeRelPathNoEscape 清理后的路径不得逃逸出目标目录
func TestSanitizeRelPathNoEscape(t *testing.T) {
	for _, in := range []string{"../a.mkv", "a/../../b.mkv", "..", "./.."} {
		got := SanitizeRelPath(in)
		if strings.HasPrefix(got, "..") || strings.Contains(got, ".."+string(filepath.Separator)) {
			t.Fatalf("SanitizeRelPath(%q) = %q 仍包含上级目录", in, got)
		}
	}
}

// TestJoinCloudPath 云端路径拼接的唯一入口：名字必须结构转义，目录前缀只裁剪末尾分隔符
func TestJoinCloudPath(t *testing.T) {
	cases := []struct{ dir, name, want string }{
		{"", "movie.mkv", "movie.mkv"},
		{"电影", "movie.mkv", "电影/movie.mkv"},
		{"电影/", "movie.mkv", "电影/movie.mkv"}, // 前缀末尾多余 "/" 被裁掉
		{"电影", "国语次时代/台/粤音轨.mkv", "电影/国语次时代_台_粤音轨.mkv"},
		{"", "国语次时代/台/粤音轨.mkv", "国语次时代_台_粤音轨.mkv"}, // 空目录不带前导 "/"
		{"电影", `a\b.mkv`, "电影/a_b.mkv"},            // '\\' 也必须转义（SanitizeRelPath 会把它当分隔符）
		{"电影", "movie:1.mkv", "电影/movie:1.mkv"},    // ':' 不属结构转义
	}
	for _, c := range cases {
		if got := JoinCloudPath(c.dir, c.name); got != c.want {
			t.Errorf("JoinCloudPath(%q, %q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
	if got := AbsCloudPath("国语次时代/台.mkv"); got != "/国语次时代_台.mkv" {
		t.Errorf("AbsCloudPath = %q", got)
	}
}

// TestLocalNameToCloudName 本地名 → 云端名只还原可逆的全角冒号
func TestLocalNameToCloudName(t *testing.T) {
	cases := map[string]string{
		"movie：1.mkv": "movie:1.mkv",
		"plain.mkv":   "plain.mkv",
		"a_b.mkv":     "a_b.mkv", // '_' 不可逆，原样保留（与云端转义结果一致）
		"无冒号":         "无冒号",
	}
	for in, want := range cases {
		if got := LocalNameToCloudName(in); got != want {
			t.Errorf("LocalNameToCloudName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLocalRelToCloudRel 本地相对路径 → 云端相对路径：分隔符统一为 '/'，逐段还原全角冒号。
func TestLocalRelToCloudRel(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"movie.mkv": "movie.mkv",
		`电影\复仇者联盟：终局之战.mkv`:   "电影/复仇者联盟:终局之战.mkv",   // Windows 分隔符 → '/'，'：' → ':'
		"电影/复仇者联盟：终局之战.mkv":   "电影/复仇者联盟:终局之战.mkv",   // '/' 已是云端分隔符，保持不变
		`国语次时代_台_粤音轨\片：名.mkv`: "国语次时代_台_粤音轨/片:名.mkv", // '_' 不可逆，原样保留
	}
	for in, want := range cases {
		if got := LocalRelToCloudRel(in); got != want {
			t.Errorf("LocalRelToCloudRel(%q) = %q, want %q", in, got, want)
		}
	}
}
