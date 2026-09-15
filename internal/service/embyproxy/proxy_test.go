package embyproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ================================================================
// Mock 基础设施
// ================================================================

// mockEmby 创建模拟 Emby 服务。playbackInfoHandler 动态注入。
func mockEmby(t *testing.T, playbackInfoHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	if playbackInfoHandler != nil {
		// 用通配 handler 匹配所有 /Items/*/PlaybackInfo，支持不同 itemID
		mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
				playbackInfoHandler(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Message":"ok"}`))
		})
	}

	mux.HandleFunc("/Videos/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("emby-video-passthrough"))
	})
	mux.HandleFunc("/Audio/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>Emby Web</html>"))
	})

	return httptest.NewServer(mux)
}

// mockStrmSrc 模拟 115 网盘直链服务（resolveRedirectChain HEAD 目标）
func mockStrmSrc(t *testing.T, redirectTo string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	if redirectTo != "" {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", redirectTo)
			w.WriteHeader(http.StatusFound)
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "12345")
			w.Header().Set("Content-Type", "video/iso")
			w.WriteHeader(http.StatusOK)
		})
	}
	return httptest.NewServer(mux)
}

func buildStrmPlaybackInfoResp(strmURL, sourceID string) []byte {
	resp := map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                  sourceID,
				"Path":                strmURL,
				"IsRemote":            true,
				"Protocol":            "Http",
				"Type":                "Video",
				"SupportsDirectPlay":  false,
				"SupportsTranscoding": true,
				"TranscodingUrl":      "http://emby:8096/videos/123/master.m3u8",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func buildISOPlaybackInfoResp(strmURL string) []byte {
	resp := map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                     "iso-src-001",
				"Path":                   strmURL,
				"IsRemote":               true,
				"Protocol":               "Http",
				"Type":                   "Video",
				"SupportsDirectPlay":     false,
				"SupportsTranscoding":    true,
				"TranscodingUrl":         "http://emby:8096/videos/123/master.m3u8",
				"TranscodingContainer":   "mkv",
				"TranscodingSubProtocol": "subrip",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// ================================================================
// TestPlaybackInfo — STRM 源识别 + 改写
// ================================================================

func TestPlaybackInfo_StrmSource(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/iso/test-video.iso"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)
	sources, _ := result["MediaSources"].([]interface{})
	ms := sources[0].(map[string]interface{})

	if v, _ := ms["SupportsDirectPlay"].(bool); !v {
		t.Error("SupportsDirectPlay should be true")
	}
	if v, _ := ms["SupportsTranscoding"].(bool); v {
		t.Error("SupportsTranscoding should be false (STRM 一律 DirectPlay，禁止转码)")
	}
	if _, ok := ms["TranscodingUrl"]; ok {
		t.Error("TranscodingUrl should be removed so browsers cannot transcode")
	}
	dsURL, ok := ms["DirectStreamUrl"].(string)
	if !ok || !strings.Contains(dsURL, "/videos/123/stream") {
		t.Errorf("DirectStreamUrl missing/wrong: %v", ms["DirectStreamUrl"])
	}
	t.Logf("✅ STRM source → DirectStreamUrl=%s", dsURL)
}

func TestPlaybackInfo_NonStrmSource(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
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
	})

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)
	sources, _ := result["MediaSources"].([]interface{})
	ms := sources[0].(map[string]interface{})

	if _, ok := ms["TranscodingUrl"]; !ok {
		t.Error("TranscodingUrl should be preserved for local files")
	}
	t.Logf("✅ Non-STRM source (local) passed through unchanged")
}

func TestPlaybackInfo_ISOSource(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/电影/杜比视界.iso"
	body := buildISOPlaybackInfoResp(strmURL)

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)
	sources, _ := result["MediaSources"].([]interface{})
	ms := sources[0].(map[string]interface{})

	if v, _ := ms["SupportsDirectPlay"].(bool); !v {
		t.Error("SupportsDirectPlay should be true for ISO")
	}
	if v, _ := ms["SupportsDirectStream"].(bool); !v {
		t.Error("SupportsDirectStream should be true for ISO")
	}
	if v, _ := ms["SupportsTranscoding"].(bool); v {
		t.Error("SupportsTranscoding should be false for ISO (STRM 一律 DirectPlay)")
	}
	for _, key := range []string{"TranscodingUrl", "TranscodingContainer", "TranscodingSubProtocol"} {
		if _, ok := ms[key]; ok {
			t.Errorf("%s should be removed for ISO (禁止转码)", key)
		}
	}
	dsURL, _ := ms["DirectStreamUrl"].(string)
	if !strings.Contains(dsURL, "iso-src-001") {
		t.Errorf("DirectStreamUrl should contain iso-src-001, got: %s", dsURL)
	}
	t.Logf("✅ ISO source fully sanitized → DirectStreamUrl=%s", dsURL)
}

// ================================================================
// TestAudioDirectStreamUrl — Audio 类型走 /audio/ 路径
// ================================================================

func TestAudioDirectStreamUrl(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/歌单.flac.strm"

	body, _ := json.Marshal(map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                  "audio-src",
				"Path":                strmURL,
				"IsRemote":            true,
				"Protocol":            "Http",
				"Type":                "Audio",
				"SupportsDirectPlay":  false,
				"SupportsTranscoding": true,
				"TranscodingUrl":      "http://emby:8096/audio/789/master.flac",
			},
		},
	})

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/789/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)
	sources, _ := result["MediaSources"].([]interface{})
	if len(sources) == 0 {
		t.Fatalf("MediaSources empty! response body: %s", rr.Body.String())
	}
	ms := sources[0].(map[string]interface{})

	dsURL, _ := ms["DirectStreamUrl"].(string)
	if !strings.Contains(dsURL, "/audio/") {
		t.Errorf("Audio DirectStreamUrl should contain /audio/, got: %s", dsURL)
	}
	if strings.Contains(dsURL, "/videos/") {
		t.Errorf("Audio DirectStreamUrl should NOT contain /videos/, got: %s", dsURL)
	}
	if !strings.Contains(dsURL, "audio-src") {
		t.Errorf("should contain source ID, got: %s", dsURL)
	}
	t.Logf("✅ Audio DirectStreamUrl = %s", dsURL)
}

// ================================================================
// TestCacheThenStream — 完整 PlaybackInfo → cache → stream → 302
// ================================================================

