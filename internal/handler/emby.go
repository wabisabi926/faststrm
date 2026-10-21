package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/embyproxy"
	"github.com/wabisabi926/faststrm/internal/service/rate"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// EmbyDeps Emby 相关 handler 依赖
type EmbyDeps struct {
	SettingsStore    *store.SettingsStore
	EmbyNotifier     *emby.Notifier
	EmbyClient       *emby.Client
	SyncDelete       *emby.SyncDelete
	EmbyProxyManager *embyproxy.Manager
}

// ==================== 辅助 ====================

// maskAPIKey 脱敏 Emby API Key
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	tail := 4
	masked := strings.Repeat("*", len(key)-8)
	return key[:4] + masked + key[len(key)-tail:]
}

// ==================== Webhook（公开回调） ====================

// embyWebhookAuthorized 校验 Emby Webhook 共享密钥。
// 支持 ?auth=、X-Webhook-Auth 或 Authorization: Bearer <token>。
// expected 为空时失败关闭：Webhook 可以触发删除同步，不能依赖“默认放行”。
func embyWebhookAuthorized(r *http.Request, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get("X-Webhook-Auth"))
	if provided == "" {
		provided = strings.TrimSpace(r.URL.Query().Get("auth"))
	}
	if provided == "" {
		authz := strings.TrimSpace(r.Header.Get("Authorization"))
		if len(authz) > len("Bearer ") && strings.EqualFold(authz[:len("Bearer ")], "Bearer ") {
			provided = strings.TrimSpace(authz[len("Bearer "):])
		}
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// sanitizeSyncDeleteMappings 过滤危险的删除同步映射：
// EmbyPath 必须是绝对路径、清理后无 .. 段；CloudPath 必须非空。
func sanitizeSyncDeleteMappings(mappings []model.SyncDeletePathMapping) []model.SyncDeletePathMapping {
	cleaned := make([]model.SyncDeletePathMapping, 0, len(mappings))
	for _, m := range mappings {
		embyPath := strings.TrimSpace(m.EmbyPath)
		cloudPath := strings.TrimSpace(m.CloudPath)
		if embyPath == "" || cloudPath == "" || strings.Contains(embyPath, "..") {
			continue
		}
		// 配置可能来自 Linux 容器（/mnt/...）也可能来自 Windows（D:\...）；
		// 在 Windows 上开发/测试时仍要保留 Unix 绝对路径的原始形态。
		if strings.HasPrefix(embyPath, "/") {
			m.EmbyPath = path.Clean(embyPath)
		} else if filepath.IsAbs(embyPath) {
			m.EmbyPath = filepath.Clean(embyPath)
		} else {
			continue
		}
		m.CloudPath = strings.TrimRight(strings.ReplaceAll(cloudPath, "\\", "/"), "/")
		if m.CloudPath == "" {
			continue
		}
		cleaned = append(cleaned, m)
	}
	return cleaned
}

// HandleEmbyWebhook POST /api/emby/webhook （公开，无 JWT）
// 接受 application/json 或 form-data 中 data=<JSON 字符串>，分发到 EmbyNotifier
func HandleEmbyWebhook(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EmbyNotifier == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "EmbyNotifier 未初始化"})
			return
		}
		if deps.SettingsStore == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "settings unavailable"})
			return
		}
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil || settings == nil {
			logger.S().Errorf("[emby/webhook] 读取设置失败，拒绝请求: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "settings unavailable"})
			return
		}
		if !embyWebhookAuthorized(r, settings.Emby.WebhookAuth) {
			logger.S().Warnf("[emby/webhook] 鉴权失败 remote=%s", r.RemoteAddr)
			httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized：请先在 Emby 通知设置中配置 Webhook 鉴权密钥",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var event emby.WebhookEvent
		contentType := r.Header.Get("Content-Type")

		switch {
		case strings.Contains(contentType, "application/json"):
			if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
		default:
			// Emby 默认 form-data：data=<JSON 字符串>
			rawData := r.FormValue("data")
			if rawData == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "missing data field"})
				return
			}
			if err := json.Unmarshal([]byte(rawData), &event); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON in data field"})
				return
			}
		}

		// 诊断日志：确认 webhook 是否真的收到（入库不通知时优先看这里）
		// 如果入库时这条日志没出现，说明 Emby 端没发 webhook（检查 Emby webhook 事件类型是否勾选了"新建项"）
		itemName := ""
		itemType := ""
		if event.Item != nil {
			itemName = event.Item.Name
			itemType = event.Item.Type
		}
		logger.S().Infof("[emby/webhook] 收到 webhook: Event=%q Item=%q Type=%q",
			event.Event, itemName, itemType)

		// 异步处理，不阻塞 webhook 响应
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := deps.EmbyNotifier.HandleWebhookEvent(ctx, event); err != nil {
				logger.S().Errorf("[emby/webhook] handle event failed: %v", err)
			}
		}()

		httpx.OkJson(w, map[string]bool{"ok": true})
	}
}

