package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestSetupAdminConcurrentOnlyOneSucceeds 复现 review R2：
// 两个同时进入的初始化请求只能有一个成功，已完成的凭据不能被后到请求覆盖。
func TestSetupAdminConcurrentOnlyOneSucceeds(t *testing.T) {
	dir := t.TempDir()
	config.SetForTest(&config.AppConfig{
		Admin: &model.AppConfig{Username: "admin"},
		Salt:  "setup_test_salt_32b_padding______",
		Paths: config.AppConfigPaths{ConfigDir: dir, ConfigPath: filepath.Join(dir, "config.json")},
	})
	issuer := auth.NewTokenIssuer([]byte("setup_jwt_secret_32b___________"))

	candidates := []struct {
		name, password, ip string
	}{
		{"owner-a", "StrongPass123!", "192.0.2.70"},
		{"owner-b", "AnotherPass123!", "192.0.2.71"},
	}
	type result struct {
		code int
		body string
	}
	results := make(chan result, len(candidates))
	var wg sync.WaitGroup
	for _, c := range candidates {
		wg.Add(1)
		go func(c struct{ name, password, ip string }) {
			defer wg.Done()
			payload, _ := json.Marshal(SetupAdminRequest{Username: c.name, Password: c.password})
			req := httptest.NewRequest(http.MethodPost, "/api/setup/admin", strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = c.ip + ":1234"
			authLoginGuard.reset(loginClientKey(req))
			rec := httptest.NewRecorder()
			SetupAdmin(issuer)(rec, req)
			results <- result{code: rec.Code, body: rec.Body.String()}
		}(c)
	}
	wg.Wait()
	close(results)

	okCount, conflictCount := 0, 0
	for res := range results {
		switch res.code {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
			if !strings.Contains(res.body, "SETUP_ALREADY_COMPLETED") {
				t.Fatalf("conflict body = %s", res.body)
			}
		default:
			t.Fatalf("unexpected setup status %d: %s", res.code, res.body)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("ok=%d conflict=%d, want 1/1", okCount, conflictCount)
	}

	admin := config.Get().Admin
	if admin == nil {
		t.Fatal("admin not initialized")
	}
	winner := candidates[0]
	if admin.Username == candidates[1].name {
		winner = candidates[1]
	} else if admin.Username != candidates[0].name {
		t.Fatalf("admin overwritten by unknown request: %+v", admin)
	}
	if admin.Password != config.HashPassword(config.Get().Salt, winner.password) {
		t.Fatalf("admin password does not match winning request: %+v", admin)
	}
	if config.NeedsSetup() {
		t.Fatal("setup should be complete after one success")
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
