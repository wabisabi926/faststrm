package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== MenuActions 适配器 ====================

// menuActionsAdapter 实现 notify.MenuActions 接口
// 将 Telegram 菜单动作委托给 Monitor / Task / Emby 等服务
type menuActionsAdapter struct {
	settingsStore *store.SettingsStore
	tasksStore    *store.TasksStore
	accountStore  *store.AccountStore
	monitor       *monitor.Monitor
	embyRefresh   *emby.MediaServerRefresh
	taskRuntime   *task.Runtime
	execDeps      task.ExecutorDeps

	// 异步流程完成后主动推送消息用
	cmdHandler *notify.CommandHandler
	// 清理扫描：复用 Web UI 同一套扫描逻辑，结果送入 TG 待确认队列
	cleanupInteraction *handler.StrmCleanupInteraction
	cleanupDeps        handler.StrmCleanupDeps

	runAll          atomic.Bool // 「全部按顺序执行」防重入
	cleanupScanning atomic.Bool // 清理扫描防重入（同一时间只允许一个）
}

// RecentEvents 最近 n 条生活事件日志（TG /status 展示用）
func (a *menuActionsAdapter) RecentEvents(ctx context.Context, n int) []map[string]any {
	if a.monitor == nil {
		return nil
	}
	logs := a.monitor.RecentEvents(ctx, n)
	out := make([]map[string]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, map[string]any{
			"eventType": l.EventType,
			"account":   l.Account,
			"success":   l.Success,
			"message":   l.Message,
			"timestamp": l.Timestamp,
		})
	}
	return out
}

// GetSystemStatus 聚合账号、监控、运行任务和 Emby 状态
func (a *menuActionsAdapter) GetSystemStatus() (map[string]any, error) {
	accounts := a.accountStore.List()
	var accountList []map[string]any
	for _, acc := range accounts {
		accountList = append(accountList, map[string]any{
			"name":      acc.Name,
			"hasCookie": acc.Cookie != "",
		})
	}

	monitorStatus := a.internalGetMonitorStatus()
	runningTasks := a.internalListRunningTasks()

	embyStatus := map[string]any{"connected": false}
	if a.embyRefresh != nil {
		embyStatus = a.embyRefresh.GetStatus()
	}

	return map[string]any{
		"accounts":     accountList,
		"monitors":     monitorStatus["monitors"],
		"runningTasks": runningTasks,
		"emby":         embyStatus,
	}, nil
}

// StartMonitor 启动指定账号的监控
func (a *menuActionsAdapter) StartMonitor(ctx context.Context, account string) error {
	if a.monitor == nil {
		return fmt.Errorf("monitor not initialized")
	}
	return a.monitor.Start(ctx, account)
}

// StopMonitor 停止指定账号的监控
func (a *menuActionsAdapter) StopMonitor(ctx context.Context, account string) error {
	if a.monitor == nil {
		return fmt.Errorf("monitor not initialized")
	}
	a.monitor.Stop(account)
	return nil
}

// StopAllMonitors 停止所有监控
func (a *menuActionsAdapter) StopAllMonitors(ctx context.Context) error {
	if a.monitor == nil {
		return fmt.Errorf("monitor not initialized")
	}
	a.monitor.StopAll()
	return nil
}

// GetMonitorStatus 获取监控状态
func (a *menuActionsAdapter) GetMonitorStatus() (map[string]any, error) {
	return a.internalGetMonitorStatus(), nil
}

func (a *menuActionsAdapter) internalGetMonitorStatus() map[string]any {
	result := map[string]any{"monitors": []map[string]any{}}
	if a.monitor == nil {
		return result
	}
	status := a.monitor.Status()
	var monitors []map[string]any
	for _, s := range status {
		monitors = append(monitors, map[string]any{
			"account": s.Account,
			"running": s.Running,
		})
	}
	result["monitors"] = monitors

	// 返回事件开关状态（供 TG 菜单动态显示）
	if s, err := a.settingsStore.ReadSettings(); err == nil {
		et := s.LifeMonitor.EventTypes
		result["eventTypes"] = map[string]bool{
			"create": et.Create,
			"remove": et.Remove,
			"rename": et.Rename,
			"move":   et.Move,
		}
	}
	return result
}