// ==================== Settings ====================

// HandleEmbySettingsGET GET /api/emby/settings 返回 Emby 配置（apiKey 脱敏）
func HandleEmbySettingsGET(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		em := settings.Emby
		// 返回前对 apiKey 脱敏，避免明文回前端
		masked := em
		masked.APIKey = maskAPIKey(em.APIKey)
		httpx.OkJson(w, masked)
	}
}

// HandleEmbyProxyStatus GET /api/emby/proxy/status
// 只读诊断：反代是否运行、监听地址、源地址与外部播放器注入开关。
func HandleEmbyProxyStatus(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EmbyProxyManager == nil {
			httpx.OkJson(w, map[string]any{
				"running":               false,
				"externalPlayerEnabled": false,
			})
			return
		}
		st := deps.EmbyProxyManager.Status()
		httpx.OkJson(w, map[string]any{
			"running":               st.Running,
			"addr":                  st.Addr,
			"embyURL":               st.EmbyURL,
			"externalPlayerEnabled": st.ExternalPlayerEnabled,
		})
	}
}

// embySettingsPatch POST /api/emby/settings 局部 patch（仅允许白名单字段）
type embySettingsPatch struct {
	URL                      *string                        `json:"url,omitempty"`
	APIKey                   *string                        `json:"apiKey,omitempty"`
	NotifyMediaAdded         *bool                          `json:"notifyMediaAdded,omitempty"`
	NotifyMediaRemoved       *bool                          `json:"notifyMediaRemoved,omitempty"`
	NotifyPlayback           *bool                          `json:"notifyPlayback,omitempty"`
	PlaybackShowProgress     *bool                          `json:"playbackShowProgress,omitempty"`
	PlaybackShowOverview     *bool                          `json:"playbackShowOverview,omitempty"`
	WebhookAuth              *string                        `json:"webhookAuth,omitempty"`
	LibraryID                *string                        `json:"libraryId,omitempty"`
	SyncDeleteEnabled        *bool                          `json:"syncDeleteEnabled,omitempty"`
	SyncDeletePathMappings   *[]model.SyncDeletePathMapping `json:"syncDeletePathMappings,omitempty"`
	SyncDeleteNotify         *bool                          `json:"syncDeleteNotify,omitempty"`
	SyncDeleteDryRun         *bool                          `json:"syncDeleteDryRun,omitempty"`
	SyncDeleteDeleteSymlink  *bool                          `json:"syncDeleteDeleteSymlink,omitempty"`
	SyncDeleteRemoveVersions *bool                          `json:"syncDeleteRemoveVersions,omitempty"`
	SyncDeleteSource         *bool                          `json:"syncDeleteSource,omitempty"`
	RefreshOnCreate          *bool                          `json:"refreshOnCreate,omitempty"`
	RefreshOnDelete          *bool                          `json:"refreshOnDelete,omitempty"`
	DebounceSeconds          *int                           `json:"debounceSeconds,omitempty"`
	ProxyPort                *int                           `json:"proxyPort,omitempty"`
	ExternalPlayerEnabled    *bool                          `json:"externalPlayerEnabled,omitempty"`
}

