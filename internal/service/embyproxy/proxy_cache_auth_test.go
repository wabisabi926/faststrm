package embyproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleMediaStream_AuthIsolationEndToEnd 复现 review R1：
// 合法令牌预热后的 CDN 302 缓存，不能被匿名或其它令牌复用。
func TestHandleMediaStream_AuthIsolationEndToEnd(t *testing.T) {
	strmSrc := mockStrmSrc(t, "")
	defer strmSrc.Close()
	strmURL := strmSrc.URL + "/movie.mkv"

	validAuthChecks := 0
	totalPlaybackInfo := 0
	emby := mockEmby(t, func(w http.ResponseWriter, r *http.Request) {
		totalPlaybackInfo++
		token := r.Header.Get("X-Emby-Token")
		if token == "" {
			token = r.URL.Query().Get("X-Emby-Token")
		}
		if token != "token-a" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		validAuthChecks++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(buildStrmPlaybackInfoResp(strmURL, "src1"))
	})
	defer emby.Close()

	proxy, err := New(emby.URL)
	if err != nil {
		t.Fatal(err)
	}

	// 令牌 A 通过 PlaybackInfo 预热 STRM 源缓存
	warm := httptest.NewRequest(http.MethodPost, emby.URL+"/Items/123/PlaybackInfo", nil)
	warm.Header.Set("X-Emby-Token", "token-a")
	proxy.Handler().ServeHTTP(httptest.NewRecorder(), warm)

	streamA := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/Videos/123/stream?Static=true&MediaSourceId=src1&api_key=token-a", nil)
		rr := httptest.NewRecorder()
		proxy.HandleMediaStream(rr, req)
		return rr
	}
	if rr := streamA(); rr.Code != http.StatusFound {
		t.Fatalf("token A stream = %d, want 302, body=%s", rr.Code, rr.Body.String())
	}
	if rr := streamA(); rr.Code != http.StatusFound {
		t.Fatalf("token A cached stream = %d, want 302", rr.Code)
	}
	// 首次 stream 用查询参数认证，与 PlaybackInfo 的请求头形态不同，
	// STRM 源缓存可能 miss 一次；记录基线后，匿名/令牌 B 不应再产生合法鉴权。
	baselineChecks := validAuthChecks
	baselineCalls := totalPlaybackInfo

	// 匿名请求：必须重新经过上游鉴权，不能拿到令牌 A 的缓存重定向
	anon := httptest.NewRequest(http.MethodGet, "/Videos/123/stream?Static=true&MediaSourceId=src1", nil)
	anonRR := httptest.NewRecorder()
	proxy.HandleMediaStream(anonRR, anon)
	if anonRR.Code == http.StatusFound {
		t.Fatalf("anonymous request reused cached CDN redirect: Location=%q", anonRR.Header().Get("Location"))
	}

	// 令牌 B：与 A 共享 URL/UA/条目，但缓存必须隔离
	reqB := httptest.NewRequest(http.MethodGet, "/Videos/123/stream?Static=true&MediaSourceId=src1&api_key=token-b", nil)
	rrB := httptest.NewRecorder()
	proxy.HandleMediaStream(rrB, reqB)
	if rrB.Code == http.StatusFound {
		t.Fatalf("token B reused token A cached redirect: Location=%q", rrB.Header().Get("Location"))
	}

	if validAuthChecks != baselineChecks {
		t.Fatalf("upstream valid auth checks = %d, baseline %d (anonymous/token B must not reuse A's cache)",
			validAuthChecks, baselineChecks)
	}
	if totalPlaybackInfo <= baselineCalls {
		t.Fatalf("anonymous/token B should re-check upstream, PlaybackInfo calls = %d, baseline %d",
			totalPlaybackInfo, baselineCalls)
	}
}

func TestStrmSourcesCacheIsolatesAuthHeaders(t *testing.T) {
	p := &Proxy{strmSourcesCache: make(map[strmCacheKey]strmCacheEntry)}
	sources := map[string]strmSourceMeta{"s1": {path: "/x.strm"}}
	p.cacheStrmSources("item-1", "auth-a", sources)

	if _, ok := p.getCachedStrmSources("item-1", "auth-b"); ok {
		t.Fatal("cache must not be reused across different auth/header hashes")
	}
	if _, ok := p.getCachedStrmSources("item-1", "auth-a"); !ok {
		t.Fatal("cache should be available for the same auth/header hash")
	}
}

func TestComputeHeaderHashIncludesQueryToken(t *testing.T) {
	reqA := httptest.NewRequest(http.MethodGet, "/videos/1/stream?Static=true&api_key=token-a", nil)
	reqB := httptest.NewRequest(http.MethodGet, "/videos/1/stream?Static=true&api_key=token-b", nil)
	reqAnon := httptest.NewRequest(http.MethodGet, "/videos/1/stream?Static=true", nil)

	hashA := computeHeaderHash(reqA)
	hashB := computeHeaderHash(reqB)
	hashAnon := computeHeaderHash(reqAnon)
	if hashA == hashB || hashA == hashAnon || hashB == hashAnon {
		t.Fatalf("query token must isolate cache hashes: a=%s b=%s anon=%s", hashA, hashB, hashAnon)
	}
	if authCacheHash(reqAnon) != "" {
		t.Fatal("request without credentials must not have an auth identity")
	}
}

func TestPlaybackUserCacheIsolatesAuth(t *testing.T) {
	p := &Proxy{playbackUserCache: make(map[playbackUserKey]playbackUserEntry)}
	reqA := httptest.NewRequest(http.MethodGet, "/videos/item-1/stream?api_key=token-a", nil)
	reqB := httptest.NewRequest(http.MethodGet, "/videos/item-1/stream?api_key=token-b", nil)
	reqAnon := httptest.NewRequest(http.MethodGet, "/videos/item-1/stream", nil)

	p.cachePlaybackUser(reqA, "item-1", "user-a")
	if got := p.getUserForPlayback(reqB, "item-1"); got != "" {
		t.Fatalf("token B must not reuse token A user cache, got %q", got)
	}
	if got := p.getUserForPlayback(reqAnon, "item-1"); got != "" {
		t.Fatalf("anonymous request must not reuse cached user, got %q", got)
	}
	if got := p.getUserForPlayback(reqA, "item-1"); got != "user-a" {
		t.Fatalf("same token should reuse user cache, got %q", got)
	}
}