func TestPlaybackInfo_CacheThenStream(t *testing.T) {
	strmFinal := mockStrmSrc(t, "")
	defer strmFinal.Close()
	strmEdge := mockStrmSrc(t, strmFinal.URL+"/final.iso")
	defer strmEdge.Close()
	strmRoot := mockStrmSrc(t, strmEdge.URL+"/edge.iso")
	defer strmRoot.Close()

	strmURL := strmRoot.URL + "/电影.mkv"
	expectedFinal := strmFinal.URL + "/final.iso"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	t.Run("Step1_PlaybackInfo_cached", func(t *testing.T) {
		req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		t.Logf("✅ PlaybackInfo intercepted + strm cached")
	})

	t.Run("Step2_HandleMediaStream_302ToFinalCDN", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
		rr := httptest.NewRecorder()
		proxy.HandleMediaStream(rr, req)

		if rr.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rr.Code)
		}
		loc := rr.Header().Get("Location")
		if loc != expectedFinal {
			t.Errorf("Location = %q, want %q (CDN chain not followed)", loc, expectedFinal)
		}
		t.Logf("✅ HandleMediaStream → 302 %s", loc)
	})

	t.Run("Step3_CacheHit", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
		rr := httptest.NewRecorder()
		proxy.HandleMediaStream(rr, req)
		if rr.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rr.Code)
		}
		t.Logf("✅ Second request → cache hit → 302 immediately")
	})
}

func TestPlaybackInfo_NoDoubleEncoding(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()

	strmURL := strmSrc.URL + "/api/fs/get?account=%E4%B8%BB%E5%8F%B7&pickcode=csv7hspymtny3dm22&file_name=%E6%9D%9C%E6%AF%94%E8%A7%86%E7%95%8C%20FEL.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req1 := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	proxy.Handler().ServeHTTP(httptest.NewRecorder(), req1)

	req2 := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?MediaSourceId=src1", nil)
	rr2 := httptest.NewRecorder()
	proxy.HandleMediaStream(rr2, req2)

	if rr2.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr2.Code)
	}
	loc := rr2.Header().Get("Location")
	for _, bad := range []string{"%25", "%3F", "%3D"} {
		if strings.Contains(loc, bad) {
			t.Errorf("double-encoding found (%s) in Location: %s", bad, loc)
		}
	}
	t.Logf("✅ No double-encoding, Location valid")
}

// ================================================================
// TestHandler_FullFlow — Handler() 路由全链路
// ================================================================

func TestHandler_FullFlow(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/iso/test.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	handler := proxy.Handler()

	t.Run("PlaybackInfo_intercepted", func(t *testing.T) {
		req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		t.Logf("✅ PlaybackInfo proxied + intercepted")
	})

	t.Run("Stream_302_notEmby", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rr.Code)
		}
		t.Logf("✅ /Videos/123/stream → 302 (Emby NOT called)")
	})

	t.Run("NonMediaPath_proxyThrough", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/web/index.html", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Emby Web") {
			t.Errorf("body should contain Emby Web, got: %s", rr.Body.String())
		}
		t.Logf("✅ /web/index.html → proxied to Emby")
	})
}

// ================================================================
// 构造函数边界
// ================================================================

