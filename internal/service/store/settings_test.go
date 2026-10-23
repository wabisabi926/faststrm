package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
)

// writeSettings 写入指定 JSON 内容到 config/settings.json
func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

// TestReadSettings_MigratesMissingEventTypes
// P0-2：旧配置 lifeMonitor 里缺 eventTypes 字段 → 反序列化后 4 个 bool 全 false，
// 会导致所有生活事件按 event_type_disabled_* 静默跳过（问题①"有些人正常有些人不行"）。
// 迁移必须补成默认全开；而字段确实存在时（哪怕用户刻意全关）必须尊重用户值。
func TestReadSettings_MigratesMissingEventTypes(t *testing.T) {
	// 场景 1：lifeMonitor 存在但没有 eventTypes → 补默认全开
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"lifeMonitor":{"pollInterval":10}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	et := s1.LifeMonitor.EventTypes
	if !et.Create || !et.Remove || !et.Rename || !et.Move {
		t.Errorf("缺 eventTypes 应迁移为默认全开, got create=%v remove=%v rename=%v move=%v",
			et.Create, et.Remove, et.Rename, et.Move)
	}

	// 场景 2：字段存在且用户全关 → 不得被覆盖
	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"lifeMonitor":{"eventTypes":{"create":false,"remove":false,"rename":false,"move":false}}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	et2 := s2.LifeMonitor.EventTypes
	if et2.Create || et2.Remove || et2.Rename || et2.Move {
		t.Errorf("用户显式全关不得被覆盖, got create=%v remove=%v rename=%v move=%v",
			et2.Create, et2.Remove, et2.Rename, et2.Move)
	}
}

// TestReadSettings_MigratesCleanupDefaults 清理默认值迁移（一次性）：
// 老配置降为 mark_only（auto_clean 需用户重新确认）；显式 off 不得覆盖。
func TestReadSettings_MigratesCleanupDefaults(t *testing.T) {
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"download":{"minFileSize":0}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got := s1.Download.FullSyncCleanupOrphans; got != "mark_only" {
		t.Errorf("老配置缺字段应迁移为 mark_only, got %q", got)
	}

	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"download":{"fullSyncCleanupOrphans":"off"}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got := s2.Download.FullSyncCleanupOrphans; got != "off" {
		t.Errorf("用户显式关闭不应被覆盖, got %q", got)
	}
}

// 老配置显式 auto_clean 也降为 mark_only：v1.5 的默认值无法与用户选择区分，
// 审批后的安全口径是升级后先只标记。
func TestReadSettings_MigratesAutoCleanToMarkOnly(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"download":{"fullSyncCleanupOrphans":"auto_clean"}}`)
	s, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got := s.Download.FullSyncCleanupOrphans; got != "mark_only" {
		t.Errorf("老配置 auto_clean 应降为 mark_only, got %q", got)
	}
}

// TestReadSettings_FailClosedOnCorruptJSON 损坏配置不得被当成默认值继续写回。
func TestReadSettings_FailClosedOnCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"forceProxyFormats":[`)
	s := NewSettingsStore("test_salt", dir)
	if _, err := s.ReadSettings(); err == nil {
		t.Fatal("corrupt JSON should return error")
	}
	if _, err := s.Update(func(cfg *model.Settings) error {
		cfg.StrmPrefix = "http://should-not-save"
		return nil
	}); err == nil {
		t.Fatal("Update must fail closed on corrupt config")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"forceProxyFormats":[`) {
		t.Fatalf("corrupt file must remain untouched, got %s", raw)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "settings.json.corrupt.*"))
	if len(matches) == 0 {
		t.Fatal("corrupt config should be backed up")
	}
}

// TestReadSettings_MigratesMissingPathEncoding 旧配置缺 enablePathEncoding 时补 true。
func TestReadSettings_MigratesMissingPathEncoding(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strmPrefix":"http://192.168.1.10:8090"}`)
	s, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if !s.EnablePathEncoding {
		t.Fatal("missing enablePathEncoding should default true")
	}
}

