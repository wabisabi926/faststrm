package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
)

func TestRequireSetupComplete(t *testing.T) {
	config.SetForTest(&config.AppConfig{Admin: &model.AppConfig{Username: "admin"}, Salt: "s"})
	rec := httptest.NewRecorder()
	RequireSetupComplete()(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("setup required status=%d, want 409", rec.Code)
	}

	config.SetForTest(&config.AppConfig{
		Admin: &model.AppConfig{Username: "admin", Password: config.HashPassword("s", "StrongPass123!")},
		Salt:  "s",
	})
	rec = httptest.NewRecorder()
	RequireSetupComplete()(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("setup complete status=%d, want 204", rec.Code)
	}
}
