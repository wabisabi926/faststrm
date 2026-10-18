package strmutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wabisabi926/faststrm/pkg/concurrency"
)

// 本文件收纳 task（全量/增量生成）与 monitor（生活事件）此前各自实现一份的四个函数。
// 两份实现逐行相同，只有 writeStrmFile 的返回值不同；合并后只保留这一份。

// StrmFileName 将文件名转换为 .strm 扩展名。
// 对齐 MoviePilot StrmGenerater.get_strm_filename：
//   - 普通文件：movie.mkv → movie.strm
//   - ISO 镜像保留双扩展名：game.iso → game.iso.strm
//
// 内部先做非法字符清理：调用方传入的可能是云端原始文件名（生活事件），
// 也可能已经清理过（任务侧路径），SanitizeFileName 幂等，两种入参都安全。
func StrmFileName(fileName string) string {
	fileName = SanitizeFileName(fileName)
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return fileName + ".strm"
	}
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if ext == ".iso" {
		return stem + ".iso.strm"
	}
	return stem + ".strm"
}

// IsValidPickcode 校验 115 pickcode 合法性（17 位字母数字）。
func IsValidPickcode(pc string) bool {
	if len(pc) != 17 {
		return false
	}
	for i := 0; i < len(pc); i++ {
		c := pc[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// ShouldGenerateStrm 统一判断文件是否应该生成 STRM。
// 对齐 MoviePilot StrmGenerater.should_generate_strm：
//  1. 黑名单关键词检查（不区分大小写子串匹配，若任一条目匹配则拒绝）
//  2. 最小文件大小检查（minFileSize>0 且 fileSize>0 且 fileSize<minFileSize 则拒绝）
//
// matcher 非空时走 AC 自动机；否则阈值≥8 临时构建；<8 时 contains 线性扫常数项更低。
// 返回：(拒绝原因, 是否通过)；通过时拒绝原因为空。
func ShouldGenerateStrm(fileName string, fileSize, minFileSize int64, blacklist []string, matcher ...*concurrency.StringMatcher) (string, bool) {
	// 1) 黑名单检查：不区分大小写子串匹配
	// 对齐 MoviePilot not_blacklist_key / not_blacklist_key_automaton
	if len(blacklist) > 0 {
		var m *concurrency.StringMatcher
		if len(matcher) > 0 && matcher[0] != nil {
			m = matcher[0]
		} else if concurrency.ShouldUseAC(blacklist) {
			m = concurrency.NewStringMatcher(blacklist)
		}
		if m != nil {
			if kw, ok := m.MatchAny(fileName); ok {
				return fmt.Sprintf("匹配黑名单关键词 %q", kw), false
			}
		} else {
			lowerName := strings.ToLower(fileName)
			for _, kw := range blacklist {
				if kw == "" {
					continue
				}
				if strings.Contains(lowerName, strings.ToLower(kw)) {
					return fmt.Sprintf("匹配黑名单关键词 %q", kw), false
				}
			}
		}
	}
	// 2) 最小文件大小限制
	// 对齐 MoviePilot not_min_limit：fileSize=0(未知)时默认通过
	if minFileSize > 0 && fileSize > 0 && fileSize < minFileSize {
		return "小于最小文件大小", false
	}
	return "", true
}

// WriteStrmFile 原子写入 STRM 文件：先写 .tmp 再 rename，避免并发读到半截文件。
// 内容与磁盘上一致时不写盘，返回 unchanged=true（调用方据此跳过 Emby 刷库，防假重扫）。
func WriteStrmFile(strmPath, content string) (unchanged bool, err error) {
	if existing, rerr := os.ReadFile(strmPath); rerr == nil && string(existing) == content {
		return true, nil
	}
	tmpPath := strmPath + ".tmp"
	if werr := os.WriteFile(tmpPath, []byte(content), 0o600); werr != nil {
		return false, werr
	}
	return false, os.Rename(tmpPath, strmPath)
}
