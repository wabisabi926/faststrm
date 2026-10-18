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

// TestSanitizeRelPathNoEscape 清理后的路径不得逃逸出目标目录
func TestSanitizeRelPathNoEscape(t *testing.T) {
	for _, in := range []string{"../a.mkv", "a/../../b.mkv", "..", "./.."} {
		got := SanitizeRelPath(in)
		if strings.HasPrefix(got, "..") || strings.Contains(got, ".."+string(filepath.Separator)) {
			t.Fatalf("SanitizeRelPath(%q) = %q 仍包含上级目录", in, got)
		}
	}
}