// ToggleMonitorEvent 切换监控事件类型（持久化到 settings.json）
func (a *menuActionsAdapter) ToggleMonitorEvent(ctx context.Context, account, eventType string, enabled bool) error {
	_, err := a.settingsStore.Update(func(s *model.Settings) error {
		switch eventType {
		case "create":
			s.LifeMonitor.EventTypes.Create = enabled
		case "remove":
			s.LifeMonitor.EventTypes.Remove = enabled
		case "rename":
			s.LifeMonitor.EventTypes.Rename = enabled
		case "move":
			s.LifeMonitor.EventTypes.Move = enabled
		default:
			return fmt.Errorf("未知事件类型: %s", eventType)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	return nil
}

// ExecuteTask 执行指定任务（异步）
// 任务可能跑几分钟，同步执行会阻塞 Telegram 轮询循环 / webhook 请求，
// 这里立即返回「已触发」，结果由任务自身的完成通知推送。
func (a *menuActionsAdapter) ExecuteTask(ctx context.Context, taskID string) (map[string]any, error) {
	go a.runTaskAsync(taskID)
	return map[string]any{
		"success": true,
		"taskId":  taskID,
		"message": "任务已在后台执行，完成后会推送通知",
	}, nil
}

// runTaskAsync 后台执行任务并记录失败原因（任务完成/失败由 dispatcher 通知）。
// 同时启动进度回执：定期编辑同一条 TG 消息展示阶段与进度。
func (a *menuActionsAdapter) runTaskAsync(taskID string) {
	defer func() {
		if r := recover(); r != nil {
			logger.S().Errorf("[TG] 后台任务 %s panic: %v", taskID, r)
		}
	}()
	a.startProgressWatch(taskID)
	res := task.ExecuteTask(context.Background(), taskID, a.execDeps)
	if !res.Success {
		logger.S().Warnf("[TG] 后台任务 %s 未成功: reason=%s msg=%s", taskID, res.Reason, res.Message)
	}
}

// ==================== 任务进度回执 ====================

const (
	// progressWatchInterval 进度回执更新间隔。Telegram 编辑消息有频率限制，不能太密
	progressWatchInterval = 45 * time.Second
	// progressWatchMaxAge 监听上限，防止任务状态异常导致 goroutine 泄漏
	progressWatchMaxAge = 2 * time.Hour
)

// startProgressWatch 发送一条进度消息并周期性编辑它，直到任务从 Runtime 消失。
// 单独 goroutine，不阻塞任务执行；失败（发不出/编辑不了）时静默放弃。
func (a *menuActionsAdapter) startProgressWatch(taskID string) {
	if a.cmdHandler == nil || a.taskRuntime == nil {
		return
	}
	bot := a.cmdHandler.CurrentBot()
	if bot == nil {
		return
	}
	chatID := bot.ChatID()
	if chatID == "" {
		return
	}
	taskName := a.taskNameByID(taskID)
	if taskName == "" {
		taskName = taskID
	}

	msgID, err := bot.SendMessageReturningID(context.Background(), chatID,
		progressText(taskName, nil), "HTML")
	if err != nil {
		logger.S().Warnf("[TG] 进度消息发送失败 task=%s: %v", taskID, err)
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.S().Errorf("[TG] 进度监听 panic task=%s: %v", taskID, r)
			}
		}()
		start := time.Now()
		ticker := time.NewTicker(progressWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			}
			state := a.taskRuntime.GetState(taskID)
			if state == nil {
				return // 任务已结束，结果通知由 dispatcher 负责
			}
			if time.Since(start) > progressWatchMaxAge {
				logger.S().Warnf("[TG] 进度监听超时退出 task=%s", taskID)
				return
			}
			if err := bot.EditMessageText(context.Background(), chatID, msgID,
				progressText(taskName, state), "HTML"); err != nil {
				// "message is not modified" 已在 EditMessageText 内部（ignoreEditNoop）忽略，
				// 走到这里说明消息被删/不可编辑，放弃监听
				logger.S().Debugf("[TG] 进度消息编辑失败 task=%s: %v", taskID, err)
				return
			}
		}
	}()
}

