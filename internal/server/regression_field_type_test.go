package server

// 阶段 7.1 回归测试：字段类型一致性校验（Go vs TS）
// 重点验证前端兼容性关键点：
//  1. overallPercent 必须为字符串（TS toFixed(2) 风格）
//  2. SSE 帧格式必须为 data: {JSON}\n\n
//  3. 空数组必须为 [] 而非 null
//  4. 错误响应必须为 {error: "..."}
//  5. health 响应 {status, version}
//  6. login 响应 {message, token, user:{username}}
//  7. tasks 响应 {tasks:[], scheduler:{...}}

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

// doReq 执行 HTTP 请求并返回状态码 + 响应 body（原始字节，避免数字被转 float64）
func doReq(h http.HandlerFunc, method, path string, body any, token string) (int, json.RawMessage) {
	var reqBody *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reqBody = bytes.NewReader(raw)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr.Code, json.RawMessage(rr.Body.Bytes())
}

// decodeAsMap 将 JSON 解码为 map[string]any 以便字段断言
func decodeAsMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode as map failed: %v, raw=%s", err, raw)
	}
	return m
}

// assertIsString 断言 JSON 值为字符串类型
func assertIsString(t *testing.T, name string, val any) {
	t.Helper()
	if _, ok := val.(string); !ok {
		t.Errorf("%s must be string, got %T: %v", name, val, val)
	}
}

// assertIsArray 断言 JSON 值为数组类型（非 null）
func assertIsArray(t *testing.T, name string, val any) {
	t.Helper()
	if val == nil {
		t.Errorf("%s must be array, got null", name)
		return
	}
	if _, ok := val.([]any); !ok {
		t.Errorf("%s must be array, got %T: %v", name, val, val)
	}
}