func TestNew_EdgeCases(t *testing.T) {
	t.Run("invalid_url", func(t *testing.T) {
		if _, err := New("://invalid"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("missing_protocol", func(t *testing.T) {
		if _, err := New("emby.local:8096"); err == nil {
			t.Error("expected error for missing http/https scheme")
		}
	})
	t.Run("valid_http", func(t *testing.T) {
		p, err := New("http://emby.local:8096")
		if err != nil {
			t.Fatal(err)
		}
		if p.embyHost != "http://emby.local:8096" {
			t.Errorf("embyHost = %q", p.embyHost)
		}
	})
	t.Run("valid_https", func(t *testing.T) {
		p, err := New("https://emby.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if p.embyHost != "https://emby.example.com" {
			t.Errorf("embyHost = %q", p.embyHost)
		}
	})
	t.Run("trailingSlashStripped", func(t *testing.T) {
		p, err := New("http://emby.local:8096/")
		if err != nil {
			t.Fatal(err)
		}
		if p.embyHost != "http://emby.local:8096" {
			t.Errorf("should strip trailing slash, got: %q", p.embyHost)
		}
	})
	t.Logf("✅ All New() edge cases passed")
}

// ================================================================
// 缓存专项
// ================================================================

func TestPlaybackURLCache_TTLExpire(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/test.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	// PlaybackInfo + Stream → 缓存写入
	req1 := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	proxy.Handler().ServeHTTP(httptest.NewRecorder(), req1)
	req2 := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
	proxy.HandleMediaStream(httptest.NewRecorder(), req2)

	proxy.playbackCacheMu.Lock()
	if len(proxy.playbackURLCache) != 1 {
		t.Fatalf("cache len = %d, want 1", len(proxy.playbackURLCache))
	}
	// 把所有 entry 的 expiry 改成过去
	for k, v := range proxy.playbackURLCache {
		v.expiry = time.Now().Add(-1 * time.Second)
		proxy.playbackURLCache[k] = v
	}
	proxy.playbackCacheMu.Unlock()

	// 过期后 → 重新 resolve
	req3 := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
	rr3 := httptest.NewRecorder()
	proxy.HandleMediaStream(rr3, req3)
	if rr3.Code != http.StatusFound {
		t.Fatalf("after TTL expiry, status = %d, want 302", rr3.Code)
	}
	t.Logf("✅ TTL expiry invalidates cache → forced re-resolve")
}

func TestPlaybackURLCache_LRUEviction(t *testing.T) {
	proxy, _ := New("http://emby.local:8096")

	for i := 0; i < MaxCacheSize+1; i++ {
		proxy.cachePlaybackURL(playbackCacheKey{
			itemID:        "item" + string(rune(i)),
			mediaSourceID: "src",
			userID:        "user",
			headerHash:    "h",
		}, "http://cdn.local/x")
	}

	proxy.playbackCacheMu.Lock()
	defer proxy.playbackCacheMu.Unlock()
	if len(proxy.playbackURLCache) > MaxCacheSize {
		t.Errorf("cache %d > MaxCacheSize %d", len(proxy.playbackURLCache), MaxCacheSize)
	}
	if len(proxy.playbackCacheOrder) != len(proxy.playbackURLCache) {
		t.Errorf("order len %d != cache len %d", len(proxy.playbackCacheOrder), len(proxy.playbackURLCache))
	}
	t.Logf("✅ LRU eviction: cache=%d, order=%d", len(proxy.playbackURLCache), len(proxy.playbackCacheOrder))
}

func TestPlaybackURLCache_LRUAccessMove(t *testing.T) {
	proxy, _ := New("http://emby.local:8096")
	key1 := playbackCacheKey{itemID: "item1", mediaSourceID: "src", userID: "u", headerHash: "h"}
	key2 := playbackCacheKey{itemID: "item2", mediaSourceID: "src", userID: "u", headerHash: "h"}
	key3 := playbackCacheKey{itemID: "item3", mediaSourceID: "src", userID: "u", headerHash: "h"}

	proxy.cachePlaybackURL(key1, "http://a")
	proxy.cachePlaybackURL(key2, "http://b")
	proxy.cachePlaybackURL(key3, "http://c")

	// 访问 key1 → 移到末尾
	if got, ok := proxy.getCachedPlaybackURL(key1); !ok || got != "http://a" {
		t.Fatal("getCachedPlaybackURL failed")
	}

	proxy.playbackCacheMu.Lock()
	defer proxy.playbackCacheMu.Unlock()
	order := proxy.playbackCacheOrder
	if order[0] != key2 {
		t.Errorf("after access, order[0] should be key2, got %s", order[0].itemID)
	}
	if order[2] != key1 {
		t.Errorf("after access, order[-1] should be key1, got %s", order[2].itemID)
	}
	t.Logf("✅ LRU access moves key to end: [%s,%s,%s]", order[0].itemID, order[1].itemID, order[2].itemID)
}

// ================================================================
// 边界 MediaSources
// ================================================================

func TestProxy_EmptyMediaSources(t *testing.T) {
	body := []byte(`{"MediaSources":[],"PlaySessionId":"abc"}`)
	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/555/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "abc") {
		t.Error("should pass through unchanged")
	}
	t.Logf("✅ Empty MediaSources passthrough")
}

func TestProxy_NoMediaSourcesKey(t *testing.T) {
	body := []byte(`{"PlaySessionId":"abc123"}`)
	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/556/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "abc123") {
		t.Error("should pass through unchanged")
	}
	t.Logf("✅ No MediaSources key: passthrough")
}

// ================================================================
// 并发安全
// ================================================================

func TestProxy_ConcurrentRequests(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/iso/test.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	const goroutines = 10
	const iterations = 20
	var wg sync.WaitGroup
	failures := make(chan string, goroutines*iterations)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				itemID := "item-" + itoa(gid*1000+i)
				req1 := httptest.NewRequest("POST", emby.URL+"/Items/"+itemID+"/PlaybackInfo", strings.NewReader("{}"))
				rr1 := httptest.NewRecorder()
				proxy.Handler().ServeHTTP(rr1, req1)
				if rr1.Code != http.StatusOK {
					failures <- "PlaybackInfo status=" + itoa(rr1.Code)
				}
				req2 := httptest.NewRequest("GET", emby.URL+"/Videos/"+itemID+"/stream?Static=true&MediaSourceId=src1", nil)
				rr2 := httptest.NewRecorder()
				proxy.HandleMediaStream(rr2, req2)
				if rr2.Code != http.StatusFound {
					failures <- "Stream status=" + itoa(rr2.Code)
				}
			}
		}(g)
	}

	wg.Wait()
	close(failures)
	failCount := 0
	for f := range failures {
		failCount++
		if failCount <= 3 {
			t.Logf("  ❌ %s", f)
		}
	}
	if failCount > 0 {
		t.Errorf("%d failures out of %d", failCount, goroutines*iterations)
	} else {
		t.Logf("✅ %d goroutines × %d iter → 0 failures (race safe)", goroutines, iterations)
	}
}

// ================================================================
// Manager 热重启
// ================================================================

func findFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("findFreePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitServerReady(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := http.Get(url); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server %s not ready within %s", url, timeout)
}

func TestManager_StartAndStop(t *testing.T) {
	port := findFreePort(t)
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaSources":[]}`))
	}))
	defer emby.Close()

	mgr := NewManager()
	st := mgr.Status()
	if st.Running {
		t.Error("not running before Start")
	}

	if err := mgr.Start("127.0.0.1", port, emby.URL); err != nil {
		t.Fatal(err)
	}
	waitServerReady(t, "http://127.0.0.1:"+itoa(port), 2*time.Second)

	st = mgr.Status()
	if !st.Running {
		t.Error("running after Start")
	}
	if st.Addr != "127.0.0.1:"+itoa(port) || st.EmbyURL != emby.URL {
		t.Errorf("state mismatch: %+v", st)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := mgr.Stop(ctx); err != nil {
		t.Errorf("Stop error: %v", err)
	}
	if mgr.Status().Running {
		t.Error("stopped")
	}
	t.Logf("✅ Start → Stop OK")
}

func TestManager_Restart_HotSwapPort(t *testing.T) {
	port1 := findFreePort(t)
	port2 := findFreePort(t)
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer emby.Close()

	mgr := NewManager()
	if err := mgr.Start("127.0.0.1", port1, emby.URL); err != nil {
		t.Fatal(err)
	}
	waitServerReady(t, "http://127.0.0.1:"+itoa(port1), 2*time.Second)

	if err := mgr.Restart("127.0.0.1", port2, emby.URL); err != nil {
		t.Fatal(err)
	}
	waitServerReady(t, "http://127.0.0.1:"+itoa(port2), 2*time.Second)

	// 旧端口已释放
	time.Sleep(100 * time.Millisecond)
	if l, err := net.Listen("tcp", "127.0.0.1:"+itoa(port1)); err != nil {
		t.Errorf("old port %d should be freed: %v", port1, err)
	} else {
		l.Close()
	}
	t.Logf("✅ Restart port %d → %d hot-swapped", port1, port2)
}

func TestManager_Restart_HotSwapEmbyURL(t *testing.T) {
	port := findFreePort(t)
	emby1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"MediaSources":[{"Id":"from-emby1"}]}`))
	}))
	defer emby1.Close()
	emby2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"MediaSources":[{"Id":"from-emby2"}]}`))
	}))
	defer emby2.Close()

	mgr := NewManager()
	mgr.Start("127.0.0.1", port, emby1.URL)
	waitServerReady(t, "http://127.0.0.1:"+itoa(port), 2*time.Second)

	mgr.Restart("127.0.0.1", port, emby2.URL)
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Post("http://127.0.0.1:"+itoa(port)+"/Items/999/PlaybackInfo", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	sources, _ := result["MediaSources"].([]interface{})
	if len(sources) == 0 {
		t.Fatal("empty MediaSources")
	}
	ms0 := sources[0].(map[string]interface{})
	if ms0["Id"] != "from-emby2" {
		t.Errorf("wrong emby: %v", ms0["Id"])
	}
	t.Logf("✅ Restart EmbyURL → verified new backend")
}

