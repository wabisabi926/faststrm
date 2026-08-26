package server

import (
	"context"
	"fmt"

	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
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
}

// GetSystemStatus 聚合账号、监控、运行任务和 Emby 状态
func (a *menuActionsAdapter) GetSystemStatus() (map[string]any, error) {
	accounts := a.accountStore.List()
	var accountList []map[string]any
	for _, acc := range accounts {
		accountList = append(accountList, map[string]any{
			"name":     acc.Name,
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
			"create":  et.Create,
			"remove":  et.Remove,
			"rename":  et.Rename,
			"move":    et.Move,
		}
	}
	return result
}

// ToggleMonitorEvent 切换监控事件类型（持久化到 settings.json）
func (a *menuActionsAdapter) ToggleMonitorEvent(ctx context.Context, account, eventType string, enabled bool) error {
	s, err := a.settingsStore.ReadSettings()
	if err != nil {
		return fmt.Errorf("读取设置失败: %w", err)
	}
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
	if err := a.settingsStore.SaveSettings(s); err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	return nil
}

// ExecuteTask 执行指定任务
func (a *menuActionsAdapter) ExecuteTask(ctx context.Context, taskID string) (map[string]any, error) {
	result := task.ExecuteTask(ctx, taskID, a.execDeps)
	return map[string]any{
		"success": result.Success,
		"message": result.Message,
		"taskId":  result.TaskID,
	}, nil
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

// RunFullSync 全量同步（占位实现）
func (a *menuActionsAdapter) RunFullSync(ctx context.Context) error {
	return nil
}

// RunCleanup 清理孤儿（占位实现）
func (a *menuActionsAdapter) RunCleanup(ctx context.Context) error {
	return nil
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
