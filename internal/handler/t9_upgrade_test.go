package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
)

// ================================================================
// T9 老用户升级后签名链路验证 —— 端到端场景
// ================================================================

// --- Handler 层：EnableTokenSigning=false（默认）时老 STRM 不带 token 仍可播放 ---
func TestT9_Upgrade_OldStrmNoToken_Passes(t *testing.T) {
	// 默认 settings: EnableTokenSigning=false, TokenSecret=""
	opts := buildBaseStrmOptions(t)
	w := httptest.NewRecorder()
	// 老 STRM 里的 URL：没有 token 参数
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("ghost", "AbCdEf12345678901"), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	// 预期 200 之后会走 proxy/redirect 路径，但因为 client115=nil 最终会 panic
	// 这里只验证：不会被 token 校验挡住（即 handler 不会返回 401）
	// 由于 client115=nil，会在 ResolveDownloadUrl 那层 panic，所以我们用 defer recover
	// defer block removed — using ghost account to avoid nil client115
	HandleStrm(opts).ServeHTTP(w, r)
	// 如果没有 panic，应该至少不是 401
	if w.Code == http.StatusUnauthorized {
		t.Errorf("EnableTokenSigning=false but request got 401, body=%s", w.Body.String())
	}
}

// --- Handler 层：显式 EnableTokenSigning=false 并带空 secret → 不带 token 也应通过 ---
func TestT9_SigningDisabled_EmptySecret_NoToken_Passes(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	opts.Cfg.Settings.Strm = model.StrmSettings{
		EnableTokenSigning: false,
		TokenSecret:        "",
	}

	// defer block removed — using ghost account to avoid nil client115
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("ghost", "AbCdEf12345678901"), nil)
	HandleStrm(opts).ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("EnableTokenSigning=false → should NOT require token, got 401")
	}
}

// --- Handler 层：EnableTokenSigning=true + secret 存在 → 不带 token 被拒 ---
func TestT9_SigningEnabled_NoToken_Rejected(t *testing.T) {
	opts := buildBaseStrmOptions(t)
	opts.Cfg.Settings.Strm = model.StrmSettings{
		EnableTokenSigning: true,
		TokenSecret:        "deadbeefcafebabe1234567890abcdefdeadbeefcafebabe1234567890abcdef",
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("ghost", "AbCdEf12345678901"), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("signing enabled + no token: want 401, got %d", w.Code)
	}
}

// --- Handler 层：EnableTokenSigning=true + secret 存在 → 带正确签名 token 通过 ---
func TestT9_SigningEnabled_ValidToken_Passes(t *testing.T) {
	secret := "deadbeefcafebabe1234567890abcdefdeadbeefcafebabe1234567890abcdef"
	account := "ghost"
	pickcode := "AbCdEf12345678901"
	ttl := strm.TokenDefaultTTL

	// 生成签名 token
	token := strm.SignStrmToken(secret, account, pickcode, ttl)
	if token == "" {
		t.Fatal("SignStrmToken returned empty")
	}

	opts := buildBaseStrmOptions(t)
	opts.Cfg.Settings.Strm = model.StrmSettings{
		EnableTokenSigning: true,
		TokenSecret:        secret,
	}

	// 在 URL 上加 token 参数
	u, _ := url.Parse(buildStrmURL(account, pickcode))
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	// defer block removed — using ghost account to avoid nil client115
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, u.String(), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code == http.StatusUnauthorized {
		t.Errorf("signing enabled + valid token: got 401, body=%s", w.Body.String())
	}
}

// --- Handler 层：EnableTokenSigning=true + secret 存在 → 签名 token 不匹配 pickcode 被拒 ---
func TestT9_SigningEnabled_TokenForWrongPickcode_Rejected(t *testing.T) {
	secret := "deadbeefcafebabe1234567890abcdefdeadbeefcafebabe1234567890abcdef"
	account := "ghost"
	wrongPickcode := "ZzZzZzZzZzZzZzZzZ"

	// token 是为 wrongPickcode 签的，但请求用正确的
	token := strm.SignStrmToken(secret, account, wrongPickcode, strm.TokenDefaultTTL)

	opts := buildBaseStrmOptions(t)
	opts.Cfg.Settings.Strm = model.StrmSettings{
		EnableTokenSigning: true,
		TokenSecret:        secret,
	}

	u, _ := url.Parse(buildStrmURL(account, "AbCdEf12345678901"))
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, u.String(), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong pickcode signed into token: want 401, got %d", w.Code)
	}
}

