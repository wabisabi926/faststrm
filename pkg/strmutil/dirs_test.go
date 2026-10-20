package strmutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveEmptyParentsUpTo_DeletesUntilBoundary 从最深处向上删空目录，边界目录本身保留
func TestRemoveEmptyParentsUpTo_DeletesUntilBoundary(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	n := RemoveEmptyParentsUpTo(deep, []string{root})
	if n != 3 {
		t.Fatalf("应删除 3 层空目录，got %d", n)
	}
	if _, err := os.Stat(deep); !os.IsNotExist(err) {
		t.Fatalf("最深层应已删除")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("边界目录不应被删除: %v", err)
	}
}

// TestRemoveEmptyParentsUpTo_StopsAtNonEmpty 遇到非空目录停止，且不删除该目录
func TestRemoveEmptyParentsUpTo_StopsAtNonEmpty(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep")
	deep := filepath.Join(keep, "x", "y")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// keep 目录里放一个文件，阻止向上删除
	if err := os.WriteFile(filepath.Join(keep, "poster.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	n := RemoveEmptyParentsUpTo(deep, []string{root})
	if n != 2 {
		t.Fatalf("应只删除 x/y 两层，got %d", n)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("非空目录不应被删除: %v", err)
	}
}

// TestRemoveEmptyParentsUpTo_NoBoundaryNoDelete 没有可用边界时一律不删（防误删到盘根）
func TestRemoveEmptyParentsUpTo_NoBoundaryNoDelete(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if n := RemoveEmptyParentsUpTo(deep, nil); n != 0 {
		t.Fatalf("无边界不应删除任何目录，got %d", n)
	}
	if n := RemoveEmptyParentsUpTo(deep, []string{"", "  "}); n != 0 {
		t.Fatalf("空边界不应删除任何目录，got %d", n)
	}
	if _, err := os.Stat(deep); err != nil {
		t.Fatalf("目录应保留: %v", err)
	}
}

// TestRemoveEmptyParentsUpTo_OutsideBoundaryNoDelete 目录不在边界之下时不删（配置异常保护）
func TestRemoveEmptyParentsUpTo_OutsideBoundaryNoDelete(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(base, "other")
	deep := filepath.Join(other, "a")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(base, "unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}

	if n := RemoveEmptyParentsUpTo(deep, []string{unrelated}); n != 0 {
		t.Fatalf("越界时不应删除，got %d", n)
	}
	if _, err := os.Stat(deep); err != nil {
		t.Fatalf("目录应保留: %v", err)
	}
}

// TestRemoveEmptyParentsUpTo_MultipleBoundaries 多个边界时，命中任一即停
func TestRemoveEmptyParentsUpTo_MultipleBoundaries(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "mediaA")
	rootB := filepath.Join(base, "mediaB")
	deep := filepath.Join(rootB, "剧集", "S01")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatal(err)
	}

	n := RemoveEmptyParentsUpTo(deep, []string{rootA, rootB})
	if n != 2 {
		t.Fatalf("应删除 S01/剧集 两层，got %d", n)
	}
	if _, err := os.Stat(rootB); err != nil {
		t.Fatalf("命中的边界目录应保留: %v", err)
	}
}

// TestNormalizeExt 扩展名规范化（此前 handler 与 task 各一份，现统一）
func TestNormalizeExt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{".mkv", ".mkv"},
		{"mkv", ".mkv"},
		{"MKV", ".MKV"}, // 只补点，不改大小写（调用方自行 ToLower）
		{"file.mkv", ".file.mkv"},
	}
	for _, c := range cases {
		if got := NormalizeExt(c.in); got != c.want {
			t.Fatalf("NormalizeExt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