// HandleEmbySettingsPOST POST /api/emby/settings 局部 patch 保存 Emby 设置
//
//nolint:cyclop // complexity: 36；含 EmbyProxyManager 热重启分支，与 HandleSettingsPOST(51) 同属设置 POST handler 高复杂度家族
func HandleEmbySettingsPOST(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var patch embySettingsPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		savedSettings, err := deps.SettingsStore.Update(func(settings *model.Settings) error {
			em := settings.Emby

			if patch.URL != nil {
				em.URL = *patch.URL
			}
			if patch.APIKey != nil && !containsAsterisk(*patch.APIKey) {
				em.APIKey = *patch.APIKey
			}
			if patch.NotifyMediaAdded != nil {
				em.NotifyMediaAdded = *patch.NotifyMediaAdded
			}
			if patch.NotifyMediaRemoved != nil {
				em.NotifyMediaRemoved = *patch.NotifyMediaRemoved
			}
			if patch.NotifyPlayback != nil {
				em.NotifyPlayback = *patch.NotifyPlayback
			}
			if patch.PlaybackShowProgress != nil {
				em.PlaybackShowProgress = *patch.PlaybackShowProgress
			}
			if patch.PlaybackShowOverview != nil {
				em.PlaybackShowOverview = *patch.PlaybackShowOverview
			}
			if patch.WebhookAuth != nil {
				em.WebhookAuth = *patch.WebhookAuth
			}
			if patch.LibraryID != nil {
				em.LibraryID = *patch.LibraryID
			}
			if patch.SyncDeleteEnabled != nil {
				em.SyncDeleteEnabled = *patch.SyncDeleteEnabled
			}
			if patch.SyncDeletePathMappings != nil {
				em.SyncDeletePathMappings = sanitizeSyncDeleteMappings(*patch.SyncDeletePathMappings)
			}
			if patch.SyncDeleteNotify != nil {
				em.SyncDeleteNotify = *patch.SyncDeleteNotify
			}
			if patch.SyncDeleteDryRun != nil {
				em.SyncDeleteDryRun = *patch.SyncDeleteDryRun
			}
			if patch.SyncDeleteDeleteSymlink != nil {
				em.SyncDeleteDeleteSymlink = *patch.SyncDeleteDeleteSymlink
			}
			if patch.SyncDeleteRemoveVersions != nil {
				em.SyncDeleteRemoveVersions = *patch.SyncDeleteRemoveVersions
			}
			if patch.SyncDeleteSource != nil {
				em.SyncDeleteSource = *patch.SyncDeleteSource
			}
			if patch.RefreshOnCreate != nil {
				em.RefreshOnCreate = *patch.RefreshOnCreate
			}
			if patch.RefreshOnDelete != nil {
				em.RefreshOnDelete = *patch.RefreshOnDelete
			}
			if patch.DebounceSeconds != nil {
				em.DebounceSeconds = *patch.DebounceSeconds
			}
			if patch.ProxyPort != nil {
				em.ProxyPort = *patch.ProxyPort
			}
			if patch.ExternalPlayerEnabled != nil {
				em.ExternalPlayerEnabled = *patch.ExternalPlayerEnabled
			}
			settings.Emby = em
			return nil
		})
		if err != nil {
			logger.S().Errorf("[emby/settings POST] update: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}
		em := savedSettings.Emby

		// 保存后清除 Notifier 的 Client 缓存，确保下次获取最新配置
		if deps.EmbyNotifier != nil {
			deps.EmbyNotifier.InvalidateClientCache()
		}

		// ============ EmbyProxy 热重启 ============
		// ProxyPort 或 Emby.URL 变更时自动启停反代，无需重启主程序
		if deps.EmbyProxyManager != nil {
			mgr := deps.EmbyProxyManager
			if em.ProxyPort > 0 && em.URL != "" {
				// 从 manager Status 判断当前 host，缺省沿用当前值或回退 0.0.0.0
				host := "0.0.0.0"
				if st := mgr.Status(); st.Running && st.Addr != "" {
					// 从当前 addr 解析 host 部分，避免改端口时丢 host
					if idx := strings.LastIndex(st.Addr, ":"); idx > 0 {
						host = st.Addr[:idx]
					}
				}
				forceProxyUaTokens := savedSettings.Strm.ForceProxyUaTokens
				// 强制代理格式热更新（空则回落内置默认值）
				embyproxy.SetForceProxyFormats(savedSettings.Strm.ForceProxyFormats)
				if err := mgr.Restart(host, em.ProxyPort, em.URL, forceProxyUaTokens); err != nil {
					logger.S().Warnf("[EmbyProxy] 热重启失败: %v", err)
				} else {
					logger.S().Infof("[EmbyProxy] 热重启完成: %s:%d → %s", host, em.ProxyPort, em.URL)
				}
			} else {
				// ProxyPort=0 或 Emby URL 为空 → 停服
				if st := mgr.Status(); st.Running {
					logger.S().Infof("[EmbyProxy] 配置已禁用，停服 %s", st.Addr)
					mgr.StopAll()
				}
			}
			// 外部播放器开关热更新（无需重启反代 server）
			mgr.SetExternalPlayers(em.ExternalPlayerEnabled)
		}

		// 返回保存后的配置（apiKey 脱敏）
		saved := em
		saved.APIKey = maskAPIKey(em.APIKey)
		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "Emby 通知设置已保存",
			"saved":   saved,
		})
	}
}

