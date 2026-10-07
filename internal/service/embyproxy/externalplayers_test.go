package embyproxy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPyQuote(t *testing.T) {
	cases := []struct {
		in   string
		safe string
		want string
	}{
		{"a b", "", "a%20b"},
		{"http://x/y?z=1", "", "http%3A%2F%2Fx%2Fy%3Fz%3D1"},
		{"http://x/y?z=1", ":/?&=%", "http://x/y?z=1"},
		{"A~_.-", "", "A~_.-"},
		{"中文", "", "%E4%B8%AD%E6%96%87"},
	}
	for _, c := range cases {
		if got := pyQuote(c.in, c.safe); got != c.want {
			t.Errorf("pyQuote(%q, %q) = %q, want %q", c.in, c.safe, got, c.want)
		}
	}
	t.Logf("✅ pyQuote 矩阵通过")
}

func TestIsExternalPlayerItemPath(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"/emby/users/abc/items/123", "123", true},
		{"/users/abc/items/123", "123", true},
		{"/emby/users/abc/items/123/PlaybackInfo", "", false},
		{"/emby/items/123", "", false},
		{"/emby/users/abc/items", "", false},
	}
	for _, c := range cases {
		got, ok := isExternalPlayerItemPath(c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("isExternalPlayerItemPath(%q) = (%q, %v), want (%q, %v)", c.path, got, ok, c.want, c.ok)
		}
	}
	t.Logf("✅ isExternalPlayerItemPath 矩阵通过")
}

func TestDecodeRedirectLink(t *testing.T) {
	// 无 padding 的 base64（len%4==2）应自动补齐
	if got, err := decodeRedirectLink("dmxjOi8veA"); err != nil || got != "vlc://x" {
		t.Errorf("decodeRedirectLink(无 padding) = %q, %v; want vlc://x", got, err)
	}
	// 带 padding
	enc := base64.StdEncoding.EncodeToString([]byte("mpv-handler://x"))
	if got, err := decodeRedirectLink(enc); err != nil || got != "mpv-handler://x" {
		t.Errorf("decodeRedirectLink(带 padding) = %q, %v; want mpv-handler://x", got, err)
	}
	t.Logf("✅ decodeRedirectLink 通过")
}

func TestInjectExternalURLs(t *testing.T) {
	embyHost := "http://emby.internal:8096"
	req := httptest.NewRequest(http.MethodGet, "http://proxy.local:8097/emby/users/u1/items/123", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	data := map[string]interface{}{
		"Name":     "我的电影",
		"UserData": map[string]interface{}{"PlaybackPositionTicks": float64(36000000000)}, // 3600s
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":        "src1",
				"Type":      "Video",
				"Container": "mkv",
				"MediaStreams": []interface{}{
					map[string]interface{}{"Type": "Video", "DisplayTitle": "1080p"},
					map[string]interface{}{"Type": "Subtitle", "IsExternal": true, "Language": "chi", "Codec": "srt"},
				},
			},
		},
	}

	if !injectExternalURLs(data, req, embyHost, "123", "APIKEY") {
		t.Fatal("应注入外部播放器链接")
	}
	urls, _ := data["ExternalUrls"].([]interface{})
	if len(urls) != 4 {
		t.Fatalf("应注入 4 个播放器链接，got %d", len(urls))
	}

	byPrefix := map[string]string{}
	for _, u := range urls {
		m, _ := u.(map[string]interface{})
		name, _ := m["Name"].(string)
		urlStr, _ := m["Url"].(string)
		if i := strings.Index(name, ":"); i >= 0 {
			byPrefix[name[:i]] = urlStr
		}
	}
	for _, key := range []string{"potplayer", "vlc", "infuse", "mpv"} {
		if byPrefix[key] == "" {
			t.Errorf("缺少播放器链接: %s", key)
		}
	}

	// PotPlayer 深链应经 /redirect2external 包装，且含直链流、seek、字幕
	pu, err := url.Parse(byPrefix["potplayer"])
	if err != nil {
		t.Fatalf("解析 potplayer URL 失败: %v", err)
	}
	if pu.Path != externalRedirectPath {
		t.Errorf("应走 %s，got %q", externalRedirectPath, pu.Path)
	}
	raw, err := decodeRedirectLink(pu.Query().Get("link"))
	if err != nil {
		t.Fatalf("解码 potplayer link 失败: %v", err)
	}
	if !strings.HasPrefix(raw, "potplayer://") {
		t.Errorf("应含 potplayer:// 协议: %q", raw)
	}
	if !strings.Contains(raw, "http://proxy.local:8097/emby/videos/123/stream.mkv?Static=true&MediaSourceId=src1&api_key=APIKEY") {
		t.Errorf("直链流地址不正确: %q", raw)
	}
	if !strings.Contains(raw, "/seek=01:00:00") {
		t.Errorf("seek 应为 01:00:00: %q", raw)
	}
	if !strings.Contains(raw, "/sub=http://proxy.local:8097/emby/videos/123/src1/Subtitles/1/Stream.srt?api_key=APIKEY") {
		t.Errorf("字幕地址不正确: %q", raw)
	}
	t.Logf("✅ injectExternalURLs 通过")
}

