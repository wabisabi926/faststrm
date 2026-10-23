package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// SettingsStore settings.json 读写（不加密，因为是通用配置，不含用户密钥字段）
//
// 并发安全：异步 webhook 处理协程与 HTTP handler 会同时读/写同一份 settings.json，
// 必须串行化访问，否则读侧可能读到写侧截断到 0 字节的半截文件，
// 进而触发迁移回写把真实配置覆盖回默认值（丢密钥等）。
type SettingsStore struct {
	mu   sync.Mutex
	salt string
	path string
}

// NewSettingsStore 创建 SettingsStore
func NewSettingsStore(salt, configDir string) *SettingsStore {
	return &SettingsStore{
		salt: salt,
		path: filepath.Join(configDir, "settings.json"),
	}
}

// ReadSettings 读取 Settings；文件不存在时返回默认值，读取/解析失败时返回错误。
//
// 读取失败绝不能静默回退默认值：Update 会在读取后直接写盘，静默回退会把损坏或
// 暂时不可读的配置覆盖成默认配置，造成不可逆的配置丢失。
func (s *SettingsStore) ReadSettings() (*model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out, changed, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	// 迁移后如果有变更，回写 settings.json（保持幂等，下次启动不会重复触发）
	if changed {
		logger.S().Infof("[SettingsStore] 迁移: 合并新默认值到 settings.json 并回写")
		if err := s.saveLocked(out); err != nil {
			logger.S().Warnf("[SettingsStore] 迁移回写 settings.json 失败: %v", err)
		}
	}
	return out, nil
}

