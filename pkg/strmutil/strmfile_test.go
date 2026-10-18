package strmutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStrmFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"movie.mkv", "movie.strm"},
		{"movie.MKV", "movie.strm"},
		{"game.iso", "game.iso.strm"},
		{"GAME.ISO", "GAME.iso.strm"}, // 只把扩展名归一化为小写，主体保留原样
		{"noext", "noext.strm"},
		{"dir/movie.mkv", "dir/movie.strm"},
		{"movie:1.mkv", "movie：1.strm"}, // 云端文件名直接传入也要清理
	}
	for _, c := range cases {
		if got := StrmFileName(c.in); got != c.want {
			t.Errorf("StrmFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsValidPickcode(t *testing.T) {
	valid := []string{"1q2w3e4r5t6y7u8i9", "ABCDEFGHIJ7654321"}
	invalid := []string{"", "short", "1q2w3e4r5t6y7u8i", "1q2w3e4r5t6y7u8i!", "1q2w3e4r5t6y7u8i99"}
	for _, pc := range valid {
		if !IsValidPickcode(pc) {
			t.Errorf("IsValidPickcode(%q) = false, want true", pc)
		}
	}
	for _, pc := range invalid {
		if IsValidPickcode(pc) {
			t.Errorf("IsValidPickcode(%q) = true, want false", pc)
		}
	}
}

func TestShouldGenerateStrm(t *testing.T) {
	cases := []struct {
		name      string
		fileName  string
		size      int64
		minSize   int64
		blacklist []string
		pass      bool
	}{
		{"正常", "movie.mkv", 1024, 0, nil, true},
		{"黑名单命中", "movie-sample.mkv", 1024, 0, []string{"sample"}, false},
		{"黑名单大小写", "MOVIE-SAMPLE.mkv", 1024, 0, []string{"sample"}, false},
		{"小于最小大小", "movie.mkv", 100, 200, nil, false},
		{"大小未知放行", "movie.mkv", 0, 200, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, pass := ShouldGenerateStrm(c.fileName, c.size, c.minSize, c.blacklist); pass != c.pass {
				t.Fatalf("ShouldGenerateStrm(%q, %d, %d, %v) pass=%v, want %v",
					c.fileName, c.size, c.minSize, c.blacklist, pass, c.pass)
			}
		})
	}
}

func TestWriteStrmFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "movie.strm")

	unchanged, err := WriteStrmFile(p, "http://a\n")
	if err != nil || unchanged {
		t.Fatalf("首次写入: unchanged=%v err=%v", unchanged, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "http://a\n" {
		t.Fatalf("内容不符: %q", b)
	}

	// 内容一致 → 不写盘（mtime 不变，避免 Emby 假重扫）
	info1, _ := os.Stat(p)
	unchanged, err = WriteStrmFile(p, "http://a\n")
	if err != nil || !unchanged {
		t.Fatalf("内容一致应跳过: unchanged=%v err=%v", unchanged, err)
	}
	info2, _ := os.Stat(p)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("内容一致时不应改写文件（mtime 变化会触发 Emby 重扫）")
	}

	// 内容变化 → 覆盖
	unchanged, err = WriteStrmFile(p, "http://b\n")
	if err != nil || unchanged {
		t.Fatalf("内容变化应重写: unchanged=%v err=%v", unchanged, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "http://b\n" {
		t.Fatalf("覆盖后内容不符: %q", b)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("临时文件应已被 rename，不应残留")
	}
}
