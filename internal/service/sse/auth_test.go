package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/auth"
)

func TestHandlerRequiresTokenCookie(t *testing.T) {
	issuer := auth.NewTokenIssuer([]byte("test-secret"))
	handler := Handler(issuer)

	req := httptest.NewRequest(http.MethodGet, "/api/events/stream", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without cookie, got %d", rr.Code)
	}

	token, err := issuer.Sign("alice")
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/events/stream", nil)
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 with valid cookie, got %d body=%s", rr.Code, rr.Body.String())
	}
}
