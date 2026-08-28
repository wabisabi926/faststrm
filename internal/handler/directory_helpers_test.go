package handler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendUnique(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		add   string
		want  []string
	}{
		{"add to nil", nil, "a", []string{"a"}},
		{"add to empty", []string{}, "a", []string{"a"}},
		{"add unique", []string{"a"}, "b", []string{"a", "b"}},
		{"skip duplicate", []string{"a"}, "a", []string{"a"}},
		{"skip duplicate multi", []string{"a", "b", "c"}, "b", []string{"a", "b", "c"}},
		{"add to multi", []string{"x", "y"}, "z", []string{"x", "y", "z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := appendUnique(c.input, c.add)
			if len(got) != len(c.want) {
				t.Fatalf("len mismatch: got %v (len=%d), want %v (len=%d)", got, len(got), c.want, len(c.want))
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("index %d: got %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestParseInt64(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"digits only", "123", 123, false},
		{"empty string", "", 0, false},
		{"single zero", "0", 0, false},
		{"single digit", "7", 7, false},
		{"long digits", "9999999999", 9999999999, false},
		{"letters mixed", "abc123", 0, false},
		{"letters only", "abc", 0, false},
		{"with dot", "1.5", 0, false},
		{"with minus", "-5", 0, false},
		{"with space", "12 34", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseInt64(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("err mismatch: got %v, wantErr=%v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestStrconvInt64(t *testing.T) {
	cases := []struct {
		name  string
		input int64
		want  string
	}{
		{"zero", 0, "0"},
		{"positive", 123, "123"},
		{"single digit", 5, "5"},
		{"negative", -456, "-456"},
		{"negative one", -1, "-1"},
		{"large positive", 9876543210, "9876543210"},
		{"large negative", -9876543210, "-9876543210"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strconvInt64(c.input)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestItoa64(t *testing.T) {
	// itoa64 is a thin wrapper around strconvInt64
	cases := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{123, "123"},
		{-456, "-456"},
	}
	for _, c := range cases {
		got := itoa64(c.input)
		if got != c.want {
			t.Fatalf("itoa64(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestStrconvAtoi64(t *testing.T) {
	// strconvAtoi64 is a thin wrapper around parseInt64
	cases := []struct {
		input string
		want  int64
	}{
		{"123", 123},
		{"0", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		got, _ := strconvAtoi64(c.input)
		if got != c.want {
			t.Fatalf("strconvAtoi64(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestAnyToString(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"int64", int64(123), "123"},
		{"int64 zero", int64(0), "0"},
		{"int", int(42), "42"},
		{"float64", float64(99.5), "99"},
		{"string", "hello", "hello"},
		{"empty string", "", ""},
		{"bool", true, "0"},
		{"nil", nil, "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := anyToString(c.input)
			if got != c.want {
				t.Fatalf("anyToString(%v) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestIsPathAllowed(t *testing.T) {
	// 非 fNOS 环境（无 TRIM_DATA_* 环境变量）应始终返回 true
	cases := []struct {
		name         string
		targetPath   string
		allowedPaths []string
	}{
		{"empty allowed paths", "/some/path", nil},
		{"with allowed paths", "/test/data", []string{"/test"}},
		{"absolute path", "C:\\Users", []string{"C:\\"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 确保没有 fNOS 环境变量干扰
			t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
			t.Setenv("TRIM_DATA_SHARE_PATHS", "")
			t.Setenv("FNOS_APP_DATA_DIR", "")
			t.Setenv("FNOS_APP_CONFIG_DIR", "")
			got := isPathAllowed(c.targetPath, c.allowedPaths)
			if !got {
				t.Fatalf("expected true on non-fNOS system")
			}
		})
	}
}

func TestNormalizeExt(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"already has dot", ".strm", ".strm"},
		{"no dot", "strm", ".strm"},
		{"single char", "m", ".m"},
		{"has dot not prefix", "file.strm", ".file.strm"}, // 注意：函数只检查前缀！
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeExt(c.input)
			if got != c.want {
				t.Fatalf("normalizeExt(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestRmEmptyDirs(t *testing.T) {
	t.Run("removes empty dirs but keeps dirs with files", func(t *testing.T) {
		root := t.TempDir()

		// 构建目录树：
		// root/
		//   empty1/           <- 空，应被删除
		//   hasfile/
		//     file.txt        <- 有文件，目录应保留
		//   nested/
		//     empty2/         <- 空，应被删除
		//     hasfile2/
		//       a.txt         <- 有文件，目录应保留
		//     subempty/       <- 内部有子目录，删掉后自身也变空
		//       deepempty/    <- 空
		//   rootempty/
		//     onlyempty/
		//       (all empty)   <- 整棵空树都应被删除

		empty1 := filepath.Join(root, "empty1")
		hasfile := filepath.Join(root, "hasfile")
		nested := filepath.Join(root, "nested")
		empty2 := filepath.Join(nested, "empty2")
		hasfile2 := filepath.Join(nested, "hasfile2")
		subempty := filepath.Join(nested, "subempty")
		deepempty := filepath.Join(subempty, "deepempty")
		rootempty := filepath.Join(root, "rootempty")
		onlyempty := filepath.Join(rootempty, "onlyempty")

		dirs := []string{empty1, hasfile, nested, empty2, hasfile2, subempty, deepempty, rootempty, onlyempty}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0755); err != nil {
				t.Fatalf("MkdirAll %s: %v", d, err)
			}
		}

		// 创建文件
		if err := os.WriteFile(filepath.Join(hasfile, "file.txt"), []byte("hi"), 0o644); err != nil { //nolint:gosec // G306 — 测试夹具
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(hasfile2, "a.txt"), []byte("hello"), 0o644); err != nil { //nolint:gosec // G306 — 测试夹具
			t.Fatalf("WriteFile: %v", err)
		}

		// 调用 rmEmptyDirs
		rmEmptyDirs(root)

		// 验证：root 应存在
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("root dir should exist: %v", err)
		}

		// hasfile 应存在（内部有文件）
		if _, err := os.Stat(hasfile); err != nil {
			t.Fatalf("hasfile should exist: %v", err)
		}

		// nested 应存在（内部 hasfile2 有文件）
		if _, err := os.Stat(nested); err != nil {
			t.Fatalf("nested should exist: %v", err)
		}

		// empty1 应不存在
		if _, err := os.Stat(empty1); !os.IsNotExist(err) {
			t.Fatalf("empty1 should be removed, stat err=%v", err)
		}

		// empty2 应不存在
		if _, err := os.Stat(empty2); !os.IsNotExist(err) {
			t.Fatalf("empty2 should be removed, stat err=%v", err)
		}

		// rootempty/onlyempty 整棵空树都应删除
		if _, err := os.Stat(rootempty); !os.IsNotExist(err) {
			t.Fatalf("rootempty tree should be removed, stat err=%v", err)
		}

		// subempty 应不存在（deepempty 被删后 subempty 变空，应被第二轮删除）
		if _, err := os.Stat(subempty); !os.IsNotExist(err) {
			t.Fatalf("subempty should be removed (became empty after deepempty removed), stat err=%v", err)
		}
	})

	t.Run("handles non-existent root gracefully", func(t *testing.T) {
		rmEmptyDirs("/nonexistent/path/xyz")
		// 不应 panic 或报错（rmEmptyDirs 内部忽略 walk 错误）
	})
}
