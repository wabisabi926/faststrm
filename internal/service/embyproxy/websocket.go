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
	"sync/atomic"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// wsDialTimeout WebSocket 上游建连超时
const wsDialTimeout = 10 * time.Second

// WebSocket 代理超时默认值（可被 Proxy 上的同名字段覆盖，见 wsTimeouts）
const (
	// defaultWSHandshakeTimeout 等待上游返回 101 握手响应的超时。
	// 上游 TCP 通但不回任何数据时会永久阻塞在 ReadResponse，必须设期限兜底。
	defaultWSHandshakeTimeout = 10 * time.Second
	// defaultWSIdleTimeout 双向均无数据流动达到该时长即判定连接已死。
	// 半开连接（客户端切网/掉线但无 FIN）不会触发 io.Copy 返回，只能靠空闲回收。
	defaultWSIdleTimeout = 5 * time.Minute
	// defaultWSWatchdogInterval 空闲看门狗的检查周期
	defaultWSWatchdogInterval = 30 * time.Second
)

// wsTimeouts 返回本次连接生效的超时参数：Proxy 字段为零值时回落到默认值。
// 在启动看门狗 goroutine 前取好局部变量，避免 goroutine 与字段写入并发。
func (p *Proxy) wsTimeouts() (handshake, idle, interval time.Duration) {
	handshake, idle, interval = p.wsHandshakeTimeout, p.wsIdleTimeout, p.wsWatchdogInterval
	if handshake <= 0 {
		handshake = defaultWSHandshakeTimeout
	}
	if idle <= 0 {
		idle = defaultWSIdleTimeout
	}
	if interval <= 0 {
		interval = defaultWSWatchdogInterval
	}
	return
}

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
		warnUpstream("ws-dial", "[EmbyProxy][ws] 连接上游失败 "+p.embyHost+": "+err.Error())
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
	// 去掉 Accept-Encoding：上游拒绝升级时（401/404）其错误响应体会被下面按 64KB
	// 截断回传，压缩过的响应体截断后无法解码；请求明文后截断只是内容变短，仍可读。
	fwd.Header.Del("Accept-Encoding")

	if err := fwd.Write(backendConn); err != nil {
		_ = backendConn.Close()
		logger.S().Warnf("[EmbyProxy][ws] 写出握手请求失败: %v", err)
		http.Error(w, "WebSocket handshake write failed", http.StatusBadGateway)
		return
	}

	handshakeTimeout, idleTimeout, watchdogInterval := p.wsTimeouts()
	if handshakeTimeout > 0 {
		_ = backendConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	}
	backendBuf := bufio.NewReader(backendConn)
	resp, err := http.ReadResponse(backendBuf, fwd)
	// 无论成败都清除期限，避免残留期限影响后续 relay
	_ = backendConn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = backendConn.Close()
		logger.S().Warnf("[EmbyProxy][ws] 读取上游握手响应失败: %v", err)
		http.Error(w, "WebSocket handshake read failed", http.StatusBadGateway)
		return
	}

	// 上游未升级（401/404 等）：按普通响应回传，让客户端看到真实错误
	if resp.StatusCode != http.StatusSwitchingProtocols {
		// 先读完 body 再关连接；若先关连接，body 会被截断成「已缓冲的那一小段」
		defer backendConn.Close()
		defer resp.Body.Close()
		// 读期内设期限：上游声明了长度却中途不再发数据时不会永久阻塞
		_ = backendConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		const bodyLimit = 64 * 1024
		body, _ := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
		for k, vv := range resp.Header {
			lk := strings.ToLower(k)
			// 跳过 hop-by-hop 与 body 分帧头：body 可能被截断，若原样回写上游的
			// Content-Length，客户端会按完整长度读取而遇到连接提前关闭（截断报错）。
			// 交由 net/http 重新决定长度（通常 chunked）。
			if hopByHopHeaders[lk] || lk == "content-length" {
				continue
			}
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		if resp.ContentLength > bodyLimit {
			logger.S().Warnf("[EmbyProxy][ws] 上游未升级 status=%d path=%s，错误响应体 %d 字节已截断为 %d",
				resp.StatusCode, r.URL.Path, resp.ContentLength, bodyLimit)
		} else {
			logger.S().Warnf("[EmbyProxy][ws] 上游未升级 status=%d path=%s", resp.StatusCode, r.URL.Path)
		}
		return
	}

	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		_ = backendConn.Close()
		logger.S().Warnf("[EmbyProxy][ws] hijack 客户端连接失败: %v", err)
		http.Error(w, "WebSocket client hijack failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 回写 101（头部原样，不可改动 Sec-WebSocket-Accept）。
	// 跳过 Content-Length / Transfer-Encoding：升级后是裸双向流，没有消息边界，
	// 带上这两个头会让客户端按「有长度/有分块」解析而错乱。
	if _, werr := fmt.Fprintf(clientBuf, "HTTP/1.1 101 Switching Protocols\r\n"); werr != nil {
		_ = clientConn.Close()
		_ = backendConn.Close()
		return
	}
	for k, vv := range resp.Header {
		if lk := strings.ToLower(k); lk == "content-length" || lk == "transfer-encoding" {
			continue
		}
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

	// 双向活动共享一个时间戳；任一方向读到数据即刷新。
	// 半开连接（对端已消失但无 FIN/RST）不会让 io.Copy 返回，只能靠看门狗回收，
	// 否则每个死连接会长期占住 2 个 goroutine + 2 个 socket。
	tracker := newIdleTracker()
	stopWatchdog := make(chan struct{})
	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatchdog:
				return
			case <-ticker.C:
				if idle := tracker.idleFor(); idle >= idleTimeout {
					logger.S().Infof("[EmbyProxy][ws] 空闲超时，关闭连接 path=%s client=%s idle=%s",
						r.URL.Path, r.RemoteAddr, idle.Round(time.Millisecond))
					_ = clientConn.Close()
					_ = backendConn.Close()
					return
				}
			}
		}
	}()

	done := make(chan struct{}, 2)
	// client → backend：从 bufio.Reader 读，避免丢失 hijack 前已缓冲的数据
	go func() {
		_, _ = io.Copy(backendConn, activityReader{r: clientBuf.Reader, tracker: tracker})
		done <- struct{}{}
	}()
	// backend → client：必须从 backendBuf 读 —— http.ReadResponse 会一次性多读，
	// 把 101 之后上游已推送的字节缓冲进 bufio.Reader；直接读裸 backendConn 会跳过
	// 这些字节。写侧用裸 clientConn，因为握手已 Flush，字节顺序一致。
	go func() {
		_, _ = io.Copy(clientConn, activityReader{r: backendBuf, tracker: tracker})
		done <- struct{}{}
	}()

	<-done
	close(stopWatchdog)
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

// idleTracker 记录双向最近一次数据活动时间（纳秒时间戳），供空闲看门狗判断。
// 两个 relay goroutine 与看门狗并发读写，故用原子操作。
type idleTracker struct{ last atomic.Int64 }

func newIdleTracker() *idleTracker {
	t := &idleTracker{}
	t.touch()
	return t
}

func (t *idleTracker) touch() { t.last.Store(time.Now().UnixNano()) }

func (t *idleTracker) idleFor() time.Duration {
	return time.Since(time.Unix(0, t.last.Load()))
}

// activityReader 包装 relay 的读端：读到数据即刷新活动时间。
// 只包读侧即可 —— 双向各有一个读端，合起来覆盖两个方向的流量。
type activityReader struct {
	r       io.Reader
	tracker *idleTracker
}

func (a activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.tracker.touch()
	}
	return n, err
}
