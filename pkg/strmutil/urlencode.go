package strmutil

import "strings"

// URLPathEncode 对 URL 参数值做编码，保留路径分隔符 '/'。
//
// 与标准 url.QueryEscape 的差异（这是刻意设计）：
//   - 空格编码为 %20 而非 '+'：router 的简化 unescape 不把 '+' 还原成空格，
//     用 QueryEscape 会导致回读的文件名带加号（历史 bug）
//   - 保留 '/'：file_name / file_path 这类含路径语义的值保持可读，
//     且在 query 参数值中裸 '/' 是合法字符
//
// 保留字符：字母数字与 - _ . ~ /
// 其余字节（含中文、空格、'&'、'?' 等）一律 %XX 编码。
func URLPathEncode(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) * 3)
	for _, b := range []byte(s) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') ||
			b == '-' || b == '_' || b == '.' || b == '~' || b == '/' {
			sb.WriteByte(b)
			continue
		}
		const hex = "0123456789ABCDEF"
		sb.WriteByte('%')
		sb.WriteByte(hex[b>>4])
		sb.WriteByte(hex[b&0x0F])
	}
	return sb.String()
}
