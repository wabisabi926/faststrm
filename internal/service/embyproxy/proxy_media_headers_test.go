package embyproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// 外部 STRM 源及其重定向目标不得收到 Emby 认证头；UA 仍应保留。
func TestResolveRedirectChainDoesNotForwardEmbyCredentials(t *testing.T) {
	var firstHop, secondHop http.Header

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHop = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHop = r.Header.Clone()
		w.Header().Set("Location", final.URL+"/movie.mkv")
		w.WriteHeader(http.StatusFound)
	}))
	defer origin.Close()

	p, err := New("http://emby.invalid")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "/Videos/1/stream", nil)
	req.Header.Set("X-Emby-Token", "token-secret")
	req.Header.Set("X-MediaBrowser-Token", "mb-secret")
	req.Header.Set("Authorization", `MediaBrowser Token="auth-secret"`)
	req.Header.Set("Cookie", "token=cookie-secret")
	req.Header.Set("X-Emby-UserId", "user-secret")
	req.Header.Set("User-Agent", "FastStrmTest/1.0")

	finalURL, status := p.resolveRedirectChain(context.Background(), origin.URL+"/source", req)
	if finalURL != final.URL+"/movie.mkv" {
		t.Fatalf("finalURL = %q, want %q", finalURL, final.URL+"/movie.mkv")
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	for _, h := range sensitiveForwardHeaders {
		if v := firstHop.Get(h); v != "" {
			t.Errorf("首跳收到 %s=%q，不应外发 Emby 凭据", h, v)
		}
		if v := secondHop.Get(h); v != "" {
			t.Errorf("重定向目标收到 %s=%q，不应外发 Emby 凭据", h, v)
		}
	}
	if got := firstHop.Get("User-Agent"); got != "FastStrmTest/1.0" {
		t.Errorf("首跳 User-Agent = %q，白名单内的头应保留", got)
	}
	if got := secondHop.Get("User-Agent"); got != "FastStrmTest/1.0" {
		t.Errorf("重定向目标 User-Agent = %q，白名单内的头应保留", got)
	}
}

// 跨主机重定向的兜底清理：同主机保留，跨主机清除。
func TestFollowRedirectClientStripsCredentialsAcrossHosts(t *testing.T) {
	p, err := New("http://emby.invalid")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	origin, _ := url.Parse("http://origin.invalid/a")

	newReq := func(rawURL string) *http.Request {
		u, perr := url.Parse(rawURL)
		if perr != nil {
			t.Fatalf("parse %s: %v", rawURL, perr)
		}
		req := &http.Request{URL: u, Header: make(http.Header)}
		for _, h := range sensitiveForwardHeaders {
			req.Header.Set(h, "secret")
		}
		return req
	}

	sameHost := newReq("http://origin.invalid/b")
	if err := p.followRedirectClient.CheckRedirect(sameHost, []*http.Request{{URL: origin}}); err != nil {
		t.Fatalf("CheckRedirect same host: %v", err)
	}
	for _, h := range sensitiveForwardHeaders {
		if sameHost.Header.Get(h) == "" {
			t.Errorf("同主机跳转不应清除 %s", h)
		}
	}

	crossHost := newReq("http://cdn.invalid/c")
	if err := p.followRedirectClient.CheckRedirect(crossHost, []*http.Request{{URL: origin}}); err != nil {
		t.Fatalf("CheckRedirect cross host: %v", err)
	}
	for _, h := range sensitiveForwardHeaders {
		if v := crossHost.Header.Get(h); v != "" {
			t.Errorf("跨主机跳转应清除 %s，got %q", h, v)
		}
	}
}