// progressText 渲染进度消息。含已运行时长（每次编辑内容都不同，天然规避
// "message is not modified"），阶段详情存在时一并展示。
func progressText(taskName string, state *task.RuntimeState) string {
	var sb strings.Builder
	sb.WriteString("⏳ <b>" + notify.EscapeHTML(taskName) + "</b> 运行中…\n\n")
	if state == nil {
		sb.WriteString("启动中…")
		return sb.String()
	}
	elapsed := time.Since(time.UnixMilli(state.StartedAt)).Truncate(time.Second)
	line := ""
	if state.TotalFiles > 0 {
		pct := float64(state.DownloadedFiles) / float64(state.TotalFiles) * 100
		line = fmt.Sprintf("%.0f%% (%d/%d)", pct, state.DownloadedFiles, state.TotalFiles)
	} else if state.Stage != "" {
		line = stageLabel(state.Stage)
	} else {
		line = string(state.Status)
	}
	if state.StageDetail != "" {
		line += "\n" + notify.EscapeHTML(state.StageDetail)
	}
	sb.WriteString(fmt.Sprintf("%s\n已运行 %s", line, elapsed))
	return sb.String()
}

// stageLabel 阶段常量转中文
func stageLabel(stage string) string {
	labels := map[string]string{
		task.StageStarting:   "启动中",
		task.StageScanning:   "扫描目录",
		task.StageCleanup:    "清理文件",
		task.StageWritingDB:  "写回数据库",
		task.StageGenerating: "生成 STRM",
		task.StageFinalizing: "收尾处理",
		task.StageCompleted:  "已完成",
		task.StageFailed:     "失败",
	}
	if l, ok := labels[stage]; ok {
		return l
	}
	return stage
}

// taskNameByID 查任务名（查不到返回空）
func (a *menuActionsAdapter) taskNameByID(taskID string) string {
	if a.tasksStore == nil {
		return ""
	}
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return ""
	}
	for _, t := range tasks {
		if t.ID == taskID {
			return t.Name
		}
	}
	return ""
}

