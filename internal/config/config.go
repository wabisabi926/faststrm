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
	"sync"

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

var (
	appCfgMu sync.RWMutex
	appCfg   *AppConfig
)

// Snapshot 返回当前配置的**值语义快照**，避免外部直接修改全局共享指针下的字段导致 data race。
// 需要"读取+就地修改+保存"语义的代码路径，改用 MutateAdmin / MutateSettings 或手动 Clone+Replace。
func (c *AppConfig) Snapshot() AppConfig {
	if c == nil {
		return AppConfig{}
	}
	out := *c
	if c.Settings != nil {
		s := *c.Settings
		out.Settings = &s
		// Settings 里有可变切片/指针字段：做一层深拷贝，避免调用者 append/mutation 穿透
		out.Settings.StrmExtensions = append([]string(nil), c.Settings.StrmExtensions...)
		out.Settings.DownloadExtensions = append([]string(nil), c.Settings.DownloadExtensions...)
		// 可变切片/指针字段：复制底层切片头指向的 backing array，避免 snapshot 的 append/mutation 穿透全局
		if c.Settings.Strm.ForceProxyUaTokens != nil {
			tokens := make([]string, len(c.Settings.Strm.ForceProxyUaTokens))
			copy(tokens, c.Settings.Strm.ForceProxyUaTokens)
			out.Settings.Strm.ForceProxyUaTokens = tokens
		}
		if c.Settings.Emby.SyncDeletePathMappings != nil {
			pm := make([]model.SyncDeletePathMapping, len(c.Settings.Emby.SyncDeletePathMappings))
			copy(pm, c.Settings.Emby.SyncDeletePathMappings)
			out.Settings.Emby.SyncDeletePathMappings = pm
		}
		if c.Settings.LifeMonitor.Accounts != nil {
			out.Settings.LifeMonitor.Accounts = append([]string(nil), c.Settings.LifeMonitor.Accounts...)
		}
		if c.Settings.LifeMonitor.PathMappings != nil {
			pm := make([]model.MonitorPathMapping, len(c.Settings.LifeMonitor.PathMappings))
			copy(pm, c.Settings.LifeMonitor.PathMappings)
			out.Settings.LifeMonitor.PathMappings = pm
		}

		if c.Settings.LifeMonitor.StrmGenerateBlacklist != nil {
			out.Settings.LifeMonitor.StrmGenerateBlacklist = append([]string(nil), c.Settings.LifeMonitor.StrmGenerateBlacklist...)
		}
		if c.Settings.Download.StrmGenerateBlacklist != nil {
			out.Settings.Download.StrmGenerateBlacklist = append([]string(nil), c.Settings.Download.StrmGenerateBlacklist...)
		}
		if c.Settings.Telegram.AllowedUsers != nil {
			users := make([]int64, len(c.Settings.Telegram.AllowedUsers))
			copy(users, c.Settings.Telegram.AllowedUsers)
			out.Settings.Telegram.AllowedUsers = users
		}
	}
	if c.Admin != nil {
		a := *c.Admin
		out.Admin = &a
	}
	return out
}

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

	cfg := &AppConfig{
		Paths:    paths,
		Settings: settings,
		Admin:    admin,
		Server: ServerConfig{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnvInt("SERVER_PORT", 8090), // Go 后端默认 8090
			Mode: getEnv("APP_ENV", "prod"),
		},
	}
	appCfgMu.Lock()
	appCfg = cfg
	appCfgMu.Unlock()
	return cfg, nil
}

// Get 获取全局配置（需先 Load）。返回的是当前快照（值语义拷贝），
// 调用者可以自由读取字段；但不要把快照里的指针字段再写回全局，否则仍有 race。
// 若需要修改并保存配置，使用 MutateAdmin / MutateSettings 或 Replace / ReplaceAndPersist。
func Get() AppConfig {
	appCfgMu.RLock()
	defer appCfgMu.RUnlock()
	if appCfg == nil {
		panic("config not loaded, call config.Load() first")
	}
	return appCfg.Snapshot()
}

// Ptr 返回当前全局配置指针（仅用于启动阶段需要真实引用传参的调用方；
// 业务 handler 一律禁止使用，避免跨 goroutine 共享可变对象）。
func Ptr() *AppConfig {
	appCfgMu.RLock()
	defer appCfgMu.RUnlock()
	if appCfg == nil {
		panic("config not loaded, call config.Load() first")
	}
	return appCfg
}

// SetForTest 设置全局配置（仅用于测试）。
func SetForTest(cfg *AppConfig) {
	appCfgMu.Lock()
	appCfg = cfg
	appCfgMu.Unlock()
}

// Replace 替换全局 AppConfig（仍为快照语义：先对入参做深拷贝，再替换内部指针）。
func Replace(cfg AppConfig) {
	snap := cfg.Snapshot()
	// Snapshot 返回值语义，需要取其地址保存为全局指针
	ptr := snap
	appCfgMu.Lock()
	appCfg = &ptr
	appCfgMu.Unlock()
}

