package strmutil

import "strings"

// NormalizeExt 规范化扩展名：确保带前导 "."，其余原样返回。
//
// 此前 handler（清理目录）与 service/task（任务设置合并）各写一份逐字相同的实现，
// 两处都改才生效。收敛到这里后只有一个规则来源。
func NormalizeExt(ext string) string {
	if ext == "" {
		return ext
	}
	if strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}