// CancelTask 取消指定任务
func (a *menuActionsAdapter) CancelTask(ctx context.Context, taskID string) error {
	if a.taskRuntime == nil {
		return fmt.Errorf("task runtime not initialized")
	}
	found := a.taskRuntime.Cancel(taskID)
	if !found {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// ListRunningTasks 列出运行中任务
func (a *menuActionsAdapter) ListRunningTasks() ([]map[string]any, error) {
	return a.internalListRunningTasks(), nil
}

func (a *menuActionsAdapter) internalListRunningTasks() []map[string]any {
	if a.taskRuntime == nil {
		return []map[string]any{}
	}
	running := a.taskRuntime.RunningTasks()
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return []map[string]any{}
	}

	var result []map[string]any
	for id, state := range running {
		var taskName string
		for _, t := range tasks {
			if t.ID == id {
				taskName = t.Name
				break
			}
		}
		progress := string(state.Status)
		if state.TotalFiles > 0 {
			pct := float64(state.DownloadedFiles) / float64(state.TotalFiles) * 100
			progress = fmt.Sprintf("%.0f%% (%d/%d)", pct, state.DownloadedFiles, state.TotalFiles)
		}
		result = append(result, map[string]any{
			"id":       id,
			"name":     taskName,
			"progress": progress,
		})
	}
	return result
}

// ListScheduledTasks 列出定时任务
func (a *menuActionsAdapter) ListScheduledTasks() ([]map[string]any, error) {
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, t := range tasks {
		if t.Schedule != nil && t.Schedule.Enabled {
			schedule := a.formatSchedule(t.Schedule)
			result = append(result, map[string]any{
				"id":       t.ID,
				"name":     t.Name,
				"schedule": schedule,
			})
		}
	}
	return result, nil
}

func (a *menuActionsAdapter) formatSchedule(s *task.TaskSchedule) string {
	switch s.Mode {
	case "interval":
		return fmt.Sprintf("每 %d 分钟", s.IntervalMinutes)
	case "daily":
		return fmt.Sprintf("每天 %s", s.Time)
	case "weekly":
		return fmt.Sprintf("每周 %s", s.Time)
	default:
		return "未知"
	}
}

// RefreshEmbyByPath 按路径刷新 Emby
func (a *menuActionsAdapter) RefreshEmbyByPath(ctx context.Context, path string) error {
	if a.embyRefresh == nil {
		return fmt.Errorf("emby refresh not initialized")
	}
	return a.embyRefresh.RefreshByPath(ctx, path)
}

// RefreshEmbyLibrary 刷新 Emby 媒体库
func (a *menuActionsAdapter) RefreshEmbyLibrary(ctx context.Context, libraryType string) error {
	if a.embyRefresh == nil {
		return fmt.Errorf("emby refresh not initialized")
	}
	return a.embyRefresh.RefreshLibrary(ctx, libraryType)
}

// GetEmbyStatus 获取 Emby 状态
func (a *menuActionsAdapter) GetEmbyStatus() (map[string]any, error) {
	if a.embyRefresh == nil {
		return map[string]any{"connected": false}, nil
	}
	return a.embyRefresh.GetStatus(), nil
}

// ==================== 全量同步：任务选择 ====================

// ListSyncTasks 列出全部任务（含未启用定时的手动任务），供「全量同步」选择
func (a *menuActionsAdapter) ListSyncTasks() ([]map[string]any, error) {
	if a.tasksStore == nil {
		return nil, fmt.Errorf("任务存储未初始化")
	}
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return nil, fmt.Errorf("读取任务失败: %w", err)
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		schedule := "手动"
		if t.Schedule != nil && t.Schedule.Enabled {
			schedule = a.formatSchedule(t.Schedule)
		}
		out = append(out, map[string]any{
			"id":       t.ID,
			"name":     t.Name,
			"account":  t.Account,
			"schedule": schedule,
		})
	}
	return out, nil
}

// RunTaskByID 异步执行单个任务
func (a *menuActionsAdapter) RunTaskByID(ctx context.Context, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("任务 ID 为空")
	}
	go a.runTaskAsync(taskID)
	return nil
}

// RunAllTasksSequential 异步按顺序执行全部任务：一个完成再下一个。
// 账号被其它任务占用（Blocked）时跳过，失败继续，最后推送汇总。
func (a *menuActionsAdapter) RunAllTasksSequential(ctx context.Context) error {
	if a.tasksStore == nil {
		return fmt.Errorf("任务存储未初始化")
	}
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return fmt.Errorf("读取任务失败: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("暂无任务，请先在 Web UI 创建任务")
	}
	if !a.runAll.CompareAndSwap(false, true) {
		return fmt.Errorf("「全部按顺序执行」已在进行中")
	}

	go func() {
		defer a.runAll.Store(false)
		var ok, skipped, failed int
		failedNames := make([]string, 0, 2)
		for _, t := range tasks {
			res := task.ExecuteTask(context.Background(), t.ID, a.execDeps)
			switch {
			case res.Success:
				ok++
			case res.Blocked:
				skipped++
				logger.S().Infof("[TG] 全部执行跳过 %s（%s）", t.ID, res.Message)
			default:
				failed++
				failedNames = append(failedNames, t.Name)
				logger.S().Warnf("[TG] 全部执行失败 %s: %s", t.ID, res.Message)
			}
		}
		msg := fmt.Sprintf("🏁 <b>全部任务执行完成</b>\n\n成功 %d / 跳过 %d / 失败 %d", ok, skipped, failed)
		if len(failedNames) > 0 {
			msg += "\n失败任务: " + notify.EscapeHTML(strings.Join(failedNames, "、"))
		}
		a.notifyUser(msg)
	}()
	return nil
}

