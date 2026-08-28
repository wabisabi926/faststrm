package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 懒加载 PollingManager ====================
// 解决启动时未配置 BotToken 导致 PollingManager 为 nil 的问题

var (
	sharedPollingMgr *notify.PollingManager
	sharedCmdHandler *notify.CommandHandler
	sharedMu         sync.Mutex
	// sharedCleanupHandler：保存注入的清理按钮回调（启动时创建 cleanupInteraction 后注入）
	// 目的：getCmdHandler 懒加载创建 CommandHandler 时能自动接上 cleanup 回调，不会漏
	sharedCleanupHandler notify.CleanupCallbackHandler
)

// getPollingMgr 获取或创建共享 PollingManager
func getPollingMgr(bot *notify.TelegramBot) *notify.PollingManager {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedPollingMgr == nil {
		sharedPollingMgr = notify.NewPollingManager(bot)
	}
	return sharedPollingMgr
}

// getCmdHandler 获取或创建共享 CommandHandler
func getCmdHandler(bot *notify.TelegramBot, settingsStore *store.SettingsStore, tasksStore *store.TasksStore, accountStore *store.AccountStore) *notify.CommandHandler {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedCmdHandler == nil {
		sharedCmdHandler = notify.NewCommandHandler(bot, settingsStore, tasksStore, accountStore)
		// 懒加载创建后自动注入 cleanup 回调（如果已经通过 SetSharedCleanupHandler 注入过）
		if sharedCleanupHandler != nil {
			sharedCmdHandler.SetCleanupCallbackHandler(sharedCleanupHandler)
		}
	}
	return sharedCmdHandler
}

// SetSharedCleanupHandler 暴露给 server.go 在创建 StrmCleanupInteraction 后调用
// 确保懒加载的 CommandHandler 在任何时机创建时都能拿到 cleanup 回调，STRM 清理按钮不会失效。
func SetSharedCleanupHandler(h notify.CleanupCallbackHandler) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	sharedCleanupHandler = h
	// 如果已经有懒加载或正式创建的 CommandHandler，同步补上
	if sharedCmdHandler != nil {
		sharedCmdHandler.SetCleanupCallbackHandler(h)
	}
}

// SharedCleanupHandler 让 server.go 拿到 cleanupHandler，用于 AutoPolling 中兜底 NewCommandHandler 后的注入。
func SharedCleanupHandler() notify.CleanupCallbackHandler {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	return sharedCleanupHandler
}

// resetSharedPolling 重置共享实例（BotToken 变更时调用）
func resetSharedPolling() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedPollingMgr != nil {
		sharedPollingMgr.Stop()
		sharedPollingMgr = nil
	}
	sharedCmdHandler = nil
}

// NotifyDeps Telegram 通知相关 handler 依赖
type NotifyDeps struct {
	SettingsStore  *store.SettingsStore
	Dispatcher     *notify.Dispatcher
	TelegramBot    *notify.TelegramBot
	PollingManager *notify.PollingManager
	CommandHandler *notify.CommandHandler
	TasksStore     *store.TasksStore
	AccountStore   *store.AccountStore
}

// ==================== 辅助 ====================

// maskBotToken 脱敏 Bot Token，保留 id 段与 secret 末 4 位
func maskBotToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "***"
	}
	sep := strings.Index(token, ":")
	if sep == -1 {
		return "***"
	}
	idPart := token[:sep]
	secret := token[sep+1:]
	tail := 4
	if len(secret) < tail {
		tail = len(secret)
	}
	masked := strings.Repeat("*", len(secret)-tail) + secret[len(secret)-tail:]
	return idPart + ":" + masked
}

// botFromSettings 优先复用注入的 TelegramBot；否则按 settings 构造临时实例
func (deps NotifyDeps) botFromSettings(tg model.TelegramSettings) *notify.TelegramBot {
	if deps.TelegramBot != nil {
		return deps.TelegramBot
	}
	if tg.BotToken == "" {
		return nil
	}
	bot, err := notify.CreateBotFromSettings(tg)
	if err != nil {
		logger.S().Errorf("[notify] CreateBotFromSettings failed: %v", err)
		return notify.NewTelegramBot(tg.BotToken, tg.ChatID)
	}
	return bot
}