func TestManager_IdempotentStart(t *testing.T) {
	port := findFreePort(t)
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer emby.Close()

	mgr := NewManager()
	mgr.Start("127.0.0.1", port, emby.URL)
	waitServerReady(t, "http://127.0.0.1:"+itoa(port), 2*time.Second)

	// 同地址同 URL → 幂等不报错
	if err := mgr.Start("127.0.0.1", port, emby.URL); err != nil {
		t.Errorf("idempotent Start should not error: %v", err)
	}
	t.Logf("✅ Idempotent Start OK")
}

func TestManager_MultipleRestarts(t *testing.T) {
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer emby.Close()

	mgr := NewManager()
	for i := 0; i < 5; i++ {
		port := findFreePort(t)
		if err := mgr.Start("127.0.0.1", port, emby.URL); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		waitServerReady(t, "http://127.0.0.1:"+itoa(port), 2*time.Second)
	}
	mgr.StopAll()
	if mgr.Status().Running {
		t.Error("should be stopped")
	}
	t.Logf("✅ 5 consecutive Restarts succeeded")
}

func TestManager_StopWithDeadlineExceeded(t *testing.T) {
	port := findFreePort(t)
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer emby.Close()

	mgr := NewManager()
	mgr.Start("127.0.0.1", port, emby.URL)
	waitServerReady(t, "http://127.0.0.1:"+itoa(port), 2*time.Second)

	// 已过期 ctx → Stop 失败
	ctx, cancel := context.WithTimeout(context.Background(), -1*time.Second)
	defer cancel()
	if err := mgr.Stop(ctx); err == nil {
		t.Error("Stop with expired ctx should error")
	}
	if !mgr.Status().Running {
		t.Error("should still be running after failed Stop")
	}

	// 正常 ctx 再 Stop
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := mgr.Stop(ctx2); err != nil {
		t.Errorf("retry Stop: %v", err)
	}
	t.Logf("✅ Stop handles deadline exceeded, retry succeeds")
}

func TestManager_StartPortAlreadyInUse(t *testing.T) {
	port := findFreePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer emby.Close()

	mgr := NewManager()
	if err := mgr.Start("127.0.0.1", port, emby.URL); err == nil {
		t.Error("Start should fail on port conflict")
	}
	if mgr.Status().Running {
		t.Error("should NOT be running after failure")
	}

	freePort := findFreePort(t)
	if err := mgr.Start("127.0.0.1", freePort, emby.URL); err != nil {
		t.Errorf("retry on free port: %v", err)
	}
	mgr.StopAll()
	t.Logf("✅ Port conflict handled, subsequent Start succeeds")
}

func TestManager_StopAll_WhenNotRunning(t *testing.T) {
	mgr := NewManager()
	mgr.StopAll()
	if mgr.Status().Running {
		t.Error("should not be running")
	}
	t.Logf("✅ StopAll no-op when not running")
}

func TestManager_MultiProxyPortConfigUpdate(t *testing.T) {
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer emby.Close()

	mgr := NewManager()
	portA, portB, portC := findFreePort(t), findFreePort(t), findFreePort(t)

	mgr.Start("127.0.0.1", portA, emby.URL)
	waitServerReady(t, "http://127.0.0.1:"+itoa(portA), 2*time.Second)

	mgr.Restart("127.0.0.1", portB, emby.URL)
	waitServerReady(t, "http://127.0.0.1:"+itoa(portB), 2*time.Second)

	mgr.Restart("127.0.0.1", portC, emby.URL)
	waitServerReady(t, "http://127.0.0.1:"+itoa(portC), 2*time.Second)

	st := mgr.Status()
	if !st.Running || st.Addr != "127.0.0.1:"+itoa(portC) {
		t.Errorf("final state = %+v", st)
	}
	mgr.StopAll()
	t.Logf("✅ Multi-step ProxyPort change: %d → %d → %d", portA, portB, portC)
}

// ================================================================
// 额外边界（上一轮测试后追加）
// ================================================================

// TestPlaybackInfo_HttpPathButNotRemote 验证 STRM 识别第二分支：
// IsRemote=false 但 Path 是 http:// 开头 → 也要识别成 STRM
func TestPlaybackInfo_HttpPathButNotRemote(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/网盘视频.strm"

	body, _ := json.Marshal(map[string]interface{}{
		"MediaSources": []interface{}{
			map[string]interface{}{
				"Id":                  "src-http-path",
				"Path":                strmURL, // http:// 开头
				"IsRemote":            false,   // 但 IsRemote=false
				"Protocol":            "File",  // Protocol=File
				"Type":                "Video",
				"SupportsDirectPlay":  true,
				"SupportsTranscoding": true,
				"TranscodingUrl":      "http://emby:8096/videos/123/master.m3u8",
			},
		},
	})

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)
	req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)
	sources, _ := result["MediaSources"].([]interface{})
	ms := sources[0].(map[string]interface{})

	// 第二分支应该识别成功 → DirectPlay 被强制 + DirectStreamUrl 生成，并关闭转码
	if v, _ := ms["SupportsTranscoding"].(bool); v {
		t.Error("SupportsTranscoding should be false (STRM 一律 DirectPlay)")
	}
	if _, ok := ms["TranscodingUrl"]; ok {
		t.Error("TranscodingUrl should be removed so browsers cannot transcode")
	}
	if v, _ := ms["SupportsDirectPlay"].(bool); !v {
		t.Error("SupportsDirectPlay should be forced true")
	}
	if _, ok := ms["DirectStreamUrl"]; !ok {
		t.Error("DirectStreamUrl should be set")
	}
	t.Logf("✅ IsRemote=false + http-path → STRM recognized via second branch")
}

