package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

func intPtrTest(v int) *int    { return &v }
func boolPtrTest(v bool) *bool { return &v }

func newContractSettings() *model.Settings {
	s := model.DefaultSettings()
	s.UserAgent = "contract-agent/1.0"
	s.InternalToken = "internal-secret"
	s.StrmPrefix = "http://nas:8090"
	s.EnablePathEncoding = false
	s.StrmExtensions = []string{"mkv", "mp4", "iso"}
	s.DownloadExtensions = []string{"srt", "nfo"}
	s.CleanupDefaultsV2 = true
	s.CleanupDefaultsV3 = true

	s.Download.DownloadMaxConcurrent = intPtrTest(2)
	s.Download.StrmMaxConcurrent = intPtrTest(9)
	s.Download.AutoDownloadMetadata = false
	s.Download.MinFileSize = 12345
	s.Download.StrmGenerateBlacklist = []string{"trailer"}
	s.Download.FullSyncCleanupOrphans = "mark_only"

	s.Strm.StrmUrlTemplate = "http://nas:8090/api/strm?account={account}&pickcode={pickcode}&file_name={file_name}"
	s.Strm.StrmFilenameTemplate = "[{account}] {stem}.strm"
	s.Strm.ForceProxyUaTokens = []string{"Infuse"}
	s.Strm.AccountProxyConcurrencyLimit = 7
	s.Strm.RedirectCheckTimeoutMs = 4000
	s.Strm.ForceProxyFormats = []string{"iso", "m2ts"}

	s.Emby.URL = "http://emby:8096"
	s.Emby.APIKey = "emby-secret-key"
	s.Emby.NotifyMediaAdded = false
	s.Emby.SyncDeleteEnabled = true
	s.Emby.SyncDeleteNotify = true
	s.Emby.SyncDeleteDryRun = true
	s.Emby.SyncDeleteDeleteSymlink = true
	s.Emby.SyncDeleteRemoveVersions = true
	s.Emby.SyncDeletePathMappings = []model.SyncDeletePathMapping{
		{EmbyPath: "/mnt/emby", CloudPath: "/电影", Account: "alice"},
	}
	s.Emby.RefreshOnCreate = false
	s.Emby.RefreshOnDelete = false
	s.Emby.DebounceSeconds = 20
	s.Emby.ProxyPort = 8097
	s.Emby.ExternalPlayerEnabled = true
	s.Emby.WebhookAuth = "wh-auth"
	s.Emby.LibraryID = "lib-1"

	s.Telegram.BotToken = "123456:secret-token"
	s.Telegram.ChatID = "42"
	s.Telegram.WebhookURL = "http://tg-hook"
	s.Telegram.ProxyURL = "http://proxy:1080"
	s.Telegram.WebhookSecretToken = "hook-secret"
	s.Telegram.Enabled = true
	s.Telegram.AutoPolling = false
	s.Telegram.AllowedUsers = []int64{1, 2}
	s.Telegram.AccountAlerts = &model.AccountAlertsSettings{Enabled: true, OnError: true, OnRecover: false}

	s.LifeMonitor.Enabled = true
	s.LifeMonitor.Accounts = []string{"alice"}
	s.LifeMonitor.PollInterval = 15
	s.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: "alice", CloudPath: "/电影", LocalPath: "/media/movies"},
	}
	s.LifeMonitor.EventTypes = model.EventTypesSettings{Create: true, Remove: false, Rename: true, Move: false}
	s.LifeMonitor.MinFileSize = 100
	s.LifeMonitor.FirstPullMode = "last"
	s.LifeMonitor.MoveMediaMode = "recreate"
	s.LifeMonitor.MoveOutRemoveLocalStrm = false
	s.LifeMonitor.NotifyOnlyOnError = true

	s.Cleanup.MaxThreshold = 900
	s.Cleanup.MaxRatio = 0.2
	s.Cleanup.MaxDirs = 10
	s.Cleanup.StableThreshold = 3
	s.Cleanup.ConfirmMode = "plugin_ui"
	s.Cleanup.RemoveStrm = true
	s.Cleanup.RemoveRelatedFiles = true
	s.Cleanup.RemoveEmptyDirs = true

	s.RateLimit = model.RateLimitSettings{API115Qpm: 70, PlaybackQpm: 130, LifeQpm: 50, GlobalCapQpm: 400}
	s.CookieInspect = model.CookieInspectSettings{Enabled: boolPtrTest(false), IntervalHours: 12}
	return s
}

