package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// SettingsStore settings.json 读写（不加密，因为是通用配置，不含用户密钥字段）
type SettingsStore struct {
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
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.DefaultSettings(), nil
		}
		logger.S().Warnf("[SettingsStore] ReadSettings failed: %v, returning defaults", err)
		return model.DefaultSettings(), nil
	}
	var out model.Settings
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
				}
			}
		}
	}
	// 填充默认值（策略：空则覆盖，非空则对明确新增的默认元素做追加，不破坏用户自定义）
	def := model.DefaultSettings()
	changed := false

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
	if len(out.Strm.ForceProxyUaTokens) == 0 {
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

	// 迁移后如果有变更，回写 settings.json（保持幂等，下次启动不会重复触发）
	if changed {
		logger.S().Infof("[SettingsStore] 迁移: 合并新默认值到 settings.json 并回写")
		if err := s.SaveSettings(&out); err != nil {
			logger.S().Warnf("[SettingsStore] 迁移回写 settings.json 失败: %v", err)
		}
	}
	// T9 迁移：开关打开后若 secret 未生成则自动生成并回写 settings.json
	if out.Strm.EnableTokenSigning && out.Strm.TokenSecret == "" {
		out.Strm.TokenSecret = strm.GenerateTokenSecret()
		logger.S().Infof("[SettingsStore] EnableTokenSigning=true, 生成 Strm.TokenSecret (len=%d), 回写 settings.json", len(out.Strm.TokenSecret))
		if err := s.SaveSettings(&out); err != nil {
			logger.S().Warnf("[SettingsStore] 回写 tokenSecret 失败: %v", err)
		}
	}
	return &out, nil
}

// SaveSettings 保存 Settings
func (s *SettingsStore) SaveSettings(cfg *model.Settings) error {
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
	return os.WriteFile(s.path, data, 0o644)
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