// Update 在锁内完成“读取 → 修改 → 落盘”，并返回修改后的配置。
// 多个 handler/webhook 同时保存不同字段时不会互相覆盖；mutate 返回错误时不做任何写盘。
func (s *SettingsStore) Update(mutate func(*model.Settings) error) (*model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out, changed, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	if mutate != nil {
		if err := mutate(out); err != nil {
			return nil, err
		}
		changed = true
	}
	// Update 的 mutate 可能重新写入已下线的模板字段（旧前端/手工调用），落盘前统一清空。
	if out.ClearRemovedStrmTemplates() {
		changed = true
	}
	if changed {
		if err := s.saveLocked(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// readLocked 读取并应用默认值；调用方必须持有 s.mu。
// changed 表示是否需要回写，由 ReadSettings/Update 决定落盘时机。
func (s *SettingsStore) readLocked() (*model.Settings, bool, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.DefaultSettings(), false, nil
		}
		return nil, false, fmt.Errorf("读取 settings.json 失败: %w", err)
	}
	var out model.Settings
	// P0-2：判定 lifeMonitor.eventTypes 是否缺失（旧配置/空配置反序列化后 4 个 bool 全为 false，
	// 会让所有生活事件按 event_type_disabled_* 静默跳过）。只有确认字段存在才视为已配置。
	eventTypesMissing := true
	if len(data) == 0 {
		s.backupCorruptLocked(data)
		return nil, false, fmt.Errorf("settings.json 为空，已拒绝加载以避免覆盖原配置")
	}
	if err := json.Unmarshal(data, &out); err != nil {
		s.backupCorruptLocked(data)
		return nil, false, fmt.Errorf("解析 settings.json 失败: %w", err)
	}

	def := model.DefaultSettings()
	migrationChanged := false
	// 迁移：旧配置文件没有 notifyOnlyOnError 字段
	var rawCheck map[string]json.RawMessage
	if json.Unmarshal(data, &rawCheck) == nil {
		// 旧配置缺 enablePathEncoding：反序列化得到 false，而历史默认是 true。
		// 缺少该键不能当成用户“显式关闭”。
		if _, ok := rawCheck["enablePathEncoding"]; !ok {
			out.EnablePathEncoding = true
			migrationChanged = true
			logger.S().Infof("[SettingsStore] 迁移: 旧配置缺 enablePathEncoding，补默认 true")
		}

		lmCheck := map[string]json.RawMessage{}
		if lmRaw, ok := rawCheck["lifeMonitor"]; ok {
			_ = json.Unmarshal(lmRaw, &lmCheck)
		}
		if lmRaw, ok := rawCheck["lifeMonitor"]; ok {
			var tmp map[string]json.RawMessage
			if json.Unmarshal(lmRaw, &tmp) == nil {
				if _, hasField := tmp["notifyOnlyOnError"]; !hasField {
					out.LifeMonitor.NotifyOnlyOnError = false
					logger.S().Infof("[SettingsStore] 迁移: 旧配置无 notifyOnlyOnError 字段，设为默认 false")
				}
				if _, hasET := tmp["eventTypes"]; hasET {
					eventTypesMissing = false
				}
			}
		}
		// 生活监控的布尔默认值同样不能用零值判断：缺键时补默认，显式 false 保留。
		if _, ok := lmCheck["autoDownloadMetadata"]; !ok {
			out.LifeMonitor.AutoDownloadMetadata = def.LifeMonitor.AutoDownloadMetadata
			migrationChanged = true
		}
		if _, ok := lmCheck["enableDedup"]; !ok {
			out.LifeMonitor.EnableDedup = def.LifeMonitor.EnableDedup
			migrationChanged = true
		}
		if _, ok := lmCheck["enableRateLimit"]; !ok {
			out.LifeMonitor.EnableRateLimit = def.LifeMonitor.EnableRateLimit
			migrationChanged = true
		}
		if _, ok := lmCheck["moveMediaCreateNewStrm"]; !ok {
			out.LifeMonitor.MoveMediaCreateNewStrm = def.LifeMonitor.MoveMediaCreateNewStrm
			migrationChanged = true
		}

		// 孤儿清理：老配置一次性降为 mark_only（auto_clean 需要用户重新确认）。
		if !out.CleanupOrphansDefaultsV1 {
			mode := strings.ToLower(strings.TrimSpace(out.Download.FullSyncCleanupOrphans))
			if mode == "" || mode == "auto_clean" {
				out.Download.FullSyncCleanupOrphans = "mark_only"
				logger.S().Infof("[SettingsStore] 迁移: 旧配置孤儿清理降为 mark_only")
			}
			out.CleanupOrphansDefaultsV1 = true
			migrationChanged = true
		}
	}

	// A1：自定义 URL/文件名模板下线。老配置先备份，再清空，回到标准 query + 默认文件名。
	// 迁移标记已存在但字段再次出现（手工编辑 settings.json）时同样清空，
	// 否则模板只在新生成时生效，重命名/清理仍按默认名推导，会留下重复 STRM。
	templatesPresent := out.Strm.StrmUrlTemplate != "" || out.Strm.StrmFilenameTemplate != "" ||
		out.LifeMonitor.StrmUrlTemplate != "" || out.LifeMonitor.StrmFilenameTemplate != ""
	if templatesPresent {
		s.backupFileLocked(data, "templates-v1")
		logger.S().Warnf("[SettingsStore] 自定义 STRM 模板已下线，忽略并清空（原配置已备份）")
	}
	if out.ClearRemovedStrmTemplates() {
		migrationChanged = true
	}

	// 填充默认值（策略：空则覆盖，非空则对明确新增的默认元素做追加，不破坏用户自定义）
	changed := applyDefaults(&out, def, eventTypesMissing) || migrationChanged
	return &out, changed, nil
}

// backupCorruptLocked 保留损坏配置的现场，便于人工恢复；失败不影响主错误返回。
func (s *SettingsStore) backupCorruptLocked(data []byte) {
	s.backupFileLocked(data, "corrupt")
}

func (s *SettingsStore) backupFileLocked(data []byte, label string) {
	if len(data) == 0 {
		return
	}
	backup := fmt.Sprintf("%s.%s.%s", s.path, label, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		logger.S().Warnf("[SettingsStore] 备份配置失败: %v", err)
		return
	}
	logger.S().Errorf("[SettingsStore] 已备份配置到 %s", backup)
}

// normalizeExtensions 规范化扩展名列表：去空白、去前导点、小写、去重（保留首次出现顺序）。
// 存储层统一为「无点小写」（如 "mp4"），与 model.DefaultStrmExtensions 一致；
// 匹配侧（task.normalizeExt / monitor.isMediaFile / mediaExtensionMatch）本就兼容带点与大小写。
func normalizeExtensions(list []string) ([]string, bool) {
	if len(list) == 0 {
		return list, false
	}
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	changed := false
	for _, raw := range list {
		ext := strings.ToLower(strings.TrimSpace(raw))
		ext = strings.TrimPrefix(ext, ".")
		if ext == "" {
			changed = true
			continue
		}
		if _, dup := seen[ext]; dup {
			changed = true
			continue
		}
		seen[ext] = struct{}{}
		out = append(out, ext)
		if ext != raw {
			changed = true
		}
	}
	if len(out) != len(list) {
		changed = true
	}
	return out, changed
}

// applyDefaults 为缺失字段填充默认值，返回是否发生变更（changed）。
// 独立成函数以控制 ReadSettings 的圈复杂度（cyclop 上限 25）。
func applyDefaults(out, def *model.Settings, eventTypesMissing bool) bool {
	changed := false

	// P0-2：旧配置缺 lifeMonitor.eventTypes → 补默认（全开）。字段存在时尊重用户值（含全关）。
	if eventTypesMissing {
		out.LifeMonitor.EventTypes = def.LifeMonitor.EventTypes
		changed = true
		logger.S().Infof("[SettingsStore] 迁移: 旧配置缺 lifeMonitor.eventTypes，补默认 create=%v remove=%v rename=%v move=%v",
			out.LifeMonitor.EventTypes.Create, out.LifeMonitor.EventTypes.Remove,
			out.LifeMonitor.EventTypes.Rename, out.LifeMonitor.EventTypes.Move)
	}

	// 显式留空表示「不写入自定义扩展名列表」，运行时由 EffectiveStrmExtensions 兜底；
	// 这里只做非空列表的规范化，不再把空列表写回默认值。
	if len(out.StrmExtensions) > 0 {
		// 规范化：去空白、去前导点、小写、去重。
		// 历史 settings.json 里 ".iso"×N 与 "iso" 并存会被重复处理，也让匹配/展示出现歧义。
		if norm, normChanged := normalizeExtensions(out.StrmExtensions); normChanged {
			out.StrmExtensions = norm
			changed = true
			logger.S().Infof("[SettingsStore] 迁移: STRM 扩展名已规范化去重: %v", out.StrmExtensions)
		}
		if len(out.StrmExtensions) > 0 && !sliceContains(out.StrmExtensions, "iso") {
			// v1.1.1 新增 "iso" 扩展名：对老用户 settings.json 做定向追加（不破坏用户已有自定义）
			out.StrmExtensions = append(out.StrmExtensions, "iso")
			changed = true
		}
	}
	// v1.5.0 清理阈值升级：由「单阈值 10 个」改为「比例 + 绝对 + 目录数」三重保护。
	//
	// 迁移条件刻意收紧：只有 MaxRatio 尚未出现（==0，说明从未升级过）
	// 且 MaxThreshold 仍是旧默认 10 时才升级；用户自己调过的值一律不动。
	if out.Cleanup.MaxRatio == 0 && out.Cleanup.MaxThreshold == 10 {
		out.Cleanup.MaxRatio = def.Cleanup.MaxRatio
		out.Cleanup.MaxThreshold = def.Cleanup.MaxThreshold
		out.Cleanup.MaxDirs = def.Cleanup.MaxDirs
		changed = true
		logger.S().Infof("[SettingsStore] 迁移: 清理阈值升级为 ratio=%.2f abs=%d dirs=%d（原单阈值 10）",
			out.Cleanup.MaxRatio, out.Cleanup.MaxThreshold, out.Cleanup.MaxDirs)
	}
	// 清理默认值反转：pickcode 级清理此前没有前端入口、默认关闭，绝大多数用户
	// 实际只有「按路径删除多余文件」在跑——它会删掉保留集合之外的任何本地文件。
	// 这里把 pickcode 级清理打开（一次性），路径级清理由任务级开关控制且默认关闭。
	if out.Download.FullSyncCleanupOrphans == "" {
		out.Download.FullSyncCleanupOrphans = def.Download.FullSyncCleanupOrphans
		changed = true
		logger.S().Infof("[SettingsStore] 迁移: 孤儿 STRM 清理默认开启（%s）", out.Download.FullSyncCleanupOrphans)
	}
	// 清理默认值一次性迁移（带标记，避免每次启动覆盖用户选择）：
	//   - 整个 cleanup 块缺失 → 补齐默认值
	//   - 不再自动打开连带删除；破坏性动作统一默认关闭
	if !out.CleanupDefaultsV2 {
		if out.Cleanup.MaxThreshold == 0 && out.Cleanup.MaxRatio == 0 && out.Cleanup.MaxDirs == 0 {
			out.Cleanup = def.Cleanup
		}
		out.CleanupDefaultsV2 = true
		changed = true
		logger.S().Infof("[SettingsStore] 迁移: 清理默认值已初始化（破坏性连带动作默认关闭）")
	}
	// v1.5 一次性迁移：旧版本自动打开过 RemoveRelatedFiles/RemoveEmptyDirs，
	// 这里统一关闭，之后用户在设置页手动开启的值会保留。
	if !out.CleanupDefaultsV3 {
		if out.Cleanup.RemoveRelatedFiles || out.Cleanup.RemoveEmptyDirs {
			out.Cleanup.RemoveRelatedFiles = false
			out.Cleanup.RemoveEmptyDirs = false
			logger.S().Infof("[SettingsStore] 迁移: 已关闭连带删除关联文件/空目录（默认只删 STRM，需手动开启）")
		}
		out.CleanupDefaultsV3 = true
		changed = true
	}
	if backfillStrmRouteDefaults(out, def) {
		changed = true
	}
	if applyFormatProxyDefaults(out, def) {
		changed = true
	}
	// User-Agent 显式留空表示「不写入自定义 UA」，运行时由 EffectiveUserAgent 兜底；
	// 不再把默认 UA 写回输入框/配置。
	// 老配置没有 rateLimit 块时补默认值，避免前端只改其中一项导致其余配额被写成 0（不限流）。
	if out.RateLimit == (model.RateLimitSettings{}) {
		out.RateLimit = def.RateLimit
		changed = true
		logger.S().Infof("[SettingsStore] 迁移: 补齐 115 API 限流默认值: %+v", out.RateLimit)
	}
	if applyMetadataDefaults(out, def) {
		changed = true
	}
	// 同步删除尚未启用时，缺字段默认开启试运行（首次勾选不会立刻真删）；
	// 已经启用同步删除的老用户保持原值，避免升级后突然停止删除。
	if !out.Emby.SyncDeleteDryRunProvided() && !out.Emby.SyncDeleteEnabled {
		out.Emby.SyncDeleteDryRun = true
		changed = true
	}
	if applyStrmRuntimeDefaults(out, def) {
		changed = true
	}
	return changed
}

// applyStrmRuntimeDefaults 回填 STRM 路由的运行时数值默认值（0 = 旧配置未配置）。
// 拆出来是为了控制 applyDefaults 的圈复杂度。
func applyStrmRuntimeDefaults(out, def *model.Settings) bool {
	changed := false
	if out.Strm.AccountProxyConcurrencyLimit == 0 {
		out.Strm.AccountProxyConcurrencyLimit = def.Strm.AccountProxyConcurrencyLimit
		changed = true
	}
	if out.Strm.RedirectCheckTimeoutMs == 0 {
		out.Strm.RedirectCheckTimeoutMs = def.Strm.RedirectCheckTimeoutMs
		changed = true
	}
	// 分片并发代理：0 表示未配置（旧配置文件），回填默认值
	if out.Strm.ProxyChunkSizeMB == 0 {
		out.Strm.ProxyChunkSizeMB = def.Strm.ProxyChunkSizeMB
		changed = true
	}
	if out.Strm.ProxyConcurrency == 0 {
		out.Strm.ProxyConcurrency = def.Strm.ProxyConcurrency
		changed = true
	}
	return changed
}

// applyMetadataDefaults 统一附带下载扩展名语义：
//   - 字段缺失 → 默认列表
//   - 显式 [] → 不下载任何附带文件
//   - 旧 autoDownloadMetadata=false → 迁移为空列表
//
// autoDownloadMetadata 作为兼容字段由列表长度派生。
func applyMetadataDefaults(out, def *model.Settings) bool {
	changed := false
	provided := out.DownloadExtensionsProvided()
	if out.Download.AutoDownloadMetadataProvided() && !out.Download.AutoDownloadMetadata {
		if len(out.DownloadExtensions) > 0 || !provided {
			out.DownloadExtensions = []string{}
			changed = true
			logger.S().Infof("[SettingsStore] 迁移: 自动下载元数据已关闭，附带下载扩展名置空")
		}
	} else if !provided {
		out.DownloadExtensions = append([]string{}, def.DownloadExtensions...)
		changed = true
	}
	if len(out.DownloadExtensions) > 0 {
		if norm, normChanged := normalizeExtensions(out.DownloadExtensions); normChanged {
			out.DownloadExtensions = norm
			changed = true
		}
	}
	autoDownload := len(out.DownloadExtensions) > 0
	if out.Download.AutoDownloadMetadata != autoDownload {
		out.Download.AutoDownloadMetadata = autoDownload
		changed = true
	}
	return changed
}

// backfillStrmRouteDefaults 回填 STRM 路由策略里「可安全补默认」的两个列表。
//
// ForceProxyFormats：v1.5.0 起改为可配置。字段缺失（nil）时补默认值；
// v1.5.4 起列表是「按格式强制代理」的唯一控制，显式 [] 表示关闭。
//
// ForceProxyUaTokens：只区分「从未配置」与「明确清空」——
//   - nil（settings.json 里没有这个键，典型是 v1.5.x 之前的老配置）→ 补默认值并写回，
//     否则前端显示为空、用户随手一保存就把「没配过」固化成「明确关闭」，再也回不到默认。
//   - []（显式空数组）→ 保持为空，表示用户主动选择「全部直连」，绝不回填。
func backfillStrmRouteDefaults(out, def *model.Settings) bool {
	changed := false
	// v1.5.4：旧默认列表把 m2ts/ts/vob/ifo/bup 也当原盘强制代理。
	// 只有“整份列表正好等于旧默认”才迁移，用户自定义过的列表保持原样。
	if isLegacyForceProxyFormats(out.Strm.ForceProxyFormats) {
		out.Strm.ForceProxyFormats = append([]string{}, def.Strm.ForceProxyFormats...)
		changed = true
		logger.S().Infof("[SettingsStore] 迁移: 强制代理格式从旧默认 10 项收敛为 %v", out.Strm.ForceProxyFormats)
	}
	if out.Strm.ForceProxyFormats == nil {
		out.Strm.ForceProxyFormats = def.Strm.ForceProxyFormats
		changed = true
	}
	if out.Strm.ForceProxyUaTokens == nil {
		out.Strm.ForceProxyUaTokens = append([]string{}, def.Strm.ForceProxyUaTokens...)
		logger.S().Infof("[SettingsStore] 迁移: 补齐强制代理 UA 默认值 %v", out.Strm.ForceProxyUaTokens)
		changed = true
	}
	return changed
}

// applyFormatProxyDefaults 一次性迁移「开关 + 列表」到单一列表语义：
//   - 老开关关闭（DisableSeekFormatProxy=true）→ 清空列表，保持关闭；
//   - 老开关开启且列表为空 → 补默认 5 项，保持原有“空=默认”的行为；
//   - 迁移后列表为空表示明确关闭，不再回填。
func applyFormatProxyDefaults(out, def *model.Settings) bool {
	if out.FormatProxyDefaultsV1 {
		return false
	}
	if out.Strm.DisableSeekFormatProxy {
		out.Strm.ForceProxyFormats = []string{}
	} else if len(out.Strm.ForceProxyFormats) == 0 {
		out.Strm.ForceProxyFormats = append([]string{}, def.Strm.ForceProxyFormats...)
	}
	out.Strm.DisableSeekFormatProxy = false
	out.FormatProxyDefaultsV1 = true
	logger.S().Infof("[SettingsStore] 迁移: 按格式代理改为单列表控制，当前列表 %v", out.Strm.ForceProxyFormats)
	return true
}

var legacyDefaultForceProxyFormats = []string{"iso", "bdmv", "bdav", "dvd", "bluray", "m2ts", "ts", "vob", "ifo", "bup"}

// isLegacyForceProxyFormats 只匹配旧默认整份列表（顺序不敏感）；用户增删过任一项都保留。
func isLegacyForceProxyFormats(formats []string) bool {
	if len(formats) != len(legacyDefaultForceProxyFormats) {
		return false
	}
	want := make(map[string]struct{}, len(legacyDefaultForceProxyFormats))
	for _, f := range legacyDefaultForceProxyFormats {
		want[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))] = struct{}{}
	}
	for _, f := range formats {
		key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
		if _, ok := want[key]; !ok {
			return false
		}
		delete(want, key)
	}
	return len(want) == 0
}

