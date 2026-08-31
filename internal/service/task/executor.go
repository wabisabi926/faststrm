package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/pkg/concurrency"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// taskCompleteThreshold 任务完成判定阈值（99.995%）
const taskCompleteThreshold = 99.995

// defaultStrmWorkers STRM 写入默认并发数
const defaultStrmWorkers = 20

// StrmRefresher STRM 创建/删除后的媒体库刷新接口（由 emby.MediaServerRefresh 实现）
type StrmRefresher interface {
	RefreshOnCreate(ctx context.Context, filePath string) error
	RefreshOnDelete(ctx context.Context, filePath string) error
}

// TaskNotifier 任务状态通知接口（由 notify.Dispatcher 实现，可为 nil）
type TaskNotifier interface {
	NotifyDownloadComplete(ctx context.Context, taskName string, totalFiles, downloaded int, durationMs int64) error
	NotifyError(ctx context.Context, taskName, errMsg string) error
}

// ExecutorDeps 执行器依赖
type ExecutorDeps struct {
	Client115        *client115.Client
	AccountStore     AccountReader
	SettingsStore    SettingsStore
	SQLiteDB         *sql.DB
	TaskHistory      *db.TaskHistoryRepo // 任务执行历史（可为 nil）
	TasksStore       TasksReaderWriter
	StrmCache        StrmCacheWriter
	EmbyRefresh      StrmRefresher         // Emby 刷库服务（可为 nil）
	CleanupSubmitter CleanupBatchSubmitter // STRM 清理延迟批次提交器（可为 nil）
	Notifier         TaskNotifier          // 任务完成/失败通知（可为 nil）
	BaseURL          string                // 用于拼接 strmPrefix（302模式下可留空）
	PublicBaseURL    string                // 公开可访问的 baseUrl（302 模式用户可配置）
}