// ==================== Test Connection ====================

// embyTestConnRequest POST /api/emby/test-connection 测试请求
type embyTestConnRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

// resolveEmbyTestCredentials 解析测试连接凭据：
// 前端从 /api/settings 拿到的是脱敏 APIKey（含 *），空值/掩码值时回退到已保存明文，
// 否则页面加载后直接点“测试连接”会用掩码去请求 Emby，必然 401。
func resolveEmbyTestCredentials(deps EmbyDeps, url, apiKey string) (string, string) {
	url = strings.TrimSpace(url)
	apiKey = strings.TrimSpace(apiKey)
	if deps.SettingsStore == nil || (apiKey != "" && !containsAsterisk(apiKey)) {
		return url, apiKey
	}
	saved, err := deps.SettingsStore.ReadSettings()
	if err != nil || saved == nil {
		return url, apiKey
	}
	if url == "" {
		url = strings.TrimSpace(saved.Emby.URL)
	}
	if apiKey == "" || containsAsterisk(apiKey) {
		apiKey = strings.TrimSpace(saved.Emby.APIKey)
	}
	return url, apiKey
}

// HandleEmbyTestConnection POST /api/emby/test-connection 测试 Emby 连通性
// 接受 JSON body 或 query 参数；使用临时 emby.Client 调用 GetItemDetail 验证 URL+APIKey
func HandleEmbyTestConnection(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req embyTestConnRequest
		// 优先 JSON body
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		// 兼容 query 参数
		if req.URL == "" {
			req.URL = r.URL.Query().Get("url")
		}
		if req.APIKey == "" {
			req.APIKey = r.URL.Query().Get("apiKey")
		}
		req.URL, req.APIKey = resolveEmbyTestCredentials(deps, req.URL, req.APIKey)

		if req.URL == "" || req.APIKey == "" || containsAsterisk(req.APIKey) {
			httpx.OkJson(w, map[string]any{
				"success": false,
				"message": "请先填写 Emby URL 和 API Key（若页面显示的是脱敏 Key，请重新输入完整 Key）",
			})
			return
		}

		client := emby.NewClient(req.URL, req.APIKey)
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()

		// 用一个占位 itemID 触发实际 HTTP 请求；通过返回错误判定连通性
		detail, err := client.GetItemDetail(ctx, "1")
		if err == nil {
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "连接成功",
				"detail": map[string]any{
					"id":   detail.ID,
					"name": detail.Name,
					"type": detail.Type,
				},
			})
			return
		}

		errStr := err.Error()
		// 提取 HTTP 状态码：错误格式为 "emby status %d for item %s"
		var code int
		if n, _ := fmt.Sscanf(errStr, "emby status %d", &code); n == 1 {
			switch {
			case code == http.StatusUnauthorized || code == http.StatusForbidden:
				httpx.OkJson(w, map[string]any{
					"success": false,
					"message": fmt.Sprintf("API Key 无效或未授权 (HTTP %d)", code),
				})
			case code == http.StatusNotFound:
				// Item 不存在，但连接成功
				httpx.OkJson(w, map[string]any{
					"success": true,
					"message": "连接成功",
				})
			default:
				httpx.OkJson(w, map[string]any{
					"success": false,
					"message": fmt.Sprintf("Emby 返回 HTTP %d", code),
				})
			}
			return
		}

		// 网络层错误（DNS / 连接拒绝 / 超时）
		logger.S().Debugf("[emby/test-connection] network error: %v", err)
		httpx.OkJson(w, map[string]any{
			"success": false,
			"message": "连接失败：" + errStr,
		})
	}
}

// ==================== 聚合 Settings（通用/下载/STRM/高级 + Emby + Telegram + LifeMonitor） ====================

// HandleSettingsGET GET /api/settings 返回 settings 聚合视图（提供给设置页 tab 回填）
// 敏感字段：Emby.APIKey 脱敏，Telegram.BotToken 脱敏，InternalToken 不返回
func HandleSettingsGET(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Errorf("[settings GET] read settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		clone := *settings
		clone.InternalToken = "" // 前端只读占位，不回传明文
		clone.Emby.APIKey = maskAPIKey(settings.Emby.APIKey)
		clone.Telegram.BotToken = maskBotToken(settings.Telegram.BotToken)
		// Telegram.WebhookSecretToken 完全不回前端（前端亦无对应输入框）
		clone.Telegram.WebhookSecretToken = ""
		httpx.OkJson(w, clone)
	}
}

