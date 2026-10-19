package notify

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// MenuActions 菜单项可触发的业务动作接口
// 由 server.go 注入 Monitor、Task、Emby 等服务的适配实现
type MenuActions interface {
	// ========== 系统状态 ==========
	GetSystemStatus() (map[string]any, error)

	// ========== 监控控制 ==========
	StartMonitor(ctx context.Context, account string) error
	StopMonitor(ctx context.Context, account string) error
	StopAllMonitors(ctx context.Context) error
	GetMonitorStatus() (map[string]any, error)
	ToggleMonitorEvent(ctx context.Context, account, eventType string, enabled bool) error

	// ========== 任务管理 ==========
	ExecuteTask(ctx context.Context, taskID string) (map[string]any, error)
	CancelTask(ctx context.Context, taskID string) error
	ListRunningTasks() ([]map[string]any, error)
	ListScheduledTasks() ([]map[string]any, error)
	// ListSyncTasks 列出全部任务（含未启用定时的手动任务），供「全量同步」选择
	ListSyncTasks() ([]map[string]any, error)
	// RunTaskByID 异步执行单个任务（立即返回）
	RunTaskByID(ctx context.Context, taskID string) error
	// RunAllTasksSequential 异步按顺序执行全部任务（一个完成再下一个）
	RunAllTasksSequential(ctx context.Context) error

	// ========== Emby 刷库 ==========
	RefreshEmbyByPath(ctx context.Context, path string) error
	RefreshEmbyLibrary(ctx context.Context, libraryType string) error
	GetEmbyStatus() (map[string]any, error)

	// ========== STRM 操作 ==========
	// ListCleanupMappings 列出可清理的路径映射（account/cloudPath/localPath）
	ListCleanupMappings() ([]map[string]string, error)
	// ScanCleanupMappingAsync 异步扫描指定映射，扫完把失效项送入 TG 待确认队列
	ScanCleanupMappingAsync(ctx context.Context, index int) error
}

// ==================== 回调常量 ====================

const (
	// 主菜单
	callbackMenuMain    = "menu_main"
	callbackMenuTasks   = "menu_tasks"
	callbackMenuMonitor = "menu_monitor"
	callbackMenuEmby    = "menu_emby"
	callbackMenuSTRM    = "menu_strm"
	callbackMenuStatus  = "menu_status"
	callbackMenuHelp    = "menu_help"
	callbackMenuBack    = "menu_back"

	// 监控控制
	callbackMonStart   = "mon_start:"
	callbackMonStop    = "mon_stop:"
	callbackMonStopAll = "mon_stop_all"
	callbackMonStatus  = "mon_status"
	callbackMonCreate  = "mon_toggle_create:"
	callbackMonDelete  = "mon_toggle_delete:"
	callbackMonMove    = "mon_toggle_move:"
	callbackMonRename  = "mon_toggle_rename:"

	// 任务控制
	callbackTaskExecute   = "task_exec:"
	callbackTaskCancel    = "task_cancel:"
	callbackTaskList      = "task_list"
	callbackTaskScheduled = "task_scheduled"

	// Emby 刷库
	callbackEmbyRefresh  = "emby_refresh:"
	callbackEmbyMovieLib = "emby_lib:movie"
	callbackEmbyTVLib    = "emby_lib:tv"
	callbackEmbyAllLib   = "emby_lib:all"
	callbackEmbyStatus   = "emby_status"

	// STRM 操作
	callbackStrmSync    = "strm_sync"    // 旧入口，保留兼容
	callbackStrmCleanup = "strm_cleanup" // 旧入口，保留兼容

	// STRM 操作：全量同步（任务选择）与清理孤儿（映射选择）
	callbackSyncPick  = "sync_pick"
	callbackSyncRun   = "sync_run:"
	callbackSyncAll   = "sync_all"
	callbackCleanPick = "clean_pick"
	callbackCleanScan = "clean_scan:"

	// STRM 通知内联按钮（由 dispatcher.buildInlineKeyboard 生成）
	callbackStrmRefreshEmby = "refresh_emby:"
	callbackStrmDetail      = "detail_strm:"
)

// ==================== 菜单构建函数 ====================

// BuildMainMenu 构建主菜单
func BuildMainMenu() (string, [][]InlineKeyboardButton) {
	text := "🎛️ <b>FastStrm 控制面板</b>\n\n" +
		"选择要操作的功能：\n\n" +
		"• 📊 <b>状态</b> — 系统/账号/监控状态\n" +
		"• 📺 <b>监控</b> — 生活事件监控控制\n" +
		"• 🎬 <b>任务</b> — 任务执行与管理\n" +
		"• 🎞️ <b>Emby</b> — 媒体库刷库\n" +
		"• 📁 <b>STRM</b> — 全量同步/清理\n" +
		"• ❓ <b>帮助</b> — 操作说明"

	buttons := [][]InlineKeyboardButton{
		{
			{Text: "📊 系统状态", CallbackData: callbackMenuStatus},
		},
		{
			{Text: "📺 监控控制", CallbackData: callbackMenuMonitor},
			{Text: "🎬 任务管理", CallbackData: callbackMenuTasks},
		},
		{
			{Text: "🎞️ Emby 刷库", CallbackData: callbackMenuEmby},
			{Text: "📁 STRM 操作", CallbackData: callbackMenuSTRM},
		},
		{
			{Text: "❓ 帮助", CallbackData: callbackMenuHelp},
		},
	}
	return text, buttons
}

