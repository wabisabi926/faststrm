package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// === cleanup_store.go: GenerateCleanupRequestID ===

func TestGenerateCleanupRequestID(t *testing.T) {
	id1 := GenerateCleanupRequestID()
	id2 := GenerateCleanupRequestID()

	if len(id1) != 16 {
		t.Fatalf("id length: got %d, want 16", len(id1))
	}
	if id1 == id2 {
		t.Fatal("two IDs should be different (random)")
	}
	// Should be hex string
	for _, c := range id1 {
		if !isHexChar(c) {
			t.Fatalf("ID should be hex, found %q", c)
		}
	}
}

func isHexChar(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

// === executor.go: itoa, ensureDir, jsonLine ===

func TestExecutorItoa(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{0, "0"}, {42, "42"}, {-1, "-1"}, {9999999999, "9999999999"},
	}
	for _, c := range cases {
		got := itoa(c.input)
		if got != c.want {
			t.Fatalf("itoa(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestEnsureDir(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		if err := ensureDir(""); err != nil {
			t.Fatalf("ensureDir(\"\") should return nil, got %v", err)
		}
	})

	t.Run("current dir", func(t *testing.T) {
		if err := ensureDir("."); err != nil {
			t.Fatalf("ensureDir(\".\") should return nil, got %v", err)
		}
	})

	t.Run("create nested dir", func(t *testing.T) {
		tmp := t.TempDir()
		nested := filepath.Join(tmp, "a", "b", "c")
		if err := ensureDir(nested); err != nil {
			t.Fatalf("ensureDir(%q) error: %v", nested, err)
		}
		info, err := os.Stat(nested)
		if err != nil || !info.IsDir() {
			t.Fatalf("dir should exist: %v", err)
		}
	})
}

func TestJsonLine(t *testing.T) {
	t.Run("valid struct", func(t *testing.T) {
		type point struct{ X, Y int }
		got := jsonLine(point{X: 1, Y: 2})
		want := `{"X":1,"Y":2}`
		if got != want {
			t.Fatalf("jsonLine() = %q, want %q", got, want)
		}
	})

	t.Run("nil value", func(t *testing.T) {
		got := jsonLine(nil)
		if got != "null" {
			t.Fatalf("jsonLine(nil) = %q, want %q", got, "null")
		}
	})
}

// === executor_utils.go: getStrmFileName, replaceRelPathExtToStrm, sanitizeCloudRelPath, normalizeExt ===

func TestGetStrmFileName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"mkv", "movie.mkv", "movie.strm"},
		{"mp4", "video.mp4", "video.strm"},
		{"iso", "game.iso", "game.iso.strm"},
		{"ISO uppercase", "Game.ISO", "Game.iso.strm"},
		{"no ext", "noext", "noext.strm"},
		{"double ext", "archive.tar.gz", "archive.tar.strm"},
		{"dotfile (ext=full)", ".gitignore", ".strm"}, // filepath.Ext(".gitignore") = ".gitignore", stem = ""
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := getStrmFileName(c.input)
			if got != c.want {
				t.Fatalf("getStrmFileName(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestReplaceRelPathExtToStrm(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "dir/movie.mkv", filepath.Join("dir", "movie.strm")},
		{"nested", "a/b/c/video.mp4", filepath.Join("a", "b", "c", "video.strm")},
		{"iso", "dir/game.iso", filepath.Join("dir", "game.iso.strm")},
		{"no ext", "dir/noext", filepath.Join("dir", "noext.strm")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := replaceRelPathExtToStrm(c.input)
			if got != c.want {
				t.Fatalf("replaceRelPathExtToStrm(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestSanitizeCloudRelPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "movie.mkv", "movie.mkv"},
		{"with colon", "movie:123.mkv", "movie：123.mkv"},
		{"with angle brackets", "movie<>test.mkv", "movie__test.mkv"},
		{"with pipe", "movie|test.mkv", "movie_test.mkv"},
		{"with question mark", "movie?.mkv", "movie_.mkv"},
		{"with asterisk", "movie*.mkv", "movie_.mkv"},
		{"with quotes", "movie\"test.mkv", "movie_test.mkv"},
		{"multiple replacements", "a:b<c>d|e?.mkv", "a：b_c_d_e_.mkv"},
		{"with slashes", "dir/movie.mkv", filepath.Join("dir", "movie.mkv")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeCloudRelPath(c.input)
			// On Windows, filepath.Separator is \, on Linux it's /
			// Normalize for comparison
			wantNormalized := strings.ReplaceAll(c.want, "/", string(filepath.Separator))
			if got != wantNormalized {
				t.Fatalf("sanitizeCloudRelPath(%q) = %q, want %q", c.input, got, wantNormalized)
			}
		})
	}
}

func TestNormalizeExt(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"mkv", ".mkv"},
		{".mkv", ".mkv"},
		{"mp4", ".mp4"},
		{".mp4", ".mp4"},
		{"iso", ".iso"},
		{".strm", ".strm"},
	}
	for _, c := range cases {
		got := normalizeExt(c.input)
		if got != c.want {
			t.Fatalf("normalizeExt(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
