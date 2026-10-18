package rate

import "strings"

// GlobalKey 兜底限流维度。
// 当无法从 cookie 解析出账号标识时使用，保证限流永远生效（不会因解析失败而完全不限流）。
const GlobalKey = "global"

// AccountKeyFromCookie 从 115 cookie 中解析 UID 作为限流维度。
//
// 设计动机：115 的 cookie 必然包含 UID（见 client115.ValidateCookie 对
// UID/CID/SEID/KID 的强制校验），用它作为维度可以在「不修改任何调用方函数签名」
// 的前提下，把限流粒度从「全进程一个桶」升级为「按账号分桶」。
//
// 这样扫描列目录与播放取直链即使共用同一类型，也不会跨账号互相抢占；
// 而扫描/播放分属不同类型时，更是彻底隔离。
//
// 解析失败（cookie 为空、无 UID、UID 为空值）一律回落 GlobalKey。
func AccountKeyFromCookie(cookie string) string {
	const prefix = "UID="
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if len(part) <= len(prefix) {
			continue
		}
		if !strings.EqualFold(part[:len(prefix)], prefix) {
			continue
		}
		if v := strings.TrimSpace(part[len(prefix):]); v != "" {
			return "uid:" + v
		}
	}
	return GlobalKey
}