// BuildStatusMenu 构建状态展示
func BuildStatusMenu(status map[string]any) (string, [][]InlineKeyboardButton) {
	var sb strings.Builder
	sb.WriteString("<b>📊 系统状态</b>\n\n")

	// 账号状态
	if accounts, ok := status["accounts"].([]map[string]any); ok {
		sb.WriteString("<b>👥 账号</b>\n")
		for _, acc := range accounts {
			name, _ := acc["name"].(string)
			hasCookie, _ := acc["hasCookie"].(bool)
			emoji := "⚠️"
			if hasCookie {
				emoji = "✅"
			}
			sb.WriteString(fmt.Sprintf("%s %s — Cookie: %s\n", emoji, name, boolToText(hasCookie, "有效", "未设置")))
		}
		if len(accounts) == 0 {
			sb.WriteString("暂无账号\n")
		}
		sb.WriteString("\n")
	}

	// 监控状态
	if monitors, ok := status["monitors"].([]map[string]any); ok {
		sb.WriteString("<b>📺 监控</b>\n")
		for _, m := range monitors {
			acc, _ := m["account"].(string)
			running, _ := m["running"].(bool)
			emoji := "⏸️"
			if running {
				emoji = "▶️"
			}
			sb.WriteString(fmt.Sprintf("%s %s — %s\n", emoji, acc, boolToText(running, "运行中", "已停止")))
		}
		if len(monitors) == 0 {
			sb.WriteString("暂无监控\n")
		}
		sb.WriteString("\n")
	}

	// 任务状态
	if tasks, ok := status["runningTasks"].([]map[string]any); ok {
		sb.WriteString(fmt.Sprintf("<b>🎬 运行中任务</b> (%d)\n", len(tasks)))
		for _, t := range tasks {
			name, _ := t["name"].(string)
			progress, _ := t["progress"].(string)
			sb.WriteString(fmt.Sprintf("• %s %s\n", name, progress))
		}
		if len(tasks) == 0 {
			sb.WriteString("暂无运行中任务\n")
		}
		sb.WriteString("\n")
	}

	// Emby 状态
	if embyStatus, ok := status["emby"].(map[string]any); ok {
		sb.WriteString("<b>🎞️ Emby</b>\n")
		connected, _ := embyStatus["connected"].(bool)
		emoji := "❌"
		if connected {
			emoji = "✅"
		}
		sb.WriteString(fmt.Sprintf("<b>%s 连接: %s</b>\n", emoji, boolToText(connected, "已连接", "未连接")))
	}

	sb.WriteString(fmt.Sprintf("\n<b>⏰ 时间:</b> %s", nowFormatted()))

	buttons := [][]InlineKeyboardButton{
		{
			{Text: "🔄 刷新状态", CallbackData: callbackMenuStatus},
		},
		{
			{Text: "📺 监控控制", CallbackData: callbackMenuMonitor},
			{Text: "🎬 任务管理", CallbackData: callbackMenuTasks},
		},
		{
			{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
		},
	}
	return sb.String(), buttons
}

// BuildMonitorMenu 构建监控控制菜单
func BuildMonitorMenu(monitorStatus map[string]any) (string, [][]InlineKeyboardButton) {
	var sb strings.Builder
	sb.WriteString("📺 <b>监控控制</b>\n\n")

	if monitors, ok := monitorStatus["monitors"].([]map[string]any); ok && len(monitors) > 0 {
		sb.WriteString("<b>账号监控状态</b>\n\n")
		for _, m := range monitors {
			acc, _ := m["account"].(string)
			running, _ := m["running"].(bool)
			emoji := "⏸️"
			if running {
				emoji = "▶️"
			}
			sb.WriteString(fmt.Sprintf("%s <b>%s</b> — %s\n", emoji, acc, boolToText(running, "运行中", "已停止")))
		}
	} else {
		sb.WriteString("暂无账号监控，请前往 Web UI 配置。\n")
	}

	// 事件类型状态
	eventTypes := map[string]bool{"create": true, "remove": true, "move": true, "rename": true}
	if et, ok := monitorStatus["eventTypes"].(map[string]bool); ok {
		for k, v := range et {
			eventTypes[k] = v
		}
	}
	sb.WriteString("\n<b>事件类型</b>\n")
	eventLabels := map[string]string{"create": "创建", "remove": "删除", "move": "移动", "rename": "重命名"}
	for _, key := range []string{"create", "remove", "move", "rename"} {
		sb.WriteString(fmt.Sprintf("• %s: %s\n", eventLabels[key], boolToText(eventTypes[key], "✅ 启用", "❌ 禁用")))
	}

	// 构建按钮行
	var buttons [][]InlineKeyboardButton
	if monitors, ok := monitorStatus["monitors"].([]map[string]any); ok {
		for _, m := range monitors {
			acc, _ := m["account"].(string)
			running, _ := m["running"].(bool)
			var row []InlineKeyboardButton
			if running {
				row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("⏸️ 停止 %s", acc), CallbackData: callbackMonStop + acc})
			} else {
				row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("▶️ 启动 %s", acc), CallbackData: callbackMonStart + acc})
			}
			row = append(row, InlineKeyboardButton{Text: "🔍 详情", CallbackData: "mon_detail:" + acc})
			buttons = append(buttons, row)
		}
	}

	// 事件类型切换按钮（显示当前状态，点击切换）
	eventKeys := []string{"create", "remove", "move", "rename"}
	eventEmojis := map[string]string{"create": "🎬", "remove": "🗑️", "move": "📦", "rename": "✏️"}
	for i := 0; i < len(eventKeys); i += 2 {
		var row []InlineKeyboardButton
		for j := i; j < i+2 && j < len(eventKeys); j++ {
			key := eventKeys[j]
			on := eventTypes[key]
			label := fmt.Sprintf("%s %s %s", eventEmojis[key], eventLabels[key], boolToText(on, "✅", "❌"))
			callback := fmt.Sprintf("mon_event:%s:%s", key, boolToText(on, "off", "on"))
			row = append(row, InlineKeyboardButton{Text: label, CallbackData: callback})
		}
		buttons = append(buttons, row)
	}

	buttons = append(buttons, []InlineKeyboardButton{
		{Text: "⏹️ 停止全部监控", CallbackData: callbackMonStopAll},
	})
	buttons = append(buttons, []InlineKeyboardButton{
		{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
	})

	return sb.String(), buttons
}

