package handler

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// TestStrmGenFilter_MatchesGenerationRules 锁定「清理侧补建」与「生成侧」的筛选规则一致：
// 云端存在的 .txt/.zip/torrent、命中黑名单的 trailer/sample、BDMV/STREAM 下的 m2ts
// 都不应被判为「缺失 STRM」，否则点一次「补生成」就会往 Emby 塞垃圾条目。
func TestStrmGenFilter_MatchesGenerationRules(t *testing.T) {
	settings := model.DefaultSettings()
	settings.Download.StrmGenerateBlacklist = []string{"trailer", "sample"}

	f := newStrmGenFilter(settings)

	cases := []struct {
		name    string
		path    string
		size    int64
		allowed bool
	}{
		{"普通视频", "电影/沙丘.mkv", 8 << 30, true},
		{"原盘 ISO", "电影/沙丘.iso", 40 << 30, true},
		{"文本文件", "电影/说明.txt", 1024, false},
		{"压缩包", "电影/沙丘.zip", 8 << 30, false},
		{"种子", "电影/沙丘.torrent", 1024, false},
		{"黑名单 trailer", "电影/沙丘-trailer.mkv", 100 << 20, false},
		{"黑名单 sample", "电影/Sample.mkv", 100 << 20, false},
		{"BDMV 视频流", "原盘/BDMV/STREAM/00001.m2ts", 20 << 30, false},
		{"BDMV 反斜杠路径", `原盘\BDMV\STREAM\00002.m2ts`, 20 << 30, false},
		{"独立 m2ts 不受 BDMV 规则影响", "录制/节目.m2ts", 8 << 30, true},
		{"关联文件 jpg", "电影/poster.jpg", 1024, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := f.allow(c.path, c.size)
			if ok != c.allowed {
				t.Fatalf("allow(%q)=%v (%s), want %v", c.path, ok, reason, c.allowed)
			}
		})
	}
}

// TestStrmGenFilter_MinFileSize 小于最小体积的文件不补建
func TestStrmGenFilter_MinFileSize(t *testing.T) {
	settings := model.DefaultSettings()
	settings.Download.MinFileSize = 10 << 20 // 10MB
	f := newStrmGenFilter(settings)

	if _, ok := f.allow("电影/小样.mkv", 1<<20); ok {
		t.Fatal("小于最小体积应被拒绝")
	}
	if _, ok := f.allow("电影/正片.mkv", 20<<20); !ok {
		t.Fatal("大于最小体积应通过")
	}
	// fileSize=0 表示未知，生成侧约定放行
	if _, ok := f.allow("电影/未知体积.mkv", 0); !ok {
		t.Fatal("体积未知时应放行（与生成侧一致）")
	}
}

// TestStrmGenFilter_CustomExtensions 扩展名列表来自设置，非默认列表
func TestStrmGenFilter_CustomExtensions(t *testing.T) {
	settings := model.DefaultSettings()
	settings.StrmExtensions = []string{"mkv"}
	f := newStrmGenFilter(settings)

	if _, ok := f.allow("电影/正片.mp4", 8<<30); ok {
		t.Fatal("用户只配了 mkv 时 mp4 不应补建")
	}
	if _, ok := f.allow("电影/正片.mkv", 8<<30); !ok {
		t.Fatal("列表内的 mkv 应通过")
	}
}

// TestStrmGenFilter_NilSettings 无设置时退化为内置默认扩展名
func TestStrmGenFilter_NilSettings(t *testing.T) {
	f := newStrmGenFilter(nil)
	if _, ok := f.allow("电影/正片.mkv", 8<<30); !ok {
		t.Fatal("nil settings 应退化为默认扩展名并放行 mkv")
	}
	if _, ok := f.allow("电影/说明.txt", 1024); ok {
		t.Fatal("nil settings 下 .txt 仍应被拒绝")
	}
}