func TestSettingsContract_GetPostGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	if err := settingsStore.SaveSettings(newContractSettings()); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	get := func() map[string]any {
		rec := httptest.NewRecorder()
		HandleSettingsGET(deps)(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := get()
	firstJSON, _ := json.Marshal(first)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(firstJSON))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}

	second := get()
	if !reflect.DeepEqual(first, second) {
		a, _ := json.MarshalIndent(first, "", "  ")
		b, _ := json.MarshalIndent(second, "", "  ")
		t.Fatalf("GET→POST→GET changed settings\nfirst=%s\nsecond=%s", a, b)
	}

	// 脱敏字段不能被掩码覆盖
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Emby.APIKey != "emby-secret-key" {
		t.Fatalf("emby apiKey lost: %q", stored.Emby.APIKey)
	}
	if stored.Telegram.BotToken != "123456:secret-token" {
		t.Fatalf("telegram bot token lost: %q", stored.Telegram.BotToken)
	}
	if stored.Telegram.WebhookSecretToken != "hook-secret" {
		t.Fatalf("webhook secret lost: %q", stored.Telegram.WebhookSecretToken)
	}
	if stored.InternalToken != "internal-secret" {
		t.Fatalf("internal token lost: %q", stored.InternalToken)
	}
}

func TestSettingsContract_PostDoesNotOverwriteEmbyTelegram(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	original := newContractSettings()
	if err := settingsStore.SaveSettings(original); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	body := []byte(`{
		"emby":{"url":"http://evil-emby:8096","apiKey":"evil-key","proxyPort":9999},
		"telegram":{"botToken":"evil-token","chatId":"999","enabled":false,"autoPolling":true}
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}

	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Emby.URL != original.Emby.URL || stored.Emby.APIKey != original.Emby.APIKey || stored.Emby.ProxyPort != original.Emby.ProxyPort {
		t.Fatalf("generic settings POST overwrote Emby config: %+v", stored.Emby)
	}
	if stored.Telegram.BotToken != original.Telegram.BotToken ||
		stored.Telegram.ChatID != original.Telegram.ChatID ||
		stored.Telegram.Enabled != original.Telegram.Enabled ||
		stored.Telegram.AutoPolling != original.Telegram.AutoPolling {
		t.Fatalf("generic settings POST overwrote Telegram config: %+v", stored.Telegram)
	}
}

func TestSettingsContract_StrmAdvancedFieldsWritable(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	if err := settingsStore.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}
	body := []byte(`{"strm":{"strmFilenameTemplate":"{stem}.iso.strm","forceProxyFormats":["iso"]},"download":{"strmMaxConcurrent":12}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Strm.StrmFilenameTemplate != "{stem}.iso.strm" {
		t.Fatalf("strmFilenameTemplate not applied: %q", stored.Strm.StrmFilenameTemplate)
	}
	if !reflect.DeepEqual(stored.Strm.ForceProxyFormats, []string{"iso"}) {
		t.Fatalf("forceProxyFormats not applied: %v", stored.Strm.ForceProxyFormats)
	}
	if stored.Download.StrmMaxConcurrent == nil || *stored.Download.StrmMaxConcurrent != 12 {
		t.Fatalf("strmMaxConcurrent not applied: %v", stored.Download.StrmMaxConcurrent)
	}
}

func TestSettingsContract_PartialDownloadPatchPreservesMinFileSize(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	original := newContractSettings()
	original.Download.MinFileSize = 12345
	if err := settingsStore.SaveSettings(original); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings",
		bytes.NewReader([]byte(`{"download":{"fullSyncCleanupOrphans":"auto_clean"}}`)))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Download.FullSyncCleanupOrphans != "auto_clean" {
		t.Fatalf("cleanup mode = %q, want auto_clean", stored.Download.FullSyncCleanupOrphans)
	}
	if stored.Download.MinFileSize != 12345 {
		t.Fatalf("omitted minFileSize was reset to %d", stored.Download.MinFileSize)
	}
}

func TestSettingsContract_PartialPostPreservesOmittedFields(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	original := newContractSettings()
	if err := settingsStore.SaveSettings(original); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(`{"user-agent":"new-agent"}`)))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.UserAgent != "new-agent" {
		t.Fatalf("user-agent not updated: %q", stored.UserAgent)
	}
	if stored.StrmPrefix != original.StrmPrefix {
		t.Fatalf("omitted strmPrefix lost: %q", stored.StrmPrefix)
	}
	if stored.Strm.StrmFilenameTemplate != original.Strm.StrmFilenameTemplate {
		t.Fatalf("omitted filename template lost: %q", stored.Strm.StrmFilenameTemplate)
	}
	if stored.Emby.ProxyPort != original.Emby.ProxyPort {
		t.Fatalf("omitted emby proxyPort lost: %d", stored.Emby.ProxyPort)
	}
}

