package embyproxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ================================================================
// isWebSocketUpgrade 判定
// ================================================================

func TestIsWebSocketUpgrade(t *testing.T) {
	cases := []struct {
		name    string
		upgrade string
		conn    string
		want    bool
	}{
		{"normal", "websocket", "Upgrade", true},
		{"lowercase", "websocket", "upgrade", true},
		{"mixed_case", "WebSocket", "keep-alive, Upgrade", true},
		{"no_upgrade_header", "", "Upgrade", false},
		{"wrong_upgrade", "h2c", "Upgrade", false},
		{"no_connection_token", "websocket", "keep-alive", false},
		{"connection_close", "websocket", "close", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://x/embywebsocket", nil)
		if c.upgrade != "" {
			req.Header.Set("Upgrade", c.upgrade)
		}
		if c.conn != "" {
			req.Header.Set("Connection", c.conn)
		}
		if got := isWebSocketUpgrade(req); got != c.want {
			t.Errorf("%s: isWebSocketUpgrade = %v, want %v", c.name, got, c.want)
		}
	}
}

// ================================================================
// WebSocket 透明转发（端到端：真实 TCP 升级 + 双向回显）
// ================================================================

// wsEchoBackend 模拟上游 Emby：校验升级请求 → 回 101 → 之后回显收到的字节
func wsEchoBackend(t *testing.T, gotPath *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotPath != nil {
			gotPath.Store(r.URL.Path)
		}
		if !isWebSocketUpgrade(r) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("backend: ResponseWriter 不支持 hijack")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer conn.Close()

		fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
		if err := buf.Flush(); err != nil {
			return
		}
		_, _ = io.Copy(conn, buf.Reader) // 回显
	}))
}

// readWSHandshake 手动读取状态行 + 头，直到空行；返回状态行。
// 不借助 http.ReadResponse，确保 bufio 中剩余字节（升级后的数据）不被吞掉。
func readWSHandshake(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取响应头失败: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			return statusLine
		}
	}
}

func TestWebSocketProxy_TransparentRelay(t *testing.T) {
	var gotPath atomic.Value
	backend := wsEchoBackend(t, &gotPath)
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 发起真实的 WS 升级请求
	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine := readWSHandshake(t, br)
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("期望 101 Switching Protocols，实际状态行: %q", statusLine)
	}

	// 双向透明转发：升级后发什么应原样回显
	payload := "hello-emby-ws"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("读取回显失败: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("回显不一致: got %q, want %q", got, payload)
	}

	if p, _ := gotPath.Load().(string); p != "/embywebsocket" {
		t.Fatalf("上游收到的路径 = %q, want /embywebsocket", p)
	}
	t.Logf("✅ WS 透明转发通过：101 升级 + 双向回显 + 路径保持 /embywebsocket")
}

func TestWebSocketProxy_UpstreamNotUpgraded(t *testing.T) {
	// 上游拒绝升级 → 代理应把真实状态码透传给客户端，而不是挂死
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "denied")
	}))
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine := readWSHandshake(t, br)
	if !strings.Contains(statusLine, "401") {
		t.Fatalf("期望透传 401，实际状态行: %q", statusLine)
	}
}