// LifeMonitorPatch 用于接收前端提交的 LifeMonitor 配置
// 指针类型字段（*bool）可以区分"未提供"和"显式设置为 false"
type LifeMonitorPatch struct {
	Enabled         *bool    `json:"enabled"`
	Accounts        []string `json:"accounts"`
	PollInterval    int      `json:"pollInterval"`
	PathMappings    any      `json:"pathMappings"`
	RemoveEmptyDirs *bool    `json:"removeEmptyDirs"`
	EventTypes      any      `json:"eventTypes"`
	MinFileSize     *int64   `json:"minFileSize"`
	FirstPullMode   string   `json:"firstPullMode"`
	MoveMediaMode   string   `json:"moveMediaMode"`
	// 移出映射（移到非媒体目录）时是否删除本地旧 STRM；默认 true
	MoveOutRemoveLocalStrm *bool    `json:"moveOutRemoveLocalStrm"`
	StrmPrefix             string   `json:"strmPrefix"`
	EnablePathEncoding     *bool    `json:"enablePathEncoding"`
	AutoDownloadMetadata   *bool    `json:"autoDownloadMetadata"`
	DownloadExtensions     []string `json:"downloadExtensions"`
	NotifyOnlyOnError      *bool    `json:"notifyOnlyOnError"`
}

// cleanupPatch 清理策略局部更新：指针区分“未提供”和显式 0/false。
type cleanupPatch struct {
	MaxThreshold       *int     `json:"maxThreshold"`
	MaxRatio           *float64 `json:"maxRatio"`
	MaxDirs            *int     `json:"maxDirs"`
	StableThreshold    *int     `json:"stableThreshold"`
	ConfirmMode        *string  `json:"confirmMode"`
	RemoveStrm         *bool    `json:"removeStrm"`
	RemoveEmptyDirs    *bool    `json:"removeEmptyDirs"`
	RemoveRelatedFiles *bool    `json:"removeRelatedFiles"`
}

// validateCleanupPatch 校验清理策略数值/枚举，返回空串表示通过。
func validateCleanupPatch(p *cleanupPatch) string {
	if p == nil {
		return ""
	}
	if p.MaxThreshold != nil && *p.MaxThreshold <= 0 {
		return "清理绝对上限 maxThreshold 必须大于 0"
	}
	if p.MaxDirs != nil && *p.MaxDirs <= 0 {
		return "清理目录数上限 maxDirs 必须大于 0"
	}
	if p.StableThreshold != nil && *p.StableThreshold < 0 {
		return "清理稳定阈值 stableThreshold 不能为负数"
	}
	if p.MaxRatio != nil && (*p.MaxRatio <= 0 || *p.MaxRatio > 1) {
		return "清理比例上限 maxRatio 必须在 (0,1] 范围内"
	}
	if p.ConfirmMode != nil {
		switch strings.TrimSpace(*p.ConfirmMode) {
		case "", "none", "plugin_ui", "telegram":
		default:
			return "清理确认模式 confirmMode 必须是 none/plugin_ui/telegram"
		}
	}
	return ""
}

// settingsPostBody 前端 Settings 页提交的嵌套 JSON 结构
// 与 model.Settings 的 JSON 标签对齐，兼容 React SPA 的保存格式
type settingsPostBody struct {
	UserAgent          *string  `json:"user-agent"`
	StrmExtensions     []string `json:"strmExtensions"`
	DownloadExtensions []string `json:"downloadExtensions"`
	MediaMountPath     []string `json:"mediaMountPath"`
	StrmPrefix         *string  `json:"strmPrefix"`
	// EnablePathEncoding URL 参数编码开关（默认开启）；指针区分「未提供」与显式 false
	EnablePathEncoding *bool                    `json:"enablePathEncoding"`
	Download           *model.DownloadSettings  `json:"download"`
	Strm               *model.StrmSettings      `json:"strm"`
	Emby               *model.EmbySettings      `json:"emby"`
	Telegram           *model.TelegramSettings  `json:"telegram"`
	LifeMonitor        *LifeMonitorPatch        `json:"lifeMonitor"`
	Cleanup            *cleanupPatch            `json:"cleanup"`
	RateLimit          *model.RateLimitSettings `json:"rateLimit"`
}

// containsAsterisk 判定是否是脱敏值（含 *）
func containsAsterisk(s string) bool { return strings.Contains(s, "*") }

