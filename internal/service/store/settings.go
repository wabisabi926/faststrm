package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wabisabi926/faststrm/internal/model"
)

// SettingsStore settings.json 读写（不加密，因为是通用配置，不含用户密钥字段）
// SettingsStore 内部字段已加密（internalToken, webhookAuth, botToken 等若需要，统一由上层决定是否加密）
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

// ReadSettings 读取 Settings，不存在则返回默认值
func (s *SettingsStore) ReadSettings() (*model.Settings, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.DefaultSettings(), nil
		}
		return nil, err
	}
	var out model.Settings
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
	}
	// 填充默认值（确保空字段有默认）
	def := model.DefaultSettings()
	if len(out.StrmExtensions) == 0 {
		out.StrmExtensions = def.StrmExtensions
	}
	if len(out.DownloadExtensions) == 0 {
		out.DownloadExtensions = def.DownloadExtensions
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