// SaveSettings 保存 Settings
func (s *SettingsStore) SaveSettings(cfg *model.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(cfg)
}

// saveLocked 落盘（调用方需持有 s.mu）：先写临时文件再原子重命名，
// 保证任何时刻读到的 settings.json 都是完整内容（不会被写到一半）。
func (s *SettingsStore) saveLocked(cfg *model.Settings) error {
	if cfg == nil {
		cfg = model.DefaultSettings()
	}
	// 落盘前统一规范化扩展名：去点、小写、去重。
	// 这样即使用户从 UI 或手工编辑写入 ".ISO, iso, .iso"，settings.json 也只会保留一份 "iso"。
	if norm, changed := normalizeExtensions(cfg.StrmExtensions); changed {
		cfg.StrmExtensions = norm
	}
	if norm, changed := normalizeExtensions(cfg.DownloadExtensions); changed {
		cfg.DownloadExtensions = norm
	}
	// 最后一道闸：任何写盘路径（含 SaveSettings/测试/未来调用方）都不能持久化已下线的模板。
	cfg.ClearRemovedStrmTemplates()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// sliceContains 判断字符串切片是否包含目标值
func sliceContains(slice []string, target string) bool {
	for _, v := range slice {
		if v == target {
			return true
		}
	}
	return false
}