// MutateAdmin 原子地读取-修改-写回 cfg.Admin，并可选持久化到磁盘。
// 保证修改过程与并发 Get / 其他 Mutate* 之间无 data race。
func MutateAdmin(mutate func(admin *model.AppConfig), persist bool) error {
	appCfgMu.Lock()
	defer appCfgMu.Unlock()
	if appCfg == nil {
		return fmt.Errorf("config not loaded")
	}
	// 构造新 Admin：先克隆当前值，再让用户在克隆体上修改，最后整体替换（copy-on-write）
	var nextAdmin model.AppConfig
	if appCfg.Admin != nil {
		nextAdmin = *appCfg.Admin
	}
	mutate(&nextAdmin)
	appCfg.Admin = &nextAdmin
	if persist {
		return saveAdminLocked()
	}
	return nil
}

// MutateSettings 原子地读取-修改-写回 cfg.Settings，并可选持久化到磁盘。
func MutateSettings(mutate func(settings *model.Settings), persist bool) error {
	appCfgMu.Lock()
	defer appCfgMu.Unlock()
	if appCfg == nil {
		return fmt.Errorf("config not loaded")
	}
	var next model.Settings
	if appCfg.Settings != nil {
		next = *appCfg.Settings
		// 切片/指针字段深拷贝，避免 mutate 里的 append/map 写回影响旧对象
		next.StrmExtensions = append([]string(nil), appCfg.Settings.StrmExtensions...)
		next.DownloadExtensions = append([]string(nil), appCfg.Settings.DownloadExtensions...)
		if appCfg.Settings.Strm.ForceProxyUaTokens != nil {
			tokens := make([]string, len(appCfg.Settings.Strm.ForceProxyUaTokens))
			copy(tokens, appCfg.Settings.Strm.ForceProxyUaTokens)
			next.Strm.ForceProxyUaTokens = tokens
		}
		if appCfg.Settings.Emby.SyncDeletePathMappings != nil {
			pm := make([]model.SyncDeletePathMapping, len(appCfg.Settings.Emby.SyncDeletePathMappings))
			copy(pm, appCfg.Settings.Emby.SyncDeletePathMappings)
			next.Emby.SyncDeletePathMappings = pm
		}
		if appCfg.Settings.LifeMonitor.Accounts != nil {
			next.LifeMonitor.Accounts = append([]string(nil), appCfg.Settings.LifeMonitor.Accounts...)
		}
		if appCfg.Settings.LifeMonitor.PathMappings != nil {
			pm := make([]model.MonitorPathMapping, len(appCfg.Settings.LifeMonitor.PathMappings))
			copy(pm, appCfg.Settings.LifeMonitor.PathMappings)
			next.LifeMonitor.PathMappings = pm
		}

		if appCfg.Settings.LifeMonitor.StrmGenerateBlacklist != nil {
			next.LifeMonitor.StrmGenerateBlacklist = append([]string(nil), appCfg.Settings.LifeMonitor.StrmGenerateBlacklist...)
		}
		if appCfg.Settings.Download.StrmGenerateBlacklist != nil {
			next.Download.StrmGenerateBlacklist = append([]string(nil), appCfg.Settings.Download.StrmGenerateBlacklist...)
		}
		if appCfg.Settings.Telegram.AllowedUsers != nil {
			users := make([]int64, len(appCfg.Settings.Telegram.AllowedUsers))
			copy(users, appCfg.Settings.Telegram.AllowedUsers)
			next.Telegram.AllowedUsers = users
		}
		if appCfg.Settings.Telegram.AccountAlerts != nil {
			a := *appCfg.Settings.Telegram.AccountAlerts
			next.Telegram.AccountAlerts = &a
		}
	}
	mutate(&next)
	appCfg.Settings = &next
	if persist {
		return saveSettingsLocked()
	}
	return nil
}

// SaveSettings 持久化 settings.json 到磁盘
func SaveSettings() error {
	appCfgMu.RLock()
	defer appCfgMu.RUnlock()
	return saveSettingsLocked()
}

// saveSettingsLocked 在持有锁的前提下持久化 settings.json
func saveSettingsLocked() error {
	if appCfg == nil {
		return fmt.Errorf("config not loaded")
	}
	data, err := json.MarshalIndent(appCfg.Settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appCfg.Paths.SettingsPath, data, 0600)
}

// SaveAdmin 持久化 config.json（admin 用户名/密码）到磁盘
func SaveAdmin() error {
	appCfgMu.RLock()
	defer appCfgMu.RUnlock()
	return saveAdminLocked()
}

