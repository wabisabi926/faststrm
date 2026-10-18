package strmutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRelatedFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("movie.strm")
	write("movie.nfo")
	write("movie.jpg")
	write("movie.zh.srt")
	write("movie.mkv")         // 真实视频，不能删
	write("movie_trailer.mkv") // 同前缀但不是同名，不能删
	write("other.nfo")

	n := DeleteRelatedFiles(filepath.Join(dir, "movie.strm"), DefaultRelatedExts)
	if n != 3 {
		t.Fatalf("应删除 3 个关联文件，实际 %d", n)
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	for _, name := range []string{"movie.nfo", "movie.jpg", "movie.zh.srt"} {
		if exists(name) {
			t.Errorf("%s 应被删除", name)
		}
	}
	for _, name := range []string{"movie.strm", "movie.mkv", "movie_trailer.mkv", "other.nfo"} {
		if !exists(name) {
			t.Errorf("%s 不应被删除", name)
		}
	}
}

func TestDeleteRelatedFiles_NoStrmSuffix(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := DeleteRelatedFiles(p, DefaultRelatedExts); n != 0 {
		t.Fatalf("非 .strm 输入不应删除任何文件, got %d", n)
	}
}
