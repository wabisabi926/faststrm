package model

// Settings 对应 settings.json 顶层结构
// 对齐 docs/配置项参考.md
type Settings struct {
	UserAgent          string            `json:"user-agent"`
	InternalToken      string            `json:"internalToken"`
	StrmExtensions     []string          `json:"strmExtensions"`
	DownloadExtensions []string          `json:"downloadExtensions"`
	MediaMountPath     []string          `json:"mediaMountPath"`
	StrmPrefix         string            `json:"strmPrefix"`
	EnablePathEncoding bool              `json:"enablePathEncoding"`
	Enable302          bool              `json:"enable302"`
	RemoveExtraFiles   bool              `json:"removeExtraFiles"`
	Download           DownloadSettings  `json:"download"`
	Strm               StrmSettings      `json:"strm"`
	Emby               EmbySettings      `json:"emby"`
	Telegram           TelegramSettings  `json:"telegram"`
	LifeMonitor        LifeMonitorSettings `json:"lifeMonitor"`
}

// DownloadSettings download 子项
type DownloadSettings struct {
	LinkMaxPerSecond      *int `json:"linkMaxPerSecond,omitempty"`
	LinkMaxConcurrent     *int `json:"linkMaxConcurrent,omitempty"`
	DownloadMaxConcurrent *int `json:"downloadMaxConcurrent,omitempty"`
	StrmMaxConcurrent     *int `json:"strmMaxConcurrent,omitempty"` // STRM 写入并发数，默认 20
	AutoDownloadMetadata  bool `json:"autoDownloadMetadata"`        // 是否自动下载媒体元数据（nfo/jpg/png 等）
}

// StrmSettings strm 子项（STRM 路由策略）
type StrmSettings struct {
	ForceProxyUaTokens            []string `json:"forceProxyUaTokens"`
	AccountProxyConcurrencyLimit int      `json:"accountProxyConcurrencyLimit"`
	RedirectCheckTimeoutMs       int      `json:"redirectCheckTimeoutMs"`
}

// EmbySettings emby 子项
type EmbySettings struct {
	URL                    string                 `json:"url"`
	APIKey                 string                 `json:"apiKey"`
	NotifyMediaAdded       bool                   `json:"notifyMediaAdded"`
	NotifyMediaRemoved     bool                   `json:"notifyMediaRemoved"`
	NotifyPlayback         bool                   `json:"notifyPlayback"`
	PlaybackShowProgress   bool                   `json:"playbackShowProgress"`
	PlaybackShowOverview   bool                   `json:"playbackShowOverview"`
	WebhookAuth            string                 `json:"webhookAuth"`
	LibraryID              string                 `json:"libraryId"`
	SyncDeleteEnabled      bool                   `json:"syncDeleteEnabled"`
	SyncDeletePathMappings []SyncDeletePathMapping `json:"syncDeletePathMappings"`
	SyncDeleteNotify       bool                   `json:"syncDeleteNotify"`
	SyncDeleteDryRun       bool                   `json:"syncDeleteDryRun"`
	// 媒体库刷库配置
	RefreshOnCreate   bool `json:"refreshOnCreate"`   // 创建 STRM 后是否刷库
	RefreshOnDelete   bool `json:"refreshOnDelete"`   // 删除 STRM 后是否刷库
	DebounceSeconds   int  `json:"debounceSeconds"`   // 刷库防抖秒数，默认 10
}

// SyncDeletePathMapping 删除同步路径映射
type SyncDeletePathMapping struct {
	EmbyPath  string `json:"embyPath"`
	CloudPath string `json:"cloudPath"`
	Account   string `json:"account,omitempty"`
}

// TelegramSettings telegram 子项
type TelegramSettings struct {
	BotToken           string                 `json:"botToken"`
	ChatID             string                 `json:"chatId"`
	WebhookURL         string                 `json:"webhookUrl"`
	WebhookSecretToken string                 `json:"webhookSecretToken,omitempty"`
	Enabled            bool                   `json:"enabled"`
	AutoPolling        bool                   `json:"autoPolling,omitempty"`
	AllowedUsers       []int64                `json:"allowedUsers"`
	AccountAlerts      *AccountAlertsSettings `json:"accountAlerts,omitempty"`
}

// AccountAlertsSettings 账户状态通知配置
type AccountAlertsSettings struct {
	Enabled   bool `json:"enabled"`
	OnError   bool `json:"onError"`
	OnRecover bool `json:"onRecover"`
}

