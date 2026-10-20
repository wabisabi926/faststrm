package strmutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RemoveEmptyParentsUpTo 从 dir 开始向上逐级删除空目录，返回删除数量。
//
// 停止条件（任一命中即停）：
//  1. 当前目录命中 stopAt 中的任一边界目录
//  2. 当前目录已不在任何边界目录之下（防御：配置异常时宁可少删，也不删到盘根）
//  3. os.Remove 失败（目录非空 / 无权限）
//
// 为什么要有这个函数：此前 task、monitor、emby 各写一份「向上删空目录」，
// 三份的边界判定各不相同——task 用字符串前缀校验，emby 用 normalizePath 逐级比对，
// monitor 用 map 精确比对且**没有做 Windows 大小写归一化**（D:\Media 与 d:\media
// 判不出边界，可能一路删到盘根）。合并后统一按 filepath.Abs + Clean 归一，
// Windows 下大小写不敏感比较。
//
// 调用方无需改动：三个包各自保留同名 wrapper，仅把实现委托到这里。
func RemoveEmptyParentsUpTo(dir string, stopAt []string) int {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return 0
	}
	stops := make([]string, 0, len(stopAt))
	for _, s := range stopAt {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if sa, aerr := filepath.Abs(s); aerr == nil {
			stops = append(stops, filepath.Clean(sa))
		}
	}
	// 没有可用边界时一律不删：宁可留下空目录，也不能在配置异常时递归删到盘根
	if len(stops) == 0 {
		return 0
	}

	cur := filepath.Clean(abs)
	removed := 0
	for {
		if isStopDir(cur, stops) {
			break
		}
		if !isUnderAnyStop(cur, stops) {
			break
		}
		if err := os.Remove(cur); err != nil {
			break
		}
		removed++
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return removed
}

// pathKey 归一化路径用于比较：Windows 下大小写不敏感
func pathKey(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

func isStopDir(cur string, stops []string) bool {
	k := pathKey(cur)
	for _, s := range stops {
		if k == pathKey(s) {
			return true
		}
	}
	return false
}

func isUnderAnyStop(cur string, stops []string) bool {
	k := pathKey(cur)
	sep := string(filepath.Separator)
	for _, s := range stops {
		if strings.HasPrefix(k, pathKey(s)+sep) {
			return true
		}
	}
	return false
}