// TestWebSocketProxy_UpstreamErrorBodyNotLengthMismatched 回归：上游拒绝升级且错误
// 响应体超过 64KB 截断上限时，回写响应不得原样带上上游的 Content-Length。
// 否则客户端会按完整长度读取，遇到连接提前关闭而报 unexpected EOF。
func TestWebSocketProxy_UpstreamErrorBodyNotLengthMismatched(t *testing.T) {
	const bodySize = 100 * 1024

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, strings.Repeat("x", bodySize))
	}))
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	parseReq, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/embywebsocket", nil)
	resp, err := http.ReadResponse(bufio.NewReader(conn), parseReq)
	if err != nil {
		t.Fatalf("读取代理响应失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("期望透传 401，实际: %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 {
		t.Errorf("截断响应不应带 Content-Length，实际声明 %d", resp.ContentLength)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取截断的错误响应体失败（长度声明与实际不符）: %v", err)
	}
	if len(got) != 64*1024 {
		t.Errorf("错误响应体应被截断为 64KB，实际 %d 字节", len(got))
	}
	t.Logf("✅ 超长错误响应被安全截断：无 Content-Length 声明，客户端读取无错")
}

// TestWebSocketProxy_UpstreamPushAfterHandshake 回归：上游在 101 之后紧接着推送数据。
//
// 这些字节与响应头同一次写、落在同一个 TCP 段，会被 http.ReadResponse 缓冲进
// bufio.Reader；若 relay 直接读裸连接，这部分数据就会被静默跳过。
// 断言：升级后客户端什么都不发，也应立刻收到上游推送。
func TestWebSocketProxy_UpstreamPushAfterHandshake(t *testing.T) {
	const pushed = "server-initiated-frame"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("backend: ResponseWriter 不支持 hijack")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer conn.Close()

		// 响应头与推送数据写进同一个 bufio.Writer、只 Flush 一次，
		// 确保二者落在同一个 TCP 段里，复现「数据被缓冲」的场景
		fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
		fmt.Fprint(buf, pushed)
		if err := buf.Flush(); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, buf.Reader) // 保持连接直到客户端断开
	}))
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	if statusLine := readWSHandshake(t, br); !strings.Contains(statusLine, "101") {
		t.Fatalf("期望 101 Switching Protocols，实际状态行: %q", statusLine)
	}

	// 客户端不发任何数据，直接等上游推送
	got := make([]byte, len(pushed))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("未收到上游握手后的推送数据（疑被 bufio 缓冲吞掉）: %v", err)
	}
	if string(got) != pushed {
		t.Fatalf("推送数据不一致: got %q, want %q", got, pushed)
	}
	t.Logf("✅ 上游握手后立即推送的数据完整到达客户端")
}

// TestWebSocketProxy_SurvivesServerTimeout 锁定：升级后的 WS 连接不受
// http.Server 的 ReadTimeout/WriteTimeout 影响。
//
// net/http 在 hijackLocked() 里会显式 SetDeadline(time.Time{}) 清除继承的绝对
// 期限，本用例把该行为固化成契约 —— 若将来换成别的升级方式或自行设置了期限，
// 会导致 WS 在超出服务端超时后被静默掐断，这里会先失败。
func TestWebSocketProxy_SurvivesServerTimeout(t *testing.T) {
	var gotPath atomic.Value
	backend := wsEchoBackend(t, &gotPath)
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// httptest.NewServer 无法设置超时，这里手动起一个带短超时的 http.Server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:      proxy.Handler(),
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	addr := ln.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	if statusLine := readWSHandshake(t, br); !strings.Contains(statusLine, "101") {
		t.Fatalf("期望 101 Switching Protocols，实际状态行: %q", statusLine)
	}

	// 越过 ReadTimeout / WriteTimeout（300ms）后再收发，连接应仍然可用
	time.Sleep(700 * time.Millisecond)

	payload := "after-server-timeout"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("超过服务端超时后连接被掐断（Hijack 继承的 deadline 未清除）: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("回显不一致: got %q, want %q", got, payload)
	}
	t.Logf("✅ 越过服务端 Read/WriteTimeout 后 WS 转发依然可用")
}

// ================================================================
// P1：握手超时 与 空闲看门狗
// ================================================================

// TestWebSocketProxy_UpstreamHandshakeTimeout 上游 TCP 可达但迟迟不回握手响应时，
// 代理必须在超时后返回 502，而不是永久阻塞在 http.ReadResponse（泄漏 goroutine + FD）。
func TestWebSocketProxy_UpstreamHandshakeTimeout(t *testing.T) {
	// 裸 TCP 上游：收下连接，但一个字节都不回
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		accepted <- c
	}()
	t.Cleanup(func() {
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	})

	proxy, err := New("http://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy.wsHandshakeTimeout = 200 * time.Millisecond

	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine := readWSHandshake(t, br)
	if !strings.Contains(statusLine, "502") {
		t.Fatalf("期望握手超时后返回 502，实际状态行: %q", statusLine)
	}
	t.Logf("✅ 上游不回握手响应时按超时返回 502: %s", strings.TrimSpace(statusLine))
}