// HandleSettingsPOST POST /api/settings 接受 React SPA 的嵌套 JSON 提交
// 前端通过 saveData 发送完整设置（含 strm/lifeMonitor/download/emby/telegram 嵌套对象）
// 后端解码后与现有设置合并：字符串非空才覆盖、数组直接覆盖、敏感字段脱敏值跳过
// mediaMountPath 由 SSOT 自动管理，不从保存请求覆盖
func HandleSettingsPOST(deps EmbyDeps) http.HandlerFunc { //nolint:cyclop // complexity: 51
	return func(w http.ResponseWriter, r *http.Request) {
		var body settingsPostBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			logger.S().Warnf("[settings POST] JSON decode failed: %v", err)
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		if msg := validateCleanupPatch(body.Cleanup); msg != "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}

		savedSettings, err := deps.SettingsStore.Update(func(settings *model.Settings) error {
			// ====== 通用字段（指针区分“未提供”和显式清空） ======
			if body.UserAgent != nil {
				settings.UserAgent = strings.TrimSpace(*body.UserAgent)
			}
			if body.StrmPrefix != nil {
				settings.StrmPrefix = strings.TrimSpace(*body.StrmPrefix)
			}
			if len(body.StrmExtensions) > 0 {
				settings.StrmExtensions = body.StrmExtensions
			}
			if len(body.DownloadExtensions) > 0 {
				settings.DownloadExtensions = body.DownloadExtensions
			}
			// mediaMountPath 由 SSOT 自动管理，不从保存请求覆盖

			// ====== 布尔/数值字段（前端总是发送全量，直接覆盖） ======
			// URL 参数编码开关（默认 true）：关闭后 file_name 等参数原样写入 URL
			if body.EnablePathEncoding != nil {
				settings.EnablePathEncoding = *body.EnablePathEncoding
			}

			// ====== Cleanup：阈值/确认模式/连带动作（指针字段，未提供的保持原值） ======
			if body.Cleanup != nil {
				c := settings.Cleanup
				if p := body.Cleanup.MaxThreshold; p != nil {
					c.MaxThreshold = *p
				}
				if p := body.Cleanup.MaxRatio; p != nil {
					c.MaxRatio = *p
				}
				if p := body.Cleanup.MaxDirs; p != nil {
					c.MaxDirs = *p
				}
				if p := body.Cleanup.StableThreshold; p != nil {
					c.StableThreshold = *p
				}
				if p := body.Cleanup.ConfirmMode; p != nil {
					mode := strings.TrimSpace(*p)
					if mode == "" {
						mode = "none"
					}
					c.ConfirmMode = mode
				}
				if p := body.Cleanup.RemoveStrm; p != nil {
					c.RemoveStrm = *p
				}
				if p := body.Cleanup.RemoveEmptyDirs; p != nil {
					c.RemoveEmptyDirs = *p
				}
				if p := body.Cleanup.RemoveRelatedFiles; p != nil {
					c.RemoveRelatedFiles = *p
				}
				settings.Cleanup = c
			}

			// ====== 115 API 限流（服务启动时生效，保存后需重启） ======
			if body.RateLimit != nil {
				settings.RateLimit = *body.RateLimit
			}

			// ====== Download 嵌套对象 ======
			if body.Download != nil {
				if body.Download.DownloadMaxConcurrent != nil {
					settings.Download.DownloadMaxConcurrent = body.Download.DownloadMaxConcurrent
				}
				if body.Download.StrmMaxConcurrent != nil {
					settings.Download.StrmMaxConcurrent = body.Download.StrmMaxConcurrent
				}
				settings.Download.AutoDownloadMetadata = body.Download.AutoDownloadMetadata
				// 全局文件过滤：最小文件大小阈值（字节），前端始终发送全量值，直接覆盖
				settings.Download.MinFileSize = body.Download.MinFileSize
				// 全局文件过滤：文件名黑名单（nil 表示未提供，保留旧值；空数组表示用户主动清空）
				if body.Download.StrmGenerateBlacklist != nil {
					settings.Download.StrmGenerateBlacklist = body.Download.StrmGenerateBlacklist
				}
				// STRM 覆盖模式："always"(默认覆盖) / "never"(已存在则跳过)；空值表示未提供，保留旧配置
				// 孤儿 STRM 清理策略：off / mark_only / auto_clean；空值表示未提供
				if body.Download.FullSyncCleanupOrphans != "" {
					settings.Download.FullSyncCleanupOrphans = body.Download.FullSyncCleanupOrphans
				}
			}

			// ====== STRM 嵌套对象 ======
			if body.Strm != nil {
				// STRM URL 模板：设置页「标准 URL + 附加参数勾选」生成的完整模板。
				// 空串视为未提供（保留旧值），兼容旧前端不发送该字段的情况。
				if body.Strm.StrmUrlTemplate != "" {
					settings.Strm.StrmUrlTemplate = body.Strm.StrmUrlTemplate
				}
				if body.Strm.StrmFilenameTemplate != "" {
					settings.Strm.StrmFilenameTemplate = body.Strm.StrmFilenameTemplate
				}
				// 空数组也允许覆盖：用户清空输入框时前端发送 []，必须能清掉旧值
				if body.Strm.ForceProxyUaTokens != nil {
					settings.Strm.ForceProxyUaTokens = body.Strm.ForceProxyUaTokens
				}
				if body.Strm.ForceProxyFormats != nil {
					settings.Strm.ForceProxyFormats = body.Strm.ForceProxyFormats
				}
				if body.Strm.AccountProxyConcurrencyLimit > 0 {
					settings.Strm.AccountProxyConcurrencyLimit = body.Strm.AccountProxyConcurrencyLimit
				}
				// RedirectCheckTimeout 最小 500ms，低于此值视为无效配置（不更新）
				if time.Duration(body.Strm.RedirectCheckTimeoutMs)*time.Millisecond >= 500*time.Millisecond {
					settings.Strm.RedirectCheckTimeoutMs = body.Strm.RedirectCheckTimeoutMs
				}
				// 分片并发代理：只有显式提供时才覆盖，旧前端/部分提交保留旧值
				if body.Strm.DisableRangeProxyProvided() {
					settings.Strm.DisableRangeProxy = body.Strm.DisableRangeProxy
				}
				if body.Strm.DisableSeekFormatProxyProvided() {
					settings.Strm.DisableSeekFormatProxy = body.Strm.DisableSeekFormatProxy
				}
				if v := body.Strm.ProxyChunkSizeMB; v >= 1 && v <= 64 {
					settings.Strm.ProxyChunkSizeMB = v
				}
				if v := body.Strm.ProxyConcurrency; v >= 1 && v <= 4 {
					settings.Strm.ProxyConcurrency = v
				}
			}

			// ====== Emby 嵌套对象 ======
			if body.Emby != nil {
				em := settings.Emby
				if body.Emby.URL != "" {
					em.URL = strings.TrimSpace(body.Emby.URL)
				}
				if body.Emby.APIKey != "" && !containsAsterisk(body.Emby.APIKey) {
					em.APIKey = strings.TrimSpace(body.Emby.APIKey)
				}
				if body.Emby.WebhookAuth != "" && !containsAsterisk(body.Emby.WebhookAuth) {
					em.WebhookAuth = strings.TrimSpace(body.Emby.WebhookAuth)
				}
				if body.Emby.LibraryID != "" {
					em.LibraryID = strings.TrimSpace(body.Emby.LibraryID)
				}
				em.NotifyMediaAdded = body.Emby.NotifyMediaAdded
				em.NotifyMediaRemoved = body.Emby.NotifyMediaRemoved
				em.NotifyPlayback = body.Emby.NotifyPlayback
				em.PlaybackShowProgress = body.Emby.PlaybackShowProgress
				em.PlaybackShowOverview = body.Emby.PlaybackShowOverview
				em.SyncDeleteEnabled = body.Emby.SyncDeleteEnabled
				em.SyncDeleteDryRun = body.Emby.SyncDeleteDryRun
				if body.Emby.SyncDeletePathMappings != nil {
					em.SyncDeletePathMappings = sanitizeSyncDeleteMappings(body.Emby.SyncDeletePathMappings)
				}
				settings.Emby = em
			}

			// ====== Telegram 嵌套对象 ======
			if body.Telegram != nil {
				tg := settings.Telegram
				if body.Telegram.BotToken != "" && !containsAsterisk(body.Telegram.BotToken) {
					tg.BotToken = strings.TrimSpace(body.Telegram.BotToken)
				}
				if body.Telegram.ChatID != "" {
					tg.ChatID = strings.TrimSpace(body.Telegram.ChatID)
				}
				if body.Telegram.WebhookURL != "" {
					tg.WebhookURL = strings.TrimSpace(body.Telegram.WebhookURL)
				}
				tg.Enabled = body.Telegram.Enabled
				tg.AutoPolling = body.Telegram.AutoPolling
				if body.Telegram.AllowedUsers != nil {
					tg.AllowedUsers = body.Telegram.AllowedUsers
				}
				if body.Telegram.AccountAlerts != nil {
					tg.AccountAlerts = body.Telegram.AccountAlerts
				}
				settings.Telegram = tg
			}

			// ====== LifeMonitor 嵌套对象 ======
			if body.LifeMonitor != nil {
				lm := settings.LifeMonitor
				// 使用指针类型检查，只有当字段被显式提供时才更新
				if body.LifeMonitor.Enabled != nil {
					lm.Enabled = *body.LifeMonitor.Enabled
				}
				if body.LifeMonitor.Accounts != nil {
					lm.Accounts = body.LifeMonitor.Accounts
				}
				if body.LifeMonitor.PollInterval > 0 {
					lm.PollInterval = body.LifeMonitor.PollInterval
				}
				if body.LifeMonitor.PathMappings != nil {
					// PathMappings 使用 any 类型接收，需要通过 JSON 转换
					if pathMappingsJSON, err := json.Marshal(body.LifeMonitor.PathMappings); err == nil {
						var mappings []model.MonitorPathMapping
						if err := json.Unmarshal(pathMappingsJSON, &mappings); err == nil {
							lm.PathMappings = mappings
						}
					}
				}
				if body.LifeMonitor.RemoveEmptyDirs != nil {
					lm.RemoveEmptyDirs = *body.LifeMonitor.RemoveEmptyDirs
				}
				if body.LifeMonitor.EventTypes != nil {
					if eventTypesJSON, err := json.Marshal(body.LifeMonitor.EventTypes); err == nil {
						var eventTypes model.EventTypesSettings
						if err := json.Unmarshal(eventTypesJSON, &eventTypes); err == nil {
							lm.EventTypes = eventTypes
						}
					}
				}
				if body.LifeMonitor.MinFileSize != nil {
					lm.MinFileSize = *body.LifeMonitor.MinFileSize
				}
				if body.LifeMonitor.FirstPullMode != "" {
					lm.FirstPullMode = body.LifeMonitor.FirstPullMode
				}
				if body.LifeMonitor.MoveMediaMode != "" {
					lm.MoveMediaMode = body.LifeMonitor.MoveMediaMode
				}
				// 移出映射时删除本地旧 STRM（默认 true；nil 表示旧前端未提交，保留旧值）
				if body.LifeMonitor.MoveOutRemoveLocalStrm != nil {
					lm.MoveOutRemoveLocalStrm = *body.LifeMonitor.MoveOutRemoveLocalStrm
				}
				if body.LifeMonitor.StrmPrefix != "" {
					lm.StrmPrefix = body.LifeMonitor.StrmPrefix
				}
				// life 侧没有独立编码开关：始终继承全局 Settings.EnablePathEncoding，
				// 旧前端若提交该字段也忽略，避免两套规则不一致
				if body.LifeMonitor.AutoDownloadMetadata != nil {
					lm.AutoDownloadMetadata = *body.LifeMonitor.AutoDownloadMetadata
				}
				if body.LifeMonitor.DownloadExtensions != nil {
					lm.DownloadExtensions = body.LifeMonitor.DownloadExtensions
				}
				if body.LifeMonitor.NotifyOnlyOnError != nil {
					lm.NotifyOnlyOnError = *body.LifeMonitor.NotifyOnlyOnError
				}
				settings.LifeMonitor = lm
			}

			return nil
		})
		if err != nil {
			logger.S().Errorf("[settings POST] update: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}

		// 限流参数保存即生效：重建各桶令牌额度，避免必须重启服务
		rate.ApplySettings(savedSettings)
		// 按格式强制代理：格式列表 + 两层统一开关，保存即生效
		embyproxy.SetForceProxyFormats(savedSettings.Strm.ForceProxyFormats)
		embyproxy.SetSeekFormatProxyEnabled(!savedSettings.Strm.DisableSeekFormatProxy)

		// 返回保存后的聚合（脱敏）供前端对比
		saved := *savedSettings
		saved.InternalToken = ""
		saved.Emby.APIKey = maskAPIKey(savedSettings.Emby.APIKey)
		saved.Telegram.BotToken = maskBotToken(savedSettings.Telegram.BotToken)
		saved.Telegram.WebhookSecretToken = ""
		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "设置已保存",
			"saved":   saved,
		})
	}
}
