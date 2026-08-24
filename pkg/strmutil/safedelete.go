// Package strmutil STRM 文件通用工具：硬删除、pickcode 提取
// task 与 monitor 共用，避免逻辑重复
package strmutil

import (
	"os"
	"regexp"
	"strings"
)

// DeleteStrmFile 删除单个 STRM 文件（硬删除）
func DeleteStrmFile(strmPath string) error {
	if _, err := os.Stat(strmPath); os.IsNotExist(err) {
		return nil
	}
	if err := os.Remove(strmPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeletePath 删除目录或单文件（硬删除）
func DeletePath(localPath string) error {
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(localPath)
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

// IsDeletedBak 判断路径是否为软删除的 .deleted.bak 备份文件（保留用于兼容旧配置检测）
func IsDeletedBak(p string) bool {
	return strings.HasSuffix(p, ".deleted.bak")
}