// TestReadSettings_RemovesLegacyTemplates A1：老模板备份后清空。
func TestReadSettings_RemovesLegacyTemplates(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"strmUrlTemplate":"{prefix}/x?pc={pickcode}","strmFilenameTemplate":"[a]{stem}.strm"}}`)
	s, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if s.Strm.StrmUrlTemplate != "" || s.Strm.StrmFilenameTemplate != "" {
		t.Fatalf("legacy templates should be cleared, got %q / %q", s.Strm.StrmUrlTemplate, s.Strm.StrmFilenameTemplate)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "settings.json.templates-v1.*"))
	if len(matches) == 0 {
		t.Fatal("legacy template config should be backed up")
	}
}

// TestReadSettings_CleanupRelatedDefaults 清理连带动作的一次性迁移：
// v1.5 起破坏性动作默认关闭；旧版本自动开过的值在首次升级时统一关闭。
func TestReadSettings_CleanupRelatedDefaults(t *testing.T) {
	// 1) 完全没有 cleanup 块 → 补默认（连带删除关闭）
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"download":{}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if s1.Cleanup.RemoveRelatedFiles {
		t.Error("缺 cleanup 块时不应默认连带删除关联文件")
	}
	if s1.Cleanup.RemoveEmptyDirs {
		t.Error("缺 cleanup 块时不应默认清理空目录")
	}
	if !s1.CleanupDefaultsV2 || !s1.CleanupDefaultsV3 {
		t.Error("应写入 V2/V3 迁移标记")
	}

	// 2) 旧版本自动开启 + 尚未有 V3 标记 → 一次性关闭
	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"cleanupDefaultsV2":true,"cleanup":{"maxThreshold":1000,"maxRatio":0.1,"maxDirs":20,"removeRelatedFiles":true,"removeEmptyDirs":true}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if s2.Cleanup.RemoveRelatedFiles || s2.Cleanup.RemoveEmptyDirs {
		t.Error("旧版本自动开启的破坏性动作应在 V3 迁移时关闭")
	}
	if !s2.CleanupDefaultsV3 {
		t.Error("应写入 V3 迁移标记")
	}

	// 3) V3 标记已存在 → 尊重用户之后手动开启
	dir3 := t.TempDir()
	writeSettings(t, dir3, `{"cleanupDefaultsV2":true,"cleanupDefaultsV3":true,"cleanup":{"maxThreshold":1000,"maxRatio":0.1,"maxDirs":20,"removeRelatedFiles":true}}`)
	s3, err := NewSettingsStore("test_salt", dir3).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if !s3.Cleanup.RemoveRelatedFiles {
		t.Error("V3 标记存在时不应覆盖用户手动开启")
	}
}

func TestSettingsStore_UpdateSerializesDisjointFields(t *testing.T) {
	dir := t.TempDir()
	s := NewSettingsStore("test_salt", dir)
	if _, err := s.Update(func(cfg *model.Settings) error {
		cfg.UserAgent = "initial-agent"
		cfg.StrmPrefix = "http://old"
		return nil
	}); err != nil {
		t.Fatalf("seed update: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		if _, err := s.Update(func(cfg *model.Settings) error {
			cfg.UserAgent = "agent-A"
			// 持锁期间故意变慢，暴露“读-改-写不在同一临界区”的丢更新问题
			time.Sleep(30 * time.Millisecond)
			return nil
		}); err != nil {
			t.Errorf("update A: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, err := s.Update(func(cfg *model.Settings) error {
			cfg.StrmPrefix = "http://new"
			return nil
		}); err != nil {
			t.Errorf("update B: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	got, err := s.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != "agent-A" || got.StrmPrefix != "http://new" {
		t.Fatalf("lost update: userAgent=%q prefix=%q", got.UserAgent, got.StrmPrefix)
	}
}

func TestSettingsStore_UpdateErrorDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	s := NewSettingsStore("test_salt", dir)
	if _, err := s.Update(func(cfg *model.Settings) error {
		cfg.UserAgent = "keep-me"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Update(func(cfg *model.Settings) error {
		cfg.UserAgent = "should-not-persist"
		return errors.New("validation failed")
	})
	if err == nil {
		t.Fatal("expected update error")
	}
	got, err := s.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != "keep-me" {
		t.Fatalf("failed update persisted: %q", got.UserAgent)
	}
}

// TestReadSettings_ForceProxyUaTokensNilVsEmpty 区分「从未配置」与「明确清空」：
//   - settings.json 没有 forceProxyUaTokens 键（老配置）→ 补默认值并写回
//   - 显式 [] → 保持为空（用户主动选择「全部直连」，绝不回填）
//
// 背景：老配置读出来是 nil，前端显示为空，用户随手一保存就写成 []，
// 从此「没配过」被永久固化成「明确关闭」，再也回不到默认。
func TestReadSettings_ForceProxyUaTokensNilVsEmpty(t *testing.T) {
	// 场景 1：键缺失 → 补默认
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{"strm":{"forceProxyFormats":["iso"]}}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	want := model.DefaultSettings().Strm.ForceProxyUaTokens
	if len(s1.Strm.ForceProxyUaTokens) != len(want) {
		t.Fatalf("缺键时应补默认 %v，got %v", want, s1.Strm.ForceProxyUaTokens)
	}
	for i := range want {
		if s1.Strm.ForceProxyUaTokens[i] != want[i] {
			t.Fatalf("默认值不一致: want %v, got %v", want, s1.Strm.ForceProxyUaTokens)
		}
	}

	// 场景 2：显式 [] → 保持为空
	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"strm":{"forceProxyUaTokens":[]}}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if len(s2.Strm.ForceProxyUaTokens) != 0 {
		t.Fatalf("显式空数组必须保持为空（= 全部直连），got %v", s2.Strm.ForceProxyUaTokens)
	}

	// 场景 3：有值 → 原样保留
	dir3 := t.TempDir()
	writeSettings(t, dir3, `{"strm":{"forceProxyUaTokens":["Kodi"]}}`)
	s3, err := NewSettingsStore("test_salt", dir3).ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if len(s3.Strm.ForceProxyUaTokens) != 1 || s3.Strm.ForceProxyUaTokens[0] != "Kodi" {
		t.Fatalf("已有值应原样保留，got %v", s3.Strm.ForceProxyUaTokens)
	}
}

func TestReadSettings_LegacyForceProxyFormatsMigrated(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"forceProxyFormats":["iso","bdmv","bdav","dvd","bluray","m2ts","ts","vob","ifo","bup"]}}`)
	got, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	want := model.DefaultSettings().Strm.ForceProxyFormats
	if len(got.Strm.ForceProxyFormats) != len(want) {
		t.Fatalf("legacy list should migrate to %v, got %v", want, got.Strm.ForceProxyFormats)
	}
	for i := range want {
		if got.Strm.ForceProxyFormats[i] != want[i] {
			t.Fatalf("formats = %v, want %v", got.Strm.ForceProxyFormats, want)
		}
	}
}

