package model

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// Settings 对应 settings.json 顶层结构
// 对齐 docs/配置项参考.md
type Settings struct {
	UserAgent          string   `json:"user-agent"`
	InternalToken      string   `json:"internalToken"`
	StrmExtensions     []string `json:"strmExtensions"`
	DownloadExtensions []string `json:"downloadExtensions"`
	MediaMountPath     []string `json:"mediaMountPath"`
	StrmPrefix         string   `json:"strmPrefix"`
	// EnablePathEncoding URL 参数编码开关（默认开启）：对 account / pickcode / file_name
	// 等 URL 参数做百分号编码；关闭后原样写入（文件名含 # & ? 空格时会破坏 URL 解析）。
	EnablePathEncoding bool                `json:"enablePathEncoding"`
	Download           DownloadSettings    `json:"download"`
	Strm               StrmSettings        `json:"strm"`
	RateLimit          RateLimitSettings   `json:"rateLimit"`
	Emby               EmbySettings        `json:"emby"`
	Telegram           TelegramSettings    `json:"telegram"`
	LifeMonitor        LifeMonitorSettings `json:"lifeMonitor"`
	// CleanupDefaultsV2 标记「清理默认值已迁移」，避免每次启动反复覆盖用户的选择
	CleanupDefaultsV2 bool `json:"cleanupDefaultsV2"`
	// CleanupDefaultsV3 标记「破坏性连带动作已关闭」的一次性迁移（v1.5）
	CleanupDefaultsV3 bool                  `json:"cleanupDefaultsV3"`
	Cleanup           CleanupSettings       `json:"cleanup"`
	CookieInspect     CookieInspectSettings `json:"cookieInspect"`
}

// RateLimitSettings 115 API 限流配置（按「账号 × 用途」分桶）
//
// 背景：扫描列目录与播放取直链曾共用一个 60 次/分的全局桶，
// 大库全量扫描时播放取直链会被饿死（表现为播放转圈、超时）。
// 分桶后扫描与播放互不影响；GlobalCapQpm 作为多账号叠加时的风控兜底。
//
// 注意：本配置在服务启动时生效，修改后需重启（令牌桶会按新额度重建）。
type RateLimitSettings struct {
	// API115Qpm 扫描/目录/元数据等常规调用，每账号每分钟，默认 60
	API115Qpm int `json:"api115Qpm"`
	// PlaybackQpm 播放取直链，每账号每分钟，默认 120
	PlaybackQpm int `json:"playbackQpm"`
	// LifeQpm 生活事件轮询，每账号每分钟，默认 60
	LifeQpm int `json:"lifeQpm"`
	// GlobalCapQpm 全进程兜底上限，默认 300；负数表示关闭兜底
	GlobalCapQpm int `json:"globalCapQpm"`
}

// CookieInspectSettings 115 账号 Cookie 主动巡检配置。
// 用于补齐「未被生活事件监控覆盖」的账号状态盲区：定时用权威探测接口验证 Cookie 存活。
type CookieInspectSettings struct {
	// Enabled 是否启用主动巡检；nil 视为 true（默认开启，老配置升级后仍生效）
	Enabled *bool `json:"enabled,omitempty"`
	// IntervalHours 巡检间隔（小时），默认 6；<=0 时使用默认值
	IntervalHours int `json:"intervalHours,omitempty"`
}

// DefaultCookieInspectIntervalHours 主动巡检默认间隔（小时）
const DefaultCookieInspectIntervalHours = 6

// IsEnabled 返回主动巡检是否启用（未显式配置时默认开启）
func (c *CookieInspectSettings) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// EffectiveIntervalHours 返回生效的巡检间隔（小时），非法值回退默认
func (c *CookieInspectSettings) EffectiveIntervalHours() int {
	if c.IntervalHours <= 0 {
		return DefaultCookieInspectIntervalHours
	}
	return c.IntervalHours
}

