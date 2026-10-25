package db

import "strings"

// escapeLike 转义 SQLite LIKE 元字符；调用方必须同时使用 ESCAPE '\'，
// 否则目录名中的 _ / % 会被当作通配符匹配到相邻子树。
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// literalPrefixPattern 返回匹配 s 本身及其子树的字面 LIKE 模式（配合 ESCAPE '\'）。
func literalPrefixPattern(s string) string {
	return escapeLike(s) + "/%"
}
