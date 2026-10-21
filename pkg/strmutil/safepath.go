package strmutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureNoSymlinkComponents 校验 target 在 root 下的现有路径组件不含符号链接。
//
// 词法 pathWithinRoot 只能防 `..`，挡不住 root/sub -> root 外的符号链接。
// 删除、补建、回收站恢复等破坏性操作必须同时做这一步；不存在的尾部目录视为新建，
// 但已存在的最深祖先必须是普通目录。
func EnsureNoSymlinkComponents(root, target string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return fmt.Errorf("路径为空")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("根路径无效: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("目标路径无效: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("路径越出映射根: %s", target)
	}

	parts := strings.Split(rel, string(filepath.Separator))
	cur := absRoot
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, lerr := os.Lstat(cur)
		if os.IsNotExist(lerr) {
			// 从这里开始的目录由本次操作新建，不存在符号链接组件。
			return nil
		}
		if lerr != nil {
			return fmt.Errorf("检查路径 %s 失败: %w", cur, lerr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("路径包含符号链接，已拒绝: %s", cur)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("路径中间项不是目录: %s", cur)
		}
	}
	return nil
}
