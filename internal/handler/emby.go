package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
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

// ==================== 聚合 Settings（通用/下载/STRM/高级 + Emby + Telegram） ====================

// HandleSettingsGET GET /api/settings 返回 settings 聚合视图（提供给设置页 6 个 tab 回填）
// 敏感字段：Emby.APIKey 脱敏，Telegram.BotToken 脱敏，InternalToken 不返回（前端输入框为只读占位）
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

// settingsFormPost 扁平化 form 字段（与 settings.templ 的 input.name 一一对应）
// 数组字段：以英文逗号分隔；空字符串表示留空（空数组）
// 指针类型数字字段（download）：空字符串 → nil（保留默认）
// 注意：对于 BotToken/APIKey 若接收到的是脱敏值（含 ***）则保持原值不覆写
type settingsFormPost struct {
	// General
	StrmPrefix       string `form:"strmPrefix" json:"strmPrefix"`
	MediaMountPath   string `form:"mediaMountPath" json:"mediaMountPath"`
	EnablePathEnc    string `form:"enablePathEncoding" json:"enablePathEncoding"`
	Enable302        string `form:"enable302" json:"enable302"`
	RemoveExtraFiles string `form:"removeExtraFiles" json:"removeExtraFiles"`
	// Emby
	EmbyURL               string `form:"emby.url" json:"emby.url"`
	EmbyAPIKey            string `form:"emby.apiKey" json:"emby.apiKey"`
	EmbyWebhookAuth       string `form:"emby.webhookAuth" json:"emby.webhookAuth"`
	EmbyLibraryID         string `form:"emby.libraryId" json:"emby.libraryId"`
	EmbyNotifyAdded       string `form:"emby.notifyMediaAdded" json:"emby.notifyMediaAdded"`
	EmbyNotifyRemoved     string `form:"emby.notifyMediaRemoved" json:"emby.notifyMediaRemoved"`
	EmbyNotifyPlayback    string `form:"emby.notifyPlayback" json:"emby.notifyPlayback"`
	EmbySyncDeleteEnabled string `form:"emby.syncDeleteEnabled" json:"emby.syncDeleteEnabled"`
	EmbySyncDeleteDryRun  string `form:"emby.syncDeleteDryRun" json:"emby.syncDeleteDryRun"`
	// Telegram
	TgBotToken    string `form:"telegram.botToken" json:"telegram.botToken"`
	TgChatID      string `form:"telegram.chatId" json:"telegram.chatId"`
	TgWebhookURL  string `form:"telegram.webhookUrl" json:"telegram.webhookUrl"`
	TgEnabled     string `form:"telegram.enabled" json:"telegram.enabled"`
	TgAutoPolling string `form:"telegram.autoPolling" json:"telegram.autoPolling"`
	// Download
	DownloadLinkMaxPerSec     string `form:"download.linkMaxPerSecond" json:"download.linkMaxPerSecond"`
	DownloadLinkMaxConcurrent string `form:"download.linkMaxConcurrent" json:"download.linkMaxConcurrent"`
	DownloadMaxConcurrent     string `form:"download.downloadMaxConcurrent" json:"download.downloadMaxConcurrent"`
	// STRM
	StrmForceProxyUaTokens       string `form:"strm.forceProxyUaTokens" json:"strm.forceProxyUaTokens"`
	StrmAccountProxyConcurrency  string `form:"strm.accountProxyConcurrencyLimit" json:"strm.accountProxyConcurrencyLimit"`
	StrmRedirectCheckTimeoutMs   string `form:"strm.redirectCheckTimeoutMs" json:"strm.redirectCheckTimeoutMs"`
	// Advanced
	StrmExtensions     string `form:"strmExtensions" json:"strmExtensions"`
	DownloadExtensions string `form:"downloadExtensions" json:"downloadExtensions"`
	UserAgent          string `form:"user-agent" json:"user-agent"`
}

// parseCSV 解析英文逗号分隔字符串，去除空项
func parseCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseBoolStr "true"/"1"/"on" → true, 其他 → false
func parseBoolStr(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "on", "yes":
		return true
	}
	return false
}

// parseIntPtr 空串→nil；否则按十进制解析；解析失败返回 0 的指针并记录 warning（这里静默降级为 0 指针）
func parseIntPtr(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return nil
	}
	return &v
}

// parseInt 解析失败返回 0
func parseInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0
	}
	return v
}

// containsAsterisk 判定是否是脱敏值（含 *）
func containsAsterisk(s string) bool { return strings.Contains(s, "*") }