// BuildTasksMenu 构建任务管理菜单
func BuildTasksMenu(runningTasks, scheduledTasks []map[string]any) (string, [][]InlineKeyboardButton) {
	var sb strings.Builder
	sb.WriteString("🎬 <b>任务管理</b>\n\n")

	if len(runningTasks) > 0 {
		sb.WriteString(fmt.Sprintf("<b>🔄 运行中任务</b> (%d)\n", len(runningTasks)))
		for _, t := range runningTasks {
			name, _ := t["name"].(string)
			progress, _ := t["progress"].(string)
			taskID, _ := t["id"].(string)
			sb.WriteString(fmt.Sprintf("  • <b>%s</b> — %s\n", name, progress))
			_ = taskID
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("<b>🔄 运行中任务</b>：暂无\n\n")
	}

	if len(scheduledTasks) > 0 {
		sb.WriteString(fmt.Sprintf("<b>⏰ 定时任务</b> (%d)\n", len(scheduledTasks)))
		for _, t := range scheduledTasks {
			name, _ := t["name"].(string)
			schedule, _ := t["schedule"].(string)
			taskID, _ := t["id"].(string)
			sb.WriteString(fmt.Sprintf("  • <b>%s</b> — %s\n", name, schedule))
			_ = taskID
		}
	} else {
		sb.WriteString("<b>⏰ 定时任务</b>：暂无\n")
	}

	var buttons [][]InlineKeyboardButton

	// 运行中任务的操作按钮
	for _, t := range runningTasks {
		name, _ := t["name"].(string)
		taskID, _ := t["id"].(string)
		if taskID != "" {
			buttons = append(buttons, []InlineKeyboardButton{
				{Text: fmt.Sprintf("⏹️ 取消: %s", truncate(name, 15)), CallbackData: callbackTaskCancel + taskID},
			})
		}
	}

	// 定时任务的执行按钮
	for _, t := range scheduledTasks {
		name, _ := t["name"].(string)
		taskID, _ := t["id"].(string)
		if taskID != "" {
			buttons = append(buttons, []InlineKeyboardButton{
				{Text: fmt.Sprintf("▶️ 执行: %s", truncate(name, 15)), CallbackData: callbackTaskExecute + taskID},
			})
		}
	}

	if len(runningTasks) == 0 && len(scheduledTasks) == 0 {
		buttons = append(buttons, []InlineKeyboardButton{
			{Text: "📁 前往 Web UI 创建任务", CallbackData: "task_create"},
		})
	}

	buttons = append(buttons, []InlineKeyboardButton{
		{Text: "🔄 刷新状态", CallbackData: callbackTaskList},
		{Text: "⏰ 定时任务", CallbackData: callbackTaskScheduled},
	})
	buttons = append(buttons, []InlineKeyboardButton{
		{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
	})

	return sb.String(), buttons
}

// BuildEmbyMenu 构建 Emby 刷库菜单
func BuildEmbyMenu(embyStatus map[string]any) (string, [][]InlineKeyboardButton) {
	var sb strings.Builder
	sb.WriteString("🎞️ <b>Emby 媒体库管理</b>\n\n")

	connected, _ := embyStatus["connected"].(bool)
	if connected {
		sb.WriteString("✅ Emby 已连接\n\n")
	} else {
		sb.WriteString("⚠️ Emby 未连接，请检查配置\n\n")
	}

	sb.WriteString("<b>快速刷库</b>\n")
	sb.WriteString("选择要刷新的媒体库类型：\n\n")

	pending, _ := embyStatus["pendingRefresh"].(int)
	if pending > 0 {
		sb.WriteString(fmt.Sprintf("⏳ <b>待刷新任务:</b> %d 个\n\n", pending))
	}

	buttons := [][]InlineKeyboardButton{
		{
			{Text: "🎬 电影库", CallbackData: callbackEmbyMovieLib},
			{Text: "📺 电视剧库", CallbackData: callbackEmbyTVLib},
		},
		{
			{Text: "🔄 刷新全部媒体库", CallbackData: callbackEmbyAllLib},
		},
		{
			{Text: "🔍 按路径刷库", CallbackData: "emby_refresh_path"},
		},
		{
			{Text: "🔄 刷新状态", CallbackData: callbackEmbyStatus},
		},
		{
			{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
		},
	}

	return sb.String(), buttons
}

// BuildSTRMMenu 构建 STRM 操作菜单
func BuildSTRMMenu() (string, [][]InlineKeyboardButton) {
	text := "📁 <b>STRM 文件操作</b>\n\n" +
		"选择要执行的操作：\n\n" +
		"• 🔍 <b>全量同步</b> — 选择一个任务执行，或全部按顺序执行\n" +
		"• 🧹 <b>清理孤儿</b> — 只对账并删除云端已删除的 STRM（不重新生成）\n\n" +
		"⚠️ 两个操作都在后台执行，完成后推送通知。"

	buttons := [][]InlineKeyboardButton{
		{
			{Text: "🔍 全量同步（选择任务）", CallbackData: callbackSyncPick},
		},
		{
			{Text: "🧹 清理孤儿 STRM", CallbackData: callbackCleanPick},
		},
		{
			{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
		},
	}
	return text, buttons
}

// BuildSyncTaskPicker 构建「全量同步」的任务选择页
func BuildSyncTaskPicker(tasks []map[string]any) (string, [][]InlineKeyboardButton) {
	var sb strings.Builder
	sb.WriteString("🔍 <b>全量同步</b>\n\n")
	back := [][]InlineKeyboardButton{{{Text: "🔙 返回", CallbackData: callbackMenuSTRM}}}
	if len(tasks) == 0 {
		sb.WriteString("暂无任务，请先在 Web UI 创建任务。")
		return sb.String(), back
	}
	sb.WriteString("选择要执行的任务（异步执行，完成后推送通知）：\n\n")

	buttons := make([][]InlineKeyboardButton, 0, len(tasks)+2)
	for _, t := range tasks {
		id, _ := t["id"].(string)
		name, _ := t["name"].(string)
		account, _ := t["account"].(string)
		schedule, _ := t["schedule"].(string)
		if name == "" {
			name = id
		}
		if schedule == "" {
			schedule = "手动"
		}
		sb.WriteString(fmt.Sprintf("• <b>%s</b> — %s · %s\n",
			EscapeHTML(name), EscapeHTML(account), EscapeHTML(schedule)))
		if id != "" {
			buttons = append(buttons, []InlineKeyboardButton{
				{Text: "▶️ " + truncate(name, 18), CallbackData: callbackSyncRun + id},
			})
		}
	}
	buttons = append(buttons, []InlineKeyboardButton{
		{Text: "▶️ 全部按顺序执行", CallbackData: callbackSyncAll},
	})
	buttons = append(buttons, back...)
	return sb.String(), buttons
}

// BuildCleanupMappingPicker 构建「清理孤儿」的映射选择页
func BuildCleanupMappingPicker(mappings []map[string]string) (string, [][]InlineKeyboardButton) {
	var sb strings.Builder
	sb.WriteString("🧹 <b>清理孤儿 STRM</b>\n\n")
	sb.WriteString("只对账、不重新生成：找出云端已删除或改名/移动后残留的本地 STRM，确认后删除。\n\n")
	back := [][]InlineKeyboardButton{{{Text: "🔙 返回", CallbackData: callbackMenuSTRM}}}
	if len(mappings) == 0 {
		sb.WriteString("未配置生活监控路径映射，无法按目录清理。\n可在 Web UI 的监控设置里添加路径映射，或到清理页手动指定目录。")
		return sb.String(), back
	}
	sb.WriteString("选择要扫描的目录：\n\n")

	buttons := make([][]InlineKeyboardButton, 0, len(mappings)+1)
	for i, m := range mappings {
		account := m["account"]
		cloud := m["cloudPath"]
		local := m["localPath"]
		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n  <code>%s</code> → <code>%s</code>\n",
			EscapeHTML(account), EscapeHTML(cloud), EscapeHTML(local)))
		buttons = append(buttons, []InlineKeyboardButton{
			{Text: "🧹 扫描 " + truncate(cloud, 20), CallbackData: fmt.Sprintf("%s%d", callbackCleanScan, i)},
		})
	}
	buttons = append(buttons, back...)
	return sb.String(), buttons
}

// BuildHelpMenu 构建帮助菜单
func BuildHelpMenu() (string, [][]InlineKeyboardButton) {
	text := "❓ <b>FastStrm Bot 帮助</b>\n\n" +
		"<b>📊 系统状态</b>\n" +
		"查看账号 Cookie 状态、监控运行状态、任务运行进度、Emby 连接状态。\n\n" +
		"<b>📺 监控控制</b>\n" +
		"启动/停止各账号的生活事件监控，控制事件类型（创建/删除/移动/重命名）。\n\n" +
		"<b>🎬 任务管理</b>\n" +
		"执行定时任务、取消运行中任务，查看任务进度。\n\n" +
		"<b>🎞️ Emby 刷库</b>\n" +
		"STRM 文件变更后主动刷新 Emby 媒体库，加快入库速度。\n\n" +
		"<b>📁 STRM 操作</b>\n" +
		"全量同步或清理孤儿 STRM 文件。\n\n" +
		"<b>💡 提示</b>\n" +
		"点击按钮后会自动返回结果。使用 /start 随时打开此菜单。"

	buttons := [][]InlineKeyboardButton{
		{
			{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
		},
	}
	return text, buttons
}

// ==================== 回调路由 ====================

// RouteMenuCallback 路由菜单回调到对应处理函数
func (h *CommandHandler) RouteMenuCallback(ctx context.Context, cq CallbackQuery) error { //nolint:cyclop // complexity: 33
	if cq.Message == nil || cq.Message.Chat == nil {
		return nil
	}
	chatID := fmt.Sprintf("%d", cq.Message.Chat.ID)
	messageID := int64(cq.Message.MessageID)
	data := cq.Data

	logger.S().Debugf("[Menu] Route callback: %s", data)

	switch {
	// ===== 主菜单导航 =====
	case data == callbackMenuMain:
		return h.showMainMenu(ctx, chatID, messageID)
	case data == callbackMenuStatus:
		return h.showStatusMenu(ctx, chatID, messageID)
	case data == callbackMenuTasks:
		return h.showTasksMenu(ctx, chatID, messageID)
	case data == callbackMenuMonitor:
		return h.showMonitorMenu(ctx, chatID, messageID)
	case data == callbackMenuEmby:
		return h.showEmbyMenu(ctx, chatID, messageID)
	case data == callbackMenuSTRM:
		return h.showSTRMMenu(ctx, chatID, messageID)
	case data == callbackMenuHelp:
		return h.showHelpMenu(ctx, chatID, messageID)

	// ===== 监控控制 =====
	case strings.HasPrefix(data, callbackMonStart):
		acc := strings.TrimPrefix(data, callbackMonStart)
		return h.handleStartMonitor(ctx, chatID, messageID, acc)
	case strings.HasPrefix(data, callbackMonStop):
		acc := strings.TrimPrefix(data, callbackMonStop)
		return h.handleStopMonitor(ctx, chatID, messageID, acc)
	case data == callbackMonStopAll:
		return h.handleStopAllMonitors(ctx, chatID, messageID)
	case data == callbackMonStatus:
		return h.showMonitorMenu(ctx, chatID, messageID)
	case strings.HasPrefix(data, "mon_detail:"):
		acc := strings.TrimPrefix(data, "mon_detail:")
		return h.handleMonitorDetail(ctx, chatID, messageID, acc)
	case strings.HasPrefix(data, "mon_event:"):
		parts := strings.SplitN(strings.TrimPrefix(data, "mon_event:"), ":", 2)
		if len(parts) == 2 {
			return h.handleToggleEvent(ctx, chatID, messageID, parts[0], parts[1])
		}

	// ===== 任务控制 =====
	case strings.HasPrefix(data, callbackTaskExecute):
		taskID := strings.TrimPrefix(data, callbackTaskExecute)
		return h.handleExecuteTask(ctx, chatID, messageID, taskID)
	case strings.HasPrefix(data, callbackTaskCancel):
		taskID := strings.TrimPrefix(data, callbackTaskCancel)
		return h.handleCancelTask(ctx, chatID, messageID, taskID)
	case data == callbackTaskList:
		return h.showTasksMenu(ctx, chatID, messageID)
	case data == callbackTaskScheduled:
		return h.showTasksMenu(ctx, chatID, messageID)

	// ===== Emby 刷库 =====
	case data == callbackEmbyMovieLib:
		return h.handleEmbyRefresh(ctx, chatID, messageID, "movie")
	case data == callbackEmbyTVLib:
		return h.handleEmbyRefresh(ctx, chatID, messageID, "tv")
	case data == callbackEmbyAllLib:
		return h.handleEmbyRefresh(ctx, chatID, messageID, "all")
	case data == callbackEmbyStatus:
		return h.showEmbyMenu(ctx, chatID, messageID)
	case strings.HasPrefix(data, callbackEmbyRefresh):
		path := strings.TrimPrefix(data, callbackEmbyRefresh)
		return h.handleEmbyRefreshByPath(ctx, chatID, messageID, path)
	case data == "emby_refresh_path":
		return h.tg().EditMessageText(ctx, chatID, messageID, "请在 Web UI 触发路径刷库操作。", "HTML")

	// ===== STRM 通知内联按钮（来自 dispatcher.buildInlineKeyboard） =====
	case strings.HasPrefix(data, callbackStrmRefreshEmby):
		kind := strings.TrimPrefix(data, callbackStrmRefreshEmby)
		return h.handleStrmRefreshEmby(ctx, chatID, messageID, kind)
	case strings.HasPrefix(data, callbackStrmDetail):
		path := strings.TrimPrefix(data, callbackStrmDetail)
		return h.handleStrmDetail(ctx, chatID, messageID, path)

	// ===== STRM 操作 =====
	// 全量同步：先选任务（旧入口 strm_sync 一并导向选择页）
	case data == callbackStrmSync || data == callbackSyncPick:
		return h.showSyncTaskPicker(ctx, chatID, messageID)
	case strings.HasPrefix(data, callbackSyncRun):
		return h.handleSyncRun(ctx, chatID, messageID, strings.TrimPrefix(data, callbackSyncRun))
	case data == callbackSyncAll:
		return h.handleSyncAll(ctx, chatID, messageID)
	// 清理孤儿：先选映射，扫描结果走 TG 待确认队列
	case data == callbackStrmCleanup || data == callbackCleanPick:
		return h.showCleanupMappingPicker(ctx, chatID, messageID)
	case strings.HasPrefix(data, callbackCleanScan):
		return h.handleCleanupScan(ctx, chatID, messageID, strings.TrimPrefix(data, callbackCleanScan))
	case data == "task_create":
		return h.tg().EditMessageText(ctx, chatID, messageID, "请在 Web UI 创建新任务。", "HTML")

	default:
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("✅ 已处理: %s\n\n点击下方按钮返回菜单。", data), "HTML")
	}
	return nil
}

// ==================== 菜单渲染方法 ====================

// ShowMainMenuForChat 发送主菜单到指定 chatID（用于 /start 命令）
func (h *CommandHandler) ShowMainMenuForChat(ctx context.Context, chatID string) error {
	text, buttons := BuildMainMenu()
	return h.tg().SendMessageWithButtons(ctx, chatID, text, buttons)
}

// showMainMenu 显示主菜单（编辑现有消息）
func (h *CommandHandler) showMainMenu(ctx context.Context, chatID string, messageID int64) error {
	text, buttons := BuildMainMenu()
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// showStatusMenu 显示系统状态
func (h *CommandHandler) showStatusMenu(ctx context.Context, chatID string, messageID int64) error {
	var status map[string]any
	if h.menuActions != nil {
		if s, err := h.menuActions.GetSystemStatus(); err == nil {
			status = s
		}
	}
	if status == nil {
		status = h.buildDefaultStatus()
	}
	text, buttons := BuildStatusMenu(status)
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// showMonitorMenu 显示监控菜单
func (h *CommandHandler) showMonitorMenu(ctx context.Context, chatID string, messageID int64) error {
	var status map[string]any
	if h.menuActions != nil {
		if s, err := h.menuActions.GetMonitorStatus(); err == nil {
			status = s
		}
	}
	if status == nil {
		status = map[string]any{"monitors": []map[string]any{}}
	}
	text, buttons := BuildMonitorMenu(status)
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// showTasksMenu 显示任务菜单
func (h *CommandHandler) showTasksMenu(ctx context.Context, chatID string, messageID int64) error {
	var running, scheduled []map[string]any
	if h.menuActions != nil {
		if r, err := h.menuActions.ListRunningTasks(); err == nil {
			running = r
		}
		if s, err := h.menuActions.ListScheduledTasks(); err == nil {
			scheduled = s
		}
	}
	text, buttons := BuildTasksMenu(running, scheduled)
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// showEmbyMenu 显示 Emby 菜单
func (h *CommandHandler) showEmbyMenu(ctx context.Context, chatID string, messageID int64) error {
	var status map[string]any
	if h.menuActions != nil {
		if s, err := h.menuActions.GetEmbyStatus(); err == nil {
			status = s
		}
	}
	if status == nil {
		status = map[string]any{"connected": false}
	}
	text, buttons := BuildEmbyMenu(status)
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// showSTRMMenu 显示 STRM 菜单
func (h *CommandHandler) showSTRMMenu(ctx context.Context, chatID string, messageID int64) error {
	text, buttons := BuildSTRMMenu()
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// showHelpMenu 显示帮助
func (h *CommandHandler) showHelpMenu(ctx context.Context, chatID string, messageID int64) error {
	text, buttons := BuildHelpMenu()
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// ==================== 操作处理方法 ====================

func (h *CommandHandler) handleStartMonitor(ctx context.Context, chatID string, messageID int64, account string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if err := h.menuActions.StartMonitor(ctx, account); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 启动监控失败: %v", err), "HTML")
	}
	return h.showMonitorMenu(ctx, chatID, messageID)
}

func (h *CommandHandler) handleStopMonitor(ctx context.Context, chatID string, messageID int64, account string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if err := h.menuActions.StopMonitor(ctx, account); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 停止监控失败: %v", err), "HTML")
	}
	return h.showMonitorMenu(ctx, chatID, messageID)
}

func (h *CommandHandler) handleStopAllMonitors(ctx context.Context, chatID string, messageID int64) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if err := h.menuActions.StopAllMonitors(ctx); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 停止全部监控失败: %v", err), "HTML")
	}
	return h.showMonitorMenu(ctx, chatID, messageID)
}

func (h *CommandHandler) handleMonitorDetail(ctx context.Context, chatID string, messageID int64, account string) error {
	text := fmt.Sprintf("📺 <b>%s</b> 监控详情\n\n", account)
	text += "请前往 Web UI 查看详细监控配置和事件日志。"
	buttons := [][]InlineKeyboardButton{
		{{Text: "🔙 返回监控菜单", CallbackData: callbackMenuMonitor}},
	}
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

func (h *CommandHandler) handleToggleEvent(ctx context.Context, chatID string, messageID int64, eventType, state string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	enabled := state == "on"
	eventLabels := map[string]string{
		"create": "创建",
		"remove": "删除",
		"move":   "移动",
		"rename": "重命名",
	}
	label := eventLabels[eventType]
	if label == "" {
		label = eventType
	}

	if err := h.menuActions.ToggleMonitorEvent(ctx, "", eventType, enabled); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 切换 %s 事件失败: %v", label, err), "HTML")
	}
	return h.showMonitorMenu(ctx, chatID, messageID)
}

func (h *CommandHandler) handleExecuteTask(ctx context.Context, chatID string, messageID int64, taskID string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	result, err := h.menuActions.ExecuteTask(ctx, taskID)
	if err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 执行任务失败: %v", err), "HTML")
	}
	msg := "✅ 任务已触发\n"
	if m, ok := result["message"].(string); ok && m != "" {
		msg += m
	}
	return h.tg().EditMessageText(ctx, chatID, messageID, msg, "HTML")
}

func (h *CommandHandler) handleCancelTask(ctx context.Context, chatID string, messageID int64, taskID string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if err := h.menuActions.CancelTask(ctx, taskID); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 取消任务失败: %v", err), "HTML")
	}
	return h.showTasksMenu(ctx, chatID, messageID)
}

func (h *CommandHandler) handleEmbyRefresh(ctx context.Context, chatID string, messageID int64, libType string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	labels := map[string]string{
		"movie": "电影库",
		"tv":    "电视剧库",
		"all":   "全部媒体库",
	}
	label := labels[libType]
	if label == "" {
		label = libType
	}

	if err := h.menuActions.RefreshEmbyLibrary(ctx, libType); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 刷新 %s 失败: %v", label, err), "HTML")
	}
	return h.tg().EditMessageText(ctx, chatID, messageID,
		fmt.Sprintf("✅ <b>%s</b> 刷新已触发\n\n刷新完成后 Emby 会自动通知入库。", label), "HTML")
}

func (h *CommandHandler) handleEmbyRefreshByPath(ctx context.Context, chatID string, messageID int64, path string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if err := h.menuActions.RefreshEmbyByPath(ctx, path); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, fmt.Sprintf("❌ 刷新路径失败: %v", err), "HTML")
	}
	return h.tg().EditMessageText(ctx, chatID, messageID,
		fmt.Sprintf("✅ 路径刷库已触发: <code>%s</code>", path), "HTML")
}

