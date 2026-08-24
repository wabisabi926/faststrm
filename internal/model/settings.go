package model

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Settings 对应 settings.json 顶层结构
// 对齐 docs/配置项参考.md
type Settings struct {
	UserAgent          string              `json:"user-agent"`
	InternalToken      string              `json:"internalToken"`
	StrmExtensions     []string            `json:"strmExtensions"`
	DownloadExtensions []string            `json:"downloadExtensions"`
	MediaMountPath     []string            `json:"mediaMountPath"`
	StrmPrefix         string              `json:"strmPrefix"`
	EnablePathEncoding bool                `json:"enablePathEncoding"`
	Enable302          bool                `json:"enable302"`
	RemoveExtraFiles   bool                `json:"removeExtraFiles"`
	Download           DownloadSettings    `json:"download"`
	Strm               StrmSettings        `json:"strm"`
	Emby               EmbySettings        `json:"emby"`
	Telegram           TelegramSettings    `json:"telegram"`
	LifeMonitor        LifeMonitorSettings `json:"lifeMonitor"`
	Cleanup            CleanupSettings     `json:"cleanup"`
}

// CleanupSettings STRM 清理对账配置（参考 MoviePilot p115strmhelper full_sync_remove_unless_* + cleanup_confirm_mode）
type CleanupSettings struct {
	// MaxThreshold 单次清理最大删除数阈值，超过则拒绝执行（防误删大批量）
	// 对齐 full_sync_remove_unless_max_threshold，默认 10
	MaxThreshold int `json:"maxThreshold"`
	// StableThreshold 稳定阈值，超过此值但在 MaxThreshold 以内，需二次确认
	// 对齐 full_sync_remove_unless_stable_threshold，默认 5
	StableThreshold int `json:"stableThreshold"`
	// ConfirmMode 二次确认模式：none 立即删除 / plugin_ui 插件内确认 / telegram 通知按钮确认
	// 对齐 full_sync_cleanup_confirm_mode，默认 "none"
	ConfirmMode string `json:"confirmMode"`
	// RemoveStrm 是否删除 STRM 文件本身，默认 true
	RemoveStrm bool `json:"removeStrm"`
	// RemoveEmptyDirs 是否清理无 STRM 的空目录，默认 false
	// 对齐 full_sync_remove_unless_dir
	RemoveEmptyDirs bool `json:"removeEmptyDirs"`
	// RemoveRelatedFiles 是否清理 STRM 关联的 .nfo/.jpg/.srt 等同名媒体信息文件，默认 false
	// 对齐 full_sync_remove_unless_file
	RemoveRelatedFiles bool `json:"removeRelatedFiles"`
}

