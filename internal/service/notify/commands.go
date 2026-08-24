// Package notify 的 commands 子模块：Bot 命令处理器。
// 对齐 frontend/src/lib/telegramCommands.ts 的命令列表与处理逻辑。
package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// CommandHandler Bot 命令处理器，依赖 store 层读取配置/任务/账号
type CommandHandler struct {
	bot            *TelegramBot
	settings       *store.SettingsStore
	tasks          *store.TasksStore
	accounts       *store.AccountStore
	menuActions    MenuActions
	cleanupHandler CleanupCallbackHandler
}

// CleanupCallbackHandler STRM 清理待删队列的 TG 按钮回调处理器接口
// 由 handler.StrmCleanupInteraction 实现，避免 notify → handler 循环依赖
type CleanupCallbackHandler interface {
	// HandleTelegramCallback 处理 TG 按钮 callback
	// requestID: 待删批次 ID
	// approve: true=确认删除, false=取消
	// 返回处理结果消息（用于回复用户）
	HandleTelegramCallback(ctx context.Context, requestID string, approve bool) (string, error)
}

// SetCleanupCallbackHandler 注入清理待删队列的 TG 按钮回调处理器
func (h *CommandHandler) SetCleanupCallbackHandler(handler CleanupCallbackHandler) {
	h.cleanupHandler = handler
}

// NewCommandHandler 创建命令处理器
func NewCommandHandler(bot *TelegramBot, settings *store.SettingsStore, tasks *store.TasksStore, accounts *store.AccountStore) *CommandHandler {
	return &CommandHandler{
		bot:      bot,
		settings: settings,
		tasks:    tasks,
		accounts: accounts,
	}
}

// ReplaceBot 热更新内部 TelegramBot 引用（保存配置 / 换 BotToken 后调用）
func (h *CommandHandler) ReplaceBot(bot *TelegramBot) {
	h.bot = bot
}

// SetMenuActions 注入菜单动作接口实现
func (h *CommandHandler) SetMenuActions(actions MenuActions) {
	h.menuActions = actions
}

// BotCommandList 返回 Bot 菜单命令列表
func (h *CommandHandler) BotCommandList() []BotCommand {
	return []BotCommand{
		{Command: "status", Description: "📊 系统状态"},
		{Command: "scan", Description: "🔍 全量对账"},
		{Command: "cleanup", Description: "🧹 清理孤儿"},
		{Command: "accounts", Description: "👥 账号列表"},
		{Command: "help", Description: "❓ 帮助"},
	}
}