// showSyncTaskPicker 全量同步：列出全部任务供选择
func (h *CommandHandler) showSyncTaskPicker(ctx context.Context, chatID string, messageID int64) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	tasks, err := h.menuActions.ListSyncTasks()
	if err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "❌ 读取任务失败: "+EscapeHTML(err.Error()), "HTML")
	}
	text, buttons := BuildSyncTaskPicker(tasks)
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// handleSyncRun 执行用户选中的单个任务（异步）
func (h *CommandHandler) handleSyncRun(ctx context.Context, chatID string, messageID int64, taskID string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if taskID == "" {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 任务 ID 为空。", "HTML")
	}
	if err := h.menuActions.RunTaskByID(ctx, taskID); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "❌ 触发失败: "+EscapeHTML(err.Error()), "HTML")
	}
	return h.tg().EditMessageText(ctx, chatID, messageID,
		"✅ <b>任务已触发</b>\n\n后台执行中，完成后会推送通知；进度可在 Web UI 或 /status 查看。", "HTML")
}

// handleSyncAll 按顺序执行全部任务（异步）
func (h *CommandHandler) handleSyncAll(ctx context.Context, chatID string, messageID int64) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	if err := h.menuActions.RunAllTasksSequential(ctx); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "❌ 触发失败: "+EscapeHTML(err.Error()), "HTML")
	}
	return h.tg().EditMessageText(ctx, chatID, messageID,
		"✅ <b>已开始按顺序执行全部任务</b>\n\n一个完成后自动执行下一个；账号正在跑任务时会跳过并记录。全部结束后推送汇总。", "HTML")
}