// DownloadSettings download 子项
type DownloadSettings struct {
	LinkMaxPerSecond      *int     `json:"linkMaxPerSecond,omitempty"`
	LinkMaxConcurrent     *int     `json:"linkMaxConcurrent,omitempty"`
	DownloadMaxConcurrent *int     `json:"downloadMaxConcurrent,omitempty"`
	StrmMaxConcurrent     *int     `json:"strmMaxConcurrent,omitempty"` // STRM 写入并发数，默认 20
	AutoDownloadMetadata  bool     `json:"autoDownloadMetadata"`        // 是否自动下载媒体元数据（nfo/jpg/png 等）
	MinFileSize           int64    `json:"minFileSize"`                 // 全量同步/任务执行 最小文件大小阈值（字节），默认0不限制
	StrmGenerateBlacklist []string `json:"strmGenerateBlacklist"`       // STRM 生成文件名黑名单关键词列表（如 "*trailer*", "sample.*"），大小写不敏感
	OverwriteMode         string   `json:"overwriteMode"`               // STRM 覆盖模式："always"(默认覆盖) / "never"(已存在则跳过)
	// P2-3 增量同步：以 files 表为 snapshot，对 pickcode+name+size 完全一致的条目跳过写 STRM/下载，仅处理变化的部分
	IncrementalSync bool `json:"incrementalSync"`
	// P0-1 增量对账清理：增量同步结束后，扫描 TargetPath 下所有 .strm 文件，
	// 提取内容中的 pickcode 与云端快照比对，孤儿 STRM 按以下模式处理：
	//   "off"        默认。不清理（保持原行为）
	//   "mark_only"  仅日志记录孤儿路径，不删除
	//   "auto_clean" 自动软删（.deleted.bak），超阈值时复用 CleanupSettings 二次确认流程
	// 对齐参考项目 increment_sync_remove_unless_strm
	IncrementalCleanupMode string `json:"incrementalCleanupMode"`
	// P0-2 全量对账清理：全量任务开始前预扫描 TargetPath 下所有 .strm 文件，
	// 提取 pickcode 与 DB 反查（云端最新 fileEntries）比对，孤儿 STRM 按以下模式处理：
	//   "off"        默认。不预扫描（保持原行为）
	//   "mark_only"  仅日志记录孤儿路径，不删除
	//   "auto_clean" 自动软删（.deleted.bak），超阈值时复用 CleanupSettings 二次确认流程
	// 对齐参考项目 full_sync_remove_unless_strm
	FullSyncCleanupOrphans string `json:"fullSyncCleanupOrphans"`
}

// StrmSettings strm 子项（STRM 路由策略）
type StrmSettings struct {
	ForceProxyUaTokens           []string `json:"forceProxyUaTokens"`
	AccountProxyConcurrencyLimit int      `json:"accountProxyConcurrencyLimit"`
	RedirectCheckTimeoutMs       int      `json:"redirectCheckTimeoutMs"`
	// STRM URL/文件名高级自定义模板（P1-4）
	//   支持变量：{prefix} {account} {pickcode} {filename} {ext} {stem}
	//   示例："{prefix}/api/fs/get?a={account}&p={pickcode}&n={filename}"
	//   空字符串表示走默认拼接逻辑
	StrmUrlTemplate string `json:"strmUrlTemplate"`
	// 文件名模板：默认 {stem}.strm（.iso 为 {stem}.iso.strm）
	//   支持变量：{filename} {stem} {ext}
	//   示例："[{account}] {stem}.strm"
	StrmFilenameTemplate string `json:"strmFilenameTemplate"`
}

