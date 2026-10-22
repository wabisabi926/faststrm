package embyproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveSelfRedirectUsesInternalHandler(t *testing.T) {
	p, err := New("http://emby:8096")
	if err != nil {
		t.Fatal(err)
	}
	p.SetInternalStrmHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Query().Get("redirect") != "1" {
			t.Errorf("unexpected request: %s redirect=%q", r.Method, r.URL.Query().Get("redirect"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Location", "http://cdn.example/movie.iso?t=1")
		w.WriteHeader(http.StatusFound)
	})

	req := httptest.NewRequest(http.MethodGet, "http://proxy:8097/videos/1/stream", nil)
	loc, status, ok := p.resolveSelfRedirect(context.Background(), "http://nas:8090/api/strm?account=a&pickcode=d18nbyd6w552vx2zs", req)
	if !ok || status != http.StatusFound || loc != "http://cdn.example/movie.iso?t=1" {
		t.Fatalf("ok=%v status=%d loc=%q", ok, status, loc)
	}
}

func TestProxyStreamToStrmUsesInternalHandler(t *testing.T) {
	p, err := New("http://emby:8096")
	if err != nil {
		t.Fatal(err)
	}
	p.SetInternalStrmHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("abc"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy:8097/videos/1/stream", nil)
	req.Header.Set("Range", "bytes=0-2")
	p.proxyStreamToStrm(rec, req, "http://nas:8090/api/strm?account=a&pickcode=d18nbyd6w552vx2zs")
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "abc" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestInternalStrmRequestOnlyForSelfEndpoint(t *testing.T) {
	p, _ := New("http://emby:8096")
	if _, ok := p.internalStrmRequest(context.Background(), "http://other/api/strm?x=1", http.MethodGet, nil, false); !ok {
		t.Fatal("self /api/strm should be internalizable")
	}
	if _, ok := p.internalStrmRequest(context.Background(), "http://other/api/other?x=1", http.MethodGet, nil, false); ok {
		t.Fatal("external endpoint must fall back to HTTP")
	}
}