// ExecuteTask 执行一个任务（同步执行，调用方应开 goroutine 异步调用）
// 仅执行前的抢占检查同步返回 ExecuteResult，任务本身通过 SSE 广播进度
func ExecuteTask(ctx context.Context, taskID string, deps ExecutorDeps) ExecuteResult { //nolint:cyclop // complexity: 112
	rt := GetRuntime()

	// 1) 找到任务定义
	tasks, err := deps.TasksStore.ReadTasks()
	if err != nil {
		return ExecuteResult{Success: false, Reason: "error", Message: err.Error()}
	}
	var task *Task
	for i := range tasks {
		if tasks[i].ID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return ExecuteResult{Success: false, Reason: "not_found", Message: "Task not found"}
	}

	// 2) 账号级独占锁
	ok, conflictID := rt.TryEnter(task.Account, task.ID)
	if !ok {
		return ExecuteResult{
			Success: false, Blocked: true, Reason: "account_running",
			Message: "Account already has an active task: " + conflictID,
		}
	}
	defer rt.Exit(task.Account)

	// 3) 注册为运行态（拿到 cancel 用于支持取消）
	runCtx, cancel := rt.Register(task)
	defer cancel()
	go func() {
		<-runCtx.Done()
	}()

	sseServer := sse.GetServer()
	taskStart := time.Now()

	// 设置启动阶段
	rt.SetState(task.ID, func(s *RuntimeState) {
		s.Stage = StageStarting
		s.StageDetail = "任务初始化中..."
	})
	sseServer.EmitProgress(sse.ProgressPayload{
		TaskID:         task.ID,
		OverallPercent: "0.00",
		Stage:          StageStarting,
		StageDetail:    "任务初始化中...",
	})

	// P0-4 STRM 执行历史埋点状态（defer 统一记录，避免每个 return 点重复代码）
	// downloaded 提前声明，供 defer 闭包引用（后续 worker pool 累加）
	histKind := db.StrmHistoryKindFull
	histSuccess := true
	histErrMsg := ""
	totalFiles := 0
	downloaded := new(int64)

	// TaskHistory 执行记录（与 strm_exec_history 并行，前端「任务历史」页面用）
	var execID int64
	if deps.TaskHistory != nil {
		var err error
		execID, err = deps.TaskHistory.CreateExecution(ctx, db.TaskExecution{
			TaskID: task.ID, Account: task.Account,
			OriginPath: task.OriginPath, TargetPath: task.TargetPath,
			Status: "running", StartedAt: taskStart.UnixMilli(),
		})
		if err != nil {
			logger.S().Warnf("[Task] CreateExecution failed: %v", err)
		}
	}

	defer func() {
		elapsedMs := time.Since(taskStart).Milliseconds()
		dl := int64(0)
		if downloaded != nil {
			dl = atomic.LoadInt64(downloaded)
		}
		failed := 0
		if totalFiles >= int(dl) {
			failed = totalFiles - int(dl)
		}
		recordStrmHistory(deps, task.ID, task.Account, histKind, histSuccess,
			totalFiles, int(dl), failed, elapsedMs, histErrMsg)

		// 完成 TaskHistory 执行记录
		if deps.TaskHistory != nil && execID > 0 {
			status := "completed"
			errMsg := ""
			if !histSuccess {
				status = "failed"
				errMsg = histErrMsg
			}
			_ = deps.TaskHistory.CompleteExecution(ctx, execID, status,
				db.TaskExecutionSummary{
					TotalFiles:      totalFiles,
					DownloadedFiles: int(dl),
					DeletedFiles:    0, // cleanup 阶段暂未统计
				}, errMsg, elapsedMs)
		}
	}()

	// 4) 找 115 账号 cookie
	account := findAccount(deps.AccountStore, task.Account)
	if account == nil || account.AccountType != "115" {
		msg := "No valid 115 account: " + task.Account
		histSuccess = false
		histErrMsg = msg
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{
			TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds(),
		})
		if deps.Notifier != nil {
			_ = deps.Notifier.NotifyError(context.Background(), task.Name, msg)
		}
		return ExecuteResult{Success: false, Reason: "bad_account", Message: msg}
	}

	// 5) 合并全局 settings + 任务覆盖得到最终 strm 配置
	settings, err := deps.SettingsStore.ReadSettings()
	if err != nil { //nolint:staticcheck // SA9003: 空分支为有意设计
		// best-effort: fall back to defaults if read fails
	}
	if settings == nil {
		settings = model.DefaultSettings()
	}
	resolved := resolveStrmSettings(task, settings, deps.BaseURL, deps.PublicBaseURL)

	// 6) 取 cid + exportDirParse → 拿到根 pickcode
	sseServer.EmitLog(task.ID, "info",
		fmt.Sprintf("Task starting: account=%s origin=%s target=%s", task.Account, task.OriginPath, task.TargetPath))

	cid, err := deps.Client115.FsDirGetID(ctx, task.OriginPath, account.Cookie)
	if err != nil {
		msg := "FsDirGetID failed: " + err.Error()
		histSuccess = false
		histErrMsg = msg
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		if deps.Notifier != nil {
			_ = deps.Notifier.NotifyError(context.Background(), task.Name, msg)
		}
		return ExecuteResult{Success: true, TaskID: task.ID, Message: "started but failed at dir scan"}
	}
	sseServer.EmitLog(task.ID, "info", fmt.Sprintf("Got cid=%d for path %s", cid, task.OriginPath))

	// 7) 导出目录解析（仅用于日志，实际遍历目录由 FsFiles 递归完成，不依赖 rootPick）
	//    如果此步骤失败（例如 115 files/zip 接口返回"服务器开小差"），只记录警告不中断任务。
	exportCtx, exportCancel := context.WithTimeout(ctx, 30*time.Second)
	defer exportCancel()
	rootPick := ""
	if rp, err := deps.Client115.ExportDirParse(exportCtx, itoa(cid), account.Cookie, 0); err != nil {
		warn := "ExportDirParse skipped: " + err.Error()
		sseServer.EmitLog(task.ID, "warn", warn)
	} else {
		rootPick = rp
	}
	if rootPick != "" {
		sseServer.EmitLog(task.ID, "info", fmt.Sprintf("Export root pickcode=%s", rootPick))
	} else {
		sseServer.EmitLog(task.ID, "info", "Export root pickcode unavailable, proceeding with FsFiles direct traversal")
	}

	// 8) 构建目录树（从导出结果列目录得到文件列表）
	//    简化版：我们用 fs_files 多次分页遍历目标目录（cid 已知），获取文件条目
	// 对齐 MoviePilot StrmGenerater.should_generate_strm：
	//   - minFileSize: 从全局 settings.Download.MinFileSize（full_sync_min_file_size）读取
	//   - blacklist:  从全局 settings.Download.StrmGenerateBlacklist 读取
	//   listAllFilesRecursive 内部同时会严格校验 pickcode 有效性（对齐 MoviePilot）。
	minFileSize := settings.Download.MinFileSize
	blacklist := settings.Download.StrmGenerateBlacklist
	// 设置扫描阶段（心跳在 listAllFilesRecursive 内部每3s广播）
	rt.SetState(task.ID, func(s *RuntimeState) {
		s.Stage = StageScanning
		s.StageDetail = "开始扫描云端目录..."
	})
	sseServer.EmitProgress(sse.ProgressPayload{
		TaskID:         task.ID,
		OverallPercent: "0.00",
		Stage:          StageScanning,
		StageDetail:    "开始扫描云端目录...",
	})
	fileEntries, err := listAllFilesRecursive(ctx, deps.Client115, account.Cookie, cid, task.OriginPath, resolved.StrmExtensions, resolved.DownloadExtensions, minFileSize, blacklist, task.ID, rt, sseServer)
	if err != nil {
		msg := "list files failed: " + err.Error()
		histSuccess = false
		histErrMsg = msg
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Status = StatusFailed
			s.Error = msg
			s.EndedAt = time.Now().UnixMilli()
			s.Stage = StageFailed
			s.StageDetail = msg
		})
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		if deps.Notifier != nil {
			_ = deps.Notifier.NotifyError(context.Background(), task.Name, msg)
		}
		return ExecuteResult{Success: true, TaskID: task.ID}
	}

	totalFiles = len(fileEntries)
	rt.SetState(task.ID, func(s *RuntimeState) { s.TotalFiles = totalFiles })
	sseServer.EmitLog(task.ID, "info", fmt.Sprintf("Total files: %d (strm=%d, download=%d)",
		totalFiles, countKind(fileEntries, kindStrm), countKind(fileEntries, kindDownload)))

	// ---- P2-3 增量同步：基于 files 表 snapshot 跳过未变更条目 ----
	// 执行顺序：必须在写回 filePathDb 之前取 snapshot，否则 upsert 覆盖后无差异可识别。
	incremental := settings.Download.IncrementalSync && deps.SQLiteDB != nil && task.Account != ""
	if incremental {
		histKind = db.StrmHistoryKindIncrement
	}
	if incremental {
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Stage = StageIncremental
			s.StageDetail = "正在进行增量比对..."
		})
		sseServer.EmitProgress(sse.ProgressPayload{
			TaskID:         task.ID,
			OverallPercent: "0.00",
			Stage:          StageIncremental,
			StageDetail:    "正在进行增量比对...",
		})
		snap, serr := db.ListSnapshotByAccount(deps.SQLiteDB, task.Account, task.OriginPath)
		if serr != nil {
			sseServer.EmitLog(task.ID, "warn", "增量快照读取失败，退化为全量: "+serr.Error())
			incremental = false
		} else if len(snap) > 0 {
			skipped := 0
			for _, f := range fileEntries {
				// kindSkip 已跳过的不重复处理（黑名单/过小的）
				if f.Kind == kindSkip {
					continue
				}
				if se, ok := snap[f.CloudPath]; ok &&
					se.PickCode == f.PickCode &&
					se.FileName == f.Name {
					f.Kind = kindSkip
					skipped++
				}
			}
			if skipped > 0 {
				sseServer.EmitLog(task.ID, "info", fmt.Sprintf(
					"增量模式：已跳过 %d 个未变化文件 (CloudPath+PickCode+FileName 与上次同步一致)", skipped))
			}
			// 刷新 totalFiles 统计，保持与进度一致
			totalFiles = 0
			for _, f := range fileEntries {
				if f.Kind != kindSkip {
					totalFiles++
				}
			}
			rt.SetState(task.ID, func(s *RuntimeState) { s.TotalFiles = totalFiles })
		}
	}

	// ---- P0-1 增量对账清理：扫描本地 STRM，孤儿（云端已删但本地仍存）按 mode 处理 ----
	// 注意：cloudPickcodes 来自本次 fileEntries（云端最新列表），而非 DB（DB 可能滞后），
	// 这样对账更准确。kindSkip 条目的 pickcode 仍会进入集合（buildCloudPickcodeSet 不分 Kind），
	// 避免增量跳过的条目被误判为孤儿。
	if incremental {
		mode := strings.ToLower(strings.TrimSpace(settings.Download.IncrementalCleanupMode))
		if mode != "" && mode != "off" {
			rt.SetState(task.ID, func(s *RuntimeState) {
				s.Stage = StageCleanup
				s.StageDetail = "增量对账：扫描本地孤儿 STRM..."
			})
			sseServer.EmitProgress(sse.ProgressPayload{
				TaskID:         task.ID,
				OverallPercent: "0.00",
				Stage:          StageCleanup,
				StageDetail:    "增量对账：扫描本地孤儿 STRM...",
			})
			sseServer.EmitLog(task.ID, "info", "增量对账清理：开始扫描本地孤儿 STRM")
			cloudPickcodes := buildCloudPickcodeSet(fileEntries)
			orphanN, reqID, cerr := cleanupOrphanStrms(ctx, deps.CleanupSubmitter, sseServer,
				task.ID, task.TargetPath, cloudPickcodes, mode, settings.Cleanup, deps.EmbyRefresh)
			if cerr != nil {
				sseServer.EmitLog(task.ID, "warn", "增量对账清理失败: "+cerr.Error())
				logger.S().Warnf("[Task] 增量对账清理失败 task=%s: %v", task.ID, cerr)
			} else if orphanN > 0 && reqID != "" {
				sseServer.EmitLog(task.ID, "warn",
					fmt.Sprintf("增量对账：检测到孤儿 STRM 超阈值，已入队等待二次确认 (requestID=%s, count=%d)",
						reqID, orphanN))
			} else if orphanN > 0 {
				sseServer.EmitLog(task.ID, "info",
					fmt.Sprintf("增量对账：已处理 %d 个孤儿 STRM (mode=%s)", orphanN, mode))
			}
		}
	}

	// ---- P0-2 全量预扫清理：全量任务时清理孤儿 STRM ----
	// 与 P0-1 互斥（incremental=true 跳过），cloudPickcodes = DB 快照 + 本次 fileEntries 合并，
	// 避免误删历史 STRM 或本次新增的 STRM。对齐参考项目 full_sync_remove_unless_strm。
	// 与 removeExtraFiles（基于路径）互补：本扫描基于 STRM 内容的 pickcode，更精确。
	if !incremental {
		mode := strings.ToLower(strings.TrimSpace(settings.Download.FullSyncCleanupOrphans))
		if mode != "" && mode != "off" {
			rt.SetState(task.ID, func(s *RuntimeState) {
				s.Stage = StageCleanup
				s.StageDetail = "全量预扫：扫描本地孤儿 STRM..."
			})
			sseServer.EmitProgress(sse.ProgressPayload{
				TaskID:         task.ID,
				OverallPercent: "0.00",
				Stage:          StageCleanup,
				StageDetail:    "全量预扫：扫描本地孤儿 STRM...",
			})
			sseServer.EmitLog(task.ID, "info", "全量预扫清理：开始扫描本地孤儿 STRM")
			var snapPickcodes map[string]struct{}
			if deps.SQLiteDB != nil && task.Account != "" {
				snap, serr := db.ListSnapshotByAccount(deps.SQLiteDB, task.Account, task.OriginPath)
				if serr != nil {
					sseServer.EmitLog(task.ID, "warn", "全量预扫：DB 快照读取失败: "+serr.Error())
				} else if len(snap) > 0 {
					snapPickcodes = buildCloudPickcodeSetFromSnapshot(snap)
				}
			}
			// 合并：DB 历史 pickcode + 本次云端最新 pickcode，避免误删
			cloudPickcodes := mergePickcodeSets(snapPickcodes, buildCloudPickcodeSet(fileEntries))
			orphanN, reqID, cerr := cleanupOrphanStrms(ctx, deps.CleanupSubmitter, sseServer,
				task.ID, task.TargetPath, cloudPickcodes, mode, settings.Cleanup, deps.EmbyRefresh)
			if cerr != nil {
				sseServer.EmitLog(task.ID, "warn", "全量预扫清理失败: "+cerr.Error())
				logger.S().Warnf("[Task] 全量预扫清理失败 task=%s: %v", task.ID, cerr)
			} else if orphanN > 0 && reqID != "" {
				sseServer.EmitLog(task.ID, "warn",
					fmt.Sprintf("全量预扫：检测到孤儿 STRM 超阈值，已入队等待二次确认 (requestID=%s, count=%d)",
						reqID, orphanN))
			} else if orphanN > 0 {
				sseServer.EmitLog(task.ID, "info",
					fmt.Sprintf("全量预扫：已处理 %d 个孤儿 STRM (mode=%s)", orphanN, mode))
			}
		}
	}

	// 9) 写回 filePathDb（不仅 Enable302 反查需要，生活事件 move/rename 时也依赖 DB 反查旧路径）
	if deps.SQLiteDB != nil {
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Stage = StageWritingDB
			s.StageDetail = "正在写入文件索引数据库..."
		})
		sseServer.EmitProgress(sse.ProgressPayload{
			TaskID:         task.ID,
			OverallPercent: "0.00",
			Stage:          StageWritingDB,
			StageDetail:    "正在写入文件索引数据库...",
		})
		entries := make([]db.FilePathEntry, 0, len(fileEntries))
		for _, f := range fileEntries {
			if !isValidPickcode(f.PickCode) {
				continue
			}
			entries = append(entries, db.FilePathEntry{
				Path:       f.CloudPath,
				FileName:   f.Name,
				PickCode:   f.PickCode,
				UpdateTime: time.Now().Unix(),
			})
		}
		if len(entries) > 0 {
			if werr := db.UpsertFilePathEntryBatch(deps.SQLiteDB, task.Account, entries); werr != nil {
				sseServer.EmitLog(task.ID, "warn", "write filePathDb failed: "+werr.Error())
			} else {
				sseServer.EmitLog(task.ID, "info", fmt.Sprintf("wrote %d entries to filePathDb", len(entries)))
			}
		}
	}

	// 10) 下载/写 STRM：按文件类型拆分，并发执行
	if err := os.MkdirAll(task.TargetPath, 0o755); err != nil {
		msg := "mkdir target failed: " + err.Error()
		histSuccess = false
		histErrMsg = msg
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Status = StatusFailed
			s.Error = msg
			s.EndedAt = time.Now().UnixMilli()
			s.Stage = StageFailed
			s.StageDetail = msg
		})
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		if deps.Notifier != nil {
			_ = deps.Notifier.NotifyError(context.Background(), task.Name, msg)
		}
		return ExecuteResult{Success: true, TaskID: task.ID}
	}

	perFilePct := newPerFilePercent(totalFiles)

	// ---- 写 STRM 文件（并发数可通过 settings 配置） ----
	strmWorkers := defaultStrmWorkers
	if settings.Download.StrmMaxConcurrent != nil && *settings.Download.StrmMaxConcurrent > 0 {
		strmWorkers = *settings.Download.StrmMaxConcurrent
		if strmWorkers <= 0 {
			strmWorkers = defaultStrmWorkers
		}
	}
	// 对齐 MoviePilot overwrite_mode："never" 时已存在 STRM 则跳过；"always"(默认) 始终覆盖
	overwriteNever := strings.EqualFold(settings.Download.OverwriteMode, "never")
	// P1-4 高级模板
	strmUrlTemplate := settings.Strm.StrmUrlTemplate
	strmFilenameTemplate := settings.Strm.StrmFilenameTemplate
	strmFiles := filterKind(fileEntries, kindStrm)
	cacheEntryUUID := uuid.New().String()
	cacheRelPaths := make([]string, 0, len(strmFiles))
	cacheLocalPaths := make([]string, 0, len(strmFiles))
	var cacheMu sync.Mutex
	if len(strmFiles) > 0 {
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Stage = StageGenerating
			s.StageDetail = fmt.Sprintf("正在生成 %d 个 STRM 文件...", len(strmFiles))
		})
		sseServer.EmitProgress(sse.ProgressPayload{
			TaskID:         task.ID,
			OverallPercent: "0.00",
			Stage:          StageGenerating,
			StageDetail:    fmt.Sprintf("正在生成 %d 个 STRM 文件...", len(strmFiles)),
		})
		sseServer.EmitLog(task.ID, "info", fmt.Sprintf("writing %d strm files (concurrency=%d, overwrite=%s)",
			len(strmFiles), strmWorkers, settings.Download.OverwriteMode))
		// P2-1：统一用 WorkerPool，不再每处手写 sem+wg 模板
		pool := concurrency.NewPool(strmWorkers)
		skipped := new(int64)
		for _, f := range strmFiles {
			f := f
			pool.Submit(func() error {
				// P1-4 文件名模板优先，否则回退默认（.iso 保留双扩展名）
				var strmRelPath string
				if strmFilenameTemplate != "" {
					relDir, relName := filepath.Split(f.RelPath)
					ext := strings.ToLower(filepath.Ext(f.Name))
					stem := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
					if strings.EqualFold(ext, ".iso") {
						stem = stem + ".iso"
					}
					newName := model.RenderStrmFilenameTemplate(strmFilenameTemplate, f.Name, ext, stem, task.Account)
					if newName == "" {
						newName = getStrmFileName(relName)
					}
					strmRelPath = filepath.Join(relDir, newName)
				} else {
					// 默认：正确处理 .iso 双扩展名：f.RelPath = "sub/game.iso" → "sub/game.iso.strm"
					strmRelPath = replaceRelPathExtToStrm(f.RelPath)
				}
				savePath := filepath.Join(task.TargetPath, strmRelPath)
				// 对齐 MoviePilot：overwrite_mode=="never" 且文件已存在 → 跳过
				if overwriteNever {
					if _, statErr := os.Stat(savePath); statErr == nil {
						atomic.AddInt64(skipped, 1)
						return nil
					}
				}
				content, cerr := buildStrmContent(task, f, resolved, strmUrlTemplate)
				if cerr != nil {
					sseServer.EmitLog(task.ID, "error", fmt.Sprintf("build strm %s: %v", f.RelPath, cerr))
					return nil
				}
				if cerr = ensureDir(filepath.Dir(savePath)); cerr != nil {
					sseServer.EmitLog(task.ID, "error", fmt.Sprintf("mkdir %s: %v", filepath.Dir(savePath), cerr))
					return nil
				}
				// 原子写入：先写 tmp 再 rename，避免并发读到半截文件
				if cerr = writeStrmFile(savePath, content); cerr != nil {
					sseServer.EmitLog(task.ID, "error", fmt.Sprintf("write %s: %v", savePath, cerr))
					return nil
				}
				// Emby 刷库（带防抖，对齐 MoviePilot mediaserver_helper.refresh_mediaserver）
				if deps.EmbyRefresh != nil {
					if rerr := deps.EmbyRefresh.RefreshOnCreate(ctx, savePath); rerr != nil {
						logger.S().Debugf("[Task] Emby 刷库安排失败 path=%s: %v", savePath, rerr)
					}
				}
				cacheMu.Lock()
				cacheRelPaths = append(cacheRelPaths, strmRelPath)
				cacheLocalPaths = append(cacheLocalPaths, savePath)
				cacheMu.Unlock()
				perFilePct.Mark(f.CloudPath, 100)
				atomic.AddInt64(downloaded, 1)
				done, overall := perFilePct.Overall(totalFiles)
				rt.AppendLog(task.ID, jsonLine(sse.ProgressPayload{
					TaskID: task.ID, FilePath: f.RelPath, Percent: 100, OverallPercent: overall,
				}))
				sseServer.EmitProgress(sse.ProgressPayload{
					TaskID: task.ID, FilePath: f.RelPath, Percent: 100, OverallPercent: overall, Done: done,
				})
				return nil
			})
		}
		pool.Wait()
		if overwriteNever && skipped != nil && *skipped > 0 {
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("overwrite=never：已跳过 %d 个已存在 STRM 文件", *skipped))
		}
	}

	// ---- 真实下载文件（设置了 downloadExtensions 且启用自动下载才做） ----
	if !settings.Download.AutoDownloadMetadata {
		sseServer.EmitLog(task.ID, "info", "自动下载元数据已关闭，跳过 nfo/jpg/png 等文件下载")
	} else {
		dlFiles := filterKind(fileEntries, kindDownload)
		if len(dlFiles) > 0 {
			dlWorkers := 10
			if settings.Download.DownloadMaxConcurrent != nil && *settings.Download.DownloadMaxConcurrent > 0 {
				dlWorkers = *settings.Download.DownloadMaxConcurrent
				if dlWorkers <= 0 {
					dlWorkers = 10
				}
			}
			rt.SetState(task.ID, func(s *RuntimeState) {
				s.Stage = StageGenerating
				s.StageDetail = fmt.Sprintf("正在下载 %d 个元数据文件...", len(dlFiles))
			})
			sseServer.EmitProgress(sse.ProgressPayload{
				TaskID:         task.ID,
				OverallPercent: "0.00",
				Stage:          StageGenerating,
				StageDetail:    fmt.Sprintf("正在下载 %d 个元数据文件...", len(dlFiles)),
			})
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("downloading %d files (concurrency=%d)", len(dlFiles), dlWorkers))
			runDownloads(runCtx, task.ID, dlFiles, task.TargetPath, task.Account, account.Cookie, deps.Client115,
				dlWorkers, perFilePct, downloaded, totalFiles, rt, sseServer)
		}
	}

	// ---- 11) removeExtraFiles：清理本地多余文件 ----
	if task.RemoveExtraFiles {
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Stage = StageCleanup
			s.StageDetail = "正在清理本地多余文件..."
		})
		sseServer.EmitProgress(sse.ProgressPayload{
			TaskID:         task.ID,
			OverallPercent: "95.00",
			Stage:          StageCleanup,
			StageDetail:    "正在清理本地多余文件...",
		})
		sseServer.EmitLog(task.ID, "info", "scanning for extra files to remove")
		deleted, reqID, derr := removeExtraFiles(ctx, deps.CleanupSubmitter, task.ID, task.TargetPath, fileEntries, settings)
		if derr != nil {
			sseServer.EmitLog(task.ID, "warn", "removeExtraFiles err: "+derr.Error())
		} else if reqID != "" {
			// 延迟清理批次已入队，等待用户二次确认（Web UI / Telegram）
			sseServer.EmitLog(task.ID, "warn",
				fmt.Sprintf("检测到孤儿文件超过稳定阈值，已延迟删除并等待二次确认 (requestID=%s)", reqID))
			sseServer.EmitLog(task.ID, "info",
				"请到 Web UI 清理页 /api/strmCleanup/pending 或 Telegram 按钮确认")
			rt.SetState(task.ID, func(s *RuntimeState) { s.DeletedFiles = 0 })
		} else {
			rt.SetState(task.ID, func(s *RuntimeState) { s.DeletedFiles = deleted })
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("removed %d extra files", deleted))
			// Emby 刷库（删除后刷新整个目标目录，对齐 MoviePilot full_sync_media_server_refresh）
			if deps.EmbyRefresh != nil && deleted > 0 {
				if rerr := deps.EmbyRefresh.RefreshOnDelete(ctx, task.TargetPath); rerr != nil {
					logger.S().Debugf("[Task] Emby 刷库安排失败 path=%s: %v", task.TargetPath, rerr)
				}
			}
		}
	}

	// ---- 11.8) P1-3 DB 幽灵记录清理 ----
	// removeExtraFiles 后（或用户手动删除本地 STRM）files 表可能残留已失效 file_id → path 映射，
	// 导致 302 模式 pickcode 反查返回错误指向。此处基于 task.TargetPath 做范围扫描：
	//   遍历 DB 中该 account 条目 → 按 LifeMonitor.PathMappings 换算 localPath →
	//   若 localPath 落在 TargetPath 范围内且本地文件不存在 → 判定幽灵并清理 DB 记录。
	if deps.SQLiteDB != nil && task.Account != "" {
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Stage = StageFinalizing
			s.StageDetail = "正在清理数据库幽灵记录..."
		})
		sseServer.EmitProgress(sse.ProgressPayload{
			TaskID:         task.ID,
			OverallPercent: "97.00",
			Stage:          StageFinalizing,
			StageDetail:    "正在清理数据库幽灵记录...",
		})
		targetAbs, aerr := filepath.Abs(task.TargetPath)
		if aerr != nil {
			targetAbs = task.TargetPath
		}
		mappings := settings.LifeMonitor.PathMappings
		resolve := func(entry db.FilePathEntry) (strmPath string, shouldCheck bool) {
			// 匹配路径映射：按 account 过滤，最长 cloudPath 前缀匹配
			cloudPath := entry.Path
			if cloudPath == "" {
				return "", false
			}
			var bestLocal string
			var bestPrefixLen int
			for _, mp := range mappings {
				if mp.Account != "" && mp.Account != task.Account {
					continue
				}
				key := strings.TrimRight(mp.CloudPath, "/")
				// 精确匹配根
				if cloudPath == mp.CloudPath || cloudPath == key {
					if len(key) > bestPrefixLen {
						bestPrefixLen = len(key)
						bestLocal = mp.LocalPath
					}
					continue
				}
				norm := key + "/"
				if strings.HasPrefix(cloudPath, norm) {
					if len(key) > bestPrefixLen {
						bestPrefixLen = len(key)
						rel := cloudPath[len(norm):]
						bestLocal = filepath.Join(mp.LocalPath, sanitizeCloudRelPath(rel))
					}
				}
			}
			if bestLocal == "" {
				return "", false
			}
			// bestLocal 是 cloudPath 对应的完整本地路径（含文件名）
			dir := filepath.Dir(bestLocal)
			base := filepath.Base(bestLocal)
			strmPath = filepath.Join(dir, getStrmFileName(base))
			abs, err := filepath.Abs(strmPath)
			if err != nil {
				abs = strmPath
			}
			// 仅检查落在 TargetPath 下的条目
			rel, err := filepath.Rel(targetAbs, abs)
			if err != nil {
				return abs, false
			}
			if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				return abs, false
			}
			return abs, true
		}
		if n, perr := db.PurgeOrphanEntries(deps.SQLiteDB, task.Account, 0, resolve); perr != nil {
			sseServer.EmitLog(task.ID, "warn", "DB 幽灵记录清理失败: "+perr.Error())
			logger.S().Warnf("[Task] PurgeOrphanEntries failed task=%s: %v", task.ID, perr)
		} else if n > 0 {
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("DB 幽灵记录已清理 %d 条", n))
			logger.S().Infof("[Task] PurgeOrphanEntries task=%s deleted=%d", task.ID, n)
		}
	}

	// 11.5) 保存 STRM 生成缓存（供清理使用）
	if deps.StrmCache != nil && len(cacheRelPaths) > 0 {
		rt.SetState(task.ID, func(s *RuntimeState) {
			s.Stage = StageFinalizing
			s.StageDetail = "正在保存 STRM 缓存..."
		})
		sseServer.EmitProgress(sse.ProgressPayload{
			TaskID:         task.ID,
			OverallPercent: "99.00",
			Stage:          StageFinalizing,
			StageDetail:    "正在保存 STRM 缓存...",
		})
		cacheMu.Lock()
		relSnapshot := append([]string(nil), cacheRelPaths...)
		localSnapshot := append([]string(nil), cacheLocalPaths...)
		cacheMu.Unlock()
		sort.Strings(relSnapshot)
		sort.Strings(localSnapshot)
		if err := deps.StrmCache.Save(StrmCacheEntryLike{
			UUID:       cacheEntryUUID,
			TaskID:     task.ID,
			Target:     task.TargetPath,
			Account:    task.Account,
			RelPaths:   relSnapshot,
			LocalPaths: localSnapshot,
		}); err != nil {
			sseServer.EmitLog(task.ID, "warn", "save strm cache failed: "+err.Error())
		} else {
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("saved strm cache uuid=%s entries=%d", cacheEntryUUID, len(relSnapshot)))
		}
	}

	// 12) 完成
	rt.SetState(task.ID, func(s *RuntimeState) {
		s.Status = StatusCompleted
		s.DownloadedFiles = int(atomic.LoadInt64(downloaded))
		s.EndedAt = time.Now().UnixMilli()
		s.Stage = StageCompleted
		dl := atomic.LoadInt64(downloaded)
		s.StageDetail = fmt.Sprintf("任务完成，共处理 %d 个文件", dl)
	})
	dl := atomic.LoadInt64(downloaded)
	durationMs := time.Since(taskStart).Milliseconds()
	_, overall := perFilePct.Overall(totalFiles)
	sseServer.EmitProgress(sse.ProgressPayload{
		TaskID: task.ID, Done: true, OverallPercent: overall,
		Stage: StageCompleted, StageDetail: fmt.Sprintf("任务完成，共处理 %d 个文件", dl),
	})
	sseServer.EmitComplete(sse.CompletePayload{
		TaskID: task.ID, Status: string(StatusCompleted),
		TotalFiles: totalFiles, DownloadedFiles: int(dl), DurationMs: durationMs,
	})
	sseServer.EmitLog(task.ID, "info",
		fmt.Sprintf("Task done in %d ms: %d/%d files", durationMs, dl, totalFiles))

	// 发送任务完成通知（仅在至少有一个文件成功处理时通知）
	if deps.Notifier != nil && (dl > 0 || totalFiles == 0) {
		_ = deps.Notifier.NotifyDownloadComplete(context.Background(), task.Name, totalFiles, int(dl), durationMs)
	}

	return ExecuteResult{Success: true, TaskID: task.ID}
}