// EmbySettings emby 子项
type EmbySettings struct {
	URL                    string                  `json:"url"`
	APIKey                 string                  `json:"apiKey"`
	NotifyMediaAdded       bool                    `json:"notifyMediaAdded"`
	NotifyMediaRemoved     bool                    `json:"notifyMediaRemoved"`
	NotifyPlayback         bool                    `json:"notifyPlayback"`
	PlaybackShowProgress   bool                    `json:"playbackShowProgress"`
	PlaybackShowOverview   bool                    `json:"playbackShowOverview"`
	WebhookAuth            string                  `json:"webhookAuth"`
	LibraryID              string                  `json:"libraryId"`
	SyncDeleteEnabled      bool                    `json:"syncDeleteEnabled"`
	SyncDeletePathMappings []SyncDeletePathMapping `json:"syncDeletePathMappings"`
	SyncDeleteNotify       bool                    `json:"syncDeleteNotify"`
	SyncDeleteDryRun       bool                    `json:"syncDeleteDryRun"`
	// SyncDelete 增强（参考 MoviePilot p115strmhelper sync_del_*）
	// SyncDeleteDeleteSymlink 是否删除本地 STRM 软链接本身（不跟目标）
	// 对齐 sync_del_delete_symlink，默认 false
	SyncDeleteDeleteSymlink bool `json:"syncDeleteDeleteSymlink"`
	// SyncDeleteRemoveVersions 是否启用多版本删除（按 title 模糊匹配清理所有版本）
	// 对齐 sync_del_remove_versions，默认 false
	SyncDeleteRemoveVersions bool `json:"syncDeleteRemoveVersions"`
	// SyncDeleteSource 是否同步删除源文件（115 网盘原文件）
	// 对齐 sync_del_source，默认 false（敏感操作，谨慎开启）
	SyncDeleteSource bool `json:"syncDeleteSource"`
	// 媒体库刷库配置
	RefreshOnCreate bool `json:"refreshOnCreate"` // 创建 STRM 后是否刷库
	RefreshOnDelete bool `json:"refreshOnDelete"` // 删除 STRM 后是否刷库
	DebounceSeconds int  `json:"debounceSeconds"` // 刷库防抖秒数，默认 10
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
	Enabled               bool                 `json:"enabled"`
	Accounts              []string             `json:"accounts"`
	PollInterval          int                  `json:"pollInterval"`
	PathMappings          []MonitorPathMapping `json:"pathMappings"`
	RemoveEmptyDirs       bool                 `json:"removeEmptyDirs"`
	EventTypes            EventTypesSettings   `json:"eventTypes"`
	StrmPrefix            string               `json:"strmPrefix"`
	EnablePathEncoding    bool                 `json:"enablePathEncoding"`
	Enable302             bool                 `json:"enable302"`
	MinFileSize           int64                `json:"minFileSize"`           // 生活监控 最小文件大小阈值（字节），默认0不限制
	StrmGenerateBlacklist []string             `json:"strmGenerateBlacklist"` // 生活监控 独立黑名单关键词列表；空则继承全局 Download.StrmGenerateBlacklist
	OverwriteMode         string               `json:"overwriteMode"`         // 生活监控 STRM 覆盖模式："always"(默认覆盖) / "never"(已存在则跳过)；空则继承全局
	FirstPullMode         string               `json:"firstPullMode"`
	MoveMediaMode         string               `json:"moveMediaMode"`
	AutoDownloadMetadata  bool                 `json:"autoDownloadMetadata"` // 生活监控 STRM 创建后是否自动占位同名 nfo/jpg/png/srt 等关联资源（空则继承全局 Download.AutoDownloadMetadata）
	DownloadExtensions    []string             `json:"downloadExtensions"`   // 生活监控 关联资源扩展名；空则继承全局 Download.DownloadExtensions
	// P1-4 高级模板（空则继承全局 StrmSettings 模板，仍为空则走默认拼接）
	StrmUrlTemplate      string `json:"strmUrlTemplate"`
	StrmFilenameTemplate string `json:"strmFilenameTemplate"`
	// P1-5 Delete 安全兜底：
	//   false(默认)=软删除，仅把本地 STRM 重命名为 *.deleted.bak 并同步清理 DB，不硬删；
	//   true=硬删除（对齐原有行为，危险）
	EnableHardDelete  bool `json:"enableHardDelete"`
	NotifyOnlyOnError bool `json:"notifyOnlyOnError"` // true=仅错误时发TG通知，正常操作不通知
	// 事件去重配置
	EnableDedup      bool `json:"enableDedup"`      // 是否启用事件去重
	DedupWindowHours int  `json:"dedupWindowHours"` // 去重窗口（小时），默认 24
	// API 冷却配置
	EnableRateLimit bool `json:"enableRateLimit"` // 是否启用 API 冷却
	RateLimitMs     int  `json:"rateLimitMs"`     // API 调用最小间隔（毫秒），默认 1000
	// 重试配置
	MaxRetries   int `json:"maxRetries"`   // 拉取失败最大重试次数，默认 3
	RetryDelayMs int `json:"retryDelayMs"` // 重试间隔（毫秒），默认 1000
	// P0-5 生活事件整理队列无进展超时（对齐参考项目 helper/life/transfer_wait.py）
	//   TransferStallTimeoutMinutes: 单个事件处理无进展超时分钟数（文件夹递归时若超过该时长无新文件生成则中止）
	//   0=不限制。默认 30
	//   TransferWaitMode: 超时后行为 "skip"(默认,跳过该事件继续下一个) / "abort"(中止本轮轮询)
	TransferStallTimeoutMinutes int    `json:"transferStallTimeoutMinutes"`
	TransferWaitMode            string `json:"transferWaitMode"`
	// P0-6 重命名/移动自动关联文件开关
	//   RenameAutoRelatedFiles: rename 事件时是否自动 rename 同名 .nfo/.jpg/.srt 等关联资源（默认 true）
	//   MoveLocalMoveRelatedFiles: local_move 模式下 move 事件时是否自动搬移关联资源（默认 true）
	RenameAutoRelatedFiles    bool `json:"renameAutoRelatedFiles"`
	MoveLocalMoveRelatedFiles bool `json:"moveLocalMoveRelatedFiles"`
	// P0-7 媒体目录内移动三态策略细化（对齐参考项目 config.py）
	//   MoveMediaMode: "recreate"(删除/重建) / "local_move"(纯本地迁移) —— 粗粒度总开关
	//   MoveMediaKeepOldStrm: recreate 模式下是否保留旧 STRM（默认 false=删除，对齐原行为）
	//   MoveMediaCreateNewStrm: 是否在目标位置生成新 STRM（默认 true）
	//   MoveOutRemoveLocalStrm: 移出映射（移到非媒体目录）时是否删除旧 STRM（默认 true=删除）
	MoveMediaKeepOldStrm   bool `json:"moveMediaKeepOldStrm"`
	MoveMediaCreateNewStrm bool `json:"moveMediaCreateNewStrm"`
	MoveOutRemoveLocalStrm bool `json:"moveOutRemoveLocalStrm"`
	// MediaMountPath 扩展搜索目录列表
	// 当旧 STRM 不在当前 PathMapping 中时，在这些目录中搜索旧 STRM 进行清理
	// 例如：之前映射到 C:\Users\liwl\Videos，后来改为 dist\Strm，旧 STRM 需要在 Videos 中找到并删除
	MediaMountPath []string `json:"mediaMountPath"`
}