func TestReadSettings_SyncDeleteDryRunDefaultsOnWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"emby":{"syncDeleteEnabled":false}}`)
	got, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Emby.SyncDeleteDryRun {
		t.Fatal("同步删除未启用且缺 dryRun 字段时，应默认开启试运行")
	}

	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"emby":{"syncDeleteEnabled":true,"syncDeleteDryRun":false}}`)
	got2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got2.Emby.SyncDeleteDryRun {
		t.Fatal("显式 syncDeleteDryRun=false 必须保留")
	}
}

func TestReadSettings_ExplicitEmptyValuesKept(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"formatProxyDefaultsV1":true,"user-agent":"","strmExtensions":[],"strm":{"forceProxyFormats":[]}}`)
	got, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != "" {
		t.Fatalf("explicit empty user-agent must stay empty, got %q", got.UserAgent)
	}
	if got.StrmExtensions == nil || len(got.StrmExtensions) != 0 {
		t.Fatalf("explicit empty strmExtensions must stay empty, got %#v", got.StrmExtensions)
	}
	if got.Strm.ForceProxyFormats == nil || len(got.Strm.ForceProxyFormats) != 0 {
		t.Fatalf("explicit empty forceProxyFormats must stay empty, got %#v", got.Strm.ForceProxyFormats)
	}
}

func TestReadSettings_FormatProxyDefaultsMigration(t *testing.T) {
	t.Run("old switch off clears list", func(t *testing.T) {
		dir := t.TempDir()
		writeSettings(t, dir, `{"strm":{"forceProxyFormats":["iso","bdmv","bdav","dvd","bluray"],"disableSeekFormatProxy":true}}`)
		got, err := NewSettingsStore("test_salt", dir).ReadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Strm.ForceProxyFormats) != 0 {
			t.Fatalf("old switch off should migrate to empty list, got %v", got.Strm.ForceProxyFormats)
		}
		if !got.FormatProxyDefaultsV1 || got.Strm.DisableSeekFormatProxy {
			t.Fatalf("migration marker/flags wrong: marker=%v flag=%v", got.FormatProxyDefaultsV1, got.Strm.DisableSeekFormatProxy)
		}
	})

	t.Run("old empty list becomes default", func(t *testing.T) {
		dir := t.TempDir()
		writeSettings(t, dir, `{"strm":{"forceProxyFormats":[]}}`)
		got, err := NewSettingsStore("test_salt", dir).ReadSettings()
		if err != nil {
			t.Fatal(err)
		}
		want := model.DefaultSettings().Strm.ForceProxyFormats
		if len(got.Strm.ForceProxyFormats) != len(want) {
			t.Fatalf("old empty list should migrate to default %v, got %v", want, got.Strm.ForceProxyFormats)
		}
	})

	t.Run("migrated empty list stays off", func(t *testing.T) {
		dir := t.TempDir()
		writeSettings(t, dir, `{"formatProxyDefaultsV1":true,"strm":{"forceProxyFormats":[]}}`)
		got, err := NewSettingsStore("test_salt", dir).ReadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Strm.ForceProxyFormats) != 0 {
			t.Fatalf("migrated empty list should stay empty, got %v", got.Strm.ForceProxyFormats)
		}
	})
}

func TestReadSettings_CustomForceProxyFormatsKept(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"forceProxyFormats":["iso","m2ts"]}}`)
	got, err := NewSettingsStore("test_salt", dir).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Strm.ForceProxyFormats) != 2 || got.Strm.ForceProxyFormats[1] != "m2ts" {
		t.Fatalf("custom list must be kept, got %v", got.Strm.ForceProxyFormats)
	}
}