// IsUserAllowed ACL 检查：空列表表示允许所有用户
func (h *CommandHandler) IsUserAllowed(userID int64, allowedUsers []int64) bool {
	if len(allowedUsers) == 0 {
		return true
	}
	for _, id := range allowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// HandleMessage 根据消息文本分发到对应命令处理器
// 命令文本以 "/" 开头时进入命令路由；否则回复欢迎引导
func (h *CommandHandler) HandleMessage(ctx context.Context, msg Message) error {
	if msg.Chat == nil {
		return fmt.Errorf("message has no chat")
	}
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	var username string
	var userID int64
	if msg.From != nil {
		if msg.From.Username != "" {
			username = msg.From.Username
		} else {
			username = msg.From.FirstName
		}
		userID = msg.From.ID
	}
	text := msg.Text

	logger.S().Debugf("[Telegram Polling] Message from %s (%d): %s", username, userID, text)

	if !strings.HasPrefix(text, "/") {
		reply := fmt.Sprintf("👋 你好 %s！\n\nFastStrm Bot 支持以下操作：\n\n"+
			"• /status — 查看系统状态\n"+
			"• /scan — 执行全量对账\n"+
			"• /cleanup — 清理孤儿 STRM\n"+
			"• /accounts — 查看账号列表\n\n"+
			"输入 /start 打开操作菜单。", username)
		return h.bot.SendMessage(ctx, chatID, reply, "HTML")
	}

	cmd := strings.SplitN(text, " ", 2)[0]

	// ACL 检查
	allowed, err := h.allowedUsers(ctx)
	if err != nil {
		logger.S().Warnf("read telegram allowed users failed: %v", err)
	}
	if !h.IsUserAllowed(userID, allowed) {
		reply := fmt.Sprintf("❌ <b>访问被拒绝</b>\n\n你没有使用此 Bot 的权限，请联系管理员。\n\n你的 User ID: <code>%d</code>", userID)
		return h.bot.SendMessage(ctx, chatID, reply, "HTML")
	}

	switch cmd {
	case "/start":
		return h.ShowMainMenuForChat(ctx, chatID)
	case "/help":
		return h.handleHelp(ctx, chatID, username)
	case "/status":
		return h.handleStatus(ctx, chatID)
	case "/scan":
		return h.handleScanPlaceholder(ctx, chatID)
	case "/cleanup":
		return h.handleCleanupPlaceholder(ctx, chatID)
	case "/accounts":
		return h.handleAccounts(ctx, chatID)
	default:
		return h.bot.SendMessage(ctx, chatID, fmt.Sprintf("❓ 未知命令: %s\n\n输入 /help 查看所有可用命令。", cmd), "HTML")
	}
}

// HandleCallbackQuery 处理回调查询，按 callback_data 路由到对应命令
func (h *CommandHandler) HandleCallbackQuery(ctx context.Context, cq CallbackQuery) error {
	if cq.Message == nil || cq.Message.Chat == nil {
		logger.S().Error("Callback query has no message")
		return nil
	}
	chatID := fmt.Sprintf("%d", cq.Message.Chat.ID)
	logger.S().Debugf("[Telegram Polling] Callback query: %s", cq.Data)

	if err := h.bot.AnswerCallbackQuery(ctx, cq.ID, "处理中..."); err != nil {
		logger.S().Warnf("answerCallbackQuery failed: %v", err)
	}

	menuPrefixes := []string{"menu_", "mon_", "task_", "emby_", "strm_", "task_exec", "task_cancel", "mon_event:", "mon_detail:"}
	for _, prefix := range menuPrefixes {
		if strings.HasPrefix(cq.Data, prefix) {
			return h.RouteMenuCallback(ctx, cq)
		}
	}

	// P0 STRM 清理待删队列的 TG 按钮回调
	// 格式：cleanup_confirm|{request_id}|{y|n}
	if strings.HasPrefix(cq.Data, "cleanup_confirm|") {
		return h.handleCleanupConfirmCallback(ctx, chatID, cq)
	}

	switch cq.Data {
	case "status":
		return h.handleStatus(ctx, chatID)
	case "scan":
		return h.handleScanPlaceholder(ctx, chatID)
	case "cleanup":
		return h.handleCleanupPlaceholder(ctx, chatID)
	case "accounts":
		return h.handleAccounts(ctx, chatID)
	default:
		return h.bot.SendMessage(ctx, chatID, fmt.Sprintf("✅ 已处理: %s", cq.Data), "HTML")
	}
}

// handleCleanupConfirmCallback 处理 STRM 清理待删队列的 TG 按钮回调
// callback_data 格式：cleanup_confirm|{request_id}|{y|n}
func (h *CommandHandler) handleCleanupConfirmCallback(ctx context.Context, chatID string, cq CallbackQuery) error {
	if h.cleanupHandler == nil {
		return h.bot.SendMessage(ctx, chatID, "⚠️ 清理待删队列未启用（settings.cleanup.confirmMode 未开启或 SQLite 不可用）", "HTML")
	}
	requestID, approve, ok := parseCleanupCallbackData(cq.Data)
	if !ok {
		return h.bot.SendMessage(ctx, chatID, "⚠️ 无效的清理回调数据: "+cq.Data, "HTML")
	}
	msg, err := h.cleanupHandler.HandleTelegramCallback(ctx, requestID, approve)
	if err != nil {
		msg = fmt.Sprintf("❌ 处理清理回调失败: %v", err)
		logger.S().Errorf("[Telegram] cleanup callback %s approve=%v failed: %v", requestID, approve, err)
	}
	return h.bot.SendMessage(ctx, chatID, msg, "HTML")
}

// parseCleanupCallbackData 解析 cleanup_confirm|{request_id}|{y|n} 格式的 callback_data
// 返回 (requestID, approve, 是否有效)
func parseCleanupCallbackData(data string) (requestID string, approve bool, ok bool) {
	parts := strings.Split(data, "|")
	if len(parts) != 3 || parts[0] != "cleanup_confirm" {
		return "", false, false
	}
	if parts[2] != "y" && parts[2] != "n" {
		return "", false, false
	}
	return parts[1], parts[2] == "y", true
}

// handleHelp 显示帮助文本
func (h *CommandHandler) handleHelp(ctx context.Context, chatID, username string) error {
	text := "❓ <b>FastStrm Bot 命令</b>\n\n" +
		"<b>/start</b> — 打开操作菜单\n" +
		"<b>/status</b> — 查看监控状态、账号 Cookie 状态\n" +
		"<b>/scan</b> — 执行全量对账（扫描+清理+补生成）\n" +
		"<b>/cleanup</b> — 扫描孤儿 STRM\n" +
		"<b>/accounts</b> — 查看账号列表\n" +
		"<b>/help</b> — 显示此帮助\n\n" +
		"<b>说明：</b>\n" +
		"• 全量对账会暂停监控，完成后自动恢复\n" +
		"• 孤儿扫描不会自动删除，请在 Web UI 确认后清理\n" +
		"• Cookie 过期请在 Web UI 扫码刷新"
	if username != "" {
		text = fmt.Sprintf("👋 <b>你好 %s！</b>\n\n", username) + text
	}
	return h.bot.SendMessage(ctx, chatID, text, "HTML")
}

// handleStatus 显示完整系统状态（账号 + 监控 + 任务 + Emby + 事件开关）
func (h *CommandHandler) handleStatus(ctx context.Context, chatID string) error {
	var sb strings.Builder
	sb.WriteString("<b>📊 系统状态概览</b>\n\n")

	// 通过 menuActions 获取聚合状态（优先）
	if h.menuActions != nil {
		if status, err := h.menuActions.GetSystemStatus(); err == nil {
			// 账号状态
			if accounts, ok := status["accounts"].([]map[string]any); ok && len(accounts) > 0 {
				sb.WriteString("<b>👥 账号</b>\n")
				for _, acc := range accounts {
					name, _ := acc["name"].(string)
					hasCookie, _ := acc["hasCookie"].(bool)
					emoji := "⚠️"
					cookieState := "未设置"
					if hasCookie {
						emoji = "✅"
						cookieState = "有效"
					}
					sb.WriteString(fmt.Sprintf("\u00a0\u00a0%s <b>%s</b> — %s\n", emoji, name, cookieState))
				}
				sb.WriteString("\n")
			}

			// 监控状态
			if monitors, ok := status["monitors"].([]map[string]any); ok {
				sb.WriteString("<b>📺 监控</b>\n")
				if len(monitors) == 0 {
					sb.WriteString("\u00a0\u00a0暂无账号监控\n")
				} else {
					for _, m := range monitors {
						acc, _ := m["account"].(string)
						running, _ := m["running"].(bool)
						emoji := "⏸️"
						state := "已停止"
						if running {
							emoji = "▶️"
							state = "运行中"
						}
						sb.WriteString(fmt.Sprintf("\u00a0\u00a0%s <b>%s</b> — %s\n", emoji, acc, state))
					}
				}
				sb.WriteString("\n")
			}

			// 运行中任务
			if runningTasks, ok := status["runningTasks"].([]map[string]any); ok {
				sb.WriteString(fmt.Sprintf("<b>🎬 运行中任务</b> (%d)\n", len(runningTasks)))
				if len(runningTasks) == 0 {
					sb.WriteString("\u00a0\u00a0无\n")
				} else {
					for _, t := range runningTasks {
						name, _ := t["name"].(string)
						progress, _ := t["progress"].(string)
						sb.WriteString(fmt.Sprintf("\u00a0\u00a0• %s — %s\n", name, progress))
					}
				}
				sb.WriteString("\n")
			}

			// Emby 状态
			if emby, ok := status["emby"].(map[string]any); ok {
				sb.WriteString("<b>🎞️ Emby</b>\n")
				connected, _ := emby["connected"].(bool)
				if connected {
					sb.WriteString("\u00a0\u00a0✅ 已连接\n")
				} else {
					sb.WriteString("\u00a0\u00a0⚠️ 未连接\n")
				}
				sb.WriteString("\n")
			}
		}
	} else {
		// 回退：直接从 store 读取
		accounts := h.accounts.List()
		tasks, err := h.tasks.ReadTasks()
		if err != nil {
			logger.S().Warnf("read tasks for status failed: %v", err)
		}
		sb.WriteString("<b>👥 账号</b>\n")
		for _, acc := range accounts {
			hasCookie := acc.Cookie != ""
			emoji := "⚠️"
			cookieState := "未设置"
			if hasCookie {
				emoji = "✅"
				cookieState = "有效"
			}
			sb.WriteString(fmt.Sprintf("\u00a0\u00a0%s <b>%s</b> — %s\n", emoji, acc.Name, cookieState))
		}
		sb.WriteString(fmt.Sprintf("\n<b>🎬 任务</b>: %d 个\n\n", len(tasks)))
	}

	// 事件开关状态（从 settings 读取）
	if s, err := h.settings.ReadSettings(); err == nil {
		sb.WriteString("<b>🔔 事件开关</b>\n")
		et := s.LifeMonitor.EventTypes
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0创建: %s | 删除: %s | 移动: %s | 重命名: %s\n",
			boolToText(et.Create, "✅", "❌"),
			boolToText(et.Remove, "✅", "❌"),
			boolToText(et.Move, "✅", "❌"),
			boolToText(et.Rename, "✅", "❌")))
	}

	sb.WriteString(fmt.Sprintf("\n<b>⏰ %s</b>", nowFormatted()))
	return h.bot.SendMessage(ctx, chatID, sb.String(), "HTML")
}