// MonitorPathMapping 生活监控路径映射
//
//	MappingType（Phase 1.3 新增）: 映射类型，取值 "media"(默认) / "transfer"(整理目录) / "unrecognized"(未识别目录)
//	  对老配置缺省该字段时 → 按 media 处理（100% 向后兼容）。
type MonitorPathMapping struct {
	Account     string `json:"account"`
	CloudPath   string `json:"cloudPath"`
	LocalPath   string `json:"localPath"`
	MappingType string `json:"mappingType,omitempty"`
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
			StrmMaxConcurrent:     intPtr(20), // STRM 写入并发数默认 20
			AutoDownloadMetadata:  true,       // 默认开启，保持向后兼容
			MinFileSize:           0,          // 默认不限制最小文件大小
			StrmGenerateBlacklist: []string{}, // 默认空黑名单
			OverwriteMode:         "always",   // 默认始终覆盖（与 MoviePilot 默认行为一致）
		},
		Strm: StrmSettings{
			ForceProxyUaTokens:           []string{"Infuse", "VidHub", "SenPlayer", "SenPlayerHD"},
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
			PollInterval:         10,
			RemoveEmptyDirs:      true,
			EnableDedup:          true,
			DedupWindowHours:     24,
			EnableRateLimit:      true,
			RateLimitMs:          1000,
			MaxRetries:           3,
			RetryDelayMs:         1000,
			AutoDownloadMetadata: true,  // 默认与全局 Download.AutoDownloadMetadata 保持一致
			DownloadExtensions:   nil,  // 空则继承全局 DefaultDownloadExtensions
			EnableHardDelete:     true, // 默认硬删除，不生成 .deleted.bak 备份（软删除改名为 .deleted.bak）
			EventTypes: EventTypesSettings{
				Create: true,
				Remove: true,
				Rename: true,
				Move:   true,
			},
			FirstPullMode:               "latest",
			MoveMediaMode:               "recreate",
			TransferStallTimeoutMinutes: 30,     // P0-5 默认30分钟无进展超时
			TransferWaitMode:            "skip", // P0-5 默认跳过超时事件
			RenameAutoRelatedFiles:      true,   // P0-6 默认自动重命名关联资源
			MoveLocalMoveRelatedFiles:   true,   // P0-6 默认自动搬移关联资源
			MoveMediaKeepOldStrm:        false,  // P0-7 默认删除旧STRM（对齐 recreate 原行为）
			MoveMediaCreateNewStrm:      true,   // P0-7 默认生成新STRM
			MoveOutRemoveLocalStrm:      true,   // P0-7 默认移出映射时删除旧STRM
		},
		Cleanup: CleanupSettings{
			MaxThreshold:       10, // 单次最多删 10 个，超过拒绝
			StableThreshold:    5,  // 5-10 需二次确认
			ConfirmMode:        "none",
			RemoveStrm:         true,
			RemoveEmptyDirs:    false,
			RemoveRelatedFiles: false,
		},
	}
}

