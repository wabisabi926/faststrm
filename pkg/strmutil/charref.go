package strmutil

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Unescape115CharRef 解码 115 API 返回的名字中的私有字符引用。
//
// 115 会把 emoji 等特殊字符编码为 "\x02<码点十进制>"（如 "\x02128074" = 👊），
// web /files 的 path 祖先链目录名、open API file_name、下载接口 file_name、
// CDN Content-Disposition filename 等字段均可能出现。
// 对齐 p115client unescape_115_charref（正则 \x02([0-9]+) → chr）。
//
// 不含 '\x02' 时 O(1) 原样返回（名字解析是热路径，百万级条目安全）；
// 无效码点（溢出/代理区）保留原始字节，交由后续 SanitizeFileName 清理控制字符。
//
// 幂等：解码产物是常规字符（不含 '\x02'），重复调用结果不变。
func Unescape115CharRef(s string) string {
	if !strings.Contains(s, "\x02") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.IndexByte(s, '\x02')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		j := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == 0 {
			// 孤立 '\x02'（无数字跟随）：保留原样，交由 SanitizeFileName 清理
			b.WriteByte('\x02')
			continue
		}
		if n, err := strconv.ParseUint(s[:j], 10, 32); err == nil && utf8.ValidRune(rune(n)) {
			b.WriteRune(rune(n))
		} else {
			// 无效码点：保留原始字节，避免静默丢数据
			b.WriteByte('\x02')
			b.WriteString(s[:j])
		}
		s = s[j:]
	}
}