// CleanupSettings STRM 清理对账配置（参考 MoviePilot p115strmhelper full_sync_remove_unless_* + cleanup_confirm_mode）
//
// 三重保护（任一超限即进入保护态，只停删除不停生成）：
//   - MaxRatio  比例上限：待删数 / 本地总数
//   - MaxThreshold 绝对上限：待删数
//   - MaxDirs   目录数上限：待删涉及的目录数
//
// 为什么不再只用单阈值：默认 10 个的绝对阈值在大库里形同虚设——
// 网盘移走 11 个文件就会整批拒绝，自动清理基本失效；而单纯调大又失去防误删能力。
type CleanupSettings struct {
	// MaxThreshold 单次清理删除数绝对上限，超过则进入保护态
	// 语义变更（v1.5.0）：由「硬拒绝阈值」改为「绝对上限」，默认 1000
	MaxThreshold int `json:"maxThreshold"`
	// MaxRatio 单次清理删除比例上限（0-1），默认 0.10；<=0 时使用默认值
	MaxRatio float64 `json:"maxRatio"`
	// MaxDirs 单次清理涉及目录数上限，默认 20；<=0 时使用默认值
	MaxDirs int `json:"maxDirs"`
	// StableThreshold 稳定阈值，超过此值但未触发保护时，需二次确认
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
	DownloadMaxConcurrent *int     `json:"downloadMaxConcurrent,omitempty"`
	StrmMaxConcurrent     *int     `json:"strmMaxConcurrent,omitempty"` // STRM 写入并发数，默认 20
	AutoDownloadMetadata  bool     `json:"autoDownloadMetadata"`        // 是否自动下载媒体元数据（nfo/jpg/png 等）
	MinFileSize           int64    `json:"minFileSize"`                 // 全量同步/任务执行 最小文件大小阈值（字节），默认0不限制
	StrmGenerateBlacklist []string `json:"strmGenerateBlacklist"`       // STRM 生成文件名黑名单关键词列表（如 "*trailer*", "sample.*"），大小写不敏感
	OverwriteMode         string   `json:"overwriteMode"`               // STRM 覆盖模式："always"(默认覆盖) / "never"(已存在则跳过)
	// P2-3 增量同步：以 files 表为 snapshot，对 pickcode+name+size 完全一致的条目跳过写 STRM/下载，仅处理变化的部分
	// P0-1 增量对账清理：增量同步结束后，扫描 TargetPath 下所有 .strm 文件，
	// 提取内容中的 pickcode 与云端快照比对，孤儿 STRM 按以下模式处理：
	//   "off"        默认。不清理（保持原行为）
	//   "mark_only"  仅日志记录孤儿路径，不删除
	//   "auto_clean" 自动软删（.deleted.bak），超阈值时复用 CleanupSettings 二次确认流程
	// 对齐参考项目 increment_sync_remove_unless_strm
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
	// ForceProxyFormats 需要服务端代理流（而非 302 直连）的格式列表。
	// 支持容器名或扩展名，大小写不敏感、点号可省略（如 "iso" / ".ts" 等价）。
	//
	// 背景：这类格式在 302 之后由客户端直连 115 CDN 时，可能因 CDN 的 UA 绑定
	// 或签名过期导致 Range 请求被拒，因此改由服务端代理转发以保证 seek 正常。
	//
	// 默认值为原盘类格式 + ts/m2ts；空则使用内置默认值。
	// 注意：.ts 是常见的录播格式而非原盘，若带宽紧张可把它从列表中移除。
	ForceProxyFormats []string `json:"forceProxyFormats"`
	// RangeProxy 代理流是否按 Range 分片并发拉取（默认开）。
	// 大原盘单连接带宽不足会卡顿/超时，分片并发可显著改善；关闭则回退单连接 io.Copy。
	// 用 Disable 语义：零值 = 启用，旧配置文件无需迁移。
	DisableRangeProxy bool `json:"disableRangeProxy"`
	// ProxyChunkSizeMB 分片大小（MB），默认 10。
	ProxyChunkSizeMB int `json:"proxyChunkSizeMB"`
	// ProxyConcurrency 单次播放的分片并发数，默认 2。
	// 每个播放请求占用 1 个代理槽位；分片会额外建立 CDN 连接，不等同于槽位数。
	ProxyConcurrency int `json:"proxyConcurrency"`
	// DisableSeekFormatProxy 关闭「按格式强制代理」（两层统一开关）。
	//
	// faststrm 有两处按格式强制代理的决策点，二者共用本开关与 ForceProxyFormats：
	//  1. EmbyProxy 层（Emby 反代拦截 media stream）
	//  2. STRM 端点层（/api/strm 决策 302 还是代理流）
	//
	// 开启（默认）：列表内格式在两层都走服务端代理，保证 ISO/BDMV 等原盘的 Range 不被 CDN 拒绝。
	// 关闭：两层都不再按格式代理，原盘与普通格式一样走 UA 白名单 / 默认 302
	//（ISO 可能因 CDN 拒绝而播放失败）。
	DisableSeekFormatProxy bool `json:"disableSeekFormatProxy"`

	disableRangeProxySet      bool
	disableSeekFormatProxySet bool
}