// TestWebSocketProxy_IdleWatchdog 半开连接：升级成功后双向都无数据流动，
// 看门狗应在空闲超时后主动关闭两端，客户端读到 EOF 而不是一直挂着占用资源。
func TestWebSocketProxy_IdleWatchdog(t *testing.T) {
	var gotPath atomic.Value
	backend := wsEchoBackend(t, &gotPath)
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy.wsIdleTimeout = 300 * time.Millisecond
	proxy.wsWatchdogInterval = 50 * time.Millisecond

	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	if statusLine := readWSHandshake(t, br); !strings.Contains(statusLine, "101") {
		t.Fatalf("期望 101 Switching Protocols，实际状态行: %q", statusLine)
	}

	// 升级后双方都不发数据，等待看门狗回收
	start := time.Now()
	_, err = br.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("期望连接被看门狗关闭，却读到了数据")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("连接未被看门狗回收（客户端 deadline 先触发）: %v", err)
	}
	t.Logf("✅ 空闲 %s 后连接被回收: %v", time.Since(start).Truncate(10*time.Millisecond), err)
}

// TestWebSocketProxy_IdleWatchdogKeepsActiveConn 持续有流量时不能被空闲看门狗误杀
func TestWebSocketProxy_IdleWatchdogKeepsActiveConn(t *testing.T) {
	var gotPath atomic.Value
	backend := wsEchoBackend(t, &gotPath)
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy.wsIdleTimeout = 300 * time.Millisecond
	proxy.wsWatchdogInterval = 50 * time.Millisecond

	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	if statusLine := readWSHandshake(t, br); !strings.Contains(statusLine, "101") {
		t.Fatalf("期望 101 Switching Protocols，实际状态行: %q", statusLine)
	}

	// 每 100ms 收发一次，总时长跨过空闲阈值（300ms）——连接必须始终保持可用
	for i := 0; i < 8; i++ {
		payload := fmt.Sprintf("ping-%d", i)
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatalf("第 %d 次写入失败，连接疑似被误杀: %v", i, err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(br, got); err != nil {
			t.Fatalf("第 %d 次读取失败，连接疑似被误杀: %v", i, err)
		}
		if string(got) != payload {
			t.Fatalf("第 %d 次回显不一致: got %q, want %q", i, got, payload)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("✅ 持续流量下连接未被空闲看门狗误杀（跨过 %s 空闲阈值）", proxy.wsIdleTimeout)
}

// TestWebSocketProxy_StripsBodyFramingHeadersOn101 101 回写时不得带上
// Content-Length / Transfer-Encoding：升级后是裸双向流，没有消息边界，
// 带上这两个头会让客户端按「有长度/有分块」解析而错乱。
func TestWebSocketProxy_StripsBodyFramingHeadersOn101(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("backend: ResponseWriter 不支持 hijack")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer conn.Close()

		fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n"+
			"Content-Length: 0\r\n"+
			"Transfer-Encoding: chunked\r\n\r\n")
		if err := buf.Flush(); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, buf.Reader)
	}))
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	addr := strings.TrimPrefix(pServer.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /embywebsocket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	// 逐行读原始响应头，保留文本以便断言
	br := bufio.NewReader(conn)
	var raw strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取响应头失败: %v", err)
		}
		raw.WriteString(line)
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	headers := strings.ToLower(raw.String())

	if !strings.Contains(headers, "101") {
		t.Fatalf("期望 101 Switching Protocols，实际响应头:\n%s", raw.String())
	}
	if !strings.Contains(headers, "sec-websocket-accept") {
		t.Errorf("101 响应缺少 Sec-WebSocket-Accept:\n%s", raw.String())
	}
	for _, bad := range []string{"content-length", "transfer-encoding"} {
		if strings.Contains(headers, bad) {
			t.Errorf("101 响应不应回写 %s:\n%s", bad, raw.String())
		}
	}
	t.Logf("✅ 101 回写已剔除 Content-Length / Transfer-Encoding")
}
