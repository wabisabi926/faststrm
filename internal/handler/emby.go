package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// EmbyDeps Emby 相关 handler 依赖
type EmbyDeps struct {
	SettingsStore *store.SettingsStore
	EmbyNotifier  *emby.Notifier
	EmbyClient    *emby.Client
	SyncDelete    *emby.SyncDelete
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

// HandleEmbyWebhook POST /api/emby/webhook （公开，无 JWT）
// 接受 application/json 或 form-data 中 data=<JSON 字符串>，分发到 EmbyNotifier
func HandleEmbyWebhook(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EmbyNotifier == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "EmbyNotifier 未初始化"})
			return
		}

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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

// embySettingsPatch POST /api/emby/settings 局部 patch（仅允许白名单字段）
type embySettingsPatch struct {
	URL                    *string                        `json:"url,omitempty"`
	APIKey                 *string                        `json:"apiKey,omitempty"`
	NotifyMediaAdded       *bool                          `json:"notifyMediaAdded,omitempty"`
	NotifyMediaRemoved     *bool                          `json:"notifyMediaRemoved,omitempty"`
	NotifyPlayback         *bool                          `json:"notifyPlayback,omitempty"`
	PlaybackShowProgress   *bool                          `json:"playbackShowProgress,omitempty"`
	PlaybackShowOverview   *bool                          `json:"playbackShowOverview,omitempty"`
	WebhookAuth            *string                        `json:"webhookAuth,omitempty"`
	LibraryID              *string                        `json:"libraryId,omitempty"`
	SyncDeleteEnabled      *bool                          `json:"syncDeleteEnabled,omitempty"`
	SyncDeletePathMappings *[]model.SyncDeletePathMapping `json:"syncDeletePathMappings,omitempty"`
	SyncDeleteNotify       *bool                          `json:"syncDeleteNotify,omitempty"`
	SyncDeleteDryRun       *bool                          `json:"syncDeleteDryRun,omitempty"`
}

