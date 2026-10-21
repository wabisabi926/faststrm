package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/auth"
)

func TestSetupFlow(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AppConfig{
		Admin: &model.AppConfig{Username: "admin"},
		Salt:  "setup_test_salt_32b_padding______",
		Paths: config.AppConfigPaths{
			ConfigDir:  dir,
			ConfigPath: filepath.Join(dir, "config.json"),
		},
	}
	config.SetForTest(cfg)
	legacy := filepath.Join(dir, "initial-admin-password.txt")
	if err := os.WriteFile(legacy, []byte("old-plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}

	statusRec := httptest.NewRecorder()
	SetupStatus(statusRec, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	if statusRec.Code != http.StatusOK || !strings.Contains(statusRec.Body.String(), `"required":true`) {
		t.Fatalf("setup status = %d %s", statusRec.Code, statusRec.Body.String())
	}

	// 未初始化时登录必须被拒绝
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"admin"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	Login(auth.NewTokenIssuer([]byte("setup_jwt_secret_32b___________")))(loginRec, loginReq)
	if loginRec.Code != http.StatusConflict || !strings.Contains(loginRec.Body.String(), "SETUP_REQUIRED") {
		t.Fatalf("login before setup = %d %s", loginRec.Code, loginRec.Body.String())
	}

	issuer := auth.NewTokenIssuer([]byte("setup_jwt_secret_32b___________"))
	req := httptest.NewRequest(http.MethodPost, "/api/setup/admin",
		strings.NewReader(`{"username":"owner","password":"StrongPass123!"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.44:1234"
	authLoginGuard.reset(loginClientKey(req))
	rec := httptest.NewRecorder()
	SetupAdmin(issuer)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup admin = %d %s", rec.Code, rec.Body.String())
	}
	var resp SetupAdminResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Token == "" || resp.User.Username != "owner" {
		t.Fatalf("unexpected setup response: %+v", resp)
	}
	if config.NeedsSetup() {
		t.Fatal("setup should complete")
	}
	got := config.Get()
	if got.Admin == nil || got.Admin.Username != "owner" ||
		got.Admin.Password != config.HashPassword(got.Salt, "StrongPass123!") {
		t.Fatalf("admin config not persisted: %+v", got.Admin)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy plaintext password file should be removed, err=%v", err)
	}

	// 二次初始化必须拒绝
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/setup/admin",
		strings.NewReader(`{"username":"other","password":"AnotherPass123!"}`))
	req.Header.Set("Content-Type", "application/json")
	SetupAdmin(issuer)(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "SETUP_ALREADY_COMPLETED") {
		t.Fatalf("second setup = %d %s", rec.Code, rec.Body.String())
	}

	// 初始化后状态接口返回 false
	statusRec = httptest.NewRecorder()
	SetupStatus(statusRec, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	if !strings.Contains(statusRec.Body.String(), `"required":false`) {
		t.Fatalf("setup status after init = %s", statusRec.Body.String())
	}
}

func TestSetupRejectsShortPassword(t *testing.T) {
	dir := t.TempDir()
	config.SetForTest(&config.AppConfig{
		Admin: &model.AppConfig{Username: "admin"},
		Salt:  "setup_test_salt_32b_padding______",
		Paths: config.AppConfigPaths{ConfigDir: dir, ConfigPath: filepath.Join(dir, "config.json")},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/admin",
		strings.NewReader(`{"username":"admin","password":"short"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.45:1234"
	authLoginGuard.reset(loginClientKey(req))
	rec := httptest.NewRecorder()
	SetupAdmin(auth.NewTokenIssuer([]byte("setup_jwt_secret_32b___________")))(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "至少 8 位") {
		t.Fatalf("short password = %d %s", rec.Code, rec.Body.String())
	}
}
