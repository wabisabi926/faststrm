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
	// 填充默认值
	def := model.DefaultSettings()
	if len(out.StrmExtensions) == 0 {
		out.StrmExtensions = def.StrmExtensions
	}
	if out.UserAgent == "" {
		out.UserAgent = def.UserAgent
	}
	if len(out.DownloadExtensions) == 0 {
		out.DownloadExtensions = def.DownloadExtensions
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