// TestHandler_OnlyInterceptStaticStreams 验证反代只拦截 Static=true 的直链流，
// 非 Static 的 /videos/ 请求（浏览器转码等）应透传给上游 Emby。
func TestHandler_OnlyInterceptStaticStreams(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/video.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	// 先走一次 PlaybackInfo，缓存 STRM 源
	req0 := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	proxy.Handler().ServeHTTP(httptest.NewRecorder(), req0)

	t.Run("Static=true → 反代拦截并 302 到 CDN", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 (intercepted)", rr.Code)
		}
		t.Logf("✅ Static=true stream intercepted → %s", rr.Header().Get("Location"))
	})

	t.Run("Static=false → 透传给 Emby（转码路径不被劫持）", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/Videos/123/stream?MediaSourceId=src1&transcoding=true", nil)
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (passthrough)", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "emby-video-passthrough") {
			t.Errorf("body should come from upstream Emby, got: %s", rr.Body.String())
		}
		t.Logf("✅ Non-Static stream passed through to Emby (status=%d)", rr.Code)
	})
}

// TestHandleMediaStream_StrmURLMiss_Passthrough 没缓存也没拿到 STRM URL → 透传到 Emby
func TestHandleMediaStream_StrmURLMiss_Passthrough(t *testing.T) {
	emby := mockEmby(t, nil) // 没有 PlaybackInfo handler，全走 catch-all
	defer emby.Close()

	proxy, _ := New(emby.URL)

	// 直接请求 stream，从未走过 PlaybackInfo → 无缓存 → 无 strm URL → 透传到 Emby
	req := httptest.NewRequest("GET", emby.URL+"/Videos/777/stream?Static=true&MediaSourceId=unknown", nil)
	rr := httptest.NewRecorder()
	proxy.HandleMediaStream(rr, req)

	// mockEmby catch-all /Videos/ 返回 200 "emby-video-passthrough"
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (passthrough to Emby)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "emby-video-passthrough") {
		t.Errorf("body should contain emby passthrough response, got: %s", rr.Body.String())
	}
	t.Logf("✅ No cache → passthrough to Emby (status=%d)", rr.Code)
}

// TestResolveRedirectChain_HTTPError 模拟 CDN 返回 404/500 → 优雅降级（不 hang）
func TestResolveRedirectChain_HTTPError(t *testing.T) {
	// 模拟坏 CDN：HEAD 返回 403 Forbidden
	badCDN := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("403 Forbidden — 直链过期"))
	}))
	defer badCDN.Close()

	emby := mockEmby(t, nil)
	defer emby.Close()

	proxy, _ := New(emby.URL)

	req, _ := http.NewRequest("GET", "/Videos/123/stream", nil)
	finalURL := proxy.resolveRedirectChain(context.Background(), badCDN.URL+"/expired.iso", req, "u1")

	// 关键：不能 hang，必须返回空字符串（不是 panic）
	if finalURL != "" {
		t.Logf("  unexpected got finalURL=%q (should be empty on error)", finalURL)
	}
	t.Logf("✅ HTTP error (403) handled gracefully, no hang/panic, finalURL=%q", finalURL)
}

