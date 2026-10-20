package strmutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TrashDirName 回收站目录名（隐藏目录，不参与 Emby 扫描）。
const TrashDirName = ".faststrm-trash"

// TrashSessionDir 为一次清理生成回收站批次目录（按秒级时间戳）。
func TrashSessionDir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, TrashDirName, time.Now().Format("20060102-150405"))
}

// MoveToTrash 把文件移动到回收站（同卷 rename，原子且可逆）。
//
// root 为目标映射根；trashDir 可由 TrashSessionDir 提前生成，空则自动生成。
// 拒绝对符号链接/目录操作；文件不存在视为已清理（返回空路径、nil）。
func MoveToTrash(path, root, trashDir string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("trash: empty path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("trash: refuse symlink %s", path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("trash: refuse directory %s", path)
	}
	if root == "" {
		root = filepath.Dir(path)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("trash: path outside root: %s", path)
	}
	if trashDir == "" {
		trashDir = TrashSessionDir(absRoot)
	}
	dest := filepath.Join(trashDir, rel)
	if _, err := os.Lstat(dest); err == nil {
		dest = dest + "." + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(absPath, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// PurgeTrash 删除 root 回收站下超过 maxAge 的批次目录。
func PurgeTrash(root string, maxAge time.Duration) int {
	if root == "" || maxAge <= 0 {
		return 0
	}
	return purgeTrashDir(filepath.Join(root, TrashDirName), maxAge)
}

// PurgeTrashTree 递归清理 root 下所有 .faststrm-trash（含待确认批次落在子目录的情况）。
func PurgeTrashTree(root string, maxAge time.Duration) int {
	if root == "" || maxAge <= 0 {
		return 0
	}
	removed := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == TrashDirName {
			removed += purgeTrashDir(p, maxAge)
			return filepath.SkipDir
		}
		return nil
	})
	return removed
}

func purgeTrashDir(base string, maxAge time.Duration) int {
	if base == "" || maxAge <= 0 {
		return 0
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(base, entry.Name())); err == nil {
				removed++
			}
		}
	}
	return removed
}
