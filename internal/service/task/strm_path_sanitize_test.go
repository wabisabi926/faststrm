package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
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

// TestRemoveExtraFilesKeepsSanitizedPaths 回归测试：
// 磁盘上是清理后的文件名，而 fileEntries 里是云端原始文件名。
// 若「保留集合」不做同样的清理，这些 STRM 会被误判为多余文件并删除。
func TestRemoveExtraFilesKeepsSanitizedPaths(t *testing.T) {
	target := t.TempDir()
	dir := filepath.Join(target, "电影：合集")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	strmPath := filepath.Join(dir, "流浪地球2：2023.strm")
	if err := os.WriteFile(strmPath, []byte("http://x/api/strm?pickcode=1\n"), 0o644); err != nil {
		t.Fatalf("write strm: %v", err)
	}
	relatedPath := filepath.Join(dir, "流浪地球2：2023.nfo")
	if err := os.WriteFile(relatedPath, []byte("nfo"), 0o644); err != nil {
		t.Fatalf("write nfo: %v", err)
	}

	entries := []*fileItem{
		{
			RelPath:  "电影:合集/流浪地球2:2023.mkv",
			Name:     "流浪地球2:2023.mkv",
			PickCode: "1q2w3e4r5t6y7u8i9",
			Kind:     kindStrm,
		},
		{
			RelPath:  "电影:合集/流浪地球2:2023.nfo",
			Name:     "流浪地球2:2023.nfo",
			PickCode: "1q2w3e4r5t6y7u8i9",
			Kind:     kindDownload,
		},
	}
	s := &model.Settings{}
	outcome, err := removeExtraFiles(context.Background(), nil, "task-test", target, entries, s)
	if err != nil {
		t.Fatalf("removeExtraFiles: %v", err)
	}
	if outcome.Deleted != 0 {
		t.Fatalf("清理了 %d 个文件，期望 0（清理后的路径必须与写入路径一致）", outcome.Deleted)
	}
	if _, err := os.Stat(strmPath); err != nil {
		t.Fatalf("STRM 被误删: %v", err)
	}
	if _, err := os.Stat(relatedPath); err != nil {
		t.Fatalf("关联文件被误删: %v", err)
	}
}
