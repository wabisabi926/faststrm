package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// AppConfigPaths 应用运行时路径配置
type AppConfigPaths struct {
	DataDir      string // /app/data
	ConfigDir    string // /app/config
	CacheDir     string // /app/data/cache
	DBDir        string // /app/data/db
	SettingsPath string // /app/config/settings.json
	ConfigPath   string // /app/config/config.json (admin 登录配置)
	SaltPath     string // /app/config/.salt
	DefaultDir   string // /app/.config (默认配置模板目录)
	LogDir       string // /app/data/logs
}

// AppConfig 运行时聚合配置
type AppConfig struct {
	Paths    AppConfigPaths
	Settings *model.Settings
	Admin    *model.AppConfig
	Server   ServerConfig
	Salt     string // 加密盐值（用于 AES 凭据加密 + 密码哈希）
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Mode string `json:"mode"` // dev / prod
}

var appCfg *AppConfig

// Load 加载配置。优先从 JSON 文件加载，缺失字段填充默认值。
func Load(paths AppConfigPaths) (*AppConfig, error) {
	settings := model.DefaultSettings()
	admin := &model.AppConfig{Username: "admin"}

	// 读取 settings.json
	if data, err := os.ReadFile(paths.SettingsPath); err == nil {
		data = stripBOM(data)
		if jerr := json.Unmarshal(data, settings); jerr != nil {
			logger.S().Warnf("settings.json parse failed, use defaults: %v", jerr)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read settings.json: %w", err)
	}

	// 读取 config.json（admin 登录）
	if data, err := os.ReadFile(paths.ConfigPath); err == nil {
		data = stripBOM(data)
		_ = json.Unmarshal(data, admin)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config.json: %w", err)
	}

	appCfg = &AppConfig{
		Paths:    paths,
		Settings: settings,
		Admin:    admin,
		Server: ServerConfig{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnvInt("SERVER_PORT", 8090), // Go 后端默认 8090
			Mode: getEnv("APP_ENV", "prod"),
		},
	}
	return appCfg, nil
}

// Get 获取全局配置（需先 Load）
func Get() *AppConfig {
	if appCfg == nil {
		panic("config not loaded, call config.Load() first")
	}
	return appCfg
}

// SetForTest 设置全局配置（仅用于测试）
func SetForTest(cfg *AppConfig) {
	appCfg = cfg
}

// SaveSettings 持久化 settings.json 到磁盘
func SaveSettings() error {
	cfg := Get()
	data, err := json.MarshalIndent(cfg.Settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.Paths.SettingsPath, data, 0644)
}

// SaveAdmin 持久化 config.json（admin 用户名/密码）到磁盘
func SaveAdmin() error {
	cfg := Get()
	data, err := json.MarshalIndent(cfg.Admin, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.Paths.ConfigPath, data, 0644)
}

// GenerateInternalToken 如果 settings 没有 internalToken 则生成并持久化
func GenerateInternalToken() (string, error) {
	cfg := Get()
	if cfg.Settings.InternalToken != "" {
		return cfg.Settings.InternalToken, nil
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// base64url 编码（无 padding）
	token := hex.EncodeToString(buf)[:32] // 简化：32 hex 字符
	cfg.Settings.InternalToken = token
	if err := SaveSettings(); err != nil {
		return "", err
	}
	logger.S().Info("Generated new internalToken")
	return token, nil
}

// HashPassword 用 salt + sha256 哈希密码。格式: $sha256$<hex>
func HashPassword(salt, password string) string {
	h := sha256.Sum256([]byte(salt + password))
	return "$sha256$" + hex.EncodeToString(h[:])
}

// EnsureSalt 读取或生成 salt
func EnsureSalt(saltPath string) (string, error) {
	if data, err := os.ReadFile(saltPath); err == nil {
		return string(data), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(buf)
	if err := os.WriteFile(saltPath, []byte(salt), 0600); err != nil {
		return "", err
	}
	return salt, nil
}

// EnsureDirs 确保所有数据目录存在
func EnsureDirs(paths AppConfigPaths) error {
	dirs := []string{paths.DataDir, paths.ConfigDir, paths.CacheDir, paths.DBDir, paths.LogDir}
	for _, d := range dirs {
		if err := os.MkdirAll(d, fs.FileMode(0755)); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// InitApp 完整初始化流程（对应 docker-entrypoint.sh）
// 顺序: 建目录 → 拷贝默认配置 → 生成 admin 密码哈希 → 生成 internalToken → 加载配置
func InitApp(defaultRoot string) (*AppConfig, error) {
	paths := resolvePaths(defaultRoot)
	logger.S().Infof("InitApp: data=%s config=%s", paths.DataDir, paths.ConfigDir)

	// 1. 确保目录存在
	if err := EnsureDirs(paths); err != nil {
		return nil, err
	}

	// 2. 拷贝默认配置（如果不存在）
	defaultFiles := []struct {
		srcName string // .config / .settings 等前缀
		dstName string
	}{
		{".config.json", "config.json"},
		{".account.json", "account.json"},
		{".tasks.json", "tasks.json"},
		{".settings.json", "settings.json"},
	}
	for _, df := range defaultFiles {
		dst := filepath.Join(paths.ConfigDir, df.dstName)
		src := filepath.Join(paths.DefaultDir, df.srcName)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			if srcData, rerr := os.ReadFile(src); rerr == nil {
				if werr := os.WriteFile(dst, srcData, 0644); werr != nil {
					logger.S().Warnf("copy default %s failed: %v", df.dstName, werr)
				} else {
					logger.S().Infof("Created %s from default", dst)
				}
			} else {
				logger.S().Warnf("default file %s not found, creating empty JSON", src)
				_ = os.WriteFile(dst, []byte("{}"), 0644)
			}
		}
	}

	// 3. 确保 salt 存在（用于 AES 凭据加密 + 密码哈希）
	salt, err := EnsureSalt(paths.SaltPath)
	if err != nil {
		return nil, fmt.Errorf("ensure salt: %w", err)
	}

	// 4. admin 密码哈希（如果 config.json 中 username 为空或 admin，且密码为空）
	cfgPath := filepath.Join(paths.ConfigDir, "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		data = stripBOM(data)
		var ac model.AppConfig
		if jerr := json.Unmarshal(data, &ac); jerr == nil && ac.Password == "" {
			// 首次启动或密码为空：设置默认 admin/admin
			if ac.Username == "" {
				ac.Username = "admin"
			}
			ac.Password = HashPassword(salt, "admin")
			if out, oerr := json.MarshalIndent(ac, "", "  "); oerr == nil {
				_ = os.WriteFile(cfgPath, out, 0644)
				logger.S().Info("Default admin password hashed (admin/admin) in config.json")
			}
		}
	}

	// 5. 加载完整配置
	cfg, err := Load(paths)
	if err != nil {
		return nil, err
	}
	cfg.Salt = salt

	// 6. 生成 internalToken
	if _, err := GenerateInternalToken(); err != nil {
		return nil, fmt.Errorf("generate internalToken: %w", err)
	}

	return cfg, nil
}

// resolvePaths 解析应用路径
// defaultRoot: 默认配置模板目录（例如 /app/.config 或 repo/.config）
//   - 若 DATA_DIR / CONFIG_DIR 显式设置，则优先；
//   - 否则根据 defaultRoot 推导出"平行结构"：defaultRoot 的父目录 + data/config。
func resolvePaths(defaultRoot string) AppConfigPaths {
	if defaultRoot == "" {
		defaultRoot = "/app/.config"
	}
	// 推导默认的 baseData / baseCfg：把 defaultRoot 视作 "<root>/.config"，则 base = parent(defaultRoot)
	base := filepath.Clean(filepath.Join(defaultRoot, ".."))
	baseCfgDefault := filepath.Join(base, "config")
	baseDataDefault := filepath.Join(base, "data")
	// 对于 Docker 默认路径 /app/.config → parent 是 /app，所以依然得到 /app/config 与 /app/data
	// 对于 Windows 工作目录下的 .config  → parent 是项目根，得到 <repo>/config 与 <repo>/data
	baseData := getEnv("DATA_DIR", baseDataDefault)
	baseCfg := getEnv("CONFIG_DIR", baseCfgDefault)
	return AppConfigPaths{
		DataDir:      baseData,
		ConfigDir:    baseCfg,
		CacheDir:     filepath.Join(baseData, "cache"),
		DBDir:        filepath.Join(baseData, "db"),
		LogDir:       filepath.Join(baseData, "logs"),
		SettingsPath: filepath.Join(baseCfg, "settings.json"),
		ConfigPath:   filepath.Join(baseCfg, "config.json"),
		SaltPath:     filepath.Join(baseCfg, ".salt"),
		DefaultDir:   defaultRoot,
	}
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

// stripBOM 移除 UTF-8 BOM（Windows PowerShell 5 写 UTF-8 会附带 BOM）
func stripBOM(data []byte) []byte {
	bom := []byte{0xEF, 0xBB, 0xBF}
	if len(data) >= 3 && data[0] == bom[0] && data[1] == bom[1] && data[2] == bom[2] {
		return data[3:]
	}
	return data
}