// UnmarshalJSON 记录两个 bool 开关是否出现在 JSON 中，供 POST 合并时区分
// 「未提供」与「显式 false」；未提供时保留旧配置，避免旧前端把用户已关闭的开关重置。
func (s *StrmSettings) UnmarshalJSON(data []byte) error {
	type plain StrmSettings
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = StrmSettings(decoded)
	_, s.disableRangeProxySet = raw["disableRangeProxy"]
	_, s.disableSeekFormatProxySet = raw["disableSeekFormatProxy"]
	return nil
}

// DisableRangeProxyProvided 表示请求是否显式提供了 disableRangeProxy。
func (s *StrmSettings) DisableRangeProxyProvided() bool { return s != nil && s.disableRangeProxySet }

// DisableSeekFormatProxyProvided 表示请求是否显式提供了 disableSeekFormatProxy。
func (s *StrmSettings) DisableSeekFormatProxyProvided() bool {
	return s != nil && s.disableSeekFormatProxySet
}

// DefaultForceProxyFormats 默认强制代理格式（容器名 / 扩展名，均按小写匹配）。
//
// 两层（EmbyProxy / STRM 端点）共用这一份列表。
// 不含 ts：ts 是常见录播格式而非原盘，302 通常可用，没必要占 NAS 带宽。
var DefaultForceProxyFormats = []string{
	"iso", "bdmv", "bdav", "dvd", "bluray", "m2ts", "vob", "ifo", "bup",
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
	// Emby 反向代理（PlaybackInfo STRM 强制 DirectPlay）
	// 设为 0 或负数则不启动
	ProxyPort int `json:"proxyPort"` // 反代监听端口，默认 0（不启用）
	// ExternalPlayerEnabled 是否在 Web 详情页注入外部播放器起播按钮
	// （PotPlayer / VLC / Infuse / MPV），默认 false
	ExternalPlayerEnabled bool `json:"externalPlayerEnabled"`
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
	ProxyURL           string                 `json:"proxyUrl,omitempty"` // 支持 HTTP/SOCKS5 代理
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
	Enabled         bool                 `json:"enabled"`
	Accounts        []string             `json:"accounts"`
	PollInterval    int                  `json:"pollInterval"`
	PathMappings    []MonitorPathMapping `json:"pathMappings"`
	RemoveEmptyDirs bool                 `json:"removeEmptyDirs"`
	EventTypes      EventTypesSettings   `json:"eventTypes"`
	StrmPrefix      string               `json:"strmPrefix"`
	// EnablePathEncoding 生活监控侧的 URL 参数编码开关（与全局同名字段一致）；
	// 装配时若未显式设置则继承全局 Settings.EnablePathEncoding。
	EnablePathEncoding bool `json:"enablePathEncoding"`
	// StrmExtensions 生活监控生成 STRM 的视频扩展名；空则继承全局 StrmExtensions
	// （继承在 server 层装配时完成，见 wire.go 的 lifeSettingsFn）
	StrmExtensions        []string `json:"strmExtensions"`
	MinFileSize           int64    `json:"minFileSize"`           // 生活监控 最小文件大小阈值（字节），默认0不限制
	StrmGenerateBlacklist []string `json:"strmGenerateBlacklist"` // 生活监控 独立黑名单关键词列表；空则继承全局 Download.StrmGenerateBlacklist
	OverwriteMode         string   `json:"overwriteMode"`         // 生活监控 STRM 覆盖模式："always"(默认覆盖) / "never"(已存在则跳过)；空则继承全局
	FirstPullMode         string   `json:"firstPullMode"`
	MoveMediaMode         string   `json:"moveMediaMode"`
	AutoDownloadMetadata  bool     `json:"autoDownloadMetadata"` // 生活监控 STRM 创建后是否自动占位同名 nfo/jpg/png/srt 等关联资源（空则继承全局 Download.AutoDownloadMetadata）
	DownloadExtensions    []string `json:"downloadExtensions"`   // 生活监控 关联资源扩展名；空则继承全局 Download.DownloadExtensions
	// P1-4 高级模板（空则继承全局 StrmSettings 模板，仍为空则走默认拼接）
	StrmUrlTemplate      string `json:"strmUrlTemplate"`
	StrmFilenameTemplate string `json:"strmFilenameTemplate"`
	NotifyOnlyOnError    bool   `json:"notifyOnlyOnError"` // true=仅错误时发TG通知，正常操作不通知
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
	"iso",
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
		EnablePathEncoding: true, // URL 路径编码 —— 含特殊字符的路径不编码会挂
		Download: DownloadSettings{
			StrmMaxConcurrent:     intPtr(20), // STRM 写入并发数默认 20
			AutoDownloadMetadata:  true,       // 默认开启，保持向后兼容
			MinFileSize:           0,          // 默认不限制最小文件大小
			StrmGenerateBlacklist: []string{}, // 默认空黑名单
			OverwriteMode:         "always",   // 默认始终覆盖（与 MoviePilot 默认行为一致）
			// 孤儿 STRM 清理：只按 STRM 内容里的 pickcode 对账，不会碰用户自己的文件。
			FullSyncCleanupOrphans: "auto_clean",
		},
		Strm: StrmSettings{
			// ForceProxyUaTokens 仅对 STRM 端点层生效（命中则强制走 proxy）。
			// 默认预置已知「坑客户端」：这些客户端对 302 直链/签名/UA 绑定处理不佳，
			// 直连 115 CDN 时 Range 请求常被拒，表现为原盘/大文件播不出来。
			// 若带宽紧张，可在设置页清空（清空后全部直连）。
			ForceProxyUaTokens:           []string{"Infuse", "VidHub", "SenPlayer", "SenPlayerHD"},
			AccountProxyConcurrencyLimit: 8,
			RedirectCheckTimeoutMs:       5000,
			ForceProxyFormats:            append([]string{}, DefaultForceProxyFormats...),
			// 分片并发代理默认启用（DisableRangeProxy 零值 false 即启用）
			ProxyChunkSizeMB: 10,
			ProxyConcurrency: 2,
		},
		RateLimit: RateLimitSettings{
			API115Qpm:    60,  // 扫描/目录类，保守
			PlaybackQpm:  120, // 播放取直链，给足配额保证秒开
			LifeQpm:      60,  // 生活事件轮询
			GlobalCapQpm: 300, // 多账号叠加时的风控兜底
		},
		Emby: EmbySettings{
			NotifyMediaAdded:     true,
			NotifyMediaRemoved:   true,
			NotifyPlayback:       true,
			PlaybackShowProgress: true,
			RefreshOnCreate:      true,
			RefreshOnDelete:      true,
			DebounceSeconds:      10,
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
			AutoDownloadMetadata: true, // 默认与全局 Download.AutoDownloadMetadata 保持一致
			DownloadExtensions:   nil,  // 空则继承全局 DefaultDownloadExtensions
			EventTypes: EventTypesSettings{
				Create: true,
				Remove: true,
				Rename: true,
				Move:   true,
			},
			FirstPullMode:               "latest",
			MoveMediaMode:               "local_move",
			TransferStallTimeoutMinutes: 30,     // P0-5 默认30分钟无进展超时
			TransferWaitMode:            "skip", // P0-5 默认跳过超时事件
			RenameAutoRelatedFiles:      true,   // P0-6 默认自动重命名关联资源
			MoveLocalMoveRelatedFiles:   true,   // P0-6 默认自动搬移关联资源
			MoveMediaKeepOldStrm:        false,  // P0-7 默认删除旧STRM（对齐 recreate 原行为）
			MoveMediaCreateNewStrm:      true,   // P0-7 默认生成新STRM
			MoveOutRemoveLocalStrm:      true,   // P0-7 默认移出映射时删除旧STRM
		},
		Cleanup: CleanupSettings{
			MaxThreshold:       1000, // 绝对上限：大库一次清理上千个孤儿属正常
			MaxRatio:           0.10, // 比例上限 10%
			MaxDirs:            20,   // 涉及目录数上限
			StableThreshold:    5,    // 超过 5 个需二次确认
			ConfirmMode:        "none",
			RemoveStrm:         true,
			RemoveEmptyDirs:    false,
			RemoveRelatedFiles: false,
		},
		CookieInspect: CookieInspectSettings{
			Enabled:       boolPtr(true), // 默认开启主动巡检
			IntervalHours: DefaultCookieInspectIntervalHours,
		},
	}
}

