package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func generousCleanup(related, emptyDirs bool) model.CleanupSettings {
	return model.CleanupSettings{
		MaxThreshold:       1000,
		MaxRatio:           1,
		MaxDirs:            100,
		StableThreshold:    100,
		ConfirmMode:        "none",
		RemoveStrm:         true,
		RemoveRelatedFiles: related,
		RemoveEmptyDirs:    emptyDirs,
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// TestCleanup_RemovesRelatedFiles 删孤儿 STRM 时连带删同名 nfo/海报/字幕，但绝不动视频文件
func TestCleanup_RemovesRelatedFiles(t *testing.T) {
	target := t.TempDir()
	dir := filepath.Join(target, "电影", "影片A")
	pc := "abcdefghij1234567"
	strm := filepath.Join(dir, "movie.strm")
	writeFile(t, strm, `http://x/api/strm?account=a&pickcode=`+pc)
	writeFile(t, filepath.Join(dir, "movie.nfo"), "nfo")
	writeFile(t, filepath.Join(dir, "movie.jpg"), "jpg")
	writeFile(t, filepath.Join(dir, "movie.zh.srt"), "srt")
	writeFile(t, filepath.Join(dir, "movie.mkv"), "real video") // 用户本地真实文件

	cloud := map[string]string{"zzzzzzzzzzzzzzz99": "无关/x.strm"}
	n, _, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, cloud, nil, nil, "auto_clean", generousCleanup(true, false), nil, nil, nil)
	if err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if n != 1 {
		t.Fatalf("应删 1 个孤儿 STRM, got %d", n)
	}
	for _, name := range []string{"movie.strm", "movie.nfo", "movie.jpg", "movie.zh.srt"} {
		if exists(filepath.Join(dir, name)) {
			t.Errorf("%s 应被删除", name)
		}
	}
	if !exists(filepath.Join(dir, "movie.mkv")) {
		t.Error("真实视频 movie.mkv 被误删")
	}
}

// TestCleanup_KeepsRelatedFilesWhenDisabled 关闭开关时只删 STRM，元数据保留
func TestCleanup_KeepsRelatedFilesWhenDisabled(t *testing.T) {
	target := t.TempDir()
	dir := filepath.Join(target, "电影")
	pc := "abcdefghij1234567"
	writeFile(t, filepath.Join(dir, "movie.strm"), `http://x/api/strm?account=a&pickcode=`+pc)
	writeFile(t, filepath.Join(dir, "movie.nfo"), "nfo")

	cloud := map[string]string{"zzzzzzzzzzzzzzz99": "无关/x.strm"}
	if _, _, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, cloud, nil, nil, "auto_clean", generousCleanup(false, false), nil, nil, nil); err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if exists(filepath.Join(dir, "movie.strm")) {
		t.Error("STRM 应被删除")
	}
	if !exists(filepath.Join(dir, "movie.nfo")) {
		t.Error("关闭关联文件删除时 nfo 应保留")
	}
}

// TestCleanup_RemovesEmptyDirs 开启后，删完变空的目录应被回收（但不越过 targetPath）
func TestCleanup_RemovesEmptyDirs(t *testing.T) {
	target := t.TempDir()
	dir := filepath.Join(target, "电影", "影片A")
	pc := "abcdefghij1234567"
	writeFile(t, filepath.Join(dir, "movie.strm"), `http://x/api/strm?account=a&pickcode=`+pc)
	writeFile(t, filepath.Join(dir, "movie.nfo"), "nfo")

	cloud := map[string]string{"zzzzzzzzzzzzzzz99": "无关/x.strm"}
	if _, _, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, cloud, nil, nil, "auto_clean", generousCleanup(true, true), nil, nil, nil); err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if exists(dir) {
		t.Error("空目录应被删除")
	}
	if !exists(target) {
		t.Error("targetPath 本身不能被删")
	}
}