func TestReadSettings_DownloadExtensionsEmptyMeansNone(t *testing.T) {
	// 缺失 → 默认列表 + auto=true
	dir1 := t.TempDir()
	writeSettings(t, dir1, `{}`)
	s1, err := NewSettingsStore("test_salt", dir1).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s1.DownloadExtensions) == 0 || !s1.Download.AutoDownloadMetadata {
		t.Fatalf("missing list should use defaults, got %v auto=%v", s1.DownloadExtensions, s1.Download.AutoDownloadMetadata)
	}

	// 显式 [] → 不下载，auto=false
	dir2 := t.TempDir()
	writeSettings(t, dir2, `{"downloadExtensions":[]}`)
	s2, err := NewSettingsStore("test_salt", dir2).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.DownloadExtensions) != 0 || s2.Download.AutoDownloadMetadata {
		t.Fatalf("explicit empty should disable, got %v auto=%v", s2.DownloadExtensions, s2.Download.AutoDownloadMetadata)
	}

	// 旧开关 false + 非空列表 → 迁移为空
	dir3 := t.TempDir()
	writeSettings(t, dir3, `{"downloadExtensions":["srt"],"download":{"autoDownloadMetadata":false}}`)
	s3, err := NewSettingsStore("test_salt", dir3).ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s3.DownloadExtensions) != 0 || s3.Download.AutoDownloadMetadata {
		t.Fatalf("legacy toggle off should clear list, got %v auto=%v", s3.DownloadExtensions, s3.Download.AutoDownloadMetadata)
	}
}
