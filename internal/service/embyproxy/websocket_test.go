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