// ==================== 清理孤儿：映射扫描 ====================

// ListCleanupMappings 列出可清理的路径映射（来自生活监控路径映射）
func (a *menuActionsAdapter) ListCleanupMappings() ([]map[string]string, error) {
	if a.settingsStore == nil {
		return nil, fmt.Errorf("设置存储未初始化")
	}
	s, err := a.settingsStore.ReadSettings()
	if err != nil {
		return nil, fmt.Errorf("读取设置失败: %w", err)
	}
	reqs := handler.CleanupMappings(s)
	var accounts []*model.AccountInfo
	if a.accountStore != nil {
		accounts = a.accountStore.List()
	}
	reqs = handler.ExpandCleanupMappingsForAccounts(reqs, accounts)
	out := make([]map[string]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, map[string]string{
			"account":   r.Account,
			"cloudPath": r.CloudPath,
			"localPath": r.LocalPath,
		})
	}
	return out, nil
}

// ScanCleanupMappingAsync 异步扫描指定映射：只对账不生成，
// 扫完把失效 STRM 送入 TG 待确认队列（带确认/取消按钮）。
func (a *menuActionsAdapter) ScanCleanupMappingAsync(ctx context.Context, index int) error {
	if a.cleanupInteraction == nil {
		return fmt.Errorf("清理待删队列未启用（SQLite 不可用）")
	}
	mappings, err := a.ListCleanupMappings()
	if err != nil {
		return err
	}
	if index < 0 || index >= len(mappings) {
		return fmt.Errorf("映射不存在（序号 %d）", index)
	}
	if !a.cleanupScanning.CompareAndSwap(false, true) {
		return fmt.Errorf("已有清理扫描在进行中，请稍后")
	}

	m := mappings[index]
	req := handler.MappingScanRequest{
		Account:   m["account"],
		CloudPath: m["cloudPath"],
		LocalPath: m["localPath"],
	}

	go func() {
		defer a.cleanupScanning.Store(false)
		a.notifyUser(fmt.Sprintf("🔍 开始扫描 <code>%s</code> …", notify.EscapeHTML(req.CloudPath)))

		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, serr := handler.ScanCleanupMapping(sctx, a.cleanupDeps, req)
		if serr != nil {
			a.notifyUser("❌ 扫描失败: " + notify.EscapeHTML(serr.Error()))
			return
		}
		if res.Error != "" {
			a.notifyUser("❌ 扫描失败: " + notify.EscapeHTML(res.Error))
			return
		}

		summary := fmt.Sprintf(
			"🧹 <b>扫描完成</b>\n\n目录: <code>%s</code>\n云端文件: %d\n本地 STRM: %d\n失效: %d\n缺失: %d",
			notify.EscapeHTML(req.CloudPath), res.RemoteFileCount, res.LocalStrmCount,
			len(res.StaleStrms), len(res.MissingStrms))

		if len(res.StaleStrms) == 0 {
			a.notifyUser(summary + "\n\n没有需要清理的 STRM。")
			return
		}

		settings, _ := a.settingsStore.ReadSettings()
		cleanupCfg := model.CleanupSettings{}
		if settings != nil {
			cleanupCfg = settings.Cleanup
		}
		paths := make([]string, 0, len(res.StaleStrms))
		for _, s := range res.StaleStrms {
			paths = append(paths, filepath.Join(req.LocalPath, s.RelPath))
		}
		batch := handler.CleanupBatch{
			RequestID:          handler.GenerateRequestID(),
			CreatedAt:          time.Now().UnixMilli(),
			Paths:              paths,
			SamplePaths:        handler.BuildSamplePaths(paths),
			PathCount:          len(paths),
			RemoveStrm:         true,
			RemoveEmptyDirs:    cleanupCfg.RemoveEmptyDirs,
			RemoveRelatedFiles: cleanupCfg.RemoveRelatedFiles,
		}
		if aerr := a.cleanupInteraction.AppendBatch(context.Background(), batch); aerr != nil {
			a.notifyUser("❌ 入队失败: " + notify.EscapeHTML(aerr.Error()))
			return
		}
		a.notifyUser(summary)
		if nerr := a.cleanupInteraction.NotifyTelegramPending(context.Background(), batch); nerr != nil {
			logger.S().Warnf("[TG] 清理确认通知发送失败 requestID=%s: %v", batch.RequestID, nerr)
		}
	}()
	return nil
}

