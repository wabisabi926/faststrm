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

// TestPlaybackInfo_CacheThenStream 验证 PlaybackInfo 缓存 → HandleMediaStream 返回 302
func TestPlaybackInfo_CacheThenStream(t *testing.T) {
	strmURL := "http://192.168.1.10:8090/api/strm?pickcode=csv7hspymtny3dm22&file_name=FEL.iso"
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
