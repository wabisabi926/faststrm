package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteStrmHeadResponse_ISOHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodHead, "/api/strm", nil)
	rec := httptest.NewRecorder()

	writeStrmHeadResponse(rec, req, 12345, "https://cdn.example/x.iso", "", "test-UA",
		"电影/原盘/示例.iso", "d18nbyd6w552vx2zs")

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Length"); got != "12345" {
		t.Fatalf("Content-Length = %q", got)
	}
	if got := res.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	cd := res.Header.Get("Content-Disposition")
	if strings.Contains(cd, "电影/原盘") || strings.Contains(cd, "/示例") {
		t.Fatalf("Content-Disposition keeps raw path separators: %q", cd)
	}
	if !strings.Contains(cd, ".iso") {
		t.Fatalf("Content-Disposition should keep .iso, got %q", cd)
	}
}

func TestApplyMediaContentType(t *testing.T) {
	t.Run("empty type is filled", func(t *testing.T) {
		h := http.Header{}
		applyMediaContentType(h, "movie.mkv")
		if got := h.Get("Content-Type"); got != "video/x-matroska" {
			t.Fatalf("Content-Type = %q", got)
		}
	})
	t.Run("generic octet-stream is upgraded", func(t *testing.T) {
		h := http.Header{}
		h.Set("Content-Type", "application/octet-stream")
		applyMediaContentType(h, "movie.mkv")
		if got := h.Get("Content-Type"); got != "video/x-matroska" {
			t.Fatalf("Content-Type = %q", got)
		}
	})
	t.Run("specific type is preserved", func(t *testing.T) {
		h := http.Header{}
		h.Set("Content-Type", "video/mp4")
		applyMediaContentType(h, "movie.mkv")
		if got := h.Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("Content-Type = %q", got)
		}
	})
}