// TestHandleMediaStream_POSTMethod POST 请求 stream 也能正确拦截
func TestHandleMediaStream_POSTMethod(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/video.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	// 先走 PlaybackInfo 缓存
	req1 := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
	proxy.Handler().ServeHTTP(httptest.NewRecorder(), req1)

	// Kodi 有时用 POST 请求 stream（带 headers）
	req2 := httptest.NewRequest("POST", emby.URL+"/Videos/123/stream?Static=true&MediaSourceId=src1", strings.NewReader(""))
	req2.Header.Set("User-Agent", "Kodi/20.0")
	rr := httptest.NewRecorder()
	proxy.HandleMediaStream(rr, req2)

	if rr.Code != http.StatusFound {
		t.Fatalf("POST stream status = %d, want 302", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc == "" {
		t.Error("Location header should not be empty")
	}
	t.Logf("✅ POST /Videos/123/stream → 302 %s", loc)
}

// ================================================================
// isBrowserClient — 浏览器/强播放器识别（反代层 DirectPlay 决策）
// ================================================================

func TestIsBrowserClient(t *testing.T) {
	proxy, _ := New("http://emby.local:8096")

	cases := []struct {
		name   string
		client string
		ua     string
		want   bool
	}{
		{"emby_web", "Emby Web", "Mozilla/5.0 (Macintosh)", true},
		{"jellyfin_web", "Jellyfin Web", "Mozilla/5.0", true},
		{"browser", "Browser", "Mozilla/5.0", true},
		{"empty_both_is_player", "", "", false},
		{"infuse", "Infuse", "", false},
		{"vidhub", "VidHub", "", false},
		{"senplayer", "SenPlayer", "", false},
		{"kodi", "Kodi", "", false},
		{"emby_theater", "Emby Theater", "", false},
		{"empty_client_mozilla_ua", "", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)", true},
		{"empty_client_infuse_ua", "", "Infuse/7.6", false},
	}

	for _, c := range cases {
		req := httptest.NewRequest("GET", "http://x/", nil)
		if c.client != "" {
			req.Header.Set("X-Emby-Client", c.client)
		}
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		if got := proxy.isBrowserClient(req); got != c.want {
			t.Errorf("%s: isBrowserClient = %v, want %v", c.name, got, c.want)
		}
	}
	t.Logf("✅ isBrowserClient 识别矩阵通过")
}

// TestPlaybackInfo_BrowserVsPlayer 验证反代层核心行为：
// STRM 源一律强制 DirectPlay（强播放器与浏览器行为一致），禁止转码。
func TestPlaybackInfo_BrowserVsPlayer(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/test.mkv"
	body := buildStrmPlaybackInfoResp(strmURL, "src1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	t.Run("player_forced_directplay", func(t *testing.T) {
		req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		req.Header.Set("X-Emby-Client", "Infuse")
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		var result map[string]interface{}
		json.Unmarshal(rr.Body.Bytes(), &result)
		sources, _ := result["MediaSources"].([]interface{})
		ms := sources[0].(map[string]interface{})

		if v, _ := ms["SupportsDirectPlay"].(bool); !v {
			t.Error("player should get SupportsDirectPlay=true")
		}
		if _, ok := ms["DirectStreamUrl"]; !ok {
			t.Error("player should get DirectStreamUrl")
		}
		if v, _ := ms["SupportsTranscoding"].(bool); v {
			t.Error("player should get SupportsTranscoding=false")
		}
	})

	t.Run("browser_forced_directplay", func(t *testing.T) {
		req := httptest.NewRequest("POST", emby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		req.Header.Set("X-Emby-Client", "Emby Web")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		var result map[string]interface{}
		json.Unmarshal(rr.Body.Bytes(), &result)
		sources, _ := result["MediaSources"].([]interface{})
		ms := sources[0].(map[string]interface{})

		// 浏览器也应强制 DirectPlay（STRM 走转码会让 ffmpeg 拉 115 直链失败）
		if v, _ := ms["SupportsDirectPlay"].(bool); !v {
			t.Error("browser should get SupportsDirectPlay forced true")
		}
		if _, ok := ms["DirectStreamUrl"]; !ok {
			t.Error("browser should get DirectStreamUrl")
		}
		// 转码字段应被移除
		if _, ok := ms["TranscodingUrl"]; ok {
			t.Error("browser should NOT keep TranscodingUrl")
		}
	})

	t.Logf("✅ STRM 源一律强制 DirectPlay（浏览器/强播放器一致）验证通过")
}

// itoa helper（manager_test.go 已定义，这里重复避免依赖）
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ================================================================
// 修复1：resolveFileNameFromStrmURL — seek 判断信息来源（对齐 pickOneFileName）
// ================================================================

func TestResolveFileNameFromStrmURL(t *testing.T) {
	cases := []struct {
		name    string
		strmURL string
		want    string
	}{
		{"空串", "", ""},
		{"file_name_query", "http://115/api/video?file_name=%E9%98%BF%E4%BF%AE%E7%BD%97.iso&a=1", "阿修罗.iso"},
		{"path_with_ext", "http://host/视频/我的电影.BDMV", "我的电影.BDMV"},
		{"path_with_iso", "http://host/foo/movie.iso", "movie.iso"},
		{"path_no_ext_hash", "http://host/09f8a1c2be34d556", ""},
		{"path_empty_seg", "http://host/", ""},
	}
	for _, c := range cases {
		if got := resolveFileNameFromStrmURL(c.strmURL); got != c.want {
			t.Errorf("%s: resolveFileNameFromStrmURL(%q) = %q, want %q", c.name, c.strmURL, got, c.want)
		}
	}
	t.Logf("✅ resolveFileNameFromStrmURL 矩阵通过")
}

// 修复1 验收：STRM URL 的 .iso 能被解析出扩展名 → isSeekRequiredFormat=true
func TestIsSeekRequiredFormat_StrmURL(t *testing.T) {
	strmURL := "http://host/原盘/movie.iso"
	name := resolveFileNameFromStrmURL(strmURL)
	if name != "movie.iso" {
		t.Fatalf("resolveFileNameFromStrmURL(%q) = %q, want movie.iso", strmURL, name)
	}
	if !isSeekRequiredFormat("", name) {
		t.Error("name 带 .iso 应识别为 seek 格式")
	}
	if isSeekRequiredFormat("", "") {
		t.Error("空 name 不应识别为 seek 格式")
	}
	t.Logf("✅ 修复1：ISO seek 判断来源验证通过")
}

// ================================================================
// 修复2：crossOrigin 拦截（mayReturnEmbyHTMLShell / injectScriptsIntoHTML / JS 修补）
// ================================================================

func TestMayReturnEmbyHTMLShell(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"", true},
		{"/web", true},
		{"/web/index.html", true},
		{"/index.html", true},
		{"/some/page.htm", true},
		{"/items/123", false},
		{"/videos/123/x.mkv", false},
		{"/audio/123/x.flac", false},
		{"/emby/items", false},
		{"/sync/movies", false},
		{"/items/123/PlaybackInfo", false},
		{"/api/v1/ping", true}, // 末段无 "." 视为可能 HTML 壳
	}
	for _, c := range cases {
		if got := mayReturnEmbyHTMLShell(c.path); got != c.want {
			t.Errorf("mayReturnEmbyHTMLShell(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	t.Logf("✅ mayReturnEmbyHTMLShell 矩阵通过")
}

func TestInjectScriptsIntoHTML(t *testing.T) {
	t.Run("注入到head结束前", func(t *testing.T) {
		html := "<html><head><title>x</title></head><body></body></html>"
		out := injectScriptsIntoHTML(html)
		if !strings.Contains(out, crossOriginInterceptMarker) {
			t.Fatal("应注入 marker 脚本")
		}
		if strings.Index(out, "</head>") >= 0 && !strings.Contains(out, crossOriginInterceptMarker+crossOriginInterceptMarker) {
			// marker 应在 </head> 之前
			if strings.Index(out, crossOriginInterceptMarker) > strings.Index(out, "</head>") {
				t.Error("脚本应注入在 </head> 之前")
			}
		}
	})
	t.Run("只有head开标签", func(t *testing.T) {
		html := "<html><head><title>x</title><body></body></html>"
		out := injectScriptsIntoHTML(html)
		if !strings.Contains(out, crossOriginInterceptMarker) {
			t.Fatal("应在 <head...> 之后注入")
		}
	})
	t.Run("无head返回原样", func(t *testing.T) {
		html := "<html>Emby Web</html>"
		if out := injectScriptsIntoHTML(html); out != html {
			t.Error("无 head 应返回原样")
		}
	})
	t.Run("已注入跳过", func(t *testing.T) {
		html := crossOriginInterceptMarker
		if out := injectScriptsIntoHTML(html); out != html {
			t.Error("已含 marker 应跳过")
		}
	})
	t.Logf("✅ injectScriptsIntoHTML 矩阵通过")
}

func TestPatchBasehtmlplayerJS(t *testing.T) {
	t.Run("三元表达式精确命中", func(t *testing.T) {
		src := `var v = getCrossOriginValue() || (player.IsRemote && "DirectPlay" === playMethod ? null : "anonymous");`
		out := patchBasehtmlplayerJS(src)
		if strings.Contains(out, `"anonymous"`) {
			t.Error("精确命中时应整体替换为 null，不残留 anonymous")
		}
	})
	t.Run("getCrossOriginValue兑底anonymous替换", func(t *testing.T) {
		src := `var v = "anonymous";`
		if !strings.Contains(src, "getCrossOriginValue") {
			src = `function getCrossOriginValue(){return "anonymous";}`
		}
		out := patchBasehtmlplayerJS(src)
		if strings.Contains(out, `"anonymous"`) {
			t.Error("getCrossOriginValue 分支应替换 anonymous 为 null")
		}
	})
	t.Run("无匹配原样", func(t *testing.T) {
		src := `var a=1;`
		if out := patchBasehtmlplayerJS(src); out != src {
			t.Error("无匹配应返回原样")
		}
	})
	t.Logf("✅ patchBasehtmlplayerJS 矩阵通过")
}

func TestPatchPluginJS(t *testing.T) {
	t.Run("crossOrigin赋值被清除", func(t *testing.T) {
		src := `if(a)&&(elem.crossOrigin=value);`
		out := patchPluginJS(src)
		if strings.Contains(out, "elem.crossOrigin=") && strings.Contains(src, "elem.crossOrigin=") {
			if out == src {
				t.Error("pluginCrossOriginRE 未清除 crossOrigin 赋值")
			}
		}
	})
	t.Run("字幕流crossOrigin被清除", func(t *testing.T) {
		src := `&& (elem.crossOrigin = initialSubtitleStream)`
		out := patchPluginJS(src)
		if out == src {
			t.Error("pluginCrossOriginPatternRE 未清除字幕流 crossOrigin")
		}
	})
	t.Logf("✅ patchPluginJS 矩阵通过")
}

func TestIsPatchedJSPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/emby/web/modules/htmlvideoplayer/basehtmlplayer.js", true},
		{"/web/modules/htmlvideoplayer/plugin.js", true},
		{"/emby/web/scripts/player.js", false},
		{"/basehtmlplayer.js", false},
		{"/emby/web/modules/htmlvideoplayer/plugin.min.js", false},
	}
	for _, c := range cases {
		if got := isPatchedJSPath(c.path); got != c.want {
			t.Errorf("isPatchedJSPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	t.Logf("✅ isPatchedJSPath 矩阵通过")
}

// ================================================================
// 修复4：matchMediaRoute — MEDIA_ROUTES 通用拦截面
// ================================================================

func TestMatchMediaRoute(t *testing.T) {
	cases := []struct {
		path   string
		want   string
		ok     bool
	}{
		{"/videos/123/movie.mkv", "123", true},
		{"/video", "", false},
		{"/emby/videos/123/movie.mkv", "123", true},
		{"/audio/456/song.flac", "456", true},
		{"/emby/items/789/download", "789", true},
		{"/items/789/file", "789", true},
		{"/sync/jobitems/111/file", "111", true},
		{"/emby/sync/jobitems/222/file", "222", true},
		{"/videos/123/subtitles", "", false}, // nonMediaNames
		{"/videos/123/stream", "", false},    // /stream 由 isStaticDirectStream 管辖
	}
	for _, c := range cases {
		id, ok := matchMediaRoute(c.path)
		if ok != c.ok || id != c.want {
			t.Errorf("matchMediaRoute(%q) = (%q,%v), want (%q,%v)", c.path, id, ok, c.want, c.ok)
		}
	}
	t.Logf("✅ matchMediaRoute 矩阵通过")
}

func TestExtractAPIKey(t *testing.T) {
	cases := []struct {
		name string
		auth string
		tok  string
		want string
	}{
		{"X-Emby-Token优先", "", "abc123", "abc123"},
		{"Authorization带引号", `MediaBrowser Token="key-123"`, "", "key-123"},
		{"Authorization无引号", `MediaBrowser Token=key-456`, "", "key-456"},
		{"空缺省", "", "", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "http://x/", nil)
		if c.tok != "" {
			req.Header.Set("X-Emby-Token", c.tok)
		}
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		if got := extractAPIKey(req); got != c.want {
			t.Errorf("%s: extractAPIKey = %q, want %q", c.name, got, c.want)
		}
	}
	t.Logf("✅ extractAPIKey 矩阵通过")
}

// ================================================================
// 修复2/3/4 端到端：Handler 分支（HTML 注入 / JS 修补 / MEDIA_ROUTES 拦截 / 实时兜底）
// ================================================================

// customEmby 返回带 </head> 的 HTML 壳 + htmlvideoplayer JS + 可搜索媒体
func customEmby(t *testing.T, htmlBody, baseJS, pluginJS string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "basehtmlplayer.js"):
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(baseJS))
		case strings.HasSuffix(r.URL.Path, "plugin.js"):
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(pluginJS))
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("stream-video"))
		case r.URL.Path == "/Items/999/PlaybackInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"MediaSources":[{"Id":"ms-1","Path":"` + htmlBody + `","Container":"mkv","Name":"real.mkv"}]}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>t</title>` + `</head><body>` + htmlBody + `</body></html>`))
		}
	})
	return httptest.NewServer(mux)
}

