package embyproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ================================================================
// isSystemInfoPath
// ================================================================

func TestIsSystemInfoPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/emby/system/info", true},
		{"/system/info", true},
		{"/Emby/System/Info", true},
		{"/emby/system/info/public", false},
		{"/emby/items/1/playbackinfo", false},
		{"/", false},
	}
	for _, c := range cases {
		if got := isSystemInfoPath(c.path); got != c.want {
			t.Errorf("isSystemInfoPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// ================================================================
// currentProxyPort 端口推导
// ================================================================

func TestCurrentProxyPort(t *testing.T) {
	cases := []struct {
		name    string
		xffPort string
		host    string
		want    int
	}{
		{"x_forwarded_port", "9000", "192.168.1.5:8097", 9000},
		{"x_forwarded_port_multi", "9001, 9002", "x", 9001},
		{"host_port", "", "192.168.1.5:8097", 8097},
		{"host_no_port", "", "emby.example.com", 80},
		{"x_forwarded_invalid_falls_back", "abc", "192.168.1.5:8097", 8097},
		{"empty", "", "", 80},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://x/emby/system/info", nil)
		if c.xffPort != "" {
			req.Header.Set("X-Forwarded-Port", c.xffPort)
		}
		req.Host = c.host
		if got := currentProxyPort(req); got != c.want {
			t.Errorf("%s: currentProxyPort = %d, want %d", c.name, got, c.want)
		}
	}
}

// ================================================================
// rewriteSystemInfoPorts
// ================================================================

func TestRewriteSystemInfoPorts(t *testing.T) {
	in := `{
		"WebSocketPortNumber": 8096,
		"HttpServerPortNumber": 8096,
		"LocalAddress": "http://192.168.1.10:8096",
		"WanAddress": "https://emby.example.com:8096",
		"LocalAddresses": ["http://192.168.1.10:8096", "http://10.0.0.2:8096"],
		"RemoteAddresses": ["http://1.2.3.4:8096"],
		"ServerName": "keep-me"
	}`

	out, ok := rewriteSystemInfoPorts([]byte(in), 8097)
	if !ok {
		t.Fatal("期望发生端口改写")
	}

	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("改写结果非法 JSON: %v", err)
	}
	if m["WebSocketPortNumber"] != float64(8097) {
		t.Errorf("WebSocketPortNumber = %v, want 8097", m["WebSocketPortNumber"])
	}
	if m["HttpServerPortNumber"] != float64(8097) {
		t.Errorf("HttpServerPortNumber = %v, want 8097", m["HttpServerPortNumber"])
	}
	if m["LocalAddress"] != "http://192.168.1.10:8097" {
		t.Errorf("LocalAddress = %v", m["LocalAddress"])
	}
	if m["WanAddress"] != "https://emby.example.com:8097" {
		t.Errorf("WanAddress = %v", m["WanAddress"])
	}
	if m["ServerName"] != "keep-me" {
		t.Errorf("无关字段应保留, ServerName = %v", m["ServerName"])
	}
	addrs, _ := m["LocalAddresses"].([]interface{})
	if len(addrs) != 2 || addrs[0] != "http://192.168.1.10:8097" || addrs[1] != "http://10.0.0.2:8097" {
		t.Errorf("LocalAddresses 改写不符: %v", addrs)
	}
	if !strings.Contains(string(out), "8097") || strings.Contains(string(out), "8096") {
		t.Errorf("改写结果仍含旧端口或缺少新端口: %s", string(out))
	}
}

func TestRewriteSystemInfoPorts_NoChange(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		port   int
		reason string
	}{
		{"same_port", `{"WebSocketPortNumber":8097}`, 8097, "端口相同无需改写"},
		{"missing_field", `{"HttpServerPortNumber":8096}`, 8097, "缺少 WebSocketPortNumber"},
		{"zero_port", `{"WebSocketPortNumber":8096}`, 0, "代理端口非法"},
		{"invalid_json", `not-json`, 8097, "body 非 JSON"},
	}
	for _, c := range cases {
		out, ok := rewriteSystemInfoPorts([]byte(c.body), c.port)
		if ok {
			t.Errorf("%s: 不应改写（%s）", c.name, c.reason)
		}
		if string(out) != c.body {
			t.Errorf("%s: 未改写时 body 应原样返回", c.name)
		}
	}
}

// ================================================================
// serveSystemInfo 端到端
// ================================================================

func TestServeSystemInfo_RewritesToProxyPort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/system/info" {
			t.Errorf("上游路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"WebSocketPortNumber":8096,"HttpServerPortNumber":8096,"LocalAddress":"http://127.0.0.1:8096"}`)
	}))
	defer backend.Close()

	proxy, err := New(backend.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pServer := httptest.NewServer(proxy.Handler())
	defer pServer.Close()

	resp, err := http.Get(pServer.URL + "/emby/system/info")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, string(body))
	}
	// 代理端口 = httptest 随机端口，须与 r.Host 推导一致
	wantPort := float64(currentProxyPort(httptest.NewRequest(http.MethodGet, pServer.URL, nil)))
	if m["WebSocketPortNumber"] != wantPort {
		t.Errorf("WebSocketPortNumber = %v, want %v（代理端口）", m["WebSocketPortNumber"], wantPort)
	}
	if strings.Contains(string(body), "8096") {
		t.Errorf("响应仍含 Emby 原端口 8096: %s", string(body))
	}
	t.Logf("✅ system/info 端口改写通过：Emby 8096 → 代理 %v", wantPort)
}
