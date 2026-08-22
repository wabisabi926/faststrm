// Package strmutil STRM 文件通用工具：软删除/硬删除、pickcode 提取
// task 与 monitor 共用，避免逻辑重复
package strmutil

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SafeDeleteStrmFile 单个 STRM 删除兜底：
//   hardDelete=true  → os.Remove（原行为）
//   hardDelete=false → 改名为 *.deleted.bak（保留文件本体可回滚，同时不再被媒体库识别为 .strm）
func SafeDeleteStrmFile(strmPath string, hardDelete bool) error {
	if _, err := os.Stat(strmPath); os.IsNotExist(err) {
		return nil
	}
	if hardDelete {
		if err := os.Remove(strmPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	// 软删除：加 .deleted.bak 后缀（避免与任何合法 .strm 冲突）
	// 若已存在同名 .bak，则附加时间戳避免覆盖
	bakPath := strmPath + ".deleted.bak"
	if _, err := os.Stat(bakPath); err == nil {
		bakPath = fmt.Sprintf("%s.%s.deleted.bak", strmPath, time.Now().Format("20060102-150405.000"))
	}
	return os.Rename(strmPath, bakPath)
}

// SafeDeletePath 目录或单文件兜底删除：
//   hardDelete=true  → os.RemoveAll（原行为）
//   hardDelete=false → 递归将所有 .strm 改名为 *.deleted.bak；其他文件与空目录保留
func SafeDeletePath(localPath string, hardDelete bool) error {
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return nil
	}
	if hardDelete {
		return os.RemoveAll(localPath)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		// 单文件：若是 .strm 则软删；其他文件保留（什么也不做）
		if strings.EqualFold(filepath.Ext(localPath), ".strm") {
			return SafeDeleteStrmFile(localPath, false)
		}
		return nil
	}
	// 目录：递归 .strm 文件软删
	return filepath.WalkDir(localPath, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil // 走读错误跳过
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".strm") {
			return nil
		}
		// 跳过已经是 .deleted.bak 的文件
		if strings.HasSuffix(p, ".deleted.bak") {
			return nil
		}
		return SafeDeleteStrmFile(p, false)
	})
}

// pickcodeRe 匹配 STRM 内容中 pickcode=xxx 部分（17位字母数字）
// 适用于 /api/fs/get、/api/strm 与自定义模板渲染后的 URL
var pickcodeRe = regexp.MustCompile(`pickcode=([A-Za-z0-9]{17})`)

// ExtractPickcode 从 STRM 文件内容中提取 pickcode
//   - 读取 STRM 文件全部内容
//   - 用正则匹配 pickcode=xxx
//   - 找不到返回空字符串（可能是手动创建的 STRM，调用方应跳过）
func ExtractPickcode(strmPath string) (string, error) {
	data, err := os.ReadFile(strmPath)
	if err != nil {
		return "", err
	}
	m := pickcodeRe.FindSubmatch(data)
	if len(m) < 2 {
		return "", nil
	}
	return string(m[1]), nil
}

// IsDeletedBak 判断路径是否为软删除的 .deleted.bak 备份文件
func IsDeletedBak(p string) bool {
	return strings.HasSuffix(p, ".deleted.bak")
}
