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
	s.MediaMountPath = []string{"/media/movies"}
	s.CleanupDefaultsV2 = true
	s.CleanupDefaultsV3 = true

	s.Download.DownloadMaxConcurrent = intPtrTest(2)
	s.Download.StrmMaxConcurrent = intPtrTest(9)
	s.Download.AutoDownloadMetadata = false
	s.Download.MinFileSize = 12345
	s.Download.StrmGenerateBlacklist = []string{"trailer"}
	s.Download.OverwriteMode = "never"
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
	s.Emby.SyncDeleteSource = true
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

func TestSettingsContract_StrmBoolPatchPreservesOmittedFields(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("contract_salt_32b_padding_________", dir)
	original := newContractSettings()
	original.Strm.DisableRangeProxy = true
	original.Strm.DisableSeekFormatProxy = true
	if err := settingsStore.SaveSettings(original); err != nil {
		t.Fatal(err)
	}
	deps := EmbyDeps{SettingsStore: settingsStore}

	// 只提交分片大小，两个 bool 开关未提供，应保留 true
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
	if !stored.Strm.DisableRangeProxy || !stored.Strm.DisableSeekFormatProxy {
		t.Fatalf("omitted bool fields should be preserved: range=%v seek=%v",
			stored.Strm.DisableRangeProxy, stored.Strm.DisableSeekFormatProxy)
	}
	if stored.Strm.ProxyChunkSizeMB != 16 {
		t.Fatalf("proxyChunkSizeMB=%d, want 16", stored.Strm.ProxyChunkSizeMB)
	}

	// 显式 false 必须覆盖
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings",
		bytes.NewReader([]byte(`{"strm":{"disableRangeProxy":false,"disableSeekFormatProxy":false}}`)))
	req.Header.Set("Content-Type", "application/json")
	HandleSettingsPOST(deps)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err = settingsStore.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Strm.DisableRangeProxy || stored.Strm.DisableSeekFormatProxy {
		t.Fatalf("explicit false should be applied: range=%v seek=%v",
			stored.Strm.DisableRangeProxy, stored.Strm.DisableSeekFormatProxy)
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
	// 空 UA 会由 SettingsStore 的默认值迁移补回默认 UA，而不是保留旧自定义值
	if stored.UserAgent == "custom-agent" {
		t.Fatalf("user-agent should not keep old custom value, got %q", stored.UserAgent)
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
