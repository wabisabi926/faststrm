// Package embyproxy — WebSocket 透明代理
//
// Emby 客户端（Emby Web / 各类 App）依赖 /embywebsocket 做播放进度回传、会话同步、
// 远程控制与实时通知。反代若不做双向转发，这些功能会全部失效。
//
// 实现采用「升级完成后 TCP 裸转发」：WS 握手之后 frame 层对代理是透明的，
// 双向 io.Copy 即可；因为不解析 frame，也不会破坏 permessage-deflate 等扩展。
// 零第三方依赖，对齐项目「小而美」定位。
package embyproxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// wsDialTimeout WebSocket 上游建连超时
const wsDialTimeout = 10 * time.Second

// isWebSocketUpgrade 判断请求是否为 WebSocket 升级请求。
// 需同时满足 Upgrade: websocket 且 Connection 的 token 列表含 upgrade（均大小写不敏感）。
func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, v := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				return true
			}
		}
	}
	return false
}

// handleWebSocket 将客户端 WebSocket 连接透明转发到上游 Emby。
// 握手成功后双向转发；任一方向结束即关闭两端连接。
func (p *Proxy) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket: response writer does not support hijacking", http.StatusInternalServerError)
		return
	}

	backendConn, err := dialEmbyWS(p.embyHost)
	if err != nil {
		logger.S().Warnf("[EmbyProxy][ws] 连接上游失败 %s: %v", p.embyHost, err)
		http.Error(w, "WebSocket upstream dial failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 构造转发请求：保留原始路径（Emby 同时提供 /embywebsocket 与 /emby/embywebsocket），
	// Host 置为上游 host，其余头（Sec-WebSocket-Key / Upgrade / 子协议）原样透传。
	fwd := r.Clone(r.Context())
	fwd.URL.Scheme = "" // 置空后 Request.Write 走 origin-form（path + query）
	fwd.URL.Host = ""
	fwd.Host = wsUpstreamHost(p.embyHost)
	fwd.Header.Del("Host")

	if err := fwd.Write(backendConn); err != nil {
		_ = backendConn.Close()
		logger.S().Warnf("[EmbyProxy][ws] 写出握手请求失败: %v", err)
		http.Error(w, "WebSocket handshake write failed", http.StatusBadGateway)
		return
	}

	backendBuf := bufio.NewReader(backendConn)
	resp, err := http.ReadResponse(backendBuf, fwd)
	if err != nil {
		_ = backendConn.Close()
		logger.S().Warnf("[EmbyProxy][ws] 读取上游握手响应失败: %v", err)
		http.Error(w, "WebSocket handshake read failed", http.StatusBadGateway)
		return
	}

	// 上游未升级（401/404 等）：按普通响应回传，让客户端看到真实错误
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = backendConn.Close()
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		logger.S().Warnf("[EmbyProxy][ws] 上游未升级 status=%d path=%s", resp.StatusCode, r.URL.Path)
		return
	}

	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		_ = backendConn.Close()
		logger.S().Warnf("[EmbyProxy][ws] hijack 客户端连接失败: %v", err)
		return
	}

	// 回写 101（头部原样，不可改动 Sec-WebSocket-Accept）
	if _, werr := fmt.Fprintf(clientBuf, "HTTP/1.1 101 Switching Protocols\r\n"); werr != nil {
		_ = clientConn.Close()
		_ = backendConn.Close()
		return
	}
	for k, vv := range resp.Header {
		for _, v := range vv {
			fmt.Fprintf(clientBuf, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprint(clientBuf, "\r\n")
	if err := clientBuf.Flush(); err != nil {
		_ = clientConn.Close()
		_ = backendConn.Close()
		return
	}

	logger.S().Infof("[EmbyProxy][ws] 升级成功 path=%s client=%s", r.URL.Path, r.RemoteAddr)

	done := make(chan struct{}, 2)
	// client → backend：从 bufio.Reader 读，避免丢失 hijack 前已缓冲的数据
	go func() {
		_, _ = io.Copy(backendConn, clientBuf.Reader)
		done <- struct{}{}
	}()
	// backend → client：直接写裸连接（握手已 Flush，字节顺序一致），
	// 避免 bufio.Writer 未 flush 导致数据滞留
	go func() {
		_, _ = io.Copy(clientConn, backendConn)
		done <- struct{}{}
	}()

	<-done
	_ = clientConn.Close()
	_ = backendConn.Close()
	<-done
}

// dialEmbyWS 按 embyHost 的 scheme 建立到上游的裸 TCP/TLS 连接
func dialEmbyWS(embyHost string) (net.Conn, error) {
	u, err := url.Parse(embyHost)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	d := &net.Dialer{Timeout: wsDialTimeout}
	if u.Scheme == "https" {
		return tls.DialWithDialer(d, "tcp", host, &tls.Config{
			ServerName: u.Hostname(),
			MinVersion: tls.VersionTLS12,
		})
	}
	return d.Dial("tcp", host)
}

// wsUpstreamHost 取上游 host（含端口），用作握手请求的 Host 头
func wsUpstreamHost(embyHost string) string {
	u, err := url.Parse(embyHost)
	if err != nil || u.Host == "" {
		return embyHost
	}
	return u.Host
}