// showCleanupMappingPicker 清理孤儿：列出可扫描的路径映射
func (h *CommandHandler) showCleanupMappingPicker(ctx context.Context, chatID string, messageID int64) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	mappings, err := h.menuActions.ListCleanupMappings()
	if err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "❌ 读取映射失败: "+EscapeHTML(err.Error()), "HTML")
	}
	text, buttons := BuildCleanupMappingPicker(mappings)
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}

// handleCleanupScan 扫描指定映射（异步），结果由适配器送入 TG 待确认队列
func (h *CommandHandler) handleCleanupScan(ctx context.Context, chatID string, messageID int64, indexStr string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	idx, err := strconv.Atoi(strings.TrimSpace(indexStr))
	if err != nil || idx < 0 {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 无效的映射序号。", "HTML")
	}
	if err := h.menuActions.ScanCleanupMappingAsync(ctx, idx); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "❌ 扫描失败: "+EscapeHTML(err.Error()), "HTML")
	}
	return h.tg().EditMessageText(ctx, chatID, messageID,
		"🔍 <b>已开始扫描</b>\n\n只对账不生成，完成后会发送结果；如有失效 STRM，会附「确认删除 / 取消」按钮。", "HTML")
}

// ==================== 辅助函数 ====================

// handleStrmRefreshEmby 处理 STRM 通知中的"刷新 Emby"按钮
// kind: movie / tv / series
func (h *CommandHandler) handleStrmRefreshEmby(ctx context.Context, chatID string, messageID int64, kind string) error {
	if h.menuActions == nil {
		return h.tg().EditMessageText(ctx, chatID, messageID, "⚠️ 菜单操作未初始化，请重启服务。", "HTML")
	}
	labels := map[string]string{
		"movie":  "电影库",
		"tv":     "电视剧库",
		"series": "电视剧库",
	}
	label := labels[kind]
	if label == "" {
		label = kind
	}
	if err := h.menuActions.RefreshEmbyLibrary(ctx, kind); err != nil {
		return h.tg().EditMessageText(ctx, chatID, messageID,
			fmt.Sprintf("❌ 刷新 %s 失败: %v", label, err), "HTML")
	}
	return h.tg().EditMessageText(ctx, chatID, messageID,
		fmt.Sprintf("✅ <b>%s</b> 刷新已触发\n\n刷新完成后 Emby 会自动通知入库。", label), "HTML")
}