// notifyUser 通过当前 Bot 推送一条 HTML 消息（异步流程完成后回执用）
func (a *menuActionsAdapter) notifyUser(text string) {
	if a.cmdHandler == nil {
		return
	}
	bot := a.cmdHandler.CurrentBot()
	if bot == nil {
		logger.S().Warnf("[TG] 当前 Bot 不可用，跳过推送: %s", text)
		return
	}
	if err := bot.SendNotification(context.Background(), text); err != nil {
		logger.S().Warnf("[TG] 推送失败: %v", err)
	}
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

// strmCacheWriterAdapter adapts *store.StrmCacheStore to task.StrmCacheWriter
type strmCacheWriterAdapter struct{ inner *store.StrmCacheStore }

func (a *strmCacheWriterAdapter) Save(entry task.StrmCacheEntryLike) error {
	return a.inner.Save(&store.StrmCacheEntry{
		UUID: entry.UUID, TaskID: entry.TaskID, Target: entry.Target,
		Account: entry.Account, RelPaths: entry.RelPaths, LocalPaths: entry.LocalPaths,
		CreatedAt: entry.CreatedAt,
	})
}

// cleanupSubmitterAdapter 把 handler.StrmCleanupInteraction 适配为 task.CleanupBatchSubmitter
// 在 Run 主流程提前创建 cleanupInteraction 时注入，让 task 执行器 removeExtraFiles
// 能调用 AppendBatch 把延迟批次持久化到 SQLite，按 ConfirmMode=="telegram" 派发 TG 通知。
type cleanupSubmitterAdapter struct {
	interaction *handler.StrmCleanupInteraction
}

// SubmitDeferredBatch 实现 task.CleanupBatchSubmitter 接口
// interaction 为 nil（SQLite 不可用）时返回错误，调用方（removeExtraFiles）会退化为立即删除。
func (a *cleanupSubmitterAdapter) SubmitDeferredBatch(ctx context.Context, b task.DeferredCleanupBatch) (string, error) {
	if a.interaction == nil {
		return "", fmt.Errorf("cleanup interaction not initialized")
	}
	batch := handler.CleanupBatch{
		RequestID:          b.RequestID,
		CreatedAt:          b.CreatedAt.UnixMilli(),
		Paths:              b.Paths,
		SamplePaths:        handler.BuildSamplePaths(b.Paths),
		PathCount:          len(b.Paths),
		RemoveStrm:         b.RemoveStrm,
		RemoveEmptyDirs:    b.RemoveEmptyDirs,
		RemoveRelatedFiles: b.RemoveRelated,
	}
	if err := a.interaction.AppendBatch(ctx, batch); err != nil {
		return "", fmt.Errorf("AppendBatch: %w", err)
	}
	// 按 ConfirmMode 派发通知（仅 telegram 模式发 TG 按钮；plugin_ui 由前端轮询 /pending）
	if b.ConfirmMode == "telegram" {
		if err := a.interaction.NotifyTelegramPending(ctx, batch); err != nil {
			// 通知失败不影响入队结果，仅记录警告
			logger.S().Warnf("[cleanupSubmitterAdapter] NotifyTelegramPending failed (requestID=%s): %v", b.RequestID, err)
		}
	}
	return b.RequestID, nil
}