// HandleSettingsPOST POST /api/settings 扁平 form 提交（settings.templ 跨 tab 汇总提交）
// 支持 Content-Type: application/json 与 application/x-www-form-urlencoded / multipart/form-data
func HandleSettingsPOST(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Errorf("[settings POST] read settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}

		var p settingsFormPost
		ct := r.Header.Get("Content-Type")
		isJSON := strings.Contains(ct, "application/json")

		if isJSON {
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
		} else {
			if err := r.ParseForm(); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "表单解析失败"})
				return
			}
			p = settingsFormPost{
				StrmPrefix:       r.FormValue("strmPrefix"),
				MediaMountPath:   r.FormValue("mediaMountPath"),
				EnablePathEnc:    r.FormValue("enablePathEncoding"),
				Enable302:        r.FormValue("enable302"),
				RemoveExtraFiles: r.FormValue("removeExtraFiles"),

				EmbyURL:               r.FormValue("emby.url"),
				EmbyAPIKey:            r.FormValue("emby.apiKey"),
				EmbyWebhookAuth:       r.FormValue("emby.webhookAuth"),
				EmbyLibraryID:         r.FormValue("emby.libraryId"),
				EmbyNotifyAdded:       r.FormValue("emby.notifyMediaAdded"),
				EmbyNotifyRemoved:     r.FormValue("emby.notifyMediaRemoved"),
				EmbyNotifyPlayback:    r.FormValue("emby.notifyPlayback"),
				EmbySyncDeleteEnabled: r.FormValue("emby.syncDeleteEnabled"),
				EmbySyncDeleteDryRun:  r.FormValue("emby.syncDeleteDryRun"),

				TgBotToken:    r.FormValue("telegram.botToken"),
				TgChatID:      r.FormValue("telegram.chatId"),
				TgWebhookURL:  r.FormValue("telegram.webhookUrl"),
				TgEnabled:     r.FormValue("telegram.enabled"),
				TgAutoPolling: r.FormValue("telegram.autoPolling"),

				DownloadLinkMaxPerSec:     r.FormValue("download.linkMaxPerSecond"),
				DownloadLinkMaxConcurrent: r.FormValue("download.linkMaxConcurrent"),
				DownloadMaxConcurrent:     r.FormValue("download.downloadMaxConcurrent"),

				StrmForceProxyUaTokens:      r.FormValue("strm.forceProxyUaTokens"),
				StrmAccountProxyConcurrency: r.FormValue("strm.accountProxyConcurrencyLimit"),
				StrmRedirectCheckTimeoutMs:  r.FormValue("strm.redirectCheckTimeoutMs"),

				StrmExtensions:     r.FormValue("strmExtensions"),
				DownloadExtensions: r.FormValue("downloadExtensions"),
				UserAgent:          r.FormValue("user-agent"),
			}
		}

		// ====== General ======
		if p.StrmPrefix != "" {
			settings.StrmPrefix = strings.TrimSpace(p.StrmPrefix)
		}
		// 数组字段：非空才覆盖（用户若清空表示留空数组）
		if r.Form != nil {
			// 对 form 提交：仅当 name 在表单里出现过才覆盖（通过 FormValue + Content-Type 判断）
		}
		// 简化：用户从 settings 页提交时，凡是"没改"的值也会被 submitFromAllTabs 收集；
		// 但逗号分隔的"数组"字段，回填时已按 settings 原值 join，因此再提交回来是等价的。
		settings.MediaMountPath = parseCSV(p.MediaMountPath)
		settings.EnablePathEncoding = parseBoolStr(p.EnablePathEnc)
		settings.Enable302 = parseBoolStr(p.Enable302)
		settings.RemoveExtraFiles = parseBoolStr(p.RemoveExtraFiles)

		// ====== Emby ======
		if p.EmbyURL != "" {
			settings.Emby.URL = strings.TrimSpace(p.EmbyURL)
		}
		if p.EmbyAPIKey != "" && !containsAsterisk(p.EmbyAPIKey) {
			settings.Emby.APIKey = strings.TrimSpace(p.EmbyAPIKey)
		}
		if p.EmbyWebhookAuth != "" {
			settings.Emby.WebhookAuth = strings.TrimSpace(p.EmbyWebhookAuth)
		}
		if p.EmbyLibraryID != "" {
			settings.Emby.LibraryID = strings.TrimSpace(p.EmbyLibraryID)
		}
		settings.Emby.NotifyMediaAdded = parseBoolStr(p.EmbyNotifyAdded)
		settings.Emby.NotifyMediaRemoved = parseBoolStr(p.EmbyNotifyRemoved)
		settings.Emby.NotifyPlayback = parseBoolStr(p.EmbyNotifyPlayback)
		settings.Emby.SyncDeleteEnabled = parseBoolStr(p.EmbySyncDeleteEnabled)
		settings.Emby.SyncDeleteDryRun = parseBoolStr(p.EmbySyncDeleteDryRun)

		// ====== Telegram ======
		if p.TgBotToken != "" && !containsAsterisk(p.TgBotToken) {
			settings.Telegram.BotToken = strings.TrimSpace(p.TgBotToken)
		}
		if p.TgChatID != "" {
			settings.Telegram.ChatID = strings.TrimSpace(p.TgChatID)
		}
		if p.TgWebhookURL != "" {
			settings.Telegram.WebhookURL = strings.TrimSpace(p.TgWebhookURL)
		}
		settings.Telegram.Enabled = parseBoolStr(p.TgEnabled)
		settings.Telegram.AutoPolling = parseBoolStr(p.TgAutoPolling)

		// ====== Download ======
		if v := parseIntPtr(p.DownloadLinkMaxPerSec); v != nil {
			settings.Download.LinkMaxPerSecond = v
		}
		if v := parseIntPtr(p.DownloadLinkMaxConcurrent); v != nil {
			settings.Download.LinkMaxConcurrent = v
		}
		if v := parseIntPtr(p.DownloadMaxConcurrent); v != nil {
			settings.Download.DownloadMaxConcurrent = v
		}

		// ====== STRM ======
		if p.StrmForceProxyUaTokens != "" {
			settings.Strm.ForceProxyUaTokens = parseCSV(p.StrmForceProxyUaTokens)
		}
		if v := parseInt(p.StrmAccountProxyConcurrency); v > 0 {
			settings.Strm.AccountProxyConcurrencyLimit = v
		}
		if v := parseInt(p.StrmRedirectCheckTimeoutMs); v >= 500 {
			settings.Strm.RedirectCheckTimeoutMs = v
		}

		// ====== Advanced ======
		if p.StrmExtensions != "" {
			settings.StrmExtensions = parseCSV(p.StrmExtensions)
		}
		if p.DownloadExtensions != "" {
			settings.DownloadExtensions = parseCSV(p.DownloadExtensions)
		}
		if p.UserAgent != "" {
			settings.UserAgent = strings.TrimSpace(p.UserAgent)
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