// HandleEmbySettingsPOST POST /api/emby/settings 局部 patch 保存 Emby 设置
func HandleEmbySettingsPOST(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var patch embySettingsPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		em := settings.Emby

		if patch.URL != nil {
			em.URL = *patch.URL
		}
		if patch.APIKey != nil {
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
			mappings := *patch.SyncDeletePathMappings
			cleaned := make([]model.SyncDeletePathMapping, 0, len(mappings))
			for _, m := range mappings {
				if m.EmbyPath == "" || m.CloudPath == "" {
					continue
				}
				cleaned = append(cleaned, m)
			}
			em.SyncDeletePathMappings = cleaned
		}
		if patch.SyncDeleteNotify != nil {
			em.SyncDeleteNotify = *patch.SyncDeleteNotify
		}
		if patch.SyncDeleteDryRun != nil {
			em.SyncDeleteDryRun = *patch.SyncDeleteDryRun
		}
		settings.Emby = em

		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			logger.S().Errorf("[emby/settings POST] save: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}

		// 保存后清除 Notifier 的 Client 缓存，确保下次获取最新配置
		if deps.EmbyNotifier != nil {
			deps.EmbyNotifier.InvalidateClientCache()
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
		req.URL = strings.TrimSpace(req.URL)
		req.APIKey = strings.TrimSpace(req.APIKey)
		if req.URL == "" || req.APIKey == "" {
			httpx.OkJson(w, map[string]any{
				"success": false,
				"message": "请先填写 Emby URL 和 API Key",
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
	Enabled              *bool    `json:"enabled"`
	Accounts             []string `json:"accounts"`
	PollInterval         int      `json:"pollInterval"`
	PathMappings         any      `json:"pathMappings"`
	RemoveEmptyDirs      *bool    `json:"removeEmptyDirs"`
	EventTypes           any      `json:"eventTypes"`
	MinFileSize          int64    `json:"minFileSize"`
	FirstPullMode        string   `json:"firstPullMode"`
	MoveMediaMode        string   `json:"moveMediaMode"`
	StrmPrefix           string   `json:"strmPrefix"`
	EnablePathEncoding   *bool    `json:"enablePathEncoding"`
	Enable302            *bool    `json:"enable302"`
	AutoDownloadMetadata *bool    `json:"autoDownloadMetadata"`
	DownloadExtensions   []string `json:"downloadExtensions"`
	NotifyOnlyOnError    *bool    `json:"notifyOnlyOnError"`
}

// settingsPostBody 前端 Settings 页提交的嵌套 JSON 结构
// 与 model.Settings 的 JSON 标签对齐，兼容 React SPA 的保存格式
type settingsPostBody struct {
	UserAgent          string                  `json:"user-agent"`
	StrmExtensions     []string                `json:"strmExtensions"`
	DownloadExtensions []string                `json:"downloadExtensions"`
	MediaMountPath     []string                `json:"mediaMountPath"`
	StrmPrefix         string                  `json:"strmPrefix"`
	EnablePathEncoding bool                    `json:"enablePathEncoding"`
	Enable302          bool                    `json:"enable302"`
	RemoveExtraFiles   bool                    `json:"removeExtraFiles"`
	Download           *model.DownloadSettings `json:"download"`
	Strm               *model.StrmSettings     `json:"strm"`
	Emby               *model.EmbySettings     `json:"emby"`
	Telegram           *model.TelegramSettings `json:"telegram"`
	LifeMonitor        *LifeMonitorPatch       `json:"lifeMonitor"`
}

// containsAsterisk 判定是否是脱敏值（含 *）
func containsAsterisk(s string) bool { return strings.Contains(s, "*") }

// HandleSettingsPOST POST /api/settings 接受 React SPA 的嵌套 JSON 提交
// 前端通过 saveData 发送完整设置（含 strm/lifeMonitor/download/emby/telegram 嵌套对象）
// 后端解码后与现有设置合并：字符串非空才覆盖、数组直接覆盖、敏感字段脱敏值跳过
// mediaMountPath 由 SSOT 自动管理，不从保存请求覆盖
func HandleSettingsPOST(deps EmbyDeps) http.HandlerFunc { //nolint:cyclop // complexity: 51
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Errorf("[settings POST] read settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}

		var body settingsPostBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			logger.S().Warnf("[settings POST] JSON decode failed: %v", err)
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		// ====== 通用字段 ======
		if body.UserAgent != "" {
			settings.UserAgent = strings.TrimSpace(body.UserAgent)
		}
		if body.StrmPrefix != "" {
			settings.StrmPrefix = strings.TrimSpace(body.StrmPrefix)
		}
		if len(body.StrmExtensions) > 0 {
			settings.StrmExtensions = body.StrmExtensions
		}
		if len(body.DownloadExtensions) > 0 {
			settings.DownloadExtensions = body.DownloadExtensions
		}
		// mediaMountPath 由 SSOT 自动管理，不从保存请求覆盖

		// ====== 布尔/数值字段（前端总是发送全量，直接覆盖） ======
		settings.EnablePathEncoding = body.EnablePathEncoding
		settings.Enable302 = body.Enable302
		settings.RemoveExtraFiles = body.RemoveExtraFiles

		// ====== Download 嵌套对象 ======
		if body.Download != nil {
			if body.Download.LinkMaxPerSecond != nil {
				settings.Download.LinkMaxPerSecond = body.Download.LinkMaxPerSecond
			}
			if body.Download.LinkMaxConcurrent != nil {
				settings.Download.LinkMaxConcurrent = body.Download.LinkMaxConcurrent
			}
			if body.Download.DownloadMaxConcurrent != nil {
				settings.Download.DownloadMaxConcurrent = body.Download.DownloadMaxConcurrent
			}
			settings.Download.AutoDownloadMetadata = body.Download.AutoDownloadMetadata
		}

		// ====== STRM 嵌套对象 ======
		if body.Strm != nil {
			if len(body.Strm.ForceProxyUaTokens) > 0 {
				settings.Strm.ForceProxyUaTokens = body.Strm.ForceProxyUaTokens
			}
			if body.Strm.AccountProxyConcurrencyLimit > 0 {
				settings.Strm.AccountProxyConcurrencyLimit = body.Strm.AccountProxyConcurrencyLimit
			}
			// RedirectCheckTimeout 最小 500ms，低于此值视为无效配置（不更新）
			if time.Duration(body.Strm.RedirectCheckTimeoutMs)*time.Millisecond >= 500*time.Millisecond {
				settings.Strm.RedirectCheckTimeoutMs = body.Strm.RedirectCheckTimeoutMs
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
				em.SyncDeletePathMappings = body.Emby.SyncDeletePathMappings
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
			if body.LifeMonitor.MinFileSize > 0 {
				lm.MinFileSize = body.LifeMonitor.MinFileSize
			}
			if body.LifeMonitor.FirstPullMode != "" {
				lm.FirstPullMode = body.LifeMonitor.FirstPullMode
			}
			if body.LifeMonitor.MoveMediaMode != "" {
				lm.MoveMediaMode = body.LifeMonitor.MoveMediaMode
			}
			if body.LifeMonitor.StrmPrefix != "" {
				lm.StrmPrefix = body.LifeMonitor.StrmPrefix
			}
			if body.LifeMonitor.EnablePathEncoding != nil {
				lm.EnablePathEncoding = *body.LifeMonitor.EnablePathEncoding
			}
			if body.LifeMonitor.Enable302 != nil {
				lm.Enable302 = *body.LifeMonitor.Enable302
			}
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

		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			logger.S().Errorf("[settings POST] save: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}

		// 返回保存后的聚合（脱敏）供前端对比
		saved := *settings
		saved.InternalToken = ""
		saved.Emby.APIKey = maskAPIKey(settings.Emby.APIKey)
		saved.Telegram.BotToken = maskBotToken(settings.Telegram.BotToken)
		saved.Telegram.WebhookSecretToken = ""
		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "设置已保存",
			"saved":   saved,
		})
	}
}
