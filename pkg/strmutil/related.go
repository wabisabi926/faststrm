package strmutil

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultRelatedExts 删除 STRM 时允许连带删除的关联文件扩展名。
//
// 只包含元数据与字幕，**不含任何视频扩展名**：
// 用户可能把同一部影片的真实文件（.mkv/.mp4）和 STRM 放在同一目录，
// 若按「同名前缀」无差别删除，会把真实视频一起删掉。
var DefaultRelatedExts = map[string]struct{}{
	".nfo": {}, ".jpg": {}, ".jpeg": {}, ".png": {}, ".webp": {},
	".srt": {}, ".ass": {}, ".ssa": {}, ".sub": {}, ".vtt": {}, ".sup": {},
}

// IsRelatedAssetPath 判断路径是否为 STRM 的关联文件（图片 / 字幕 / nfo 等）。
//
// 用途：清理扫描时跳过这类云端文件。它们永远不该有对应的 .strm——
// 历史实现按「云端每个文件都要有一个 .strm」比对，结果给 backdrop.jpg /
// poster.jpg / logo.png 补建出了 backdrop.strm / poster.strm / logo.strm，
// 这些条目在 Emby 里会被当成无法播放的媒体项。
func IsRelatedAssetPath(p string) bool {
	if p == "" {
		return false
	}
	_, ok := DefaultRelatedExts[strings.ToLower(filepath.Ext(p))]
	return ok
}

// DeleteRelatedFiles 删除与 STRM 同名的关联文件（stem + 扩展名），返回删除数量。
func DeleteRelatedFiles(strmPath string, exts map[string]struct{}) int {
	return len(DeleteRelatedFilesDetailed(strmPath, exts))
}

// relatedFileCandidates 返回与 STRM 同名的关联文件绝对路径（不删除）。
func relatedFileCandidates(strmPath string, exts map[string]struct{}) []string {
	if strmPath == "" {
		return nil
	}
	dir := filepath.Dir(strmPath)
	base := filepath.Base(strmPath)
	stem := strings.TrimSuffix(base, ".strm")
	if stem == "" || stem == base {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == base || !strings.HasPrefix(name, stem) {
			continue
		}
		rest := strings.TrimPrefix(name, stem)
		if rest == "" || !strings.HasPrefix(rest, ".") {
			continue
		}
		// 用最后一个扩展名判定（支持 movie.zh.srt 这类多段后缀）
		if _, ok := exts[strings.ToLower(filepath.Ext(name))]; !ok {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

// DeleteRelatedFilesDetailed 删除关联文件并返回实际删除的绝对路径列表。
// 调用方需要回显删除结果（如清理页）时使用；任务/监控侧只关心数量。
func DeleteRelatedFilesDetailed(strmPath string, exts map[string]struct{}) []string {
	var removed []string
	for _, full := range relatedFileCandidates(strmPath, exts) {
		if err := os.Remove(full); err == nil {
			removed = append(removed, full)
		}
	}
	return removed
}

// MoveRelatedFilesToTrash 把关联文件移入回收站，返回实际移动后的路径。
func MoveRelatedFilesToTrash(strmPath, root, trashDir string, exts map[string]struct{}) []string {
	var moved []string
	for _, full := range relatedFileCandidates(strmPath, exts) {
		dest, err := MoveToTrash(full, root, trashDir)
		if err == nil && dest != "" {
			moved = append(moved, dest)
		}
	}
	return moved
}