// parseUserID 兼容 JSON number / string / json.Number 三种 userId 输入
func parseUserID(v any) (int64, error) {
	switch x := v.(type) {
	case nil:
		return 0, fmt.Errorf("userId is required")
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(x), 10, 64)
	case json.Number:
		return x.Int64()
	default:
		return 0, fmt.Errorf("invalid userId type")
	}
}

// ==================== Bot Config ====================

// HandleNotifyBotGET GET /api/notify/bot 返回机器人信息、webhook 信息与本地配置
func HandleNotifyBotGET(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Errorf("[notify/bot GET] read settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		tg := settings.Telegram

		base := map[string]any{
			"configured":  tg.BotToken != "",
			"botToken":    maskBotToken(tg.BotToken),
			"chatId":      tg.ChatID,
			"webhookUrl":  tg.WebhookURL,
			"enabled":     tg.Enabled,
			"autoPolling": tg.AutoPolling,
		}

		if tg.BotToken == "" {
			base["bot"] = nil
			base["webhook"] = nil
			httpx.OkJson(w, base)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		bot := deps.botFromSettings(tg)
		if bot == nil {
			bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
		}

		var botInfo *notify.BotInfo
		if info, err := bot.GetMe(ctx); err == nil {
			botInfo = info
		} else {
			logger.S().Debugf("[notify/bot GET] getMe failed: %v", err)
		}

		var webhookInfo *notify.WebhookInfo
		if info, err := bot.GetWebhookInfo(ctx); err == nil {
			webhookInfo = info
		} else {
			logger.S().Debugf("[notify/bot GET] getWebhookInfo failed: %v", err)
		}

		base["bot"] = botInfo
		base["webhook"] = webhookInfo
		httpx.OkJson(w, base)
	}
}

// NotifyBotRequest POST /api/notify/bot 配置机器人请求
type NotifyBotRequest struct {
	BotToken    string `json:"botToken"`
	ChatID      string `json:"chatId"`
	WebhookURL  string `json:"webhookUrl"`
	Enabled     *bool  `json:"enabled"`
	AutoPolling *bool  `json:"autoPolling"`
}

// HandleNotifyBotPOST POST /api/notify/bot 校验 Bot Token 后保存 Telegram 配置，可选设置 Webhook
func HandleNotifyBotPOST(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req NotifyBotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		if req.BotToken == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请提供机器人令牌"})
			return
		}

		// 校验 token 格式: digits:35-char-secret
		sep := strings.Index(req.BotToken, ":")
		if sep <= 0 || sep == len(req.BotToken)-1 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"error":   "机器人令牌格式无效",
				"details": "令牌格式应为: 123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
			})
			return
		}

		// 通过 getMe 验证 token 有效性
		probe := notify.NewTelegramBot(req.BotToken, "")
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		botInfo, err := probe.GetMe(ctx)
		if err != nil {
			logger.S().Warnf("[notify/bot POST] getMe validation failed: %v", err)
			// 根据错误类型给出中文友好提示
			errMsg := "机器人令牌无效"
			errDetail := err.Error()
			if strings.Contains(errDetail, "code=404") {
				errMsg = "机器人令牌无效（机器人不存在或令牌错误）"
			} else if strings.Contains(errDetail, "code=401") {
				errMsg = "机器人令牌已被吊销，请重新获取"
			}
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"error":   errMsg,
				"details": errDetail,
			})
			return
		}

		// 读取当前配置并合并
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Errorf("[notify/bot POST] read settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}

		old := settings.Telegram
		newTg := model.TelegramSettings{
			BotToken:           req.BotToken,
			ChatID:             req.ChatID,
			WebhookURL:         req.WebhookURL,
			Enabled:            true,
			AutoPolling:        true,
			AllowedUsers:       old.AllowedUsers,
			AccountAlerts:      old.AccountAlerts,
			WebhookSecretToken: old.WebhookSecretToken,
		}
		if req.Enabled != nil {
			newTg.Enabled = *req.Enabled
		}
		if req.AutoPolling != nil {
			newTg.AutoPolling = *req.AutoPolling
		}
		// 若未提供 chatId，沿用旧值
		if newTg.ChatID == "" {
			newTg.ChatID = old.ChatID
		}
		settings.Telegram = newTg
		// 重置共享轮询管理器，下次请求时会用新 BotToken 创建
		resetSharedPolling()

		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			logger.S().Errorf("[notify/bot POST] save settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
			return
		}

		// ============ 热更新：让 deps 中的实例同步新配置，避免通知发往旧 Bot/ChatID ============
		if deps.TelegramBot != nil {
			deps.TelegramBot.UpdateCredentials(newTg.BotToken, newTg.ChatID)
		}
		if deps.Dispatcher != nil {
			deps.Dispatcher.ApplySettings(newTg)
		}
		// 如果 deps.TelegramBot 本来是 nil（启动时未配置），但 Dispatcher 需要它，ApplySettings 内部已经懒创建了。
		// 再把懒创建出来的 tg 回传到 deps.TelegramBot？其实不用，因为后续发通知是 dispatcher 走的，handler 层的 botFromSettings 会按 settings 临时建。
		if deps.CommandHandler != nil {
			// 若 deps.TelegramBot 已更新直接用；否则临时新建一个临时 bot 给 handler
			botForCmd := deps.TelegramBot
			if botForCmd == nil && newTg.BotToken != "" {
				botForCmd = notify.NewTelegramBot(newTg.BotToken, newTg.ChatID)
			}
			if botForCmd != nil {
				deps.CommandHandler.ReplaceBot(botForCmd)
			}
		}

		// ============ 互斥：轮询 ↔ Webhook 不能共存 ============
		if req.WebhookURL != "" {
			// 切到 Webhook → 先停所有正在运行的 Polling（包括 server.go 注入的 deps.PollingManager 和懒加载的 sharedPollingMgr）
			if deps.PollingManager != nil {
				deps.PollingManager.Stop()
			}
			// resetSharedPolling() 已经在上面调用过，会 Stop sharedPollingMgr 并置 nil

			wctx, wcancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer wcancel()
			if err := probe.SetWebhook(wctx, req.WebhookURL, newTg.WebhookSecretToken); err != nil {
				logger.S().Warnf("[notify/bot POST] setWebhook failed: %v", err)
				// 不影响主流程
			}
		} else if req.AutoPolling == nil || *req.AutoPolling {
			// 留空 WebhookURL + 用户没显式取消 AutoPolling → 切到轮询模式 → 删除 Webhook
			dctx, dcancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer dcancel()
			if err := probe.DeleteWebhook(dctx); err != nil {
				logger.S().Warnf("[notify/bot POST] deleteWebhook (switch to polling): %v", err)
				// 不影响主流程，用户可以点下方"启动"手动触发删除
			}
		}

		httpx.OkJson(w, map[string]any{
			"success": true,
			"bot":     botInfo,
			"chatId":  newTg.ChatID,
			"enabled": newTg.Enabled,
			"message": "Telegram bot configured successfully",
		})
	}
}

