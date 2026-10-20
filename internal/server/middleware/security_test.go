package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
)

func TestRequirePasswordChanged(t *testing.T) {
	salt := "middleware_test_salt_32b_pad_____"
	setDefault := func(password string) {
		config.SetForTest(&config.AppConfig{
			Salt:     salt,
			Admin:    &model.AppConfig{Username: "admin", Password: pwdcrypto.HashPassword(salt, password)},
			Settings: model.DefaultSettings(),
		})
	}

	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

	setDefault("admin")
	rec := httptest.NewRecorder()
	RequirePasswordChanged()(next)(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("default password: expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("expected error code, got %s", rec.Body.String())
	}

	setDefault("new-password")
	rec = httptest.NewRecorder()
	RequirePasswordChanged()(next)(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("changed password: expected 200, got %d", rec.Code)
	}
}

func TestCORS_SameOriginOnly(t *testing.T) {
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

	t.Run("same origin echoes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/api/health", nil)
		req.Host = "example.com"
		req.Header.Set("Origin", "https://example.com")
		rec := httptest.NewRecorder()
		CORS(next)(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Fatalf("expected same-origin allow header, got %q", got)
		}
	})

	t.Run("cross origin not echoed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/api/health", nil)
		req.Host = "example.com"
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		CORS(next)(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("cross-origin must not be allowed, got %q", got)
		}
	})

	t.Run("playback path allows cross origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/api/strm", nil)
		req.Host = "example.com"
		req.Header.Set("Origin", "https://player.example")
		rec := httptest.NewRecorder()
		CORS(next)(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://player.example" {
			t.Fatalf("playback cross-origin should be allowed, got %q", got)
		}
	})

	t.Run("preflight cross origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "http://example.com/api/settings", nil)
		req.Host = "example.com"
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		CORS(next)(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("preflight cross-origin must not be allowed, got %q", got)
		}
	})
}