func TestInjectExternalURLsNoMediaSources(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://proxy.local/emby/users/u1/items/123", nil)
	data := map[string]interface{}{"Name": "x"}
	if injectExternalURLs(data, req, "http://emby.internal:8096", "123", "KEY") {
		t.Error("无 MediaSources 时不应注入")
	}
}

func TestInjectExternalPlayerIntoHTML(t *testing.T) {
	p, err := New("http://emby.internal:8096")
	if err != nil {
		t.Fatal(err)
	}
	html := "<html><head><title>x</title></head><body></body></html>"

	// 默认关闭 → 原样
	if out := p.injectExternalPlayerIntoHTML(html); out != html {
		t.Error("默认关闭时不应注入")
	}

	p.SetExternalPlayers(true)
	out := p.injectExternalPlayerIntoHTML(html)
	if !strings.Contains(out, externalPlayerMarker) {
		t.Fatal("启用后应注入外部播放器脚本")
	}
	if strings.Index(out, externalPlayerMarker) > strings.Index(out, "</head>") {
		t.Error("脚本应注入在 </head> 之前")
	}

	// 与 crossOrigin 脚本共存
	both := p.injectExternalPlayerIntoHTML(injectScriptsIntoHTML(html))
	if !strings.Contains(both, crossOriginInterceptMarker) || !strings.Contains(both, externalPlayerMarker) {
		t.Error("应同时含 crossOrigin 与外部播放器脚本")
	}
	t.Logf("✅ injectExternalPlayerIntoHTML 通过")
}

func TestHandlerRedirect2External(t *testing.T) {
	emby := mockEmby(t, nil)
	defer emby.Close()

	p, err := New(emby.URL)
	if err != nil {
		t.Fatal(err)
	}
	p.SetExternalPlayers(true)

	link := base64.StdEncoding.EncodeToString([]byte("potplayer://demo"))
	req := httptest.NewRequest(http.MethodGet, "http://proxy.local/redirect2external?link="+url.QueryEscape(link), nil)
	rr := httptest.NewRecorder()
	p.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("应 302，got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "potplayer://demo" {
		t.Errorf("Location = %q, want potplayer://demo", loc)
	}
	t.Logf("✅ /redirect2external 分发通过")
}

func TestHandlerItemExternalURLsInjection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Name":"Movie","MediaSources":[{"Id":"s1","Type":"Video","Container":"mkv"}]}`))
	}))
	defer upstream.Close()

	p, err := New(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	p.SetExternalPlayers(true)

	req := httptest.NewRequest(http.MethodGet, "http://proxy.local:8097/emby/users/u1/items/123", nil)
	req.Header.Set("X-Emby-Token", "KEY")
	rr := httptest.NewRecorder()
	p.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，got %d", rr.Code)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("响应应为 JSON: %v", err)
	}
	urls, _ := data["ExternalUrls"].([]interface{})
	if len(urls) != 4 {
		t.Fatalf("应注入 4 条外部播放器链接，got %d", len(urls))
	}
	t.Logf("✅ 详情页 Items 注入 分发通过")
}
