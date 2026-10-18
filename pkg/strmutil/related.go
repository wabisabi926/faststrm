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

// DeleteRelatedFiles 删除与 STRM 同名的关联文件（stem + 扩展名），返回删除数量。
//
// 匹配规则：文件名以 `<stem>` 开头、剩余部分为空或以 "." 开头，且扩展名在 exts 白名单内。
// 这样 "movie.nfo" / "movie.zh.srt" 会被删，"movie.mkv" / "movie_trailer.mkv" 不会。
func DeleteRelatedFiles(strmPath string, exts map[string]struct{}) int {
	if strmPath == "" {
		return 0
	}
	dir := filepath.Dir(strmPath)
	base := filepath.Base(strmPath)
	stem := strings.TrimSuffix(base, ".strm")
	if stem == "" || stem == base {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	deleted := 0
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
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			deleted++
		}
	}
	return deleted
}