// ==================== 内部：辅助 ====================

// recordStrmHistory P0-4 埋点 helper：记录 STRM 执行历史到 DB
// 失败不阻断主流程，仅日志。对齐参考项目 core/history/strm.py
func recordStrmHistory(deps ExecutorDeps, taskID, account string, kind db.StrmHistoryKind,
	success bool, total, successN, failedN int, elapsedMs int64, errMsg string) {
	if deps.SQLiteDB == nil {
		return
	}
	entry := db.StrmHistoryEntry{
		TaskID:       taskID,
		Kind:         kind,
		Account:      account,
		Success:      success,
		TotalFiles:   total,
		SuccessFiles: successN,
		FailedFiles:  failedN,
		ElapsedMs:    elapsedMs,
		ErrorMsg:     errMsg,
	}
	if _, err := db.InsertStrmHistory(deps.SQLiteDB, entry); err != nil {
		logger.S().Warnf("[Task] recordStrmHistory failed task=%s: %v", taskID, err)
	}
}

type fileKind int

const (
	kindStrm     fileKind = 1
	kindDownload fileKind = 2
	kindSkip     fileKind = 3
)

type fileItem struct {
	CloudPath string // 绝对云端路径：originPath + "/" + rel
	RelPath   string // 相对路径（不含 originPath 前缀）
	Name      string
	PickCode  string
	Size      int64
	Ext       string
	Kind      fileKind
}