func TestSettingsContract_CanClearStrmTemplatesAndFormats(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	original := newContractSettings()
	original.Strm.StrmUrlTemplate = "http://old/api/strm?pickcode={pickcode}"
	original.Strm.StrmFilenameTemplate = "{stem}.old.strm"
	original.Strm.ForceProxyFormats = []string{"iso"}
	if err := settingsStore.SaveSettings(original); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	body := []byte(`{"strm":{"strmUrlTemplate":"","strmFilenameTemplate":"","forceProxyFormats":[]}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Strm.StrmUrlTemplate != "" || stored.Strm.StrmFilenameTemplate != "" {
		t.Fatalf("templates should be cleared: url=%q name=%q",
			stored.Strm.StrmUrlTemplate, stored.Strm.StrmFilenameTemplate)
	}
	if len(stored.Strm.ForceProxyFormats) != 0 {
		t.Fatalf("forceProxyFormats should be empty (proxy off), got %v", stored.Strm.ForceProxyFormats)
	}
}

func TestSettingsContract_StrmBoolPatchPreservesOmittedFields(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	original := newContractSettings()
	original.Strm.DisableRangeProxy = true
	if err := settingsStore.SaveSettings(original); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	// 只提交分片大小，range 开关未提供，应保留 true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(`{"strm":{"proxyChunkSizeMB":16}}`)))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("partial POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Strm.DisableRangeProxy {
		t.Fatal("omitted disableRangeProxy should be preserved")
	}
	if stored.Strm.ProxyChunkSizeMB != 16 {
		t.Fatalf("proxyChunkSizeMB=%d, want 16", stored.Strm.ProxyChunkSizeMB)
	}

	// 显式 false 必须覆盖
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings",
		bytes.NewReader([]byte(`{"strm":{"disableRangeProxy":false}}`)))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err = settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Strm.DisableRangeProxy {
		t.Fatal("explicit false should be applied")
	}
}

func TestSettingsContract_CanClearOptionalStringsAndMinFileSize(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	pre := model.DefaultSettings()
	pre.UserAgent = "custom-agent"
	pre.StrmPrefix = "http://old-prefix:8090"
	pre.LifeMonitor.MinFileSize = 12345
	if err := settingsStore.SaveSettings(pre); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"user-agent":"","strmPrefix":"","lifeMonitor":{"minFileSize":0}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: settingsStore})(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}

	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.StrmPrefix != "" {
		t.Fatalf("strmPrefix should be cleared, got %q", stored.StrmPrefix)
	}
	if stored.LifeMonitor.MinFileSize != 0 {
		t.Fatalf("minFileSize should be cleared to 0, got %d", stored.LifeMonitor.MinFileSize)
	}
	// 显式清空必须保持为空，运行时由 EffectiveUserAgent 兜底默认 UA。
	if stored.UserAgent != "" {
		t.Fatalf("user-agent should be cleared, got %q", stored.UserAgent)
	}
}

func TestSettingsContract_CleanupPatch(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	if err := settingsStore.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	body := []byte(`{"cleanup":{"maxThreshold":50,"maxRatio":0.2,"maxDirs":10,"stableThreshold":3,"confirmMode":"plugin_ui","removeRelatedFiles":true}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Cleanup.MaxThreshold != 50 || stored.Cleanup.MaxRatio != 0.2 || stored.Cleanup.MaxDirs != 10 ||
		stored.Cleanup.StableThreshold != 3 || stored.Cleanup.ConfirmMode != "plugin_ui" || !stored.Cleanup.RemoveRelatedFiles {
		t.Fatalf("cleanup patch not applied: %+v", stored.Cleanup)
	}

	invalid := []byte(`{"cleanup":{"confirmMode":"unknown"}}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(invalid))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid confirmMode status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsContract_RateLimitPatch(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	if err := settingsStore.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"rateLimit":{"api115Qpm":70,"playbackQpm":130,"lifeQpm":50,"globalCapQpm":400}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(EmbyDeps{SettingsStore: settingsStore})(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	want := model.RateLimitSettings{API115Qpm: 70, PlaybackQpm: 130, LifeQpm: 50, GlobalCapQpm: 400}
	if stored.RateLimit != want {
		t.Fatalf("rateLimit = %+v, want %+v", stored.RateLimit, want)
	}
}

// TestSettingsContract_EmptyValuesKeptAsEmpty 校验留空语义：
//   - User-Agent / STRM 扩展名：留空 → 保持空，运行时由 Effective* 兜底
//   - 元数据扩展名：留空 → 保持空，表示不下载附带文件
//   - 强制代理 UA 标识：留空 → 保持为空（= 全部直连）
func TestSettingsContract_EmptyValuesKeptAsEmpty(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	pre := model.DefaultSettings()
	pre.UserAgent = "custom-agent"
	pre.StrmExtensions = []string{"mkv"}
	pre.DownloadExtensions = []string{"srt"}
	pre.Strm.ForceProxyUaTokens = []string{"Infuse"}
	if err := settingsStore.SaveSettings(pre); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	body := []byte(`{"user-agent":"","strmExtensions":[],"downloadExtensions":[],"strm":{"forceProxyUaTokens":[]}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}

	stored, err := settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.UserAgent != "" {
		t.Fatalf("空 UA 应保持为空，got %q", stored.UserAgent)
	}
	if stored.StrmExtensions == nil || len(stored.StrmExtensions) != 0 {
		t.Fatalf("空 STRM 扩展名应保持为空，got %#v", stored.StrmExtensions)
	}
	if len(stored.DownloadExtensions) != 0 {
		t.Fatalf("空元数据扩展名应表示不下载，got %v", stored.DownloadExtensions)
	}
	if stored.Download.AutoDownloadMetadata {
		t.Fatal("空元数据扩展名时 autoDownloadMetadata 应为 false")
	}
	// 强制代理 UA 的空值有语义（全部直连），必须保持为空
	if len(stored.Strm.ForceProxyUaTokens) != 0 {
		t.Fatalf("空强制代理 UA 应保持为空，got %v", stored.Strm.ForceProxyUaTokens)
	}

	// saved 也必须回传用户提交的空值，前端不再做默认值回填。
	var resp struct {
		Saved struct {
			UserAgent          string   `json:"user-agent"`
			StrmExtensions     []string `json:"strmExtensions"`
			DownloadExtensions []string `json:"downloadExtensions"`
		} `json:"saved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Saved.UserAgent != "" {
		t.Fatalf("saved.user-agent should stay empty, got %q", resp.Saved.UserAgent)
	}
	if len(resp.Saved.StrmExtensions) != 0 {
		t.Fatalf("saved.strmExtensions should stay empty, got %v", resp.Saved.StrmExtensions)
	}
	if len(resp.Saved.DownloadExtensions) != 0 {
		t.Fatalf("saved.downloadExtensions 应保持空，got %v", resp.Saved.DownloadExtensions)
	}
}
