package handler

import (
	"context"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

const (
	// maintenanceInterval 维护周期：一天一次。回收站保留 7 天，日粒度足够。
	maintenanceInterval = 24 * time.Hour
	// maintenanceStartupDelay 启动后延迟：避开启动期的目录扫描与配置迁移。
	maintenanceStartupDelay = 1 * time.Minute
	// trashRetention 回收站保留时长（与设置页文案一致）
	trashRetention = 7 * 24 * time.Hour
	// folderCacheRetention folders 目录路径缓存保留时长。
	// 监控关闭期间云端目录改名不会更新缓存，过期后淘汰并由调用方回退到接口解析。
	folderCacheRetention = 7 * 24 * time.Hour
)

// StartMaintenance 启动后台维护：清理过期回收站 + 裁剪执行记录。
//
// 为什么需要它：这两个动作此前都是「机会式」触发——回收站只在跑清理/任务时顺带清，
// 执行记录只在创建新记录时裁剪。长期不操作的用户，回收站会一直攒着过期文件，
// 执行记录也可能在没有任何新任务的情况下停在旧状态。
//
// 启动后延迟 1 分钟跑一次（顺便修复升级前的积压），之后每 24 小时一次。
// 与项目其他后台组件（调度器、监控、Emby 反代）一致：不单独做优雅停止，随进程退出。
func StartMaintenance(deps StrmCleanupDeps) {
	go func() {
		time.Sleep(maintenanceStartupDelay)
		runMaintenance(deps)
		ticker := time.NewTicker(maintenanceInterval)
		defer ticker.Stop()
		for range ticker.C {
			runMaintenance(deps)
		}
	}()
}

// runMaintenance 执行一次维护。任何一步失败都不影响其它步骤。
func runMaintenance(deps StrmCleanupDeps) {
	if deps.SettingsStore == nil {
		return
	}
	settings, err := deps.SettingsStore.ReadSettings()
	if err != nil {
		logger.S().Warnf("[maintenance] 读取设置失败，跳过本轮: %v", err)
		return
	}

	// 1) 过期回收站：只删超过保留期的批次，未过期的一律不动
	removed := 0
	roots := allowedCleanupRoots(settings, deps)
	for _, root := range roots {
		removed += strmutil.PurgeTrashTree(root, trashRetention)
	}

	// 2) 执行记录：保留最近 N 条（含日志）
	if deps.TaskHistory != nil {
		if terr := deps.TaskHistory.TrimExecutions(context.Background(), db.TaskHistoryKeepExecutions); terr != nil {
			logger.S().Warnf("[maintenance] 裁剪执行记录失败: %v", terr)
		}
	}

	// 3) folders 目录缓存：淘汰超过 TTL 的记录，避免监控关闭期间目录改名后继续用旧路径
	if deps.SQLiteDB != nil {
		cutoff := time.Now().Add(-folderCacheRetention).Unix()
		if n, ferr := db.PurgeStaleFolders(deps.SQLiteDB, cutoff); ferr != nil {
			logger.S().Warnf("[maintenance] 清理 folders 缓存失败: %v", ferr)
		} else if n > 0 {
			logger.S().Infof("[maintenance] 已清理 %d 条过期 folders 缓存", n)
		}
	}

	logger.S().Infof("[maintenance] 完成：清理 %d 个过期回收站批次（扫描 %d 个根目录）", removed, len(roots))
}
