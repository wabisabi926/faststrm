package embyproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockEmby 返回模拟的 PlaybackInfo 响应
func mockEmby(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/123/PlaybackInfo", handler)
	mux.HandleFunc("/Videos/123/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-video-data"))
	})
	mux.HandleFunc("/Audio/456/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		w.WriteHeader(http.StatusOK)
	})
	// catch-all 透传反代测试用
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>Emby Web</html>"))
	})
	return httptest.NewServer(mux)
}

// TestPlaybackInfo_StrmSource 验证 STRM 源被识别并强制 DirectPlay
func TestPlaybackInfo_StrmSource(t *testing.T) {
	// Mock Emby 返回 STRM-like MediaSource
	mockResp := map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                     "src1",
				"Path":                   "http://192.168.1.10:8090/api/strm?pickcode=abc",
				"IsRemote":               true,
				"Protocol":               "Http",
				"Type":                   "Video",
				"SupportsDirectPlay":     false,
				"SupportsTranscoding":    true,
				"TranscodingUrl":         "http://emby:8096/videos/123/master.m3u8",
				"TranscodingContainer":   "m3u8",
				"TranscodingSubProtocol": "hls",
			},
		},
	}
	body, _ := json.Marshal(mockResp)

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, err := New(emby.URL)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo?UserId=abc", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	sources, _ := result["MediaSources"].([]interface{})
	if len(sources) != 1 {
		t.Fatalf("MediaSources len = %d, want 1", len(sources))
	}
	ms := sources[0].(map[string]interface{})

	// ✅ 验证: SupportsDirectPlay 被强制为 true
	if v, _ := ms["SupportsDirectPlay"].(bool); !v {
		t.Errorf("SupportsDirectPlay = false, want true")
	}

	// ✅ 验证: SupportsTranscoding 被设为 false
	if v, _ := ms["SupportsTranscoding"].(bool); v {
		t.Errorf("SupportsTranscoding = true, want false")
	}

	// ✅ 验证: TranscodingUrl 被删除
	if _, ok := ms["TranscodingUrl"]; ok {
		t.Errorf("TranscodingUrl should be deleted, but still present")
	}

	// ✅ 验证: DirectStreamUrl 被设置
	dsURL, ok := ms["DirectStreamUrl"].(string)
	if !ok || dsURL == "" {
		t.Errorf("DirectStreamUrl not set")
	} else {
		if !strings.Contains(dsURL, "/videos/123/stream") {
			t.Errorf("DirectStreamUrl = %q, want to contain /videos/123/stream", dsURL)
		}
		t.Logf("DirectStreamUrl = %s", dsURL)
	}

	t.Logf("✅ STRM source correctly forced to DirectPlay")
}

// TestPlaybackInfo_NonStrmSource 验证非 STRM 源（本地文件）原样透传
func TestPlaybackInfo_NonStrmSource(t *testing.T) {
	mockResp := map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                  "local1",
				"Path":                "/mnt/media/Movie.mkv",
				"IsRemote":            false,
				"Protocol":            "File",
				"Type":                "Video",
				"SupportsDirectPlay":  true,
				"SupportsTranscoding": true,
				"TranscodingUrl":      "http://emby:8096/videos/123/master.m3u8",
			},
		},
	}
	body, _ := json.Marshal(mockResp)

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, err := New(emby.URL)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	sources, _ := result["MediaSources"].([]interface{})
	ms := sources[0].(map[string]interface{})

	// ✅ 验证: 本地文件原样透传，TranscodingUrl 保留
	if _, ok := ms["TranscodingUrl"]; !ok {
		t.Errorf("TranscodingUrl should be preserved for local files")
	}
	if v, _ := ms["SupportsTranscoding"].(bool); !v {
		t.Errorf("SupportsTranscoding should stay true for local files")
	}

	t.Logf("✅ Non-STRM source passed through unchanged")
}

// TestPlaybackInfo_CacheThenStream 验证 PlaybackInfo 缓存后，HandleMediaStream 返回 302
// 使用 URL 编码的 STRM URL（跟真实 STRM 文件一致），验证不会双重编码
func TestPlaybackInfo_CacheThenStream(t *testing.T) {
	strmURL := "http://192.168.1.10:8090/api/fs/get?account=%E4%B8%BB%E5%8F%B7&pickcode=csv7hspymtny3dm22&file_name=%E6%9D%9C%E6%AF%94%E8%A7%86%E7%95%8C%20FEL.iso"
	mockResp := map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                 "src1",
				"Path":               strmURL,
				"IsRemote":           true,
				"Protocol":           "Http",
				"Type":               "Video",
				"SupportsDirectPlay": false,
			},
		},
	}
	body, _ := json.Marshal(mockResp)

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, err := New(emby.URL)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Step 1: PlaybackInfo 请求（触发缓存）
	t.Run("Step1_PlaybackInfo", func(t *testing.T) {
		req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("PlaybackInfo status = %d", rr.Code)
		}
		t.Logf("✅ PlaybackInfo intercepted + cached")
	})

	// Step 2: HandleMediaStream → 应该返回 302 到 STRM URL
	t.Run("Step2_HandleMediaStream", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?MediaSourceId=src1", nil)
		rr := httptest.NewRecorder()
		proxy.HandleMediaStream(rr, req)

		if rr.Code != http.StatusFound {
			t.Fatalf("HandleMediaStream status = %d, want 302 Found", rr.Code)
		}
		loc := rr.Header().Get("Location")
		if loc != strmURL {
			t.Errorf("Location = %q, want %q", loc, strmURL)
		}
		t.Logf("✅ HandleMediaStream → 302 %s", loc)
	})
}

