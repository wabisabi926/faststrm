// Package embyproxy — /emby/system/info 端口改写
//
// 客户端从 Emby 拿到的 system/info 里带着 Emby 自身端口（含 WebSocketPortNumber）。
// 不改写的话，客户端会绕过反代直连 Emby 原端口 —— 容器/公网场景直接连不通，
// 且 WebSocket 会连到没有代理的地址，导致实时通知全部失效。
//
// 对齐参考项目 embyreverseproxy _system_info_handler。
package embyproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// isSystemInfoPath 判断是否为 system/info 路径（/emby/system/info 与 /system/info）
func isSystemInfoPath(path string) bool {
	pl := strings.ToLower(path)
	return pl == "/emby/system/info" || pl == "/system/info"
}

// serveSystemInfo 代理 system/info，并把响应中的端口改写为代理自身端口
func (p *Proxy) serveSystemInfo(w http.ResponseWriter, r *http.Request) {
	target := p.embyHost + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")
	// 去掉 Accept-Encoding，确保上游返回未压缩内容以便改写 body
	req.Header.Del("Accept-Encoding")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] system/info 请求失败: %v", err)
		http.Error(w, "Emby Error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if resp.StatusCode == http.StatusOK && r.Method != http.MethodHead {
		proxyPort := currentProxyPort(r, p.proxyPort)
		if rewritten, ok := rewriteSystemInfoPorts(body, proxyPort); ok {
			body = rewritten
			logger.S().Infof("[EmbyProxy] system/info 端口改写: path=%s port=%d", r.URL.Path, proxyPort)
		}
	}

	for k, vv := range resp.Header {
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] || lk == "content-encoding" || lk == "content-length" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// rewriteSystemInfoPorts 把 system/info JSON 中的端口字段替换为代理端口。
// 仅当 WebSocketPortNumber 存在且与代理端口不同才改写；返回 (改写后 body, 是否改写)。
func rewriteSystemInfoPorts(body []byte, proxyPort int) ([]byte, bool) {
	if proxyPort <= 0 {
		return body, false
	}
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return body, false
	}
	originPort, ok := jsonIntValue(data["WebSocketPortNumber"])
	if !ok || originPort <= 0 || originPort == proxyPort {
		return body, false
	}

	data["WebSocketPortNumber"] = proxyPort
	if _, has := data["HttpServerPortNumber"]; has {
		data["HttpServerPortNumber"] = proxyPort
	}
	for _, key := range []string{"LocalAddresses", "RemoteAddresses"} {
		arr, isArr := data[key].([]interface{})
		if !isArr {
			continue
		}
		for i, v := range arr {
			if s, isStr := v.(string); isStr {
				arr[i] = replacePort(s, originPort, proxyPort)
			}
		}
		data[key] = arr
	}
	for _, key := range []string{"LocalAddress", "WanAddress"} {
		if s, isStr := data[key].(string); isStr {
			data[key] = replacePort(s, originPort, proxyPort)
		}
	}

	out, err := json.Marshal(data)
	if err != nil {
		return body, false
	}
	return out, true
}

// replacePort 只替换字符串中「作为端口出现」的 originPort。
// 用字符串全局替换（strings.ReplaceAll）会误伤主机名/IP 里的同名片段，
// 例如 originPort=80 时 "http://192.168.1.80:80" 会变成 "http://192.168.1.8097:8097"。
func replacePort(s string, originPort, newPort int) string {
	op := strconv.Itoa(originPort)
	np := strconv.Itoa(newPort)

	// 形态一：带 scheme 的 URL，如 http://192.168.1.10:8096
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if u.Port() == op {
			u.Host = net.JoinHostPort(u.Hostname(), np)
			return u.String()
		}
		return s
	}

	// 形态二：裸 host:port
	if host, port, err := net.SplitHostPort(s); err == nil && port == op {
		return net.JoinHostPort(host, np)
	}
	return s
}

// jsonIntValue 把 JSON 数值/字符串转为 int
func jsonIntValue(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

// currentProxyPort 推导代理自身对外端口：
//  1. X-Forwarded-Port（前置 nginx/Cloudflare 时最准确）
//  2. r.Host 中的端口
//  3. fallbackPort（反代实际监听端口，由 Manager.Start 注入）
//  4. 以上都拿不到时兜底 80
func currentProxyPort(r *http.Request, fallbackPort int) int {
	if fp := r.Header.Get("X-Forwarded-Port"); fp != "" {
		if idx := strings.Index(fp, ","); idx != -1 {
			fp = fp[:idx]
		}
		if n, err := strconv.Atoi(strings.TrimSpace(fp)); err == nil && n > 0 {
			return n
		}
	}
	if host := r.Host; host != "" {
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			if n, err := strconv.Atoi(host[idx+1:]); err == nil && n > 0 {
				return n
			}
		}
	}
	if fallbackPort > 0 {
		return fallbackPort
	}
	return 80
}
