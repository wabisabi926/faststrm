package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestDownloadMetadataFile_ValidatesLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abc"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	localPath := filepath.Join(dir, "movie.srt")
	if _, err := downloadMetadataFile(context.Background(), srv.URL, "", dir, localPath, 4); err == nil {
		t.Fatal("length mismatch should fail")
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("failed download must not leave file, err=%v", err)
	}

	written, err := downloadMetadataFile(context.Background(), srv.URL, "", dir, localPath, 3)
	if err != nil {
		t.Fatal(err)
	}
	if written != 3 {
		t.Fatalf("written=%d, want 3", written)
	}
	data, err := os.ReadFile(localPath)
	if err != nil || string(data) != "abc" {
		t.Fatalf("downloaded content=%q err=%v", data, err)
	}
}
