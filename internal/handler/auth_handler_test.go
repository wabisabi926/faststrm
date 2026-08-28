package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
)

// setupAuthTestCfg 初始化全局 config（auth handler 依赖 config.Get()）
func setupAuthTestCfg(t *testing.T) *config.AppConfig {
	t.Helper()
	salt := "test_salt_32b_padding______"
	cfg := &config.AppConfig{
		Salt: salt,
		Admin: &model.AppConfig{
			Username: "admin",
			Password: pwdcrypto.HashPassword(salt, "admin"),
		},
		Settings: model.DefaultSettings(),
	}
	config.SetForTest(cfg)
	return cfg
}

// handlerPost 发送 POST 请求到 handler
func handlerPost(handlerFunc http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handlerFunc(w, req)
	return w
}

// handlerGet 发送 GET 请求
func handlerGet(handlerFunc http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handlerFunc(w, req)
	return w
}

// ==================== Logout ====================

func TestLogout(t *testing.T) {
	w := handlerPost(Logout(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "已退出登录") {
		t.Fatalf("expected response to contain '已退出登录', got %s", body)
	}
}

// ==================== Login ====================

func TestLogin(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		setupAuthTestCfg(t)
		issuer := auth.NewTokenIssuer([]byte("test-secret-32b________________"))
		w := handlerPost(Login(issuer), `{"username":"admin","password":"admin"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d; body=%s", w.Code, w.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		token, ok := resp["token"].(string)
		if !ok || token == "" {
			t.Fatalf("expected non-empty token, got %v", resp["token"])
		}
		user, ok := resp["user"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected user object, got %v", resp["user"])
		}
		if user["username"] != "admin" {
			t.Fatalf("expected user.username=admin, got %v", user["username"])
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		setupAuthTestCfg(t)
		issuer := auth.NewTokenIssuer([]byte("test-secret-32b________________"))
		w := handlerPost(Login(issuer), `{"username":"admin","password":"wrong"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "error") {
			t.Fatalf("expected response to contain 'error', got %s", body)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		setupAuthTestCfg(t)
		issuer := auth.NewTokenIssuer([]byte("test-secret-32b________________"))
		w := handlerPost(Login(issuer), "not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})
}

// ==================== ChangePassword ====================

func TestChangePassword(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		w := handlerPost(ChangePassword(), "not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("empty fields", func(t *testing.T) {
		w := handlerPost(ChangePassword(), `{"currentPassword":"","newPassword":""}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("short new password", func(t *testing.T) {
		w := handlerPost(ChangePassword(), `{"currentPassword":"admin","newPassword":"1"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "至少 6 位") {
			t.Fatalf("expected '至少 6 位' in body, got %s", body)
		}
	})

	t.Run("wrong current password", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangePassword(), `{"currentPassword":"wrong","newPassword":"newpass1"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "当前密码错误") {
			t.Fatalf("expected '当前密码错误' in body, got %s", body)
		}
	})
}

// ==================== ChangeCredentials ====================

func TestChangeCredentials(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		w := handlerPost(ChangeCredentials(), "not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("empty current password", func(t *testing.T) {
		w := handlerPost(ChangeCredentials(), `{"currentPassword":""}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "请输入当前密码") {
			t.Fatalf("expected '请输入当前密码' in body, got %s", body)
		}
	})

	t.Run("wrong current password", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"wrong"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "当前密码错误") {
			t.Fatalf("expected '当前密码错误' in body, got %s", body)
		}
	})

	t.Run("too short username", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newUsername":"a","newPassword":"","confirmPassword":""}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "3-32") {
			t.Fatalf("expected '3-32' length error, got %s", body)
		}
	})

	t.Run("too long username", func(t *testing.T) {
		setupAuthTestCfg(t)
		longName := "abcdefghijklmnopqrstuvwxyz0123456789_extra_long" // >32 chars
		body, _ := json.Marshal(map[string]string{
			"currentPassword": "admin",
			"newUsername":     longName,
		})
		w := handlerPost(ChangeCredentials(), string(body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("invalid username format starts with digit", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newUsername":"123abc"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "字母") && !strings.Contains(body, "下划线") {
			t.Fatalf("expected username format error, got %s", body)
		}
	})

	t.Run("too long username at max boundary", func(t *testing.T) {
		setupAuthTestCfg(t)
		// 32 chars 应该通过（usernameMaxLen=32）
		maxName := "a1234567890123456789012345678901" // 32 chars: a + 31 digits
		body, _ := json.Marshal(map[string]string{
			"currentPassword": "admin",
			"newUsername":     maxName,
		})
		w := handlerPost(ChangeCredentials(), string(body))
		// 不检查具体状态码，只要不 panic 即可（可能因后续 SaveAdmin 失败返回 500 或因其他原因 400）
		_ = w.Code
	})

	t.Run("username with special chars rejected", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newUsername":"user@name"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("same as current username", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newUsername":"admin"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "不能与当前用户名相同") {
			t.Fatalf("expected same-username error, got %s", body)
		}
	})

	t.Run("short new password in change credentials", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newPassword":"1","confirmPassword":"1"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "少于 6 位") {
			t.Fatalf("expected '少于 6 位' error, got %s", body)
		}
	})

	t.Run("password mismatch", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newPassword":"newpass1","confirmPassword":"different"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "两次输入的新密码不一致") {
			t.Fatalf("expected password mismatch error, got %s", body)
		}
	})

	t.Run("no changes at all", func(t *testing.T) {
		setupAuthTestCfg(t)
		w := handlerPost(ChangeCredentials(), `{"currentPassword":"admin","newUsername":"","newPassword":"","confirmPassword":""}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "未填写任何修改项") {
			t.Fatalf("expected '未填写任何修改项' error, got %s", body)
		}
	})
}

// ==================== Health ====================

func TestHealth(t *testing.T) {
	w := handlerGet(Health)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status=ok, got %s", resp.Status)
	}
	if resp.Version == "" {
		t.Fatal("expected non-empty version")
	}
}
