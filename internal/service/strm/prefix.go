package strm

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// 默认服务端口（与 config 中的默认监听端口保持一致）
const defaultServicePort = 8090

// IsUnusablePrefix 判断 STRM 前缀是否无法被其他设备使用。
//
// 命中以下任一情况返回 true：
//   - 空串
//   - 无法解析为 URL，或缺少 host
//   - host 为 0.0.0.0 / :: / 127.0.0.1 / 127.x.x.x / localhost
//
// 为什么需要这个判断：默认配置 SERVER_HOST=0.0.0.0 且 strmPrefix 为空时，
// 兜底 baseURL 会拼成 http://0.0.0.0:8090，写进 STRM 后除本机外都无法播放。
// 这类前缀必须在生成前就拦下来，而不是等用户播放失败再排查。
func IsUnusablePrefix(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return true
	}
	return isLoopbackHost(u.Hostname())
}

// isLoopbackHost 判断 host 是否为回环/未指定地址
func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	switch h {
	case "", "0.0.0.0", "::", "[::]", "localhost":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// DetectLANIP 返回本机首个可用的非回环 IPv4 地址；无可用网卡时返回空串。
//
// 用于在用户未配置 strmPrefix 时给出一个「至少局域网内可播」的兜底地址，
// 避免直接写入 0.0.0.0。
func DetectLANIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
			continue
		}
		return ip4.String()
	}
	return ""
}

// ResolveStrmPrefix 按优先级从候选列表里挑选可用的 STRM 前缀。
//
// 参数：
//   - candidates：按优先级排列的候选前缀（任务级 > 全局配置 > 请求 Host > baseURL）
//   - port：兜底使用内网 IP 时的端口；<=0 时使用 8090
//
// 返回值：
//   - prefix：可用前缀（已去除末尾 /）；无法解析时为空串
//   - source：命中来源，用于日志与排障
//     "configured" 命中候选；"auto_lan_ip" 使用本机内网 IP 兜底；"unresolved" 全部失败
//
// 注意：本函数不会返回回环/0.0.0.0 前缀，宁可返回空让上层报错，
// 也不生成一个「看似成功、实则播不了」的 STRM。
func ResolveStrmPrefix(candidates []string, port int) (prefix string, source string) {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if IsUnusablePrefix(c) {
			continue
		}
		return strings.TrimRight(c, "/"), "configured"
	}
	if ip := DetectLANIP(); ip != "" {
		if port <= 0 {
			port = defaultServicePort
		}
		return fmt.Sprintf("http://%s:%d", ip, port), "auto_lan_ip"
	}
	return "", "unresolved"
}

// PortFromBaseURL 从 baseURL 中提取端口，失败时返回默认端口。
// 供「baseURL 不可用但需要沿用其端口做内网兜底」的场景使用。
func PortFromBaseURL(baseURL string) int {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return defaultServicePort
	}
	if p := u.Port(); p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return defaultServicePort
}