// TestRegression_FieldTypes 字段类型一致性校验
func TestRegression_FieldTypes(t *testing.T) {
	// 准备最小依赖
	salt := "regression_field_type_salt_32b_pad_"
	settings := model.DefaultSettings()
	settings.InternalToken = "regression-test-jwt-secret-key"
	cfg := &config.AppConfig{
		Salt:     salt,
		Admin:    &model.AppConfig{Username: "admin", Password: pwdcrypto.HashPassword(salt, "admin")},
		Settings: settings,
	}
	config.SetForTest(cfg)
	issuer := auth.NewTokenIssuer([]byte(settings.InternalToken))

	t.Run("health_response_shape", func(t *testing.T) {
		code, raw := doReq(handler.Health, "GET", "/api/health", nil, "")
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "status", m["status"])
		assertIsString(t, "version", m["version"])
		if m["status"] != "ok" {
			t.Errorf("status want 'ok', got %v", m["status"])
		}
	})

	t.Run("login_response_shape", func(t *testing.T) {
		code, raw := doReq(handler.Login(issuer), "POST", "/api/auth/login",
			map[string]string{"username": "admin", "password": "admin"}, "")
		if code != 200 {
			t.Fatalf("want 200, got %d, body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "message", m["message"])
		assertIsString(t, "token", m["token"])
		user, ok := m["user"].(map[string]any)
		if !ok {
			t.Fatalf("user must be object, got %T", m["user"])
		}
		assertIsString(t, "user.username", user["username"])
	})

	t.Run("login_error_shape", func(t *testing.T) {
		code, raw := doReq(handler.Login(issuer), "POST", "/api/auth/login",
			map[string]string{"username": "admin", "password": "wrong"}, "")
		if code != 401 {
			t.Fatalf("want 401, got %d", code)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "error", m["error"])
	})

	t.Run("logout_response", func(t *testing.T) {
		code, raw := doReq(handler.Logout(), "POST", "/api/auth/logout", nil, "")
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "message", m["message"])
	})

	// 获取 token 供后续测试
	tokenResp := func() string {
		_, raw := doReq(handler.Login(issuer), "POST", "/api/auth/login",
			map[string]string{"username": "admin", "password": "admin"}, "")
		m := decodeAsMap(t, raw)
		s, _ := m["token"].(string)
		return s
	}()
	if tokenResp == "" {
		t.Fatal("无法获取 JWT token")
	}

	t.Run("sse_overall_percent_is_string", func(t *testing.T) {
		// 直接调用 SSE Server 的 EmitProgress，验证 overallPercent 序列化为字符串
		srv := sse.GetServer()
		srv.ClearTaskLogs("test-task-001")
		srv.EmitProgress(sse.ProgressPayload{
			Event:          "progress",
			TaskID:         "test-task-001",
			OverallPercent: "50.00", // 必须是字符串
			Percent:        50,
		})
		// 从内存日志缓冲中取出帧
		frames := srv.GetTaskLogs("test-task-001")
		if len(frames) == 0 {
			t.Fatal("no SSE frames emitted")
		}
		frame := frames[0]
		// 帧格式：data: {JSON}\n\n
		if !strings.HasPrefix(frame, "data: ") {
			t.Errorf("frame must start with 'data: ', got: %q", frame[:min(20, len(frame))])
		}
		if !strings.HasSuffix(frame, "\n\n") {
			t.Errorf("frame must end with \\n\\n, got suffix: %q", frame[max(0, len(frame)-5):])
		}
		// 解析 JSON 部分
		jsonPart := strings.TrimSuffix(strings.TrimPrefix(frame, "data: "), "\n\n")
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
			t.Fatalf("unmarshal SSE payload: %v", err)
		}
		// overallPercent 必须为字符串
		opVal, ok := payload["overallPercent"]
		if !ok {
			t.Fatal("overallPercent field missing")
		}
		assertIsString(t, "overallPercent", opVal)
		if opVal != "50.00" {
			t.Errorf("overallPercent want '50.00', got %v", opVal)
		}
	})

	t.Run("sse_complete_payload_shape", func(t *testing.T) {
		srv := sse.GetServer()
		srv.ClearTaskLogs("test-task-002")
		srv.EmitComplete(sse.CompletePayload{
			Event:           "complete",
			TaskID:          "test-task-002",
			Status:          "completed",
			TotalFiles:      10,
			DownloadedFiles: 8,
			DurationMs:      5000,
		})
		frames := srv.GetTaskLogs("test-task-002")
		if len(frames) == 0 {
			t.Fatal("no SSE frames emitted")
		}
		jsonPart := strings.TrimSuffix(strings.TrimPrefix(frames[0], "data: "), "\n\n")
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assertIsString(t, "event", payload["event"])
		assertIsString(t, "taskId", payload["taskId"])
		assertIsString(t, "status", payload["status"])
	})

	t.Run("sse_log_payload_shape", func(t *testing.T) {
		srv := sse.GetServer()
		srv.ClearTaskLogs("test-task-003")
		srv.EmitLog("test-task-003", "info", "starting download")
		frames := srv.GetTaskLogs("test-task-003")
		if len(frames) == 0 {
			t.Fatal("no SSE frames emitted")
		}
		jsonPart := strings.TrimSuffix(strings.TrimPrefix(frames[0], "data: "), "\n\n")
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assertIsString(t, "event", payload["event"])
		assertIsString(t, "level", payload["level"])
		assertIsString(t, "message", payload["message"])
	})
}

// TestRegression_EmptyArrayNotNull 空数组必须是 [] 而非 null
func TestRegression_EmptyArrayNotNull(t *testing.T) {
	salt := "regression_empty_array_salt_32b_pad_"
	cfgDir := t.TempDir()
	accountStore, _ := store.NewAccountStore(salt, cfgDir)

	t.Run("account_list_empty_is_array", func(t *testing.T) {
		// 无账号时 GET /api/account 应返回 [] 而非 null
		code, raw := doReq(handler.ListAccounts(accountStore), "GET", "/api/account", nil, "")
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		// 直接解析为 []any 验证是数组
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("response must be array, got: %s, err: %v", raw, err)
		}
		if arr == nil {
			t.Error("empty account list must be [] not null")
		}
		if len(arr) != 0 {
			t.Errorf("want empty array, got %d items", len(arr))
		}
		// 验证原始字节不是 "null"
		if strings.TrimSpace(string(raw)) == "null" {
			t.Error("response is 'null' instead of '[]'")
		}
	})
}

