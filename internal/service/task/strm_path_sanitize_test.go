package task

import (
	"path/filepath"
	"testing"
)

// TestResolveStrmRelPathSanitizes 云端文件名/目录名含非法字符时，
// 本地 STRM 路径必须被清理（冒号转全角、其余特殊字符转下划线）。
func TestResolveStrmRelPathSanitizes(t *testing.T) {
	cases := []struct {
		name     string
		template string
		relPath  string
		fileName string
		want     []string
	}{
		{
			name:     "默认命名-目录和文件名都含冒号",
			relPath:  "电影:合集/流浪地球2:2023.mkv",
			fileName: "流浪地球2:2023.mkv",
			want:     []string{"电影：合集", "流浪地球2：2023.strm"},
		},
		{
			name:     "默认命名-问号星号竖线",
			relPath:  "a|b/c?d/movie*.mkv",
			fileName: "movie*.mkv",
			want:     []string{"a_b", "c_d", "movie_.strm"},
		},
		{
			name:     "ISO 双扩展名保留",
			relPath:  "原盘:合集/game.iso",
			fileName: "game.iso",
			want:     []string{"原盘：合集", "game.iso.strm"},
		},
		{
			name:     "文件名模板-模板产物同样清理",
			template: "{stem} [{account}]",
			relPath:  "dir:1/movie:2.mkv",
			fileName: "movie:2.mkv",
			want:     []string{"dir：1", "movie：2 [acc].strm"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fileItem{RelPath: c.relPath, Name: c.fileName}
			got := resolveStrmRelPath(c.template, "acc", f)
			want := filepath.Join(c.want...)
			if got != want {
				t.Fatalf("resolveStrmRelPath(%q) = %q, want %q", c.relPath, got, want)
			}
		})
	}
}