// intPtr 返回 int 的指针（用于设置可选配置字段）
func intPtr(v int) *int {
	return &v
}

// ==================== P1-4 高级模板渲染 ====================

// RenderStrmUrlTemplate 渲染 STRM URL 模板。
// 支持变量：{prefix} {account} {pickcode} {filename} {ext} {stem}
// - account/pickcode/filename 自动 URL QueryEscape（安全编码，避免破坏 URL）
// - ext: 带 "." 的小写扩展名（如 ".mkv", ".iso"）；无扩展则为 ""
// - stem: 文件名去扩展名；.iso 文件 stem = "name.iso"（保持与默认行为一致）
// 当 template == "" 时返回空字符串，由调用方回退默认拼接逻辑。
func RenderStrmUrlTemplate(template, prefix, account, pickcode, fileName, ext, stem string) string {
	if template == "" {
		return ""
	}
	encodedAccount := url.QueryEscape(account)
	encodedPickcode := url.QueryEscape(pickcode)
	encodedFileName := url.QueryEscape(fileName)
	r := strings.NewReplacer(
		"{prefix}", strings.TrimRight(prefix, "/"),
		"{account}", encodedAccount,
		"{pickcode}", encodedPickcode,
		"{filename}", encodedFileName,
		"{ext}", ext,
		"{stem}", stem,
	)
	result := r.Replace(template)
	// 确保末尾无多余换行，仅保留模板原有的 "\n"
	result = strings.TrimRight(result, "\r\n") + "\n"
	return result
}

// RenderStrmFilenameTemplate 渲染 STRM 文件名模板。
// 支持变量：{filename} {stem} {ext} {account}
// - account 是扩展变量（方便文件名区分账号），默认不启用但可由模板显式引用
// - ext 带 "."；.iso 时 stem 已包含 ".iso"
// 当 template == "" 时返回空字符串，由调用方走默认命名（{stem}.strm / {stem}.iso.strm）
// 返回值强制以 ".strm" 结尾（防止模板误配置丢失扩展名）
func RenderStrmFilenameTemplate(template, fileName, ext, stem, account string) string {
	if template == "" {
		return ""
	}
	r := strings.NewReplacer(
		"{filename}", fileName,
		"{stem}", stem,
		"{ext}", ext,
		"{account}", account,
	)
	name := r.Replace(template)
	// 清理文件名非法字符（最小 Windows 非法字符）
	name = strings.ReplaceAll(name, ":", "：")
	for _, c := range []string{"<", ">", "\"", "|", "?", "*"} {
		name = strings.ReplaceAll(name, c, "_")
	}
	// 强制 .strm 后缀，若已有则不重复
	if !strings.EqualFold(filepath.Ext(name), ".strm") {
		name = name + ".strm"
	}
	return name
}