func countKind(items []*fileItem, k fileKind) int {
	n := 0
	for _, it := range items {
		if it.Kind == k {
			n++
		}
	}
	return n
}
func filterKind(items []*fileItem, k fileKind) []*fileItem {
	out := make([]*fileItem, 0, len(items))
	for _, it := range items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

func findAccount(s AccountReader, name string) *model.AccountInfo {
	return s.Get(name)
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func jsonLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// perFilePercent 每文件进度追踪（简单版：STRM 完成即 100，下载会渐进更新）
type perFilePercent struct {
	mu sync.Mutex
	m  map[string]int
}

func newPerFilePercent(total int) *perFilePercent {
	return &perFilePercent{m: make(map[string]int, total)}
}

func (p *perFilePercent) Mark(key string, pct int) {
	p.mu.Lock()
	p.m[key] = pct
	p.mu.Unlock()
}

func (p *perFilePercent) Update(key string, pct int) {
	p.mu.Lock()
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	p.m[key] = pct
	p.mu.Unlock()
}

// Overall 返回 (是否全部100, overallPercent 字符串 "xx.xx")
func (p *perFilePercent) Overall(total int) (bool, string) {
	p.mu.Lock()
	sum := 0
	cnt := 0
	for _, v := range p.m {
		sum += v
		cnt++
	}
	p.mu.Unlock()
	if total <= 0 {
		return true, "100.00"
	}
	// 未标记的文件按 0 算
	sum += (total - cnt) * 0
	overall := float64(sum) / float64(total)
	done := overall >= taskCompleteThreshold
	return done, fmt.Sprintf("%.2f", overall)
}