// intPtr 返回 int 的指针（用于设置可选配置字段）
func intPtr(v int) *int {
	return &v
}

// boolPtr 返回 bool 的指针（用于三态可选配置字段）
func boolPtr(v bool) *bool {
	return &v
}

// ==================== time.Duration 便捷 getter ====================
// 字段保留 JSON 里的 int/int64（毫秒/秒/分钟单位，保持兼容），
// 内部实现统一用 time.Duration，避免调用方到处写 `time.Duration(x) * time.Millisecond`。

// RedirectCheckTimeout STRM 重定向可达性检查超时。默认 5s。
func (s *StrmSettings) RedirectCheckTimeout() time.Duration {
	if s.RedirectCheckTimeoutMs <= 0 {
		return 5 * time.Second
	}
	return time.Duration(s.RedirectCheckTimeoutMs) * time.Millisecond
}

// PollInterval 生活监控轮询间隔。默认 10s，最小值 1s，0 用默认。
func (l *LifeMonitorSettings) PollIntervalDur() time.Duration {
	if l.PollInterval <= 0 {
		return 10 * time.Second
	}
	return time.Duration(l.PollInterval) * time.Second
}

// RetryDelay 生活监控拉取失败重试间隔。默认 1s。
func (l *LifeMonitorSettings) RetryDelay() time.Duration {
	if l.RetryDelayMs <= 0 {
		return time.Second
	}
	return time.Duration(l.RetryDelayMs) * time.Millisecond
}