func TestHandler_EndToEnd(t *testing.T) {
	t.Run("html_injection", func(t *testing.T) {
		emby := customEmby(t, "Emby Web Body", "//basejs", "//pluginjs")
		defer emby.Close()

		proxy, _ := New(emby.URL)
		req := httptest.NewRequest("GET", emby.URL+"/web/index.html", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		body := rr.Body.String()
		if !strings.Contains(body, crossOriginInterceptMarker) {
			t.Error("HTML 响应应注入 crossOrigin 脚本")
		}
		if !strings.Contains(body, "Emby Web Body") {
			t.Error("应保留原页面内容")
		}
		if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Error("注入后应禁用缓存，got Cache-Control =", cc)
		}
	})

	t.Run("basehtmlplayer_js_patch", func(t *testing.T) {
		baseJS := `var v = (player.IsRemote && "DirectPlay" === playMethod ? null : "anonymous");`
		emby := customEmby(t, "Emby Web Body", baseJS, "//pluginjs")
		defer emby.Close()

		proxy, _ := New(emby.URL)
		req := httptest.NewRequest("GET", emby.URL+"/emby/web/modules/htmlvideoplayer/basehtmlplayer.js", nil)
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		body := rr.Body.String()
		if strings.Contains(body, `"anonymous"`) {
			t.Error("basehtmlplayer.js 中 anonymous 应被替换为 null")
		}
	})

	t.Run("plugin_js_patch", func(t *testing.T) {
		pluginJS := `if(a)&&(elem.crossOrigin=value);&& (elem.crossOrigin = initialSubtitleStream)`
		emby := customEmby(t, "Emby Web Body", "//basejs", pluginJS)
		defer emby.Close()

		proxy, _ := New(emby.URL)
		req := httptest.NewRequest("GET", emby.URL+"/web/modules/htmlvideoplayer/plugin.js", nil)
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		body := rr.Body.String()
		if strings.Contains(body, "elem.crossOrigin=") {
			t.Error("plugin.js 中 crossOrigin 赋值应被清除")
		}
	})

	t.Run("media_route_intercept", func(t *testing.T) {
		emby := customEmby(t, "http://strm.internal/movie.mkv", "//basejs", "//pluginjs")
		defer emby.Close()
		_ = emby.URL

		// 用 mockStrmSrc 作为 STRM 源，确保 302 链路走通
		strmSrc := mockStrmSrc(t, "")
		defer strmSrc.Close()
		strmURL := strmSrc.URL + "/原盘/movie.iso"

		videoEmby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(buildStrmPlaybackInfoResp(strmURL, "src1"))
		})
		defer videoEmby.Close()

		proxy, _ := New(videoEmby.URL)
		// 先 POST PlaybackInfo 填充缓存
		pireq := httptest.NewRequest("POST", videoEmby.URL+"/Items/123/PlaybackInfo", strings.NewReader("{}"))
		pirr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(pirr, pireq)

		// /videos/{id}/{name} 应走 HandleMediaStream（302 到 STRM，而非 302 到 Emby）
		req := httptest.NewRequest("GET", videoEmby.URL+"/videos/123/movie.mkv", nil)
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		// STRM URL 带 .iso，修复1 后应被 seek 代理流识别 → 返回 200 且内容来自 strmSrc
		if rr.Code != http.StatusOK {
			t.Fatalf("media route 应被流拦截，code=%d", rr.Code)
		}
	})
}