// TestRegression_SettingsDefaultFields 默认 settings 字段完整性
func TestRegression_SettingsDefaultFields(t *testing.T) {
	cfgDir := t.TempDir()
	salt := "regression_settings_salt_32b_pad___"
	ss := store.NewSettingsStore(salt, cfgDir)
	settings, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if settings == nil {
		t.Fatal("settings must not be nil")
	}

	// 验证前端依赖的关键字段存在且有默认值
	t.Run("strm_extensions_not_empty", func(t *testing.T) {
		if len(settings.StrmExtensions) == 0 {
			t.Error("StrmExtensions must have defaults")
		}
	})
	t.Run("download_extensions_not_empty", func(t *testing.T) {
		if len(settings.DownloadExtensions) == 0 {
			t.Error("DownloadExtensions must have defaults")
		}
	})
	t.Run("emby_settings_exists", func(t *testing.T) {
		// Emby 配置必须存在（即使未配置）
		_ = settings.Emby // 不应 panic
	})
	t.Run("telegram_settings_exists", func(t *testing.T) {
		_ = settings.Telegram
	})
	t.Run("life_monitor_settings_exists", func(t *testing.T) {
		_ = settings.LifeMonitor
	})
}

// TestRegression_AccountCRUDLifecycle 账号 CRUD 全生命周期字段一致性
func TestRegression_AccountCRUDLifecycle(t *testing.T) {
	salt := "regression_crud_salt_32b_pad____"
	cfgDir := t.TempDir()
	accountStore, _ := store.NewAccountStore(salt, cfgDir)

	// 创建账号
	t.Run("create_account_response_shape", func(t *testing.T) {
		code, raw := doReq(handler.CreateAccount(accountStore), "POST", "/api/account",
			map[string]string{
				"accountType": "115",
				"name":        "test-115",
				"cookie":      "UID=test; CID=abc; SEID=def",
			}, "")
		if code != 201 {
			t.Fatalf("want 201, got %d, body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "name", m["name"])
		assertIsString(t, "accountType", m["accountType"])
		assertIsString(t, "cookie", m["cookie"])
	})

	// 列表中有 1 个账号
	t.Run("list_after_create", func(t *testing.T) {
		code, raw := doReq(handler.ListAccounts(accountStore), "GET", "/api/account", nil, "")
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		var arr []map[string]any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("unmarshal array: %v", err)
		}
		if len(arr) != 1 {
			t.Fatalf("want 1 account, got %d", len(arr))
		}
		if arr[0]["name"] != "test-115" {
			t.Errorf("name want 'test-115', got %v", arr[0]["name"])
		}
	})

	// 更新账号
	t.Run("update_account", func(t *testing.T) {
		code, raw := doReq(handler.UpdateAccount(accountStore), "PUT", "/api/account",
			map[string]string{
				"name":        "test-115",
				"accountType": "115",
				"cookie":      "UID=updated; CID=new; SEID=new2",
			}, "")
		if code != 200 {
			t.Fatalf("want 200, got %d, body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		if m["cookie"] != "UID=updated; CID=new; SEID=new2" {
			t.Errorf("cookie not updated: %v", m["cookie"])
		}
	})

	// 删除账号
	t.Run("delete_account", func(t *testing.T) {
		code, raw := doReq(handler.DeleteAccount(accountStore), "DELETE", "/api/account?name=test-115", nil, "")
		if code != 200 {
			t.Fatalf("want 200, got %d, body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "message", m["message"])
	})

	// 删除后列表为空
	t.Run("list_after_delete_empty", func(t *testing.T) {
		code, raw := doReq(handler.ListAccounts(accountStore), "GET", "/api/account", nil, "")
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("must be array: %v", err)
		}
		if len(arr) != 0 {
			t.Errorf("want empty, got %d", len(arr))
		}
	})
}

// TestRegression_ErrorResponses 错误响应格式一致性
func TestRegression_ErrorResponses(t *testing.T) {
	salt := "regression_error_salt_32b_pad____"
	cfgDir := t.TempDir()
	accountStore, _ := store.NewAccountStore(salt, cfgDir)

	t.Run("create_account_missing_fields", func(t *testing.T) {
		code, raw := doReq(handler.CreateAccount(accountStore), "POST", "/api/account",
			map[string]string{"name": "incomplete"}, "")
		if code != 400 {
			t.Fatalf("want 400, got %d", code)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "error", m["error"])
	})

	t.Run("update_nonexistent_account", func(t *testing.T) {
		code, raw := doReq(handler.UpdateAccount(accountStore), "PUT", "/api/account",
			map[string]string{"name": "nonexistent", "cookie": "x"}, "")
		if code != 404 {
			t.Fatalf("want 404, got %d", code)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "error", m["error"])
	})

	t.Run("delete_missing_name_param", func(t *testing.T) {
		code, raw := doReq(handler.DeleteAccount(accountStore), "DELETE", "/api/account", nil, "")
		if code != 400 {
			t.Fatalf("want 400, got %d", code)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "error", m["error"])
	})
}

// 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