// handleScanPlaceholder 全量对账占位：实际触发由调用方接入 strm 对账服务后启用
func (h *CommandHandler) handleScanPlaceholder(ctx context.Context, chatID string) error {
	text := "🔍 <b>全量对账</b>\n\n该命令需在调用方接入 strm 对账服务后启用，或前往 Web UI 触发。"
	return h.bot.SendMessage(ctx, chatID, text, "HTML")
}

// handleCleanupPlaceholder 孤儿清理占位：实际触发由调用方接入 strm 清理服务后启用
func (h *CommandHandler) handleCleanupPlaceholder(ctx context.Context, chatID string) error {
	text := "🧹 <b>清理孤儿</b>\n\n该命令需在调用方接入 strm 清理服务后启用，或前往 Web UI 触发。"
	return h.bot.SendMessage(ctx, chatID, text, "HTML")
}

// handleAccounts 列出已配置账号及其 Cookie 状态
func (h *CommandHandler) handleAccounts(ctx context.Context, chatID string) error {
	accounts := h.accounts.List()
	if len(accounts) == 0 {
		return h.bot.SendMessage(ctx, chatID, "👥 <b>账号列表</b>\n\n暂无账号，请在 Web UI 添加。", "HTML")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>👥 账号列表 (%d)</b>\n\n", len(accounts)))
	for _, acc := range accounts {
		hasCookie := acc.Cookie != ""
		emoji := "⚠️"
		if hasCookie {
			emoji = "✅"
		}
		cookieState := "未设置"
		if hasCookie {
			cookieState = "已设置"
		}
		sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", emoji, acc.Name))
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0\u00a0Cookie: %s\n\n", cookieState))
	}
	sb.WriteString("💡 Cookie 过期时可在 Web UI 账号管理页扫码刷新。")
	return h.bot.SendMessage(ctx, chatID, sb.String(), "HTML")
}

// allowedUsers 从 settings 读取 Telegram allowedUsers 列表
func (h *CommandHandler) allowedUsers(ctx context.Context) ([]int64, error) {
	s, err := h.settings.ReadSettings()
	if err != nil {
		return nil, err
	}
	return s.Telegram.AllowedUsers, nil
}

// nowFormatted 返回当前时间的格式化字符串
func nowFormatted() string {
	return time.Now().Format("2006-01-02 15:04:05")
}