// HandleNotifyBotDELETE DELETE /api/notify/bot 删除 Webhook 并清空 Telegram 配置
func HandleNotifyBotDELETE(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		tg := settings.Telegram

		// 如果存在 botToken，尝试删除 webhook
		if tg.BotToken != "" {
			bot := deps.botFromSettings(tg)
			if bot == nil {
				bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if err := bot.DeleteWebhook(ctx); err != nil {
				logger.S().Warnf("[notify/bot DELETE] deleteWebhook failed: %v", err)
			}
		}

		// 重置共享轮询管理器
		resetSharedPolling()
		// 保留 allowedUsers / accountAlerts 子配置，仅清空 bot 本体相关字段
		settings.Telegram = model.TelegramSettings{
			AllowedUsers:  tg.AllowedUsers,
			AccountAlerts: tg.AccountAlerts,
		}
		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
			return
		}

		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "Telegram bot configuration removed",
		})
	}
}

// ==================== Polling ====================

// HandleNotifyPollingGET GET /api/notify/polling 返回轮询状态与 webhook 信息
func HandleNotifyPollingGET(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		tg := settings.Telegram
		if tg.BotToken == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Telegram 未配置"})
			return
		}

		running := false
		pollingMgr := deps.PollingManager
		if pollingMgr == nil {
			bot := deps.botFromSettings(tg)
			if bot == nil {
				bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
			}
			pollingMgr = getPollingMgr(bot)
		}
		if pollingMgr != nil {
			running = pollingMgr.IsRunning()
		}

		var webhookInfo *notify.WebhookInfo
		bot := deps.botFromSettings(tg)
		if bot == nil {
			bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if info, err := bot.GetWebhookInfo(ctx); err == nil {
			webhookInfo = info
		} else {
			logger.S().Debugf("[notify/polling GET] getWebhookInfo failed: %v", err)
		}

		// 构建轮询状态提示
		var message string
		if running {
			message = "轮询运行中，每 5 秒检查一次新消息"
		} else if webhookInfo != nil && webhookInfo.URL != "" {
			message = "Webhook 模式：已配置 webhook URL，实时接收消息"
		} else {
			message = "轮询已停止"
		}

		httpx.OkJson(w, map[string]any{
			"polling": running,
			"webhook": webhookInfo,
			"message": message,
		})
	}
}

