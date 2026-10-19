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
//   - "/" → "_"（115 文件名可带 '/'，落盘前必须替换；选用 '_' 与其余非法字符的映射一致，
//     且与参考项目 Windows 落盘结果相同——参考项目先 '/'→'|' 再由 sanitize 把 '|'→'_'）
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
		case r == '/':
			b.WriteByte('_')
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

// EscapeName 云端文件名拼入路径前的「结构转义」。
//
// 115 文件名本身可含 '/'（及脏数据中的 '\'），直接拼进云端路径字符串后，
// 下游 SanitizeRelPath 等按分隔符切分的逻辑会把它误当目录分隔符，一个文件被拆成多级目录。
// 对齐参考项目 p115client posix_escape_name 的思路（在构建路径时对文件名转义），
// 但替换字符不用参考项目的 '|'：'|' 在 Windows 非法，本项目全平台部署（Win/Linux/飞牛），
// 统一映射为全平台合法的 '_'，与参考项目 Windows 最终落盘结果一致，也与 SanitizeFileName 的映射相同。
//
// 与 SanitizeFileName 的分工：
//   - EscapeName：路径构建点调用（task 扫描 / monitor 事件拼云端路径），只做结构转义，
//     不改动 ':' 等字符，保证 RelPath/CloudPath/DB 记录与历史格式一致
//   - SanitizeFileName：本地落盘前的合法性清理（冒号转全角等），兜底覆盖 '/'
//
// 幂等：结果不含 '/' 与 '\'，重复调用结果不变。
func EscapeName(name string) string {
	if !strings.ContainsAny(name, "/\\") {
		return name
	}
	return strings.NewReplacer("/", "_", "\\", "_").Replace(name)
}

// JoinCloudPath 把「目录前缀 + 单个 115 名字」拼成云端路径（"/" 分隔）。
//
// 名字先做结构转义（EscapeName）再拼接，保证结果里所有 "/" 都是真实目录分隔符。
// 目录为空时返回转义后的名字本身（不带前导 "/"，与相对路径语义一致）。
//
// 这是云端路径拼接的唯一入口：新增拼接点必须走它，不要手写 dir + "/" + name——
// 名字里的 "/" 一旦漏转义，会被下游 SanitizeRelPath 当成目录分隔符，一个文件被拆成多级目录。
func JoinCloudPath(dir, name string) string {
	if dir == "" {
		return EscapeName(name)
	}
	return strings.TrimRight(dir, "/") + "/" + EscapeName(name)
}

// AbsCloudPath 拼「/名字」形式的绝对云端路径（根目录下的兜底路径）。
func AbsCloudPath(name string) string {
	return "/" + EscapeName(name)
}

// LocalNameToCloudName 把本地落盘名反推为云端名。
//
// SanitizeFileName 的映射大多不可逆（'/' '<' '>' '"' '|' '?' '*' '\' 全部塌缩为 "_"），
// 唯一可逆的是全角冒号：云端 ':' → 本地 '：'。因此这里只还原 '：'，其余字符原样返回。
// 仅用于「本地路径 → DB 云端路径」的匹配场景（如 Emby 删除同步的 DB 记录清理），
// 不能用于反查 115 真实文件名。
func LocalNameToCloudName(name string) string {
	if !strings.Contains(name, "：") {
		return name
	}
	return strings.ReplaceAll(name, "：", ":")
}

// LocalRelToCloudRel 把本地相对路径反推为云端相对路径：分隔符统一为 '/'，逐段还原可逆字符。
//
// 与 SanitizeRelPath 互为逆方向：后者是「云端 → 本地」（'/' 分隔 → OS 分隔、':' → '：'），
// 这里只做可逆部分（OS 分隔 → '/'、'：' → ':'），用于「已有本地相对路径、要拼出云端路径」的场景
// （如清理页为漏项重建 STRM 时的 {file_path} 模板变量，必须与生成侧 f.CloudPath 同形态）。
// '_' << '/' 不可逆，原样保留（与云端转义结果本就一致）。
func LocalRelToCloudRel(localRel string) string {
	if localRel == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(localRel, "\\", "/"), "/")
	for i := range parts {
		parts[i] = LocalNameToCloudName(parts[i])
	}
	return strings.Join(parts, "/")
}

// SanitizeRelPath 逐段清理云端相对路径，返回当前平台的本地相对路径。
//
// 这是「云端路径 → 本地路径」的唯一入口：task（全量/增量生成）与 monitor（事件生成）
// 必须共用它，否则同一个文件会被两套规则写到两个不同位置。
// 传入的路径以 "/" 分隔（云端语义）；输出使用 filepath.Separator。
//
// 注意：入参中的 "/" 被当作目录分隔符处理。云端文件名若本身含 '/'，
// 必须在拼接成本路径之前先对文件名调用 EscapeName，否则文件名会被拆成多级目录。
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
