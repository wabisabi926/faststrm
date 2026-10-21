package monitor

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func newMetadataTestMonitor(downloadExts, strmExts []string) *Monitor {
	m := &Monitor{}
	m.SetAppSettingsFn(func() *model.Settings {
		return &model.Settings{
			StrmExtensions:     strmExts,
			DownloadExtensions: downloadExts,
		}
	})
	return m
}

func TestIsDownloadExtFile(t *testing.T) {
	m := newMetadataTestMonitor(
		[]string{"srt", "ass", "nfo", "jpg"},
		[]string{"mkv", "mp4", "iso", "m2ts"},
	)
	cases := []struct {
		name string
		want bool
	}{
		{"电影.srt", true},
		{"电影.ass", true},
		{"电影.nfo", true},
		{"海报.jpg", true},
		{"电影.mkv", false},  // STRM 扩展名优先
		{"电影.iso", false},  // ISO 生成 STRM
		{"电影.m2ts", false}, // 原盘流生成 STRM
		{"无扩展名", false},
		{"", false},
	}
	for _, c := range cases {
		if got := m.isDownloadExtFile(c.name); got != c.want {
			t.Fatalf("isDownloadExtFile(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsDownloadExtFile_StrmPriority(t *testing.T) {
	// 同时出现在两个列表：按 STRM 处理
	m := newMetadataTestMonitor([]string{"srt", "mkv"}, []string{"mkv"})
	if m.isDownloadExtFile("电影.mkv") {
		t.Fatal("扩展名同时命中时应优先按 STRM 处理")
	}
}

func TestIsDownloadExtFile_NoSettingsFn(t *testing.T) {
	m := &Monitor{}
	if m.isDownloadExtFile("电影.srt") {
		t.Fatal("未注入设置时应返回 false（功能关闭）")
	}
}