// HandleNotifyPollingPOST POST /api/notify/polling 删除 webhook 后启动长轮询
func HandleNotifyPollingPOST(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		tg := settings.Telegram
		if tg.BotToken == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Telegram 未配置"})
			return
		}
		// 懒初始化 PollingManager
		pollingMgr := deps.PollingManager
		bot := deps.botFromSettings(tg)
		if bot == nil {
			bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
		}
		if pollingMgr == nil {
			pollingMgr = getPollingMgr(bot)
		}
		if pollingMgr == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "PollingManager 未初始化"})
			return
		}

		// 删除现有 webhook（轮询模式与 webhook 互斥）
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := bot.DeleteWebhook(ctx); err != nil {
			logger.S().Infof("[notify/polling POST] deleteWebhook (may be none): %v", err)
		}

		// 启动轮询：将 update 分发给 CommandHandler
		cmdHandler := deps.CommandHandler
		if cmdHandler == nil {
			cmdHandler = getCmdHandler(bot, deps.SettingsStore, deps.TasksStore, deps.AccountStore)
		}
		// 注册 Bot 命令菜单（SetMyCommands）
		if err := bot.SetMyCommands(ctx, cmdHandler.BotCommandList()); err != nil {
			logger.S().Warnf("[notify/polling POST] SetMyCommands 失败: %v", err)
		} else {
			logger.S().Infof("[notify/polling POST] Bot 命令菜单已注册")
		}
		handlerFn := func(ctx context.Context, update notify.Update) error {
			if cmdHandler == nil {
				return nil
			}
			if update.Message != nil {
				return cmdHandler.HandleMessage(ctx, *update.Message)
			}
			if update.CallbackQuery != nil {
				return cmdHandler.HandleCallbackQuery(ctx, *update.CallbackQuery)
			}
			return nil
		}
		if err := pollingMgr.Start(context.Background(), handlerFn); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"error":   "启动轮询失败",
				"details": err.Error(),
			})
			return
		}

		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "轮询已启动",
		})
	}
}

// HandleNotifyPollingDELETE DELETE /api/notify/polling 停止轮询，若存在 webhookUrl 则恢复 webhook
func HandleNotifyPollingDELETE(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		tg := settings.Telegram
		if tg.BotToken == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Telegram 未配置"})
			return
		}
		pollingMgr := deps.PollingManager
		if pollingMgr == nil {
			bot := deps.botFromSettings(tg)
			if bot == nil {
				bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
			}
			pollingMgr = getPollingMgr(bot)
		}
		if pollingMgr == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "PollingManager 未初始化"})
			return
		}

		pollingMgr.Stop()

		// 若配置中存在 webhook URL，则恢复 webhook
		if tg.WebhookURL != "" {
			bot := deps.botFromSettings(tg)
			if bot == nil {
				bot = notify.NewTelegramBot(tg.BotToken, tg.ChatID)
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			if err := bot.SetWebhook(ctx, tg.WebhookURL, tg.WebhookSecretToken); err != nil {
				logger.S().Warnf("[notify/polling DELETE] setWebhook failed: %v", err)
			}
		}

		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "轮询已停止",
		})
	}
}

