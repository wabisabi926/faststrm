package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
)

// ---------- strm test helpers ----------

// buildBaseStrmOptions 构造「不含真实 client / 仅用于参数校验路径」的 StrmOptions
// 注意：仅能测试「在 opts.Client115 被调用前就 return」的路径（参数校验 + account 不存在）
func buildBaseStrmOptions(t *testing.T) StrmOptions {
	t.Helper()
	accountStore := newTestAccountStore(t)
	// 预置一个账号用于「account 存在但参数非法」场景
	valid := true
	_ = accountStore.Upsert(&model.AccountInfo{Name: "acc1", AccountType: "115", Cookie: "UID=1", CookieValid: &valid})
	return StrmOptions{
		Cfg:          &config.AppConfig{Settings: &model.Settings{}},
		Client115:    nil, // 参数校验路径用不到；如果被调用就会 panic（测试失败）
		AccountStore: accountStore,
	}
}

func bodyError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		return ""
	}
	s, _ := m["error"].(string)
	return s
}

// buildStrmURL 用 net/url 安全拼接查询参数（避免含空格/特殊字符的 pickcode 破坏 URL 语法）
func buildStrmURL(account, pickcode string) string {
	v := url.Values{}
	if account != "" {
		v.Set("account", account)
	}
	if pickcode != "" {
		v.Set("pickcode", pickcode)
	}
	return "/api/strm?" + v.Encode()
}

// ================================================================
// HandleStrm — 参数校验（在调用 115 API 之前就返回的分支）
// ================================================================

func TestHandleStrm_MissingAccount(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("", "abcdef123456"), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if !contains(bodyError(t, w), "Missing account") {
		t.Errorf("error wrong: %q", bodyError(t, w))
	}
}

func TestHandleStrm_MissingPickcode(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("acc1", ""), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
	if !contains(bodyError(t, w), "Missing pickcode") {
		t.Errorf("error wrong: %q", bodyError(t, w))
	}
}

func TestHandleStrm_InvalidPickcode(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	tests := []struct {
		name     string
		pickcode string
	}{
		{"chinese chars", "文件123456"},
		{"spaces", "abc def12345"},
		{"path traversal", "../etc/passwd"},
		{"url in pickcode", "http://evil.com/x"},
		{"too short", "a1"},
		{"only dots", "............"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, buildStrmURL("acc1", tt.pickcode), nil)
			HandleStrm(opts).ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Errorf("pickcode=%q status: got %d, want 400 (body=%s)",
					tt.pickcode, w.Code, w.Body.String())
			}
			if !contains(bodyError(t, w), "Bad pickcode") &&
				!contains(bodyError(t, w), "Missing pickcode") {
				t.Errorf("pickcode=%q error wrong: %q", tt.pickcode, bodyError(t, w))
			}
		})
	}
}

// 合法 pickcode 形式：严格 17 位大小写字母数字
func TestHandleStrm_ValidPickcodeFormat(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	validFormats := []string{
		"AbCdEf12345678901",   // 17 chars mixed
		"AB12CD34EF56GH78I",   // 17 chars uppercase+digit
		"1234567890abcdefg",   // 17 chars lowercase+digit
	}
	for _, pc := range validFormats {
		t.Run("fmt_"+pc[:6], func(t *testing.T) {
			w := httptest.NewRecorder()
			// account 不存在 → 预期 404（说明通过了 pickcode 校验）
			r := httptest.NewRequest(http.MethodGet, buildStrmURL("nonexist", pc), nil)
			HandleStrm(opts).ServeHTTP(w, r)
			// 404 就意味着通过了参数校验，走到了 accountStore.Get 查询这一步
			if w.Code != http.StatusNotFound {
				t.Errorf("pickcode=%q should pass format check, got status=%d body=%s",
					pc, w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleStrm_AccountNotFound(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	w := httptest.NewRecorder()
	// 17 位合法 pickcode + 不存在的 account → 404
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("ghost", "AbCdEf12345678901"), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
	if !contains(bodyError(t, w), "Account not found") {
		t.Errorf("error wrong: %q", bodyError(t, w))
	}
}

func TestHandleStrm_HEADMethod_ValidatesParams(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	w := httptest.NewRecorder()
	// HEAD 也应该执行相同的参数校验
	r := httptest.NewRequest(http.MethodHead, buildStrmURL("acc1", ""), nil)
	HandleStrm(opts).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("HEAD missing pickcode: got %d, want 400", w.Code)
	}
}