// TransferStallTimeout 单个事件处理无进展超时；0 表示不限制（返回 0 让调用方判断）。
func (l *LifeMonitorSettings) TransferStallTimeout() time.Duration {
	if l.TransferStallTimeoutMinutes <= 0 {
		return 0
	}
	return time.Duration(l.TransferStallTimeoutMinutes) * time.Minute
}

// DedupWindow 去重窗口。默认 24h。
func (l *LifeMonitorSettings) DedupWindow() time.Duration {
	if l.DedupWindowHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(l.DedupWindowHours) * time.Hour
}

// RateLimit 115 API 调用最小间隔。默认 1s。
func (l *LifeMonitorSettings) RateLimit() time.Duration {
	if l.RateLimitMs <= 0 {
		return time.Second
	}
	return time.Duration(l.RateLimitMs) * time.Millisecond
}

// ==================== P1-4 高级模板渲染 ====================

// StrmURLTemplateVars STRM URL 模板变量集。
//
// 变量命名同时兼容两套（对齐 p115strmhelper 便于模板迁移）：
//   - p115strmhelper 风格：{base_url} {pickcode} {file_id} {file_name} {file_path} {file_stem} {file_suffix}
//   - faststrm 既有风格：{prefix} {account} {pickcode} {filename} {ext} {stem}
//
// 编码约定：account/pickcode/filename/file_name/file_path 自动 URL QueryEscape
// （file_path 含 '/'，编码后为 %2F，适合放在 query 参数里）；
// file_path_raw 为不编码的原始路径，适合拼进 URL 路径段（如 /play/{file_path_raw}）。
// file_id / ext / file_suffix / stem / file_stem 原样输出。
type StrmURLTemplateVars struct {
	Template  string
	Prefix    string // STRM 前缀（服务地址），别名 {base_url}
	Account   string // 115 账号名
	PickCode  string // 17 位 pickcode（文件唯一标识，移动/改名不变）
	FileName  string // 文件名（含扩展名）
	Ext       string // 带 "." 的小写扩展名；无扩展则为 ""
	Stem      string // 文件名去扩展名；.iso 文件 stem = "name.iso"
	CloudPath string // 云端完整路径（相对映射根，形如 "电影/沙丘/x.mkv"）；{file_path} 编码版 / {file_path_raw} 原始版
	FileID    string // 云端 file_id；未知时为空串
	// DisableEncode 为 true 时所有变量原样输出（对应设置页「URL 编码」开关关闭）。
	// 零值 false = 编码（安全默认）。
	DisableEncode bool
}