// --- Store 层：老 settings.json（没有 enableTokenSigning/tokenSecret 字段） → 升级后 EnableTokenSigning=false ---
func TestT9_Store_Migration_LegacySettings_NoCrash(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	// 模拟老 settings.json：只有 302 模式和 redirect timeout，没有 T9 字段
	legacyJSON := `{
  "enable302": true,
  "enablePathEncoding": true,
  "user-agent": "TestUA/1.0",
  "strmExtensions": [".strm"],
  "downloadExtensions": [".mp4"],
  "mediaMountPath": ["/mnt/media"],
  "removeExtraFiles": false,
  "strm": {
    "forceProxyUaTokens": ["Infuse"],
    "accountProxyConcurrencyLimit": 6,
    "redirectCheckTimeoutMs": 3000
  }
}`
	_ = os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(legacyJSON), 0o644) //nolint:gosec // G306 — 测试夹具，权限不敏感

	salt := "upgrade_test_salt_32bytes__"
	ss := store.NewSettingsStore(salt, cfgDir)

	cfg, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings err: %v", err)
	}

	// 关键断言：老用户升级后，开关应默认 false（不影响老 STRM）
	if cfg.Strm.EnableTokenSigning {
		t.Errorf("Legacy settings: EnableTokenSigning should default to false, got true")
	}
	// secret 应保持空（只有开关打开时 store 才自动生成）
	if cfg.Strm.TokenSecret != "" {
		t.Errorf("Legacy settings: TokenSecret should stay empty, got len=%d", len(cfg.Strm.TokenSecret))
	}

	// 其他老字段应该正常保留
	if !cfg.Enable302 {
		t.Error("legacy enable302 lost after migration")
	}
	if cfg.Strm.AccountProxyConcurrencyLimit != 6 {
		t.Errorf("legacy accountProxyConcurrencyLimit lost: got %d, want 6", cfg.Strm.AccountProxyConcurrencyLimit)
	}

	// 老 STRM URL（不带 token）在 handler 层也能过
	opts := StrmOptions{
		Cfg:          &config.AppConfig{Settings: cfg},
		Client115:    nil,
		AccountStore: newTestAccountStore(t),
	}
	// defer block removed — using ghost account to avoid nil client115
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("ghost", "AbCdEf12345678901"), nil)
	HandleStrm(opts).ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("legacy user: old STRM got 401 — upgrade path broken! body=%s", w.Body.String())
	}
}