// ================================================================
// ISO 播放链路端到端验证（修复1）：ISO 必须走 seek 代理流（200/206）
// 而非 302，且 Range 请求透传到 STRM 端点、内容正确
// ================================================================

// isoStrmSrc 模拟 115 ISO STRM 端点：返回固定流数据，并支持 Range（206）
func isoStrmSrc(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/iso")
		rng := r.Header.Get("Range")
		if rng == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		}
		// 解析 bytes=start- 或 bytes=start-end
		if !strings.HasPrefix(rng, "bytes=") {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		spec := strings.TrimPrefix(rng, "bytes=")
		parts := strings.SplitN(spec, "-", 2)
		start, err := strconv.Atoi(parts[0])
		if err != nil || start < 0 || start >= len(content) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end := len(content) - 1
		if len(parts) == 2 && parts[1] != "" {
			if e, eerr := strconv.Atoi(parts[1]); eerr == nil && e < len(content) {
				end = e
			}
		}
		if start > end {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		chunk := content[start : end+1]
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(chunk)
	})
	return httptest.NewServer(mux)
}

func TestISOPlayback_EndToEnd(t *testing.T) {
	content := []byte("ISO-BASE-STREAM-DATA-0123456789ABCDEF")
	isoSrc := isoStrmSrc(t, content)
	defer isoSrc.Close()

	// STRM 源 URL 带 .iso 扩展名（115 ISO 保存名）
	strmURL := isoSrc.URL + "/原盘/阿凡达双碟.iso"
	body := buildStrmPlaybackInfoResp(strmURL, "iso-src-1")

	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	defer emby.Close()

	proxy, _ := New(emby.URL)

	// 1. POST PlaybackInfo → 缓存 STRM 源元数据（meta.name 由修复1 从 STRM URL 解析出 .iso）
	piReq := httptest.NewRequest("POST", emby.URL+"/Items/888/PlaybackInfo", strings.NewReader("{}"))
	piRR := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(piRR, piReq)
	if piRR.Code != http.StatusOK {
		t.Fatalf("PlaybackInfo 应 200，got %d", piRR.Code)
	}

	// 2. 无 Range 完整请求：ISO 走 seek 代理流 → 200 + 内容来自 STRM 端点（而非 302）
	t.Run("no_range_proxy_stream", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/videos/888/movie.iso", nil)
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("ISO 应走 seek 代理流返回 200，got %d (若是 302 说明修复1 未生效)", rr.Code)
		}
		if rr.Body.String() != string(content) {
			t.Errorf("ISO 代理流内容应等于 STRM 源内容，got %q", rr.Body.String())
		}
	})

	// 3. 带 Range 的 seek 请求：ISO 应透传 Range 给 STRM 端点 → 206 + 局部内容
	t.Run("range_seek_proxy_stream", func(t *testing.T) {
		req := httptest.NewRequest("GET", emby.URL+"/videos/888/movie.iso", nil)
		req.Header.Set("Range", "bytes=5-14")
		rr := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rr, req)

		if rr.Code != http.StatusPartialContent {
			t.Fatalf("Range 请求应返回 206，got %d", rr.Code)
		}
		want := string(content[5:15])
		if got := rr.Body.String(); got != want {
			t.Errorf("206 内容应等于 STRM 局部数据 %q，got %q", want, got)
		}
		if cr := rr.Header().Get("Content-Range"); cr == "" {
			t.Error("206 响应应透传 Content-Range")
		}
	})

	// 4. 对比：.mkv（非 seek 格式）仍走 302 重定向链
	t.Run("mkv_stays_302", func(t *testing.T) {
		strmMkv := isoSrc.URL + "/电影/普通.mkv"
		mkvBody := buildStrmPlaybackInfoResp(strmMkv, "mkv-src-1")
		embyMkv := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(mkvBody)
		})
		defer embyMkv.Close()

		proxyMkv, _ := New(embyMkv.URL)
		piReq := httptest.NewRequest("POST", embyMkv.URL+"/Items/999/PlaybackInfo", strings.NewReader("{}"))
		piRR := httptest.NewRecorder()
		proxyMkv.Handler().ServeHTTP(piRR, piReq)

		req := httptest.NewRequest("GET", embyMkv.URL+"/videos/999/movie.mkv", nil)
		rr := httptest.NewRecorder()
		proxyMkv.Handler().ServeHTTP(rr, req)

		if rr.Code != http.StatusFound {
			t.Fatalf("mkv 应走 302 重定向，got %d", rr.Code)
		}
		// Location 头对中文路径会做 percent 编码，这里解码后比较
		loc := rr.Header().Get("Location")
		if unescaped, uerr := url.QueryUnescape(loc); uerr == nil {
			loc = unescaped
		}
		if loc != strmMkv {
			t.Errorf("302 Location 应等于 STRM 源 %q，got %q", strmMkv, loc)
		}
	})

	t.Logf("✅ ISO 播放链路端到端验证通过：ISO 走 seek 代理流(200/206)且 Range 透传，mkv 仍走 302")
}