// encode 按开关对模板变量值编码；默认用 URLPathEncode（空格 %20、保留 '/'）。
func (v StrmURLTemplateVars) encode(s string) string {
	if v.DisableEncode {
		return s
	}
	return strmutil.URLPathEncode(s)
}

// encodeStrict 完整编码（'/' 也编码为 %2F），用于把路径塞进单个 query 参数。
func (v StrmURLTemplateVars) encodeStrict(s string) string {
	if v.DisableEncode {
		return s
	}
	return strmutil.URLPathEncodeStrict(s)
}

// RenderStrmUrlTemplate 渲染 STRM URL 模板。
// 当 Template == "" 时返回空字符串，由调用方回退默认拼接逻辑。
func RenderStrmUrlTemplate(v StrmURLTemplateVars) string {
	if v.Template == "" {
		return ""
	}
	prefix := strings.TrimRight(v.Prefix, "/")
	encodedAccount := v.encode(v.Account)
	encodedPickcode := v.encode(v.PickCode)
	encodedFileName := v.encode(v.FileName)
	encodedFilePath := v.encode(v.CloudPath)
	encodedFilePathStrict := v.encodeStrict(v.CloudPath)
	r := strings.NewReplacer(
		"{prefix}", prefix,
		"{base_url}", prefix,
		"{account}", encodedAccount,
		"{pickcode}", encodedPickcode,
		"{filename}", encodedFileName,
		"{file_name}", encodedFileName,
		"{ext}", v.Ext,
		"{file_suffix}", v.Ext,
		"{stem}", v.Stem,
		"{file_stem}", v.Stem,
		"{file_path}", encodedFilePath,
		"{file_path_raw}", v.CloudPath,
		// 完整编码（'/' → %2F）：用于精简路径式 URL 的 path 参数
		"{file_path_strict}", encodedFilePathStrict,
		"{file_id}", v.FileID,
	)
	result := r.Replace(v.Template)
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
	// 清理文件名非法字符：统一走 strmutil（冒号转全角、其余特殊字符转下划线），
	// 与任务侧/监控侧的目录段清理保持同一套规则
	name = strmutil.SanitizeFileName(name)
	// 强制 .strm 后缀，若已有则不重复
	if !strings.EqualFold(filepath.Ext(name), ".strm") {
		name = name + ".strm"
	}
	return name
}

// ==================== BDMV 原盘过滤 ====================

// IsBdmvStreamPath 判断路径是否位于蓝光原盘的 BDMV/STREAM 视频流目录内。
// 用途：跳过 BDMV 原盘的 m2ts 视频流，避免一个原盘被拆成上百个 STRM（对齐参考项目
// p115strmhelper directory_upload_skip_bdmv_stream）。只按路径判定、不按扩展名，
// 因此独立的 .m2ts / .ts 文件（不在 BDMV/STREAM 结构内）仍照常生成。
// 大小写不敏感，同时兼容 "/" 与 "\"。
func IsBdmvStreamPath(p string) bool {
	if p == "" {
		return false
	}
	// 用 ReplaceAll 而非 filepath.ToSlash：后者只替换当前系统的分隔符，
	// 在 Linux 上不会把 "\" 转成 "/"，导致跨平台的判定结果不一致。
	s := "/" + strings.Trim(strings.ToLower(strings.ReplaceAll(p, `\`, "/")), "/") + "/"
	return strings.Contains(s, "/bdmv/stream/")
}
