package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

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

// ReadSettings 读取 Settings，不存在或权限不足则返回默认值
func (s *SettingsStore) ReadSettings() (*model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.DefaultSettings(), nil
		}
		logger.S().Warnf("[SettingsStore] ReadSettings failed: %v, returning defaults", err)
		return model.DefaultSettings(), nil
	}
	var out model.Settings
	// P0-2：判定 lifeMonitor.eventTypes 是否缺失（旧配置/空配置反序列化后 4 个 bool 全为 false，
	// 会让所有生活事件按 event_type_disabled_* 静默跳过）。只有确认字段存在才视为已配置。
	eventTypesMissing := true
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			logger.S().Warnf("[SettingsStore] ReadSettings json unmarshal failed: %v, returning defaults", err)
			return model.DefaultSettings(), nil
		}
		// 迁移：旧配置文件没有 notifyOnlyOnError 字段
		var rawCheck map[string]json.RawMessage
		if json.Unmarshal(data, &rawCheck) == nil {
			if lmRaw, ok := rawCheck["lifeMonitor"]; ok {
				var lmCheck map[string]json.RawMessage
				if json.Unmarshal(lmRaw, &lmCheck) == nil {
					if _, hasField := lmCheck["notifyOnlyOnError"]; !hasField {
						out.LifeMonitor.NotifyOnlyOnError = false
						logger.S().Infof("[SettingsStore] 迁移: 旧配置无 notifyOnlyOnError 字段，设为默认 false")
					}
					if _, hasET := lmCheck["eventTypes"]; hasET {
						eventTypesMissing = false
					}
				}
			}
		}
	}
	// 填充默认值（策略：空则覆盖，非空则对明确新增的默认元素做追加，不破坏用户自定义）
	changed := applyDefaults(&out, model.DefaultSettings(), eventTypesMissing)

	// 迁移后如果有变更，回写 settings.json（保持幂等，下次启动不会重复触发）
	if changed {
		logger.S().Infof("[SettingsStore] 迁移: 合并新默认值到 settings.json 并回写")
		if err := s.saveLocked(&out); err != nil {
			logger.S().Warnf("[SettingsStore] 迁移回写 settings.json 失败: %v", err)
		}
	}
	return &out, nil
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

	if len(out.StrmExtensions) == 0 {
		out.StrmExtensions = def.StrmExtensions
		changed = true
	} else {
		// v1.1.1 新增 "iso" 扩展名：对老用户 settings.json 做定向追加（不破坏用户已有自定义）
		if !sliceContains(out.StrmExtensions, "iso") {
			out.StrmExtensions = append(out.StrmExtensions, "iso")
			changed = true
		}
	}
	if out.UserAgent == "" {
		out.UserAgent = def.UserAgent
		changed = true
	}
	if len(out.DownloadExtensions) == 0 {
		out.DownloadExtensions = def.DownloadExtensions
		changed = true
	}
	if len(out.Strm.ForceProxyUaTokens) == 0 && len(def.Strm.ForceProxyUaTokens) > 0 {
		out.Strm.ForceProxyUaTokens = def.Strm.ForceProxyUaTokens
		changed = true
	}
	if out.Strm.AccountProxyConcurrencyLimit == 0 {
		out.Strm.AccountProxyConcurrencyLimit = def.Strm.AccountProxyConcurrencyLimit
		changed = true
	}
	if out.Strm.RedirectCheckTimeoutMs == 0 {
		out.Strm.RedirectCheckTimeoutMs = def.Strm.RedirectCheckTimeoutMs
		changed = true
	}
	return changed
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