// ==================== Send ====================

// NotifySendRequest POST /api/notify/send 发送消息请求
type NotifySendRequest struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

// taskStatusData task_status 类型数据
type taskStatusData struct {
	TaskName string `json:"taskName"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

// downloadCompleteData download_complete 类型数据
type downloadCompleteData struct {
	TaskName   string `json:"taskName"`
	TotalFiles int    `json:"totalFiles"`
	Downloaded int    `json:"downloaded"`
	DurationMs int64  `json:"durationMs"`
}

// errorData error 类型数据
type errorData struct {
	TaskName string `json:"taskName"`
	Message  string `json:"message"`
}

// infoData info 类型数据
type infoData struct {
	Message string `json:"message"`
}

// HandleNotifySend POST /api/notify/send 按 type 格式化并通过 Dispatcher 发送通知
func HandleNotifySend(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req NotifySendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		if deps.Dispatcher == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "Dispatcher 未初始化"})
			return
		}

		ctx := r.Context()
		var sendErr error
		switch req.Type {
		case "task_status":
			var d taskStatusData
			if err := json.Unmarshal(req.Data, &d); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid data for task_status"})
				return
			}
			sendErr = deps.Dispatcher.NotifyTaskStatus(ctx, d.TaskName, d.Status, d.Detail)
		case "download_complete":
			var d downloadCompleteData
			if err := json.Unmarshal(req.Data, &d); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid data for download_complete"})
				return
			}
			sendErr = deps.Dispatcher.NotifyDownloadComplete(ctx, d.TaskName, d.TotalFiles, d.Downloaded, d.DurationMs)
		case "error":
			var d errorData
			if err := json.Unmarshal(req.Data, &d); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid data for error"})
				return
			}
			sendErr = deps.Dispatcher.NotifyError(ctx, d.TaskName, d.Message)
		case "info":
			var msgText string
			if len(req.Data) > 0 {
				var d infoData
				if err := json.Unmarshal(req.Data, &d); err != nil {
					httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid data for info"})
					return
				}
				msgText = firstNonEmpty(d.Message, string(req.Data))
			} else {
				msgText = req.Message
			}
			if msgText == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "message is required for info"})
				return
			}
			msg := fmt.Sprintf("ℹ️ <b>Info</b>\n\n%s\n\n<b>Time:</b> %s",
				msgText,
				time.Now().Local().Format("2006-01-02 15:04:05"))
			sendErr = deps.Dispatcher.Notify(ctx, msg)
		default:
			// 无 type：直接发送 message 文本
			if req.Message == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
				return
			}
			sendErr = deps.Dispatcher.Notify(ctx, req.Message)
		}

		if sendErr != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{
				"error":   "Failed to send Telegram message",
				"details": sendErr.Error(),
			})
			return
		}
		httpx.OkJson(w, map[string]any{"success": true})
	}
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ==================== Allowed Users ====================

// HandleNotifyUsersGET GET /api/notify/users 返回 allowedUsers 列表
func HandleNotifyUsersGET(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		users := settings.Telegram.AllowedUsers
		if users == nil {
			users = []int64{}
		}
		type userItem struct {
			ID int64 `json:"id"`
		}
		items := make([]userItem, 0, len(users))
		for _, id := range users {
			items = append(items, userItem{ID: id})
		}
		httpx.OkJson(w, map[string]any{"users": items})
	}
}

// notifyUsersRequest 添加/删除用户请求（userId 兼容 number 或 string）
type notifyUsersRequest struct {
	UserID any `json:"userId"`
}

// HandleNotifyUsersPOST POST /api/notify/users 添加用户到 allowedUsers
func HandleNotifyUsersPOST(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req notifyUsersRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		userID, err := parseUserID(req.UserID)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		for _, id := range settings.Telegram.AllowedUsers {
			if id == userID {
				httpx.WriteJson(w, http.StatusConflict, map[string]string{"error": "User already exists"})
				return
			}
		}
		settings.Telegram.AllowedUsers = append(settings.Telegram.AllowedUsers, userID)
		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
			return
		}
		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "User added successfully",
		})
	}
}

// HandleNotifyUsersDELETE DELETE /api/notify/users?userId=xxx 从 allowedUsers 移除用户
func HandleNotifyUsersDELETE(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("userId")
		if raw == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
			return
		}
		userID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Invalid userId"})
			return
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		next := make([]int64, 0, len(settings.Telegram.AllowedUsers))
		found := false
		for _, id := range settings.Telegram.AllowedUsers {
			if id == userID {
				found = true
				continue
			}
			next = append(next, id)
		}
		if !found {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "User not found"})
			return
		}
		settings.Telegram.AllowedUsers = next
		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
			return
		}
		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "User removed successfully",
		})
	}
}

// ==================== Account Alerts ====================

// HandleNotifyAlertsGET GET /api/notify/alerts 返回账户状态通知配置
func HandleNotifyAlertsGET(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		alerts := settings.Telegram.AccountAlerts
		resp := map[string]any{
			"enabled":   false,
			"onError":   true,
			"onRecover": true,
		}
		if alerts != nil {
			resp["enabled"] = alerts.Enabled
			resp["onError"] = alerts.OnError
			resp["onRecover"] = alerts.OnRecover
		}
		httpx.OkJson(w, resp)
	}
}

// NotifyAlertsRequest POST /api/notify/alerts 保存账户状态通知配置请求
type NotifyAlertsRequest struct {
	Enabled   *bool `json:"enabled"`
	OnError   *bool `json:"onError"`
	OnRecover *bool `json:"onRecover"`
}

// HandleNotifyAlertsPOST POST /api/notify/alerts 更新账户状态通知配置
func HandleNotifyAlertsPOST(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req NotifyAlertsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}

		alerts := settings.Telegram.AccountAlerts
		if alerts == nil {
			alerts = &model.AccountAlertsSettings{}
		}
		if req.Enabled != nil {
			alerts.Enabled = *req.Enabled
		}
		if req.OnError != nil {
			alerts.OnError = *req.OnError
		}
		if req.OnRecover != nil {
			alerts.OnRecover = *req.OnRecover
		}
		settings.Telegram.AccountAlerts = alerts

		if err := deps.SettingsStore.SaveSettings(settings); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
			return
		}
		httpx.OkJson(w, map[string]any{
			"success": true,
			"message": "账户状态通知配置保存成功",
			"config":  alerts,
		})
	}
}

// ==================== Telegram Webhook（公开回调） ====================

// HandleTelegramWebhook POST /api/notify/webhook （公开，无 JWT）
// 校验 x-telegram-bot-api-secret-token 头，解析 TelegramUpdate 并分发到 CommandHandler
func HandleTelegramWebhook(deps NotifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Errorf("[notify/webhook] read settings: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "服务器内部错误"})
			return
		}
		tg := settings.Telegram
		if tg.BotToken == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Telegram not configured"})
			return
		}

		// 校验 secret_token（若配置了）
		if tg.WebhookSecretToken != "" {
			provided := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
			if provided != tg.WebhookSecretToken {
				logger.S().Warnf("[Telegram] Webhook secret_token 验证失败")
				httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
				return
			}
		}

		var update notify.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid update payload"})
			return
		}

		ctx := r.Context()
		if update.Message != nil && deps.CommandHandler != nil {
			if err := deps.CommandHandler.HandleMessage(ctx, *update.Message); err != nil {
				logger.S().Warnf("[Telegram Webhook] HandleMessage: %v", err)
			}
		}
		if update.CallbackQuery != nil && deps.CommandHandler != nil {
			if err := deps.CommandHandler.HandleCallbackQuery(ctx, *update.CallbackQuery); err != nil {
				logger.S().Warnf("[Telegram Webhook] HandleCallbackQuery: %v", err)
			}
		}

		httpx.OkJson(w, map[string]bool{"ok": true})
	}
}