// saveAdminLocked 在持有锁的前提下持久化 config.json
func saveAdminLocked() error {
	if appCfg == nil {
		return fmt.Errorf("config not loaded")
	}
	data, err := json.MarshalIndent(appCfg.Admin, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appCfg.Paths.ConfigPath, data, 0600)
}

// GenerateInternalToken 如果 settings 没有 internalToken 则生成并持久化
func GenerateInternalToken() (string, error) {
	appCfgMu.Lock()
	defer appCfgMu.Unlock()
	if appCfg == nil {
		return "", fmt.Errorf("config not loaded")
	}
	if appCfg.Settings.InternalToken != "" {
		return appCfg.Settings.InternalToken, nil
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// base64url 编码（无 padding）
	token := hex.EncodeToString(buf)[:32] // 简化：32 hex 字符
	// copy-on-write：替换新 Settings 指针，避免直接写共享结构体
	var next model.Settings
	if appCfg.Settings != nil {
		next = *appCfg.Settings
	}
	next.InternalToken = token
	appCfg.Settings = &next
	if err := saveSettingsLocked(); err != nil {
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

// migrateDotFile 如果旧版 dot-prefix 文件存在而新版不存在，则重命名迁移
// 例如 .tasks.json → tasks.json（Go 早期代码用 dot 前缀，新版和 TS 版统一为无前缀）
func migrateDotFile(configDir, oldName, newName string) {
	oldPath := filepath.Join(configDir, oldName)
	newPath := filepath.Join(configDir, newName)
	if _, err := os.Stat(oldPath); err == nil {
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			if rerr := os.Rename(oldPath, newPath); rerr == nil {
				logger.S().Infof("migrated %s → %s", oldName, newName)
			}
		}
	}
}

// migrateSQLite 如果旧版 SQLite 数据库在 configDir 下而新版 dataDir 下不存在，则迁移
// TS 版数据库路径: configDir/filePathDb.sqlite
// Go 版数据库路径: dataDir/filePathDb.sqlite
// 同时迁移 -wal 和 -shm 文件（WAL 模式附属文件）
func migrateSQLite(configDir, dataDir string) {
	dbFile := "filePathDb.sqlite"
	oldPath := filepath.Join(configDir, dbFile)
	newPath := filepath.Join(dataDir, dbFile)
	if _, err := os.Stat(oldPath); err == nil {
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			// 迁移主数据库文件
			if rerr := os.Rename(oldPath, newPath); rerr == nil {
				logger.S().Infof("migrated SQLite %s from config/ to data/", dbFile)
			}
			// 迁移 WAL / SHM 附属文件（如果存在）
			for _, suffix := range []string{"-wal", "-shm"} {
				oldWAL := oldPath + suffix
				newWAL := newPath + suffix
				if _, err := os.Stat(oldWAL); err == nil {
					_ = os.Rename(oldWAL, newWAL)
				}
			}
		}
	}
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

	// 1a. 一次性数据迁移：旧版 dot-prefix 文件 → 新版无 dot 文件
	//     Go 早期代码用 .tasks.json，新版统一为 tasks.json（和 TS 版对齐）
	migrateDotFile(paths.ConfigDir, ".tasks.json", "tasks.json")

	// 1b. 一次性数据迁移：旧版 SQLite 从 config/ 迁移到 data/
	//     TS 版数据库在 config/filePathDb.sqlite，Go 版在 data/filePathDb.sqlite
	migrateSQLite(paths.ConfigDir, paths.DataDir)

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
				if werr := os.WriteFile(dst, srcData, 0600); werr != nil {
					logger.S().Warnf("copy default %s failed: %v", df.dstName, werr)
				} else {
					logger.S().Infof("Created %s from default", dst)
				}
			} else {
				logger.S().Warnf("default file %s not found, creating empty JSON", src)
				_ = os.WriteFile(dst, []byte("{}"), 0600)
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
				_ = os.WriteFile(cfgPath, out, 0600)
				logger.S().Info("Default admin password hashed (admin/admin) in config.json")
			}
		}
	}

	// 5. 加载完整配置
	if _, err = Load(paths); err != nil {
		return nil, err
	}
	// Load 已经把 cfg 放到全局了，但 Load 内部没有填 Salt。
	// 用写锁 clone 一份替换，保证与并发 Get 之间无 data race。
	appCfgMu.Lock()
	if appCfg != nil {
		clone := *appCfg
		clone.Salt = salt
		appCfg = &clone
	}
	appCfgMu.Unlock()

	// 6. 生成 internalToken（GenerateInternalToken 自己拿写锁）
	if _, err := GenerateInternalToken(); err != nil {
		return nil, fmt.Errorf("generate internalToken: %w", err)
	}

	appCfgMu.RLock()
	defer appCfgMu.RUnlock()
	if appCfg == nil {
		return nil, fmt.Errorf("config not loaded after InitApp")
	}
	out := appCfg.Snapshot()
	return &out, nil
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