// handleStrmDetail 处理 STRM 通知中的"查看详情"按钮
func (h *CommandHandler) handleStrmDetail(ctx context.Context, chatID string, messageID int64, ref string) error {
	// ref 是新格式的短 ID；兼容旧消息里直接塞的完整路径
	cloudPath := ref
	if p, ok := LookupDetailPath(ref); ok {
		cloudPath = p
	}
	text := fmt.Sprintf("🔍 <b>STRM 详情</b>\n\n"+
		"<b>云端路径:</b> <code>%s</code>\n\n"+
		"💡 在 Web UI 中可查看完整事件日志和文件详情。",
		EscapeHTML(cloudPath))

	buttons := [][]InlineKeyboardButton{
		{
			{Text: "🔄 刷新 Emby 电影库", CallbackData: callbackEmbyMovieLib},
			{Text: "🔄 刷新 Emby 电视剧库", CallbackData: callbackEmbyTVLib},
		},
		{
			{Text: "🔙 返回主菜单", CallbackData: callbackMenuMain},
		},
	}
	return h.tg().EditMessageTextWithButtons(ctx, chatID, messageID, text, buttons)
}
func (h *CommandHandler) buildDefaultStatus() map[string]any {
	accounts := h.accounts.List()
	var accountList []map[string]any
	for _, acc := range accounts {
		accountList = append(accountList, map[string]any{
			"name":      acc.Name,
			"hasCookie": acc.Cookie != "",
		})
	}
	return map[string]any{
		"accounts":     accountList,
		"monitors":     []map[string]any{},
		"runningTasks": []map[string]any{},
		"emby": map[string]any{
			"connected": false,
		},
	}
}

func boolToText(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
