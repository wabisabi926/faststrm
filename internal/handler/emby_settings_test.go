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

// ================================================================
// HandleSettingsPOST — 移出映射删除本地 STRM 开关
// 回归：显式 false 必须能关掉；旧前端未提交（nil）时必须保留旧值，不能被重置为默认 true
// ================================================================

func TestHandleSettingsPOST_MoveOutRemoveLocalStrm(t *testing.T) {
	ss := newTestSettingsStore(t)

	pre := model.DefaultSettings()
	pre.LifeMonitor.MoveOutRemoveLocalStrm = true
	if err := ss.SaveSettings(pre); err != nil {
		t.Fatalf("save pre settings: %v", err)
	}

	// 1) 显式提交 false → 必须关掉
	body := `{"lifeMonitor":{"moveOutRemoveLocalStrm":false}}`
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
	if got.LifeMonitor.MoveOutRemoveLocalStrm {
		t.Fatal("显式提交 false 后仍为 true")
	}

	// 2) 旧前端不提交该字段（nil）→ 保留旧值 false，不得被默认值覆盖
	body2 := `{"lifeMonitor":{"pollInterval":20}}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: ss}).ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", w2.Code, w2.Body.String())
	}
	got2, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got2.LifeMonitor.MoveOutRemoveLocalStrm {
		t.Fatal("未提交该字段时不应被重置为 true（应保留旧值 false）")
	}
}

// ================================================================
// HandleSettingsPOST — URL 编码开关 + STRM URL 模板
// 回归：关闭编码必须能保存；旧前端不提交时保留旧值
// ================================================================

func TestHandleSettingsPOST_EnablePathEncodingAndTemplate(t *testing.T) {
	ss := newTestSettingsStore(t)

	// 1) 显式关闭编码 + 设置模板
	body := `{"enablePathEncoding":false,"strm":{"strmUrlTemplate":"{prefix}/api/strm?account={account}&pickcode={pickcode}&file_name={file_name}&file_id={file_id}"}}`
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
	if got.EnablePathEncoding {
		t.Fatal("显式提交 enablePathEncoding=false 后仍为 true")
	}
	if got.Strm.StrmUrlTemplate == "" {
		t.Fatal("strmUrlTemplate 未保存")
	}

	// 2) 旧前端不提交这两个字段 → 保留旧值
	body2 := `{"strm":{"forceProxyUaTokens":["Infuse"]}}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: ss}).ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", w2.Code, w2.Body.String())
	}
	got2, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got2.EnablePathEncoding {
		t.Fatal("未提交 enablePathEncoding 时不应被重置为 true（应保留 false）")
	}
	if got2.Strm.StrmUrlTemplate != got.Strm.StrmUrlTemplate {
		t.Fatal("未提交 strmUrlTemplate 时不应被清空")
	}
}

func TestResolveEmbyTestCredentials_FallbackToSaved(t *testing.T) {
	ss := newTestSettingsStore(t)
	s := model.DefaultSettings()
	s.Emby.URL = "http://emby-saved:8096"
	s.Emby.APIKey = "real-saved-key"
	if err := ss.SaveSettings(s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	deps := EmbyDeps{SettingsStore: ss}

	url, key := resolveEmbyTestCredentials(deps, "", "abcd****wxyz")
	if url != "http://emby-saved:8096" || key != "real-saved-key" {
		t.Fatalf("masked key fallback failed: url=%q key=%q", url, key)
	}

	url, key = resolveEmbyTestCredentials(deps, "http://emby-new:8096", "new-key")
	if url != "http://emby-new:8096" || key != "new-key" {
		t.Fatalf("explicit credentials should not be overwritten: url=%q key=%q", url, key)
	}

	url, key = resolveEmbyTestCredentials(deps, "http://emby-new:8096", "***")
	if url != "http://emby-new:8096" || key != "real-saved-key" {
		t.Fatalf("masked key fallback should keep explicit URL: url=%q key=%q", url, key)
	}
}

func TestEmbyWebhookAuthorized(t *testing.T) {
	newReq := func(target string) *http.Request {
		return httptest.NewRequest(http.MethodPost, target, nil)
	}
	if !embyWebhookAuthorized(newReq("/api/emby/webhook"), "") {
		t.Fatal("empty expected should allow all")
	}
	if !embyWebhookAuthorized(newReq("/api/emby/webhook?auth=s3cret"), "s3cret") {
		t.Fatal("query auth should pass")
	}
	r := newReq("/api/emby/webhook")
	r.Header.Set("X-Webhook-Auth", "s3cret")
	if !embyWebhookAuthorized(r, "s3cret") {
		t.Fatal("X-Webhook-Auth should pass")
	}
	r = newReq("/api/emby/webhook")
	r.Header.Set("Authorization", "Bearer s3cret")
	if !embyWebhookAuthorized(r, "s3cret") {
		t.Fatal("Bearer token should pass")
	}
	if embyWebhookAuthorized(newReq("/api/emby/webhook?auth=wrong"), "s3cret") {
		t.Fatal("wrong auth must be rejected")
	}
}
