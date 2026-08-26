package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

// ---------- test helpers ----------

// newTestAccountStore 创建一个空的 AccountStore（使用测试临时目录，不污染真实数据）
func newTestAccountStore(t *testing.T) *store.AccountStore {
	t.Helper()
	s, err := store.NewAccountStore("test-salt-1234", t.TempDir())
	if err != nil {
		t.Fatalf("NewAccountStore failed: %v", err)
	}
	return s
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

func mustReadBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal body %q: %v", w.Body.String(), err)
	}
	return m
}

// ================================================================
// ListAccounts
// ================================================================

func TestListAccounts_Empty(t *testing.T) {
	s := newTestAccountStore(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/account", nil)
	ListAccounts(s).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var list []model.AccountInfo
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("list len: got %d, want 0", len(list))
	}
}

func TestListAccounts_Multiple(t *testing.T) {
	s := newTestAccountStore(t)
	valid := true
	_ = s.Upsert(&model.AccountInfo{Name: "a1", AccountType: "115", Cookie: "UID=1", CookieValid: &valid})
	_ = s.Upsert(&model.AccountInfo{Name: "a2", AccountType: "openlist", Account: "u", Password: "p", URL: "http://x"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/account", nil)
	ListAccounts(s).ServeHTTP(w, r)

	var list []model.AccountInfo
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("list len: got %d, want 2", len(list))
	}
	names := map[string]bool{}
	for _, a := range list {
		names[a.Name] = true
	}
	if !names["a1"] || !names["a2"] {
		t.Errorf("names missing: %v", names)
	}
}

// ================================================================
// CreateAccount
// ================================================================

func TestCreateAccount_Validation(t *testing.T) {
	tests := []struct {
		name       string
		body       CreateAccountRequest
		wantStatus int
		wantErrKey string // 错误信息中包含的关键字
	}{
		{
			name:       "missing accountType",
			body:       CreateAccountRequest{Name: "a"},
			wantStatus: http.StatusBadRequest,
			wantErrKey: "accountType",
		},
		{
			name:       "missing name",
			body:       CreateAccountRequest{AccountType: "115"},
			wantStatus: http.StatusBadRequest,
			wantErrKey: "accountType", // "accountType and name are required"
		},
		{
			name:       "115 without cookie",
			body:       CreateAccountRequest{AccountType: "115", Name: "nocookie"},
			wantStatus: http.StatusBadRequest,
			wantErrKey: "cookie",
		},
		{
			name:       "openlist missing account",
			body:       CreateAccountRequest{AccountType: "openlist", Name: "o", Password: "p", URL: "http://x"},
			wantStatus: http.StatusBadRequest,
			wantErrKey: "account",
		},
		{
			name:       "openlist missing password",
			body:       CreateAccountRequest{AccountType: "openlist", Name: "o", Account: "u", URL: "http://x"},
			wantStatus: http.StatusBadRequest,
			wantErrKey: "password",
		},
		{
			name:       "openlist missing url",
			body:       CreateAccountRequest{AccountType: "openlist", Name: "o", Account: "u", Password: "p"},
			wantStatus: http.StatusBadRequest,
			wantErrKey: "url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestAccountStore(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/account", jsonBody(t, tt.body))
			r.Header.Set("Content-Type", "application/json")
			CreateAccount(s).ServeHTTP(w, r)
			if w.Code != tt.wantStatus {
				t.Fatalf("status: got %d, want %d (body=%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			body := mustReadBody(t, w)
			errStr, _ := body["error"].(string)
			if tt.wantErrKey != "" && !contains(errStr, tt.wantErrKey) {
				t.Errorf("error %q missing key %q", errStr, tt.wantErrKey)
			}
		})
	}
}

func TestCreateAccount_DuplicateName(t *testing.T) {
	s := newTestAccountStore(t)
	valid := true
	_ = s.Upsert(&model.AccountInfo{Name: "exist", AccountType: "115", Cookie: "k=v", CookieValid: &valid})

	w := httptest.NewRecorder()
	body := CreateAccountRequest{AccountType: "115", Name: "exist", Cookie: "UID=2"}
	r := httptest.NewRequest(http.MethodPost, "/api/account", jsonBody(t, body))
	r.Header.Set("Content-Type", "application/json")
	CreateAccount(s).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	resp := mustReadBody(t, w)
	if !contains(resp["error"].(string), "already exists") {
		t.Errorf("dup error wrong: %v", resp["error"])
	}
}

func TestCreateAccount_Success115(t *testing.T) {
	s := newTestAccountStore(t)
	w := httptest.NewRecorder()
	body := CreateAccountRequest{AccountType: "115", Name: "newacc", Cookie: "UID=1; CID=2; SEID=abcdef1234567890"}
	r := httptest.NewRequest(http.MethodPost, "/api/account", jsonBody(t, body))
	r.Header.Set("Content-Type", "application/json")
	CreateAccount(s).ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	// store 应该有这个账号
	if !s.Has("newacc") {
		t.Error("account was not saved")
	}
	acc := s.Get("newacc")
	if acc.AccountType != "115" {
		t.Errorf("AccountType: got %s, want 115", acc.AccountType)
	}
	if acc.LastCookieCheck == 0 {
		t.Error("LastCookieCheck should be set after create")
	}
}

// ================================================================
// UpdateAccount (核心约束：originalName 查找)
// ================================================================

func TestUpdateAccount_OriginalNameLookup(t *testing.T) {
	s := newTestAccountStore(t)
	_ = s.Upsert(&model.AccountInfo{Name: "oldname", AccountType: "115", Cookie: "UID=old"})

	// originalName=oldname，修改 name → newname
	w := httptest.NewRecorder()
	req := UpdateAccountRequest{
		OriginalName: "oldname",
		Name:         "newname",
		AccountType:  "115",
		Cookie:       "UID=new",
	}
	r := httptest.NewRequest(http.MethodPut, "/api/account", jsonBody(t, req))
	r.Header.Set("Content-Type", "application/json")
	UpdateAccount(s).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	// 旧名应当不存在
	if s.Has("oldname") {
		t.Error("oldname should have been removed after rename")
	}
	// 新名应当存在
	if !s.Has("newname") {
		t.Error("newname should exist after rename")
	}
	if s.Get("newname").Cookie != "UID=new" {
		t.Errorf("cookie not updated: %s", s.Get("newname").Cookie)
	}
}

func TestUpdateAccount_WithoutOriginalName_FallsBackToName(t *testing.T) {
	s := newTestAccountStore(t)
	_ = s.Upsert(&model.AccountInfo{Name: "a", AccountType: "115", Cookie: "UID=1"})

	w := httptest.NewRecorder()
	// 不传 OriginalName，用新的 Name=a 查找（没改名场景正常更新）
	req := UpdateAccountRequest{Name: "a", AccountType: "115", Cookie: "UID=2"}
	r := httptest.NewRequest(http.MethodPut, "/api/account", jsonBody(t, req))
	r.Header.Set("Content-Type", "application/json")
	UpdateAccount(s).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if s.Get("a").Cookie != "UID=2" {
		t.Errorf("cookie not updated: %s", s.Get("a").Cookie)
	}
}

func TestUpdateAccount_NotFound(t *testing.T) {
	s := newTestAccountStore(t)
	w := httptest.NewRecorder()
	req := UpdateAccountRequest{Name: "nope", AccountType: "115", Cookie: "UID=1"}
	r := httptest.NewRequest(http.MethodPut, "/api/account", jsonBody(t, req))
	r.Header.Set("Content-Type", "application/json")
	UpdateAccount(s).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
}

func TestUpdateAccount_EmptyName(t *testing.T) {
	s := newTestAccountStore(t)
	w := httptest.NewRecorder()
	req := UpdateAccountRequest{Name: ""}
	r := httptest.NewRequest(http.MethodPut, "/api/account", jsonBody(t, req))
	r.Header.Set("Content-Type", "application/json")
	UpdateAccount(s).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

// ================================================================
// DeleteAccount
// ================================================================

func TestDeleteAccount_MissingName(t *testing.T) {
	s := newTestAccountStore(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/account", nil)
	DeleteAccount(s).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestDeleteAccount_NotExists(t *testing.T) {
	s := newTestAccountStore(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/account?name=nope", nil)
	DeleteAccount(s).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
}

func TestDeleteAccount_Success(t *testing.T) {
	s := newTestAccountStore(t)
	_ = s.Upsert(&model.AccountInfo{Name: "delme", AccountType: "115", Cookie: "UID=1"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/account?name=delme", nil)
	DeleteAccount(s).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}
	if s.Has("delme") {
		t.Error("account was not deleted")
	}
}

// ================================================================
// GetAccountStatus
// ================================================================

func TestGetAccountStatus_NamesFilter(t *testing.T) {
	s := newTestAccountStore(t)
	valid := true
	_ = s.Upsert(&model.AccountInfo{Name: "a1", AccountType: "115", Cookie: "UID=1", CookieValid: &valid})
	_ = s.Upsert(&model.AccountInfo{Name: "a2", AccountType: "115", Cookie: "UID=2", CookieValid: &valid})
	_ = s.Upsert(&model.AccountInfo{Name: "o1", AccountType: "openlist", URL: "http://x"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/account/status?names=a1,o1", nil)
	GetAccountStatus(s).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var resp struct {
		Results []AccountStatusInfo `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Results) != 2 {
		t.Fatalf("results len: got %d, want 2 (body=%s)", len(resp.Results), w.Body.String())
	}
	for _, r := range resp.Results {
		if r.Name != "a1" && r.Name != "o1" {
			t.Errorf("unexpected result name: %s", r.Name)
		}
		if r.Status == "" {
			t.Errorf("status empty for %s", r.Name)
		}
	}
}

func TestGetAccountStatus_All(t *testing.T) {
	s := newTestAccountStore(t)
	// 构造格式完整的 cookie（UID+CID+SEID+KID），ValidateCookie 才返回 valid=true
	_ = s.Upsert(&model.AccountInfo{Name: "a1", AccountType: "115", Cookie: "UID=1; CID=2; SEID=abcdef; KID=xyz"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/account/status", nil)
	GetAccountStatus(s).ServeHTTP(w, r)

	var resp struct {
		Results []AccountStatusInfo `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Results) != 1 {
		t.Fatalf("results len: got %d, want 1", len(resp.Results))
	}
	// a1 cookie 有效字段全，状态是 ok
	if resp.Results[0].Status != "ok" {
		t.Errorf("a1 status: got %s, want ok", resp.Results[0].Status)
	}
}

func TestGetAccountStatus_115NoCookie(t *testing.T) {
	s := newTestAccountStore(t)
	_ = s.Upsert(&model.AccountInfo{Name: "empty", AccountType: "115"}) // cookie 为空

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/account/status", nil)
	GetAccountStatus(s).ServeHTTP(w, r)

	var resp struct {
		Results []AccountStatusInfo `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Results[0].Status != "error" {
		t.Errorf("empty 115 cookie status: got %s, want error", resp.Results[0].Status)
	}
}

// ---------- tiny helper ----------

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
