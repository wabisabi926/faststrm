package strmutil

import (
	"path/filepath"
	"strings"
)

// illegalFileChars 本地文件系统非法（或会破坏路径层级）的字符。
// 115 云端允许这些字符，直接落盘在 Windows / SMB 共享上会失败（"文件名、目录名或卷标语法不正确"），
// 在 Linux 上则会造成 Windows 客户端无法访问。
const illegalFileChars = "<>\"|?*\\"

// SanitizeFileName 清理单个文件/目录名中的非法字符。
//
// 规则（与 p115strmhelper PathUtils.sanitize_path_parts 对齐，并补齐两处会静默出错的边界）：
//   - ":"  → "："（全角冒号，保留可读性，避免片名变成下划线串）
//   - < > " | ? * \ → "_"
//   - 控制字符（0x00-0x1F、0x7F）→ "_"
//   - 去掉段尾的空格与点：Windows 会静默截断，导致「写入路径」与「后续查找路径」不一致，
//     表现为同一个文件每次任务都被重写
//
// 幂等：结果中的 "：" 与 "_" 都不在替换集合内，重复调用结果不变。
func SanitizeFileName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name) + 4)
	for _, r := range name {
		switch {
		case r == ':':
			b.WriteRune('：')
		case r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		case strings.ContainsRune(illegalFileChars, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), " .")
	if out == "" {
		// 全是被清理掉的字符（或 "." / ".."）：退化为 "_"，同时避免路径穿越
		return "_"
	}
	return out
}

// SanitizeRelPath 逐段清理云端相对路径，返回当前平台的本地相对路径。
//
// 这是「云端路径 → 本地路径」的唯一入口：task（全量/增量生成）与 monitor（事件生成）
// 必须共用它，否则同一个文件会被两套规则写到两个不同位置。
// 传入的路径以 "/" 分隔（云端语义）；输出使用 filepath.Separator。
func SanitizeRelPath(rel string) string {
	if rel == "" {
		return ""
	}
	// 云端不会出现 "\"，但脏数据里可能有；统一按 "/" 切分后再逐段清理
	rel = strings.ReplaceAll(rel, "\\", "/")
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		// "." 是 filepath.Join 在 Windows 上处理「首段含冒号」时引入的伪段（".\\dir:1\\x"），
		// 必须原样丢弃；".." 则交给 SanitizeFileName 变成 "_"，防止路径穿越。
		if p == "" || p == "." {
			continue
		}
		out = append(out, SanitizeFileName(p))
	}
	if len(out) == 0 {
		return ""
	}
	return filepath.Join(out...)
}
