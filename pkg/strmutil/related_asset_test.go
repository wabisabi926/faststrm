package strmutil

import "testing"

// TestIsRelatedAssetPath 关联文件（图片/字幕/nfo）不得生成 STRM
func TestIsRelatedAssetPath(t *testing.T) {
	assets := []string{
		"兰香如故 (2026)/backdrop.jpg",
		"兰香如故 (2026)/poster.JPG",
		"兰香如故 (2026)/fanart.jpg",
		"兰香如故 (2026)/logo.png",
		"电影/movie.nfo",
		"电影/movie.zh.srt",
		"电影/movie.ass",
		"电影/movie.webp",
	}
	for _, p := range assets {
		if !IsRelatedAssetPath(p) {
			t.Fatalf("%q 应判定为关联文件", p)
		}
	}
	videos := []string{
		"电影/movie.mkv",
		"电影/movie.mp4",
		"原盘/007.iso",
		"电影/movie.ts",
		"",
	}
	for _, p := range videos {
		if IsRelatedAssetPath(p) {
			t.Fatalf("%q 不应判定为关联文件", p)
		}
	}
}