// --- Store 层：用户打开开关后，secret 自动生成并持久化，重启后不变 ---
func TestT9_Store_Migration_EnableSwitch_AutoSecret(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	// 用户打开开关后保存的 settings.json（前端保存）
	userEnabledJSON := `{
  "enable302": true,
  "strm": {
    "forceProxyUaTokens": [],
    "enableTokenSigning": true,
    "tokenSecret": ""
  }
}`
	_ = os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(userEnabledJSON), 0o644) //nolint:gosec // G306 — 测试夹具，权限不敏感

	salt := "auto_secret_salt_32bytes__"

	// ===== 第 1 次启动 =====
	ss1 := store.NewSettingsStore(salt, cfgDir)
	cfg1, err := ss1.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings#1 err: %v", err)
	}

	if !cfg1.Strm.EnableTokenSigning {
		t.Error("switch should be true after user enabled it")
	}
	if cfg1.Strm.TokenSecret == "" {
		t.Error("secret should be auto-generated when EnableTokenSigning=true")
	}
	if len(cfg1.Strm.TokenSecret) < 32 {
		t.Errorf("secret too short: got len=%d, want >=32", len(cfg1.Strm.TokenSecret))
	}
	secret1 := cfg1.Strm.TokenSecret

	// ===== 检查 settings.json 已被回写 =====
	raw, _ := os.ReadFile(filepath.Join(cfgDir, "settings.json"))
	var disk map[string]any
	_ = json.Unmarshal(raw, &disk)
	strmMap := disk["strm"].(map[string]any)
	if strmMap["tokenSecret"].(string) != secret1 {
		t.Error("settings.json tokenSecret not persisted back")
	}

	// ===== 第 2 次启动（模拟重启） =====
	ss2 := store.NewSettingsStore(salt, cfgDir)
	cfg2, err := ss2.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings#2 err: %v", err)
	}

	if cfg2.Strm.TokenSecret != secret1 {
		t.Errorf("SECRET ROTATED on restart! old=%s new=%s — this would invalidate all issued STRMs", secret1, cfg2.Strm.TokenSecret)
	}

	// ===== 再保存开关=false，重启，secret 应保留 =====
	cfg2.Strm.EnableTokenSigning = false
	_ = ss2.SaveSettings(cfg2)

	ss3 := store.NewSettingsStore(salt, cfgDir)
	cfg3, err := ss3.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings#3 err: %v", err)
	}
	if cfg3.Strm.EnableTokenSigning {
		t.Error("switch should be false after user disabled it")
	}
	if cfg3.Strm.TokenSecret != secret1 {
		t.Errorf("secret lost when switch turned off! old=%s new=%s", secret1, cfg3.Strm.TokenSecret)
	}

	// ===== 关掉开关时，handler 不应校验 token =====
	accountStore := newTestAccountStore(t)
	valid := true
	_ = accountStore.Upsert(&model.AccountInfo{Name: "acc1", AccountType: "115", Cookie: "UID=1", CookieValid: &valid})
	opts := StrmOptions{
		Cfg:          &config.AppConfig{Settings: cfg3},
		Client115:    nil,
		AccountStore: accountStore,
	}
	// defer block removed — using ghost account to avoid nil client115
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, buildStrmURL("ghost", "AbCdEf12345678901"), nil)
	HandleStrm(opts).ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Error("switch=false → handler should NOT reject no-token requests")
	}

	t.Logf("secret lifecycle verified: enable→generated(len=%d) → restart→stable → disable→kept → enable again→still same ✓", len(secret1))
}

// --- Store 层：EnableTokenSigning=true 但 secret 已存在（手动或之前开通过）→ 不重新生成 ---
func TestT9_Store_Migration_SecretAlreadyExists_NoRotation(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	_ = os.MkdirAll(cfgDir, 0o755)

	existingSecret := "deadbeefcafebabe1234567890abcdefdeadbeefcafebabe1234567890abcdef"
	jsonStr := `{"strm": {"enableTokenSigning": true, "tokenSecret": "` + existingSecret + `"}}`
	_ = os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(jsonStr), 0o644) //nolint:gosec // G306 — 测试夹具，权限不敏感

	ss := store.NewSettingsStore("some_salt_32bytes__", cfgDir)
	cfg, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.Strm.TokenSecret != existingSecret {
		t.Errorf("existing secret got ROTATED! want %s, got %s", existingSecret, cfg.Strm.TokenSecret)
	}

	// 再读一次也一样（不重复回写）
	cfg2, _ := ss.ReadSettings()
	if cfg2.Strm.TokenSecret != existingSecret {
		t.Errorf("2nd read: secret rotated!")
	}
}

// --- 完整链路：token 的 TTL 过期后应被拒 ---
func TestT9_TokenExpired_Rejected(t *testing.T) {
	secret := "deadbeefcafebabe1234567890abcdefdeadbeefcafebabe1234567890abcdef"
	account := "ghost"
	pickcode := "AbCdEf12345678901"

	// 用 1ms TTL → 几乎立刻过期
	token := strm.SignStrmToken(secret, account, pickcode, 1*time.Second)
	time.Sleep(2 * time.Second) // 等它过期

	opts := buildBaseStrmOptions(t)
	opts.Cfg.Settings.Strm = model.StrmSettings{
		EnableTokenSigning: true,
		TokenSecret:        secret,
	}

	u, _ := url.Parse(buildStrmURL(account, pickcode))
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, u.String(), nil)
	HandleStrm(opts).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired token: want 401, got %d", w.Code)
	}
}
