package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

// newTestSettingsStore 创建基于临时目录的 SettingsStore
func newTestSettingsStore(t *testing.T) *store.SettingsStore {
	t.Helper()
	return store.NewSettingsStore("test-salt-1234", t.TempDir())
}

// ================================================================
// HandleSettingsPOST — STRM 强制代理 UA 标识保存
// 回归：清空（发送空数组）必须能覆盖旧值，而不是被 len>0 忽略
// ================================================================

func TestHandleSettingsPOST_ClearForceProxyUaTokens(t *testing.T) {
	ss := newTestSettingsStore(t)

	// 预置：settings 里已存在 UA tokens
	pre := model.DefaultSettings()
	pre.Strm.ForceProxyUaTokens = []string{"Infuse", "VidHub", "SenPlayer", "SenPlayerHD"}
	if err := ss.SaveSettings(pre); err != nil {
		t.Fatalf("save pre settings: %v", err)
	}

	// 前端清空输入框后提交：strm.forceProxyUaTokens = []
	body := `{"strm":{"forceProxyUaTokens":[]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: ss}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// 验证：保存后 tokens 应为空，而不是残留旧值
	got, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if len(got.Strm.ForceProxyUaTokens) != 0 {
		t.Fatalf("ForceProxyUaTokens should be cleared, got %v", got.Strm.ForceProxyUaTokens)
	}
}

func TestHandleSettingsPOST_UpdateForceProxyUaTokens(t *testing.T) {
	ss := newTestSettingsStore(t)

	pre := model.DefaultSettings()
	pre.Strm.ForceProxyUaTokens = []string{"Infuse"}
	if err := ss.SaveSettings(pre); err != nil {
		t.Fatalf("save pre settings: %v", err)
	}

	body := `{"strm":{"forceProxyUaTokens":["VidHub","SenPlayer"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: ss}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", w.Code, w.Body.String())
	}

	got, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	want := []string{"VidHub", "SenPlayer"}
	if len(got.Strm.ForceProxyUaTokens) != len(want) {
		t.Fatalf("ForceProxyUaTokens: got %v, want %v", got.Strm.ForceProxyUaTokens, want)
	}
	for i := range want {
		if got.Strm.ForceProxyUaTokens[i] != want[i] {
			t.Fatalf("ForceProxyUaTokens[%d]: got %q, want %q", i, got.Strm.ForceProxyUaTokens[i], want[i])
		}
	}
}

func TestHandleSettingsPOST_NoStrmKeepsOldTokens(t *testing.T) {
	ss := newTestSettingsStore(t)

	pre := model.DefaultSettings()
	pre.Strm.ForceProxyUaTokens = []string{"Infuse", "VidHub"}
	if err := ss.SaveSettings(pre); err != nil {
		t.Fatalf("save pre settings: %v", err)
	}

	// 完全不提交 strm 字段 → 应保留旧值
	body := `{"user-agent":"test"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: ss}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", w.Code, w.Body.String())
	}

	got, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if len(got.Strm.ForceProxyUaTokens) != 2 {
		t.Fatalf("ForceProxyUaTokens should be preserved, got %v", got.Strm.ForceProxyUaTokens)
	}
}

// 附带确认：StrmSettings 的 JSON tag 能正确序列化 forceProxyUaTokens
func TestStrmSettings_JSONTag(t *testing.T) {
	s := model.StrmSettings{ForceProxyUaTokens: []string{"A", "B"}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"forceProxyUaTokens"`) {
		t.Fatalf("json tag missing forceProxyUaTokens: %s", string(b))
	}
}