// LifeMonitorSettings lifeMonitor 子项
type LifeMonitorSettings struct {
	Enabled            bool                 `json:"enabled"`
	Accounts           []string             `json:"accounts"`
	PollInterval       int                  `json:"pollInterval"`
	PathMappings       []MonitorPathMapping `json:"pathMappings"`
	RemoveEmptyDirs    bool                 `json:"removeEmptyDirs"`
	EventTypes         EventTypesSettings   `json:"eventTypes"`
	StrmPrefix         string               `json:"strmPrefix"`
	EnablePathEncoding bool                 `json:"enablePathEncoding"`
	Enable302          bool                 `json:"enable302"`
	MinFileSize        int64                `json:"minFileSize"`
	FirstPullMode      string               `json:"firstPullMode"`
	MoveMediaMode      string               `json:"moveMediaMode"`
	// 事件去重配置
	EnableDedup      bool `json:"enableDedup"`      // 是否启用事件去重
	DedupWindowHours int  `json:"dedupWindowHours"` // 去重窗口（小时），默认 24
	// API 冷却配置
	EnableRateLimit bool `json:"enableRateLimit"` // 是否启用 API 冷却
	RateLimitMs     int  `json:"rateLimitMs"`     // API 调用最小间隔（毫秒），默认 1000
	// 重试配置
	MaxRetries   int `json:"maxRetries"`   // 拉取失败最大重试次数，默认 3
	RetryDelayMs int `json:"retryDelayMs"` // 重试间隔（毫秒），默认 1000
}

// MonitorPathMapping 生活监控路径映射
type MonitorPathMapping struct {
	Account   string `json:"account"`
	CloudPath string `json:"cloudPath"`
	LocalPath string `json:"localPath"`
}

// EventTypesSettings 各类事件开关
type EventTypesSettings struct {
	Create bool `json:"create"`
	Remove bool `json:"remove"`
	Rename bool `json:"rename"`
	Move   bool `json:"move"`
}

// AppConfig 对应 config.json（admin 账号配置）
type AppConfig struct {
	Username string `json:"username"`
	Password string `json:"password"` // 格式: $sha256$<hex>
}

// DefaultUserAgent 115 API 默认 UA（iOS 115 客户端）
const DefaultUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/116.0.5845.89 Mobile/15E148 Safari/604.1"

// DefaultStrmExtensions STRM 扩展名默认值
var DefaultStrmExtensions = []string{
	"mp4", "mkv", "avi", "mov", "rmvb", "webm", "flv", "m3u8",
	"mp3", "flac", "wav", "aac", "ogg", "m4a", "ts", "m2ts",
}

// DefaultDownloadExtensions 下载扩展名默认值
var DefaultDownloadExtensions = []string{
	"srt", "ass", "sub", "nfo", "jpg", "png",
}

// DefaultSettings 返回带默认值的 Settings
func DefaultSettings() *Settings {
	return &Settings{
		UserAgent:          DefaultUserAgent,
		StrmExtensions:     append([]string{}, DefaultStrmExtensions...),
		DownloadExtensions: append([]string{}, DefaultDownloadExtensions...),
		MediaMountPath:     []string{},
		Download: DownloadSettings{
			StrmMaxConcurrent:    intPtr(20),  // STRM 写入并发数默认 20
			AutoDownloadMetadata: true,        // 默认开启，保持向后兼容
		},
		Strm: StrmSettings{
			ForceProxyUaTokens:            []string{"Infuse", "VidHub", "SenPlayer", "SenPlayerHD"},
			AccountProxyConcurrencyLimit: 8,
			RedirectCheckTimeoutMs:       5000,
		},
		Emby: EmbySettings{
			RefreshOnCreate: true,
			RefreshOnDelete: true,
			DebounceSeconds: 10,
		},
		Telegram: TelegramSettings{
			AccountAlerts: &AccountAlertsSettings{
				Enabled:   false,
				OnError:   true,
				OnRecover: true,
			},
		},
		LifeMonitor: LifeMonitorSettings{
			PollInterval:     10,
			RemoveEmptyDirs:  true,
			EnableDedup:      true,
			DedupWindowHours: 24,
			EnableRateLimit:  true,
			RateLimitMs:      1000,
			MaxRetries:       3,
			RetryDelayMs:     1000,
			EventTypes: EventTypesSettings{
				Create: true,
				Remove: true,
				Rename: true,
				Move:   true,
			},
			FirstPullMode: "latest",
			MoveMediaMode: "recreate",
		},
	}
}

// intPtr 返回 int 的指针（用于设置可选配置字段）
func intPtr(v int) *int {
	return &v
}