// TestHandler_FullFlowThroughHandler 验证完整链路经过 Handler() 路由：
// 1. POST /Items/{id}/PlaybackInfo → 反代拦截 → 强制 DirectPlay + 缓存 STRM URL
// 2. GET /Videos/{id}/stream?MediaSourceId=xxx → Handler() 路由到 HandleMediaStream → 302 到 STRM URL
// 3. GET /Items/other → 非 /videos/ /audio/ 路径 → 透传反代到 Emby
func TestHandler_FullFlowThroughHandler(t *testing.T) {
	// 使用 URL 编码的 STRM URL（跟真实 STRM 文件一致）
	strmURL := "http://192.168.50.250:8090/api/fs/get?account=%E4%B8%BB%E5%8F%B7&pickcode=csv7hspymtny3dm22&file_name=%E6%9D%9C%E6%AF%94%E8%A7%86%E7%95%8C%20FEL.iso"
	mockResp := map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                 "src1",
				"Path":               strmURL,
				"IsRemote":           true,
				"Protocol":           "Http",
				"Type":               "Video",
				"SupportsDirectPlay": false,
			},
		},
	}
	body, _ := json.Marshal(mockResp)

	embySrvCalled := false
	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		embySrvCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, err := New(emby.URL)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	handler := proxy.Handler()

	// Step 1: PlaybackInfo 经过 Handler() → 反代拦截 → 强制 DirectPlay
	t.Run("Step1_PlaybackInfo_ThroughHandler", func(t *testing.T) {
		embySrvCalled = false
		req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if !embySrvCalled {
			t.Error("Emby backend should have been called for PlaybackInfo")
		}

		var result map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatalf("json unmarshal: %v", err)
		}
		sources, _ := result["MediaSources"].([]interface{})
		ms := sources[0].(map[string]interface{})
		if v, _ := ms["SupportsDirectPlay"].(bool); !v {
			t.Error("SupportsDirectPlay should be true")
		}
		if v, _ := ms["SupportsTranscoding"].(bool); v {
			t.Error("SupportsTranscoding should be false")
		}
		dsURL, _ := ms["DirectStreamUrl"].(string)
		if !strings.Contains(dsURL, "/videos/123/stream") {
			t.Errorf("DirectStreamUrl = %q, want /videos/123/stream", dsURL)
		}
		t.Logf("✅ Step1: PlaybackInfo intercepted, DirectStreamUrl=%s", dsURL)
	})

	// Step 2: /Videos/123/stream 经过 Handler() → 路由到 HandleMediaStream → 302
	t.Run("Step2_StreamRequest_ThroughHandler", func(t *testing.T) {
		embySrvCalled = false
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 Found (got body: %s)", rr.Code, rr.Body.String())
		}
		loc := rr.Header().Get("Location")
		// 验证不会双重编码：? 应该还是 ?，% 不应该变成 %25
		if strings.Contains(loc, "%3F") {
			t.Errorf("Location contains %%3F (double-encoded ?): %s", loc)
		}
		if strings.Contains(loc, "%3D") {
			t.Errorf("Location contains %%3D (double-encoded =): %s", loc)
		}
		if strings.Contains(loc, "%26") {
			t.Errorf("Location contains %%26 (double-encoded &): %s", loc)
		}
		if strings.Contains(loc, "%25E") {
			t.Errorf("Location contains %%25E (double-encoded %%): %s", loc)
		}
		// 验证 Location 就是原始 STRM URL
		if loc != strmURL {
			t.Errorf("Location = %q, want exact STRM URL %q", loc, strmURL)
		}
		if embySrvCalled {
			t.Error("Emby backend should NOT be called for /videos/ stream — should be 302 from cache")
		}
		t.Logf("✅ Step2: /videos/123/stream → 302 %s", loc)
	})

	// Step 3: 非 /videos/ /audio/ 路径 → 透传反代到 Emby
	t.Run("Step3_NonMediaPath_ProxyToEmby", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/web/index.html", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		// mock Emby catch-all 返回 200 + <html>Emby Web</html>
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (proxied to Emby)", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Emby Web") {
			t.Errorf("body should contain Emby Web content, got: %s", rr.Body.String())
		}
		t.Logf("✅ Step3: /web/index.html → proxied to Emby (status=%d, body=%d bytes)", rr.Code, rr.Body.Len())
	})
}

// TestNew_InvalidURL 验证 v1.2.1 防 panic: 无效 URL 返回 error
func TestNew_InvalidURL(t *testing.T) {
	_, err := New("://invalid")
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
	t.Logf("✅ New() returns error for invalid URL: %v", err)
}

// TestNew_ValidURL 验证正常创建
func TestNew_ValidURL(t *testing.T) {
	p, err := New("http://emby.local:8096")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("proxy is nil")
	}
	t.Logf("✅ New() works: embyHost=%s", p.embyHost)
}
