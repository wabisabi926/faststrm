// Package monitor 生活事件监控与 STRM 同步
// event_handler.go 事件处理 handler（对齐 frontend/src/lib/eventMonitorHandlers.ts）
package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/pkg/concurrency"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// ==================== 常量 ====================

const (
	// MaxRecursionDepth 文件夹递归扫描最大深度
	MaxRecursionDepth = 10
	// MaxFolderFiles 单个文件夹处理的最大文件数
	MaxFolderFiles = 1000
)

// ==================== pathMapping ====================

// pathMapping 匹配到的云端→本地路径映射
type pathMapping struct {
	cloudPath    string
	localPath    string
	relativePath string
}

// ==================== processEvent：事件分发器 ====================

// processEvent 事件分发器：根据事件类型调用对应 handler
// 对齐 TS processEvent + Phase 1：
//   ① 先调用 preProcessEvent 做决策/日志/Write-Ahead DB/type=17 拦截；
//   ② 再按 skipReason / ShouldAct 进入 legacy handler；
//   ③ 返回值通过 ctx 中携带的 *PollCounts（若有）累计 effective/skipped。
func (m *Monitor) processEvent(ctx context.Context, account string, event client115.LifeEventItem, lifeClient *client115.LifeClient) error {
	config := m.settingsFn()
	eventType := event.Type

	// 忽略无需操作的事件类型（标星/浏览/标签等）
	if client115.IgnoreBehaviorTypes[eventType] {
		logger.S().Debugf("[Monitor] 事件类型 %d (%s) 无需处理，跳过", eventType, event.BehaviorType)
		// 仍然视为 skipped 类（不是有效副作用），但不占 error
		pollCountsAddSkipped(ctx, "ignore_behavior_type_"+event.BehaviorType)
		return nil
	}

	// 解析云端路径（四级回退，对齐参考项目策略）
	//  0) event.FilePath（极少由 API 直接填充）
	//  1) DB getByID(file_id) → path（参考项目首选，避免对每个事件打祖先链 API）
	//     - 但 DB path 如果是 SINGLE_SEG（不含"/"）且非根目录级映射命中，视为历史脏数据（旧错误写入），丢弃
	//  2) lifeClient.ResolvePath：parentID 合法→ResolveDirPath(parentID)；否则用 file_id 自身查祖先链
	//     - ResolvePath 内部仍有 祖先链→根目录列目录→二级目录列目录→ResolveDirPath 四级降级
	const (
		srcEventRaw  = "EVENT_RAW"
		srcDB        = "DB"
		srcDBRejected = "DB_SINGLE_REJECTED" // DB 有记录但仅单段+未命中根映射，强制丢弃重查
		srcAPIPID    = "API_PARENT_ID"
		srcAPIFID    = "API_FILE_ID"
		srcAPIRootLs = "API_ROOT_FSLIST"
		srcFallback  = "BARE_FILENAME"
	)
	_ = srcAPIPID
	_ = srcAPIFID
	_ = srcAPIRootLs
	_ = srcFallback

	rawCloudPath := event.FilePath
	source := srcEventRaw

	if strings.TrimSpace(rawCloudPath) == "" && m.sqliteDB != nil && strings.TrimSpace(event.FileID) != "" {
		// P0-2: 同时查 files 和 folders 表（对齐参考项目 get_by_id 统一查询）
		if entry, err := db.GetFileOrFolderEntry(m.sqliteDB, account, event.FileID); err == nil && entry != nil && entry.Path != "" {
			// 判断 DB 路径可信度：
			//   - MULTI_SEG（含"/"）→ 信任
			//   - SINGLE_SEG 但 mapping 中存在 "该路径本身即为 CloudPrefix"（如 "电影"）→ 信任（new_folder type=17 常见）
			//   - 否则 → 历史脏数据（旧版 parent_id=0 直写），丢弃后强制走 API
			np := normalizeCloudPath(entry.Path)
			trustDB := strings.Contains(np, "/")
			if !trustDB {
				// 检查该单段是否就是映射前缀（即确为根目录下一级文件夹）
				for _, mm := range config.PathMappings {
					mp := normalizeCloudPath(mm.CloudPath)
					if mm.Account != "" && !strings.EqualFold(strings.TrimSpace(mm.Account), strings.TrimSpace(account)) {
						continue
					}
					if mp == np && mp != "" {
						trustDB = true
						break
					}
				}
			}
			if trustDB {
				rawCloudPath = entry.Path
				source = srcDB
				logger.S().Debugf("[Monitor] 路径DB反查: fileID=%s name=%s → %s (src=DB_TRUSTED)",
					event.FileID, event.FileName, rawCloudPath)
			} else {
				source = srcDBRejected
				logger.S().Infof("[Monitor] 路径DB反查拒绝(单段脏数据): fileID=%s name=%s dbPath=%s → 将强制走API解析",
					event.FileID, event.FileName, entry.Path)
			}
		}
	}
	if strings.TrimSpace(rawCloudPath) == "" && lifeClient != nil {
		// 走 API：ResolvePath(parentID, fileID, fileName)，内部对 parent_id=0 会用 file_id 祖先链降级
		rawCloudPath = lifeClient.ResolvePath(ctx, event.ParentID, event.FileID, event.FileName)
		// 根据结果进一步推断来源（为了 EVENT_DECIDE 可读）
		if rawCloudPath != "" {
			np := normalizeCloudPath(rawCloudPath)
			if strings.Contains(np, "/") {
				if strings.TrimSpace(event.ParentID) != "" && event.ParentID != "0" {
					source = srcAPIPID
				} else {
					source = srcAPIFID // parent=0 但通过 file_id 祖先链解出多段路径
				}
			} else {
				source = srcFallback // 最终仅得到裸文件名
			}
		} else {
			source = srcFallback
		}
		logger.S().Debugf("[Monitor] 路径API解析: parentID=%s fileID=%s name=%s → %s (src=%s)",
			event.ParentID, event.FileID, event.FileName, rawCloudPath, source)
	}

	// —— move/rename 事件：四象限决策矩阵（对齐参考项目 MoviePilot MonitorLife.move()）
	//  在 generic preProcessEvent 之前拦截，按 old_is_media × new_is_media 路由
	isMoveOrRename := (client115.MoveEventTypes[eventType] && config.EventTypes.Move) ||
		(client115.RenameEventTypes[eventType] && config.EventTypes.Rename)
	if isMoveOrRename {
		qHandled, qErr := m.handleMoveRenameQuadrant(ctx, account, event, rawCloudPath, source, config, lifeClient)
		if qHandled {
			return qErr
		}
	}

	// —— Phase 1.2：preProcessEvent（EVENT_DECIDE 日志 + normalize & unknown 清理 + Write-Ahead DB + type=17 单独分支）
	//  把来源信息 source 注入进去（通过包级局部变量不便，这里直接改调用：使用新的带 source 参数的入口包装）
	decision, handled := m.preProcessEventWithSource(ctx, account, event, rawCloudPath, source, config)
	if handled {
		// preProcessEvent 已经处理：
		//   • type=17 命中 MEDIA → effective=1（Write-Ahead + appendLog）
		//   • type=17 未命中 MEDIA 或其他 pre 自消化 → skipped=1
		if decision.EventKind == "new_folder" && decision.MappingType == MappingTypeMedia && decision.CloudPath != "" && decision.SkipReason == "" {
			pollCountsAddEffective(ctx)
			m.markDedupProcessed(event)
		} else {
			reason := decision.SkipReason
			if reason == "" {
				reason = "pre_digested_no_action"
			}
			pollCountsAddSkipped(ctx, reason)
			m.markDedupProcessed(event)
		}
		return nil
	}

	// 使用 normalize 后的 cloudPath
	cloudPath := decision.CloudPath
	if cloudPath == "" {
		// EVENT_DECIDE 已记录 cloud_path_unresolved；此处把错误再返回 pollOnce，pollOnce 会累计 Errors 并推进 LastErr UI 可见
		pollCountsAddSkipped(ctx, "cloud_path_unresolved")
		m.markDedupProcessed(event)
		return fmt.Errorf("event file_path 为空且路径解析失败，无法处理")
	}

	// —— 如果 skipReason 非空，就不再继续 handler（跳过）
	if decision.SkipReason != "" {
		// 特殊：Move/Rename 事件即使 MappingType=None（新路径不在映射）也要尝试 handleMoveOutEvent 兜底清理
		//  保持 legacy 逻辑
		if (client115.MoveEventTypes[eventType] && config.EventTypes.Move) ||
			(client115.RenameEventTypes[eventType] && config.EventTypes.Rename) {
			start := time.Now()
			err := m.handleMoveOutEvent(ctx, account, event, cloudPath)
			kind := db.StrmHistoryKindMove
			if client115.RenameEventTypes[eventType] {
				kind = db.StrmHistoryKindRename
			}
			m.recordMonitorHistory(account, kind, err, time.Since(start))
			if err == nil {
				pollCountsAddEffective(ctx)
				m.markDedupProcessed(event)
			}
			return err
		}
		pollCountsAddSkipped(ctx, decision.SkipReason)
		m.markDedupProcessed(event)
		return nil
	}

	// —— skipReason 空但 MappingType 不是 MEDIA：Phase2+ 才有转移/未识别专用流，此处按 skipped 处理
	if decision.MappingType != MappingTypeMedia {
		pollCountsAddSkipped(ctx, "mapping_type_"+string(decision.MappingType)+"_phase2_not_handled")
		m.markDedupProcessed(event)
		return nil
	}

	// 匹配路径映射（legacy）—— 这里因为 decision 已经命中 MEDIA 所以肯定非 nil
	mapping := matchPathMapping(cloudPath, config.PathMappings, account)
	if mapping == nil {
		// double-check（理论上不会发生）
		pollCountsAddSkipped(ctx, "legacy_matchPathMapping_returned_nil_unexpectedly")
		return nil
	}

	// P0-5 整理队列无进展超时：为每个事件处理创建带超时的 child context
	// 对齐参考项目 helper/life/transfer_wait.py stall_timeout_minutes
	stallTimeout := time.Duration(config.TransferStallTimeoutMinutes) * time.Minute
	if stallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, stallTimeout)
		defer cancel()
	}

	// 根据事件类型分发
	var handlerErr error
	var histKind db.StrmHistoryKind
	start := time.Now()

	switch {
	case client115.CreateEventTypes[eventType]:
		if !config.EventTypes.Create {
			return nil
		}
		histKind = db.StrmHistoryKindMonitor
		handlerErr = m.handleCreateEvent(ctx, account, event, mapping, cloudPath, lifeClient, true)

	case client115.DeleteEventTypes[eventType]:
		if !config.EventTypes.Remove {
			return nil
		}
		histKind = db.StrmHistoryKindDelete
		handlerErr = m.handleDeleteEvent(ctx, account, event, mapping, cloudPath, lifeClient)

	case client115.MoveEventTypes[eventType]:
		if !config.EventTypes.Move {
			return nil
		}
		histKind = db.StrmHistoryKindMove
		handlerErr = m.handleMoveEvent(ctx, account, event, mapping, cloudPath, lifeClient)

	case client115.RenameEventTypes[eventType]:
		if !config.EventTypes.Rename {
			return nil
		}
		histKind = db.StrmHistoryKindRename
		handlerErr = m.handleRenameEvent(ctx, account, event, mapping, cloudPath, lifeClient)

	default:
		// 未处理的事件类型，记录为 folder-sync 并跳过
		m.appendLog(ctx, account, "folder-sync", true, cloudPath, mapping.localPath,
			fmt.Sprintf("未处理的事件类型 type=%d", eventType))
		return nil
	}

	// P0-5 超时检测：若 handler 因 stall timeout 中止，按 TransferWaitMode 决策
	handlerErr = m.handleStallError(ctx, account, event, cloudPath, handlerErr, stallTimeout)
	m.recordMonitorHistory(account, histKind, handlerErr, time.Since(start))
	// —— Phase 1.1：累计 effective/skipped（handler 无 err → effective=1；stallWaitMode=skip 消错 → skipped=1）
	if handlerErr == nil {
		// "abort 模式下 handlerErr 非 nil=走 pollOnce 的 Errors；nil 则 effective"
		// 也兼容 skip 模式下 stallError 返回 nil 但其实未处理——这种场景由 handleStallError 自己 appendLog 了 stall-timeout
		//  我们仍然视为 skipped 更准确（因为没真副作用）。通过 log 语义识别：
		if config.TransferWaitMode == "skip" && ctx.Err() != nil && (ctx.Err() == context.DeadlineExceeded ||
			handlerErr == nil && stallTimeout > 0) {
			pollCountsAddSkipped(ctx, "stall_timeout_skip_mode")
		} else {
			pollCountsAddEffective(ctx)
			m.markDedupProcessed(event)
		}
	} else {
		// 保留错误给 pollOnce 累计 Errors
	}
	return handlerErr
}

// markDedupProcessed 标记事件为已处理（游标模式下无需标记，保留为空操作兼容调用点）
// 游标模式（from_time + from_id）天然去重，不需要 in-memory dedup
func (m *Monitor) markDedupProcessed(event client115.LifeEventItem) {
}

// handleStallError P0-5 处理整理队列无进展超时后的行为
// 若 handler 返回 context.DeadlineExceeded 且配置了 stall timeout：
//   - TransferWaitMode="skip"(默认)：记录日志后返回 nil，跳过该事件继续下一个
//   - TransferWaitMode="abort"：返回原始错误，中止本轮轮询
func (m *Monitor) handleStallError(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	cloudPath string,
	err error,
	stallTimeout time.Duration,
) error {
	if stallTimeout <= 0 || err == nil {
		return err
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	config := m.settingsFn()
	mode := strings.ToLower(strings.TrimSpace(config.TransferWaitMode))
	if mode == "" {
		mode = "skip"
	}

	warnMsg := fmt.Sprintf("事件处理无进展超时(%dm) account=%s file=%s cloudPath=%s",
		config.TransferStallTimeoutMinutes, account, event.FileName, cloudPath)
	logger.S().Warnf("[Monitor] %s", warnMsg)
	m.appendLog(ctx, account, "stall-timeout", false, cloudPath, "",
		fmt.Sprintf("无进展超时: %s", event.FileName))

	if mode == "abort" {
		return fmt.Errorf("整理队列无进展超时: %s", event.FileName)
	}
	// skip 模式：吞掉错误，事件被跳过
	return nil
}

// recordMonitorHistory P0-4 埋点：记录生活事件 STRM 处理历史到 DB
// 每个事件记录1条（对齐 executor.go 的1任务1记录模式），失败不阻断主流程。
// taskID 用 "monitor:{eventID}" 格式，便于按生活事件检索
func (m *Monitor) recordMonitorHistory(account string, kind db.StrmHistoryKind, err error, elapsed time.Duration) {
	if m.sqliteDB == nil {
		return
	}
	success := err == nil
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	entry := db.StrmHistoryEntry{
		TaskID:       "monitor",
		Kind:         kind,
		Account:      account,
		Success:      success,
		TotalFiles:   1,
		SuccessFiles: 0,
		FailedFiles:  0,
		ElapsedMs:    elapsed.Milliseconds(),
		ErrorMsg:     errMsg,
	}
	if success {
		entry.SuccessFiles = 1
	} else {
		entry.FailedFiles = 1
	}
	if _, herr := db.InsertStrmHistory(m.sqliteDB, entry); herr != nil {
		logger.S().Warnf("[Monitor] recordMonitorHistory failed account=%s kind=%s: %v",
			account, kind, herr)
	}
}

// ==================== handleCreateEvent：创建 STRM ====================

// singleFileCreateInput 内部单文件 STRM 创建输入（解耦 LifeEventItem 和 FsFileEntry）
type singleFileCreateInput struct {
	CloudPath string
	FileName  string
	PickCode  string
	FileSize  int64
	FileID    string // 可选（DB 写回用）
	ParentID  string // 可选（DB 写回用）
}

// createStrmForSingleFile 内部：单个媒体文件生成一个 STRM + 写回 DB + Emby刷新
// （共用：生活事件文件 create、文件夹递归内子文件、move/rename 失败fallback）
// P2-2：blacklistMatcher 为批量场景传入的预构建 AC 自动机；空则按阈值回退 contains
func (m *Monitor) createStrmForSingleFile(
	ctx context.Context,
	account string,
	in singleFileCreateInput,
	localParentDir string, // 本地父目录（不含文件名，不含 strm 后缀）
	notifyKind string, // 为空则不发通知；否则传 "文件"
	blacklistMatcher ...*concurrency.StringMatcher,
) (strmPathOut string, errOut error) {
	config := m.settingsFn()

	// 1. 扩展名检查
	if !isMediaFile(in.FileName, model.DefaultStrmExtensions) {
		return "", nil // 非媒体静默跳过
	}
	// 2. pickcode 严格校验（对齐 MoviePilot len=17 && isalnum）
	if !isValidPickcode(in.PickCode) {
		msg := fmt.Sprintf("pickcode 无效(需17位字母数字): %q file=%q", in.PickCode, in.FileName)
		logger.S().Warnf("[Monitor] createStrmForSingleFile: %s", msg)
		return "", fmt.Errorf("%s", msg)
	}
	// 3. 最小文件大小阈值 + 黑名单关键词（对齐 MoviePilot StrmGenerater.should_generate_strm）
	if reason, pass := shouldGenerateStrm(in.FileName, in.FileSize, config.MinFileSize, config.StrmGenerateBlacklist, blacklistMatcher...); !pass {
		logger.S().Debugf("[Monitor] createStrmForSingleFile 跳过%s：%q", reason, in.FileName)
		return "", nil // 不通过静默跳过
	}

	// 4. 构建本地 STRM 路径（.iso 保留双扩展名）+ P1-4 文件名模板
	var strmFileName string
	if config.StrmFilenameTemplate != "" {
		ext := strings.ToLower(filepath.Ext(in.FileName))
		stem := strings.TrimSuffix(in.FileName, filepath.Ext(in.FileName))
		if strings.EqualFold(ext, ".iso") {
			stem = stem + ".iso"
		}
		strmFileName = model.RenderStrmFilenameTemplate(config.StrmFilenameTemplate, in.FileName, ext, stem, account)
	}
	if strmFileName == "" {
		strmFileName = getStrmFileName(in.FileName)
	}
	strmPath := filepath.Join(localParentDir, strmFileName)

	// 4.5 对齐 MoviePilot overwrite_mode："never" 时已存在 STRM 则跳过
	if strings.EqualFold(config.OverwriteMode, "never") {
		if _, statErr := os.Stat(strmPath); statErr == nil {
			logger.S().Debugf("[Monitor] createStrmForSingleFile overwrite=never，跳过已存在: %s", strmPath)
			return strmPath, nil
		}
	}

	// 5. 生成 STRM URL 内容（P1-4 高级模板优先）
	content := generateStrmContent(
		in.CloudPath, config.StrmPrefix, config.EnablePathEncoding,
		config.Enable302, account, in.PickCode, in.FileName,
		config.StrmUrlTemplate,
	)
	if content == "" {
		return strmPath, fmt.Errorf("生成 STRM 内容失败: %s", in.FileName)
	}

	// 6. 确保父目录存在 + 原子写入
	if err := os.MkdirAll(filepath.Dir(strmPath), 0o755); err != nil {
		return strmPath, fmt.Errorf("创建父目录失败: %w", err)
	}
	if err := writeStrmFile(strmPath, content); err != nil {
		return strmPath, fmt.Errorf("写入 STRM 失败: %w", err)
	}

	// 7. 写回 filePathDb（302 模式 STRM 路由反查 pickcode）
	// —— 关键：先清理同 file_id 的旧 STRM，再写新 DB（对齐参考项目 create() L1590-1626）
	// 参考项目行为：检查 DB 中是否已有同 file_id 的旧记录，若旧路径 ≠ 新路径则删除旧 STRM
	if m.sqliteDB != nil {
		// 7a. 查找是否有旧 STRM（同 file_id 但不同路径）
		oldEntry, _ := db.GetFileOrFolderEntry(m.sqliteDB, account, in.FileID)
		if oldEntry != nil && oldEntry.Path != in.CloudPath {
			logger.S().Infof("[Monitor] createStrm: 检测到旧STRM fileID=%s oldPath=%s newPath=%s → 清理旧STRM",
				in.FileID, oldEntry.Path, in.CloudPath)
			m.cleanupOldStrmByFileID(account, in.FileID, oldEntry.Path, oldEntry.FileName, in.FileName, config)
		}

		// 7b. 写回新 DB 记录
		fileID := in.FileID
		if fileID == "" {
			fileID = "0"
		}
		parentID := in.ParentID
		if parentID == "" {
			parentID = "0"
		}
		entry := db.FilePathEntry{
			FileID:     fileID,
			Path:       in.CloudPath,
			FileName:   in.FileName,
			ParentID:   parentID,
			PickCode:   in.PickCode,
			UpdateTime: time.Now().Unix(),
		}
		if err := db.UpsertFilePathEntry(m.sqliteDB, account, entry); err != nil {
			logger.S().Warnf("[Monitor] 写回 filePathDb 失败 path=%s pickcode=%s: %v",
				in.CloudPath, in.PickCode, err)
		}
	}

	// 8.5 P1-2 AutoDownloadMetadata：创建同名关联资源占位文件（.nfo/.jpg/.png/.srt 等）
	// 占位空文件先建好目录结构/文件名对应关系，真实内容由全量扫描 runDownloads 或用户触发补齐
	// 对齐 MoviePilot auto_download_metadata 的 create-only 模式
	if config.AutoDownloadMetadata {
		placeholders := createRelatedAssetPlaceholders(strmPath, in.FileName, config.DownloadExtensions)
		if placeholders > 0 {
			logger.S().Debugf("[Monitor] STRM 关联资源占位 %d 个 (stem=%s)", placeholders,
				strings.TrimSuffix(filepath.Base(strmPath), ".strm"))
		}
	}

	// 9. Emby 刷库
	if m.embyRefresh != nil {
		if err := m.embyRefresh.RefreshOnCreate(ctx, strmPath); err != nil {
			logger.S().Warnf("[Monitor] Emby 刷库安排失败 path=%s: %v", strmPath, err)
		}
	}

	logger.S().Infof("[Monitor] STRM 已创建: %s", strmPath)
	return strmPath, nil
}

// handleCreateEvent 在映射的本地路径创建 STRM 文件
// 对齐 MoviePilot MonitorLife._create：
//   - 文件：直接生成 STRM（走 pickcode 严格校验 + .iso 命名）
//   - 文件夹：递归遍历内部所有媒体文件，逐个生成 STRM
func (m *Monitor) handleCreateEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
	lifeClient *client115.LifeClient,
	notify bool,
) error {
	// 文件夹事件：先 mkdir，然后递归遍历内部媒体文件生成 STRM
	if event.FileCategory == 0 {
		if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
			m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
				fmt.Sprintf("mkdir 失败: %v", err))
			return fmt.Errorf("mkdir 失败: %w", err)
		}
		strmCreated := 0
		var createErr error
		if lifeClient != nil {
			strmCreated, createErr = m.handleCreateFolderRecursive(
				ctx, account, mapping, lifeClient, event.FileID, cloudPath, 0,
			)
			if createErr != nil {
				logger.S().Warnf("[Monitor] 文件夹递归部分失败 folder=%s: %v", cloudPath, createErr)
			}
		} else {
			logger.S().Warnf("[Monitor] lifeClient 为空，无法递归文件夹内容 folder=%s", cloudPath)
		}
		m.appendLog(ctx, account, "create", true, cloudPath, mapping.localPath,
			fmt.Sprintf("文件夹已创建，内部生成 STRM %d 个", strmCreated))
		logger.S().Infof("[Monitor] 文件夹已创建: %s (内部STRM: %d)", mapping.localPath, strmCreated)
		if notify {
			m.notifyCreate(ctx, account, cloudPath, "目录", mapping.localPath, 0)
		}
		return createErr
	}

	// 单文件事件
	in := singleFileCreateInput{
		CloudPath: cloudPath,
		FileName:  event.FileName,
		PickCode:  event.PickCode,
		FileSize:  event.FileSize,
		FileID:    event.FileID,
		ParentID:  event.ParentID,
	}
	// 关键：mapping.localPath 已包含相对路径（如 dist\Strm\小王子），直接作为 STRM 目录
	// 不能用 filepath.Dir()，否则会丢失最后一级目录
	localParentDir := mapping.localPath
	strmPath, err := m.createStrmForSingleFile(ctx, account, in, localParentDir, "文件")
	if err != nil {
		m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath, err.Error())
		return err
	}
	if strmPath == "" {
		// 非媒体/过小等静默跳过
		m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
			fmt.Sprintf("未生成 STRM: %s", event.FileName))
		return nil
	}
	m.appendLog(ctx, account, "create", true, cloudPath, strmPath, "STRM 已创建")
	if notify {
		m.notifyCreate(ctx, account, cloudPath, "文件", strmPath, event.FileSize)
	}
	return nil
}

// handleCreateFolderRecursive DFS 遍历文件夹，对每个媒体文件创建 STRM
// 对齐 MoviePilot _create 中 iter_files_with_path batched 的处理逻辑
// P2-2：blacklistMatcher 在 depth==0 时构建一次，递归传递复用，避免内层反复构造
func (m *Monitor) handleCreateFolderRecursive(
	ctx context.Context,
	account string,
	rootMapping *pathMapping,
	lifeClient *client115.LifeClient,
	folderID string,
	folderCloudPath string,
	depth int,
	blacklistMatcher ...*concurrency.StringMatcher,
) (int, error) {
	if depth > MaxRecursionDepth {
		return 0, fmt.Errorf("超过递归最大深度 %d folder=%s", MaxRecursionDepth, folderCloudPath)
	}
	// 修正：rootMapping.localPath 可能已包含 relativePath（如 movie name），
	// 回退为映射根，否则文件 localParentDir 会多一层同名目录
	rootLocalPath := rootMapping.localPath
	if rootMapping.relativePath != "" {
		rel := sanitizePathParts(rootMapping.relativePath)
		if rel != "" && strings.HasSuffix(rootMapping.localPath, rel) {
			rootLocalPath = strings.TrimSuffix(rootMapping.localPath, rel)
			rootLocalPath = strings.TrimSuffix(rootLocalPath, string(filepath.Separator))
		}
	}
	totalCreated := 0
	processedFiles := 0
	config := m.settingsFn()
	// P2-2 构建黑名单 AC 自动机（在递归入口 depth==0 构建，内部层共用）
	var matcher *concurrency.StringMatcher
	if len(blacklistMatcher) > 0 && blacklistMatcher[0] != nil {
		matcher = blacklistMatcher[0]
	} else if concurrency.ShouldUseAC(config.StrmGenerateBlacklist) {
		matcher = concurrency.NewStringMatcher(config.StrmGenerateBlacklist)
	}
	pageLimit := 1000

	for offset := 0; ; offset += pageLimit {
		select {
		case <-ctx.Done():
			return totalCreated, ctx.Err()
		default:
		}
		resp, err := lifeClient.FsFiles(ctx, folderID, pageLimit, offset)
		if err != nil {
			return totalCreated, fmt.Errorf("FsFiles cid=%s offset=%d: %w", folderID, offset, err)
		}
		if resp == nil || len(resp.Data) == 0 {
			break
		}
		for _, entry := range resp.Data {
			processedFiles++
			if processedFiles > MaxFolderFiles*MaxRecursionDepth {
				logger.S().Warnf("[Monitor] 文件夹递归达到处理上限 folder=%s done=%d",
					folderCloudPath, processedFiles)
				return totalCreated, nil
			}
			entryCloudPath := folderCloudPath + "/" + entry.Name
			if entry.IsDir {
			// P1-5: 子目录写入 folders 表（对齐参考项目 process_life_dir_item upsert_batch）
			if m.sqliteDB != nil {
				subCIDStr := fmt.Sprintf("%v", entry.CID)
				if subCIDStr != "" && subCIDStr != "0" {
					_ = db.UpsertFolderEntry(m.sqliteDB, account, db.FilePathEntry{
						FileID:     subCIDStr,
						Path:       entryCloudPath,
						FileName:   entry.Name,
						ParentID:   folderID,
						UpdateTime: time.Now().Unix(),
					})
				}
			}
			// 子目录：先 mkdir，然后递归
			subRel := strings.TrimPrefix(
				strings.TrimPrefix(entryCloudPath, rootMapping.cloudPath), "/")
			subLocal := filepath.Join(rootLocalPath, sanitizePathParts(subRel))
			_ = os.MkdirAll(subLocal, 0o755)
			// 子目录的 cid 是 entry.CID（转为 string）
			subCIDStr := fmt.Sprintf("%v", entry.CID)
			n, subErr := m.handleCreateFolderRecursive(
				ctx, account, rootMapping, lifeClient, subCIDStr, entryCloudPath, depth+1, matcher,
			)
			totalCreated += n
			if subErr != nil {
				logger.S().Warnf("[Monitor] 子目录递归失败 sub=%s: %v", entryCloudPath, subErr)
			}
			continue
		}
			// 媒体文件：计算本地父目录并生成
			relFromRoot := strings.TrimPrefix(
				strings.TrimPrefix(entryCloudPath, rootMapping.cloudPath), "/")
			relLocal := sanitizePathParts(relFromRoot)
			localParentDir := filepath.Join(rootLocalPath, filepath.Dir(relLocal))
			in := singleFileCreateInput{
				CloudPath: entryCloudPath,
				FileName:  entry.Name,
				PickCode:  entry.PickCode,
				FileSize:  entry.Size,
				FileID:    fmt.Sprintf("%v", entry.FID),
				ParentID:  folderID,
			}
			strmPath, serr := m.createStrmForSingleFile(ctx, account, in, localParentDir, "", matcher)
			if serr != nil {
				logger.S().Warnf("[Monitor] 文件夹内子文件生成失败 cloud=%s: %v", entryCloudPath, serr)
				continue
			}
			if strmPath != "" {
				totalCreated++
			}
		}
		if len(resp.Data) < pageLimit {
			break
		}
		_ = config // 避免 config 未使用警告（后续可接入上限动态配置）
	}
	return totalCreated, nil
}

// ==================== handleDeleteEvent：删除 STRM ====================

// handleDeleteEvent 删除 STRM 文件及相关文件
// P0-3: 删除前校验网盘文件是否仍存在（对齐参考项目 remove() 中的 storagechain.get_file_item 检查）
func (m *Monitor) handleDeleteEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
	lifeClient *client115.LifeClient,
) error {
	config := m.settingsFn()

	// 对齐参考项目 remove() L1439-1443: DB无路径记录时不删除，"防止误删不处理"
	if m.sqliteDB != nil && event.FileID != "" && event.FileID != "0" {
		entry, err := db.GetFileOrFolderEntry(m.sqliteDB, account, event.FileID)
		if err != nil || entry == nil || entry.Path == "" {
			logger.S().Infof("[Monitor] delete: DB无路径记录，跳过防止误删 fid=%s name=%s（对齐参考项目 L1439-1443）",
				event.FileID, event.FileName)
			m.appendLog(ctx, account, "delete", false, cloudPath, mapping.localPath,
				"跳过: DB无路径记录，防止误删")
			return nil
		}
	}

	// P0-3: 网盘存在性校验 — 如果文件仍在网盘上，跳过删除（可能只是移动事件被误报为删除）
	// 对齐参考项目: cloud_file_item = storagechain.get_file_item(p115client, file_id)
	//               if cloud_file_item: return  # 文件仍存在，不删除本地 STRM
	if lifeClient != nil && lifeClient.FsClient() != nil && event.FileID != "" && event.FileID != "0" {
		if fileExists, err := m.checkCloudFileExists(ctx, lifeClient, event.FileID, event.FileName); err == nil && fileExists {
			logger.S().Infof("[Monitor] 网盘文件仍存在，跳过删除: fid=%s name=%s cloudPath=%s",
				event.FileID, event.FileName, cloudPath)
			m.appendLog(ctx, account, "delete", true, cloudPath, mapping.localPath,
				"跳过: 网盘文件仍存在（可能为移动事件）")
			return nil
		}
	}

	// 文件夹事件：递归删除目录（P1-5 Delete 安全兜底）
	if event.FileCategory == 0 {
		if err := strmutil.SafeDeletePath(mapping.localPath, config.EnableHardDelete); err != nil {
			m.appendLog(ctx, account, "delete", false, cloudPath, mapping.localPath,
				fmt.Sprintf("删除目录失败: %v", err))
			return fmt.Errorf("删除目录失败: %w", err)
		}
		// 清理空父目录
		if config.RemoveEmptyDirs {
			removeEmptyParents(filepath.Dir(mapping.localPath), config.PathMappings)
		}
		actionWord := "已删除"
		if !config.EnableHardDelete {
			actionWord = "已软删除(.deleted.bak)"
		}
		m.appendLog(ctx, account, "delete", true, cloudPath, mapping.localPath, "文件夹"+actionWord)
		m.notifyDelete(ctx, account, cloudPath, "目录", mapping.localPath)
		logger.S().Infof("[Monitor] 文件夹%s: %s", actionWord, mapping.localPath)
		return nil
	}

	// 文件事件：删除 STRM + 相关文件（P1-5 Delete 安全兜底）
	strmFileName := getStrmFileName(event.FileName)
	// 关键：mapping.localPath 已包含相对路径（如 dist\Strm\小王子），直接拼接文件名
	strmPath := filepath.Join(mapping.localPath, strmFileName)

	// 删除 STRM 文件
	if err := strmutil.SafeDeleteStrmFile(strmPath, config.EnableHardDelete); err != nil {
		m.appendLog(ctx, account, "delete", false, cloudPath, strmPath,
			fmt.Sprintf("删除 STRM 失败: %v", err))
		return fmt.Errorf("删除 STRM 失败: %w", err)
	}

	// 删除相关文件（.nfo/.jpg/.srt 等同名文件）
	deletedRelated := deleteRelatedFiles(strmPath)

	// 清理空父目录
	if config.RemoveEmptyDirs {
		removeEmptyParents(filepath.Dir(strmPath), config.PathMappings)
	}

	m.appendLog(ctx, account, "delete", true, cloudPath, strmPath,
		fmt.Sprintf("STRM 已删除: %s (关联文件 %d 个)", strmPath, deletedRelated))
	m.notifyDelete(ctx, account, cloudPath, "文件", strmPath)
	logger.S().Infof("[Monitor] STRM 已删除: %s (关联 %d)", strmPath, deletedRelated)

	// 通知 Emby 刷库
	if m.embyRefresh != nil {
		if err := m.embyRefresh.RefreshOnDelete(ctx, strmPath); err != nil {
			logger.S().Warnf("[Monitor] Emby 刷库安排失败 path=%s: %v", strmPath, err)
		}
	}

	return nil
}

// checkCloudFileExists 通过 FsFiles API 检查文件是否仍存在于网盘
// 对齐参考项目 storagechain.get_file_item(file_id) 存在性校验
func (m *Monitor) checkCloudFileExists(ctx context.Context, lifeClient *client115.LifeClient, fileID, fileName string) (bool, error) {
	fsClient := lifeClient.FsClient()
	if fsClient == nil {
		return false, fmt.Errorf("fsClient not initialized")
	}
	// 用 file_id 作为 cid 查询：如果文件仍存在，API 会返回其信息
	// 对齐参考项目: file_item = p115client.fs_files(cid=file_id)
	resp, err := fsClient.FsFiles(ctx, fileID, 1, 0, lifeClient.Cookie())
	if err != nil || resp == nil || !resp.State {
		// API 失败或 state=false → 文件不存在（已删除）
		return false, nil
	}
	// 如果返回了数据，说明文件仍存在
	return len(resp.Data) > 0, nil
}

// handleMoveRenameQuadrant 对齐参考项目 MoviePilot MonitorLife.move() 的四象限决策矩阵
// 按 old_is_media × new_is_media 路由到对应 handler：
//   - 其他→媒体: create STRM (handleMoveEvent/handleRenameEvent with create fallback)
//   - 媒体→媒体: move/recreate (handleMoveEvent/handleRenameEvent)
//   - 媒体→其他: remove old STRM (handleMoveOutEvent)
//   - 其他→其他: 尝试本地文件名兜底，无则跳过
//
// 返回 handled=true 表示已处理（caller 直接返回），handled=false 表示未处理（caller 走 generic skip）
func (m *Monitor) handleMoveRenameQuadrant(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	newRawCloudPath string,
	cloudPathSource string,
	config model.LifeMonitorSettings,
	lifeClient *client115.LifeClient,
) (handled bool, err error) {
	// 1. 获取旧路径（DB 反查，对齐参考项目 client.py L1094-1098 get_by_id(file_id)）
	// 关键：必须在写新路径到 DB 之前反查，否则反查到的是新路径，handler 无法定位旧 STRM
	oldCloudPath := m.resolveOldCloudPathByFileID(account, event.FileID)

	// 2. 判定 old_is_media / new_is_media
	newCloudPath := normalizeCloudPath(newRawCloudPath)
	oldIsMedia := isCloudPathInMediaMapping(oldCloudPath, config.PathMappings, account)
	newIsMedia := isCloudPathInMediaMapping(newCloudPath, config.PathMappings, account)

	// 3. Write-Ahead DB：⚠️ 此处不能提前写新路径！
	// 对齐参考项目 client.py L1118-1124 _sync_rename_path_records：DB 同步必须在反查旧路径之后、
	// 由 handler 内部按场景调用（文件夹 rename/move 成功后 UpdatePathPrefixBatch，文件成功后 UpsertFilePathEntry）。
	// 若此处提前 writeAheadFilePath，handleRenameEvent/handleMoveEvent 内部 resolveOldCloudPathByFileID
	// 反查到的将是新路径，导致 oldLocalPath==mapping.localPath，走"本地无旧 STRM"分支，
	// 只 create 新 STRM 不删旧 STRM（用户反馈的 rename 后旧 strm 残留 bug 根因）。

	// 4. 四象限分类
	var quadrant string
	switch {
	case !oldIsMedia && newIsMedia:
		quadrant = "OTHER_TO_MEDIA"
	case oldIsMedia && newIsMedia:
		quadrant = "MEDIA_TO_MEDIA"
	case oldIsMedia && !newIsMedia:
		quadrant = "MEDIA_TO_OTHER"
	default:
		quadrant = "OTHER_TO_OTHER"
	}

	logger.S().Infof("[Monitor] EVENT_DECIDE quadrant=%s kind=%s type=%d fid=%s pid=%s name=%q oldCloudPath=%q newCloudPath=%q oldIsMedia=%v newIsMedia=%v",
		quadrant, eventKindLabel(event.Type), event.Type, event.FileID, event.ParentID, event.FileName,
		oldCloudPath, newCloudPath, oldIsMedia, newIsMedia)

	// 5. 四象限路由
	switch quadrant {
	case "OTHER_TO_MEDIA":
		// 其他→媒体: 生成 STRM（对齐参考项目 move() L966-975 + rename() 中 OTHER_TO_MEDIA 走 create 路径）
		// 关键：旧路径不在媒体映射 → 没有旧 STRM 可 rename，直接走 create 路径在新位置生成 STRM
		// 对齐参考项目: old_file_path=None 时不扫描本地旧文件，直接 _create()
		mapping := matchPathMapping(newCloudPath, config.PathMappings, account)
		if mapping == nil {
			return true, fmt.Errorf("OTHER_TO_MEDIA: mapping=nil unexpected")
		}
		logger.S().Infof("[Monitor] OTHER_TO_MEDIA path match: cloudPath=%s → localPath=%s relativePath=%q account=%s",
			newCloudPath, mapping.localPath, mapping.relativePath, account)

		// —— 先清理旧 STRM：重命名前可能有旧 STRM 在别的位置 ——
		m.cleanupOldStrmForOtherToMedia(ctx, account, event, oldCloudPath, config)

		start := time.Now()
		// OTHER_TO_MEDIA 始终走 create（新路径首次进入媒体目录）
		histKind := db.StrmHistoryKindMonitor
		err = m.handleCreateEvent(ctx, account, event, mapping, newCloudPath, lifeClient, false)
		m.recordMonitorHistory(account, histKind, err, time.Since(start))
		if err == nil {
			pollCountsAddEffective(ctx)
			m.markDedupProcessed(event)
		}
		return true, err

	case "MEDIA_TO_MEDIA":
		// 媒体→媒体: move/recreate
		mapping := matchPathMapping(newCloudPath, config.PathMappings, account)
		if mapping == nil {
			return true, fmt.Errorf("MEDIA_TO_MEDIA: mapping=nil unexpected")
		}
		start := time.Now()
		var histKind db.StrmHistoryKind
		if client115.MoveEventTypes[event.Type] {
			histKind = db.StrmHistoryKindMove
			err = m.handleMoveEvent(ctx, account, event, mapping, newCloudPath, lifeClient)
		} else {
			histKind = db.StrmHistoryKindRename
			err = m.handleRenameEvent(ctx, account, event, mapping, newCloudPath, lifeClient)
		}
		m.recordMonitorHistory(account, histKind, err, time.Since(start))
		if err == nil {
			pollCountsAddEffective(ctx)
			m.markDedupProcessed(event)
		}
		return true, err

	case "MEDIA_TO_OTHER":
		// 媒体→其他: 删除旧 STRM
		start := time.Now()
		err = m.handleMoveOutEvent(ctx, account, event, newCloudPath)
		kind := db.StrmHistoryKindMove
		if client115.RenameEventTypes[event.Type] {
			kind = db.StrmHistoryKindRename
		}
		m.recordMonitorHistory(account, kind, err, time.Since(start))
		if err == nil {
			pollCountsAddEffective(ctx)
			m.markDedupProcessed(event)
		}
		return true, err

	default:
		// 其他→其他: 尝试本地文件名兜底
		// 对齐参考项目: DB无旧路径时检查本地是否有匹配的STRM
		if config.MoveOutRemoveLocalStrm {
			localPath := m.findLocalStrmByFileName(account, event.FileName, event.FileCategory, config.PathMappings)
			if localPath != "" {
				logger.S().Infof("[Monitor] OTHER_TO_OTHER 本地兜底命中: file=%s localPath=%s → 按移出处理",
					event.FileName, localPath)
				start := time.Now()
				err = m.handleMoveOutEvent(ctx, account, event, newCloudPath)
				m.recordMonitorHistory(account, db.StrmHistoryKindMove, err, time.Since(start))
				if err == nil {
					pollCountsAddEffective(ctx)
					m.markDedupProcessed(event)
				}
				return true, err
			}
		}
		// 无本地兜底命中：跳过
		pollCountsAddSkipped(ctx, "quadrant_OTHER_TO_OTHER_skip")
		m.markDedupProcessed(event)
		return true, nil
	}
}

// ==================== handleMoveOutEvent：移出映射兜底清理 ====================

// handleMoveOutEvent 处理"移出映射"场景：新 cloudPath 不在任何映射内，但旧路径可能在映射内
// 对齐 MoviePilot monitor_life_move_out_media_remove_local_strm=true 行为：
//   - 通过 fileId 反查 DB 中旧 cloudPath
//   - 若旧路径落在映射内 → 删除旧 STRM + 关联资源 + 空目录清理 + DB 记录删除
//   - 若旧路径也不在映射内 → 静默跳过（旧 STRM 不在本服务管理范围内）
//
// 该函数是 processEvent 中 mapping==nil 分支的兜底，专门处理 Move/Rename 事件。
// 对 Delete 事件无需调用本函数，因为 cloudPath 已是删除前路径（即旧路径），mapping!=nil 时正常删除。
func (m *Monitor) handleMoveOutEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	newCloudPath string,
) error {
	config := m.settingsFn()

	oldCloudPath := m.resolveOldCloudPathByFileID(account, event.FileID)
	oldLocalPath, _ := resolveOldLocalPath(oldCloudPath, config.PathMappings, account)

	// DB 查不到旧路径时：在本地 Strm 目录中按文件名匹配查找
	if oldLocalPath == "" {
		oldLocalPath = m.findLocalStrmByFileName(account, event.FileName, event.FileCategory, config.PathMappings)
		if oldLocalPath != "" {
			oldCloudPath = event.FileName // 裸文件名作为 cloudPath 记录
			logger.S().Infof("[Monitor] DB无旧路径，本地文件名匹配到: file=%s localPath=%s",
				event.FileName, oldLocalPath)
		}
	}
	if oldLocalPath == "" {
		logger.S().Infof("[Monitor] 移出映射跳过：DB无旧路径且本地未匹配 fileID=%s name=%s newCloudPath=%s",
			event.FileID, event.FileName, newCloudPath)
		return nil
	}

	logger.S().Infof("[Monitor] 检测到移出映射: fileID=%s oldCloudPath=%s → newCloudPath=%s，清理旧 STRM",
		event.FileID, oldCloudPath, newCloudPath)

	// P0-7: MoveOutRemoveLocalStrm=false 时保留旧 STRM，仅记录日志
	if !config.MoveOutRemoveLocalStrm {
		logger.S().Infof("[Monitor] 移出映射但 MoveOutRemoveLocalStrm=false，保留旧 STRM: %s", oldLocalPath)
		m.appendLog(ctx, account, "move-out", true, oldCloudPath, oldLocalPath,
			fmt.Sprintf("移出映射保留旧STRM: %s → %s", oldCloudPath, newCloudPath))
		return nil
	}

	// 文件夹移出：递归删除旧本地目录（P1-5 Delete 安全兜底）
	if event.FileCategory == 0 {
		if err := strmutil.SafeDeletePath(oldLocalPath, config.EnableHardDelete); err != nil {
			m.appendLog(ctx, account, "move-out", false, oldCloudPath, oldLocalPath,
				fmt.Sprintf("移出映射删除目录失败: %v", err))
			return fmt.Errorf("移出映射删除目录失败: %w", err)
		}
		if config.RemoveEmptyDirs {
			removeEmptyParents(filepath.Dir(oldLocalPath), config.PathMappings)
		}
		// DB 中清理以 oldCloudPath 为前缀的所有记录
		if m.sqliteDB != nil {
			if n, derr := db.DeleteByPathPrefix(m.sqliteDB, account, oldCloudPath); derr != nil {
				logger.S().Warnf("[Monitor] 移出映射后 DB 清理失败 %s: %v", oldCloudPath, derr)
			} else if n > 0 {
				logger.S().Infof("[Monitor] 移出映射后 DB 清理 %d 条 (prefix=%s)", n, oldCloudPath)
			}
		}
		actionWord := "旧目录已删除"
		if !config.EnableHardDelete {
			actionWord = "旧目录已软删除(.deleted.bak)"
		}
		m.appendLog(ctx, account, "move-out", true, oldCloudPath, oldLocalPath,
			fmt.Sprintf("移出映射：%s %s → %s", actionWord, oldCloudPath, newCloudPath))
		m.notifyMove(ctx, account, oldCloudPath, "目录", oldLocalPath)
		if m.embyRefresh != nil {
			if err := m.embyRefresh.RefreshOnDelete(ctx, oldLocalPath); err != nil {
				logger.S().Warnf("[Monitor] Emby 刷库安排失败 path=%s: %v", oldLocalPath, err)
			}
		}
		return nil
	}

	// 文件移出：删除旧 STRM + 关联资源（P1-5 Delete 安全兜底）
	strmFileName := getStrmFileName(filepath.Base(oldLocalPath))
	strmPath := filepath.Join(filepath.Dir(oldLocalPath), strmFileName)
	if err := strmutil.SafeDeleteStrmFile(strmPath, config.EnableHardDelete); err != nil {
		m.appendLog(ctx, account, "move-out", false, oldCloudPath, strmPath,
			fmt.Sprintf("移出映射删除 STRM 失败: %v", err))
		return fmt.Errorf("移出映射删除 STRM 失败: %w", err)
	}
	deletedRelated := deleteRelatedFiles(strmPath)
	if config.RemoveEmptyDirs {
		removeEmptyParents(filepath.Dir(strmPath), config.PathMappings)
	}
	// DB 中清理该 fileID 对应记录
	if m.sqliteDB != nil {
		if derr := db.RemoveFilePathEntry(m.sqliteDB, account, event.FileID); derr != nil {
			logger.S().Warnf("[Monitor] 移出映射后 DB 清理失败 fileID=%s: %v", event.FileID, derr)
		} else {
			logger.S().Infof("[Monitor] 移出映射后 DB 已清理 (fileID=%s)", event.FileID)
		}
	}
	m.appendLog(ctx, account, "move-out", true, oldCloudPath, strmPath,
		fmt.Sprintf("移出映射：STRM 已删除 %s (关联 %d) → %s", strmPath, deletedRelated, newCloudPath))
	m.notifyMove(ctx, account, oldCloudPath, "文件", strmPath)
	if m.embyRefresh != nil {
		if err := m.embyRefresh.RefreshOnDelete(ctx, strmPath); err != nil {
			logger.S().Warnf("[Monitor] Emby 刷库安排失败 path=%s: %v", strmPath, err)
		}
	}
	return nil
}

// ==================== handleMoveEvent：移动 STRM ====================

// cleanupOldStrmAssets recreate 模式下清理旧 STRM + 关联资源 + DB 记录
// 用于 MoveMediaMode == "recreate" 时，在新位置重新生成前先清理旧的本地痕迹，
// 避免旧位置残留 STRM 导致 Emby 出现幽灵媒体。local_move 模式不调用本函数（走原子 rename）。
func (m *Monitor) cleanupOldStrmAssets(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	oldCloudPath, oldLocalPath string,
	config model.LifeMonitorSettings,
) {
	if oldLocalPath == "" {
		return
	}
	// 文件夹：递归删除旧本地目录 + DB 按前缀清理（P1-5 Delete 安全兜底）
	if event.FileCategory == 0 {
		if err := strmutil.SafeDeletePath(oldLocalPath, config.EnableHardDelete); err != nil {
			logger.S().Warnf("[Monitor] recreate 清理旧目录失败 %s: %v", oldLocalPath, err)
		}
		if config.RemoveEmptyDirs {
			removeEmptyParents(filepath.Dir(oldLocalPath), config.PathMappings)
		}
		if m.sqliteDB != nil && oldCloudPath != "" {
			if n, derr := db.DeleteByPathPrefix(m.sqliteDB, account, oldCloudPath); derr != nil {
				logger.S().Warnf("[Monitor] recreate DB 前缀清理失败 %s: %v", oldCloudPath, derr)
			} else if n > 0 {
				logger.S().Infof("[Monitor] recreate DB 前缀清理 %d 条 (%s)", n, oldCloudPath)
			}
		}
		actionWord := "旧目录已清理"
		if !config.EnableHardDelete {
			actionWord = "旧目录已软清理(.deleted.bak)"
		}
		m.appendLog(ctx, account, "move-recreate", true, oldCloudPath, oldLocalPath,
			fmt.Sprintf("recreate 模式：%s %s", actionWord, oldLocalPath))
		return
	}
	// 文件：删除旧 STRM + 关联资源 + DB 按 fileID 清理（P1-5 Delete 安全兜底）
	oldStrmPath := filepath.Join(filepath.Dir(oldLocalPath), getStrmFileName(filepath.Base(oldLocalPath)))
	if err := strmutil.SafeDeleteStrmFile(oldStrmPath, config.EnableHardDelete); err != nil {
		logger.S().Warnf("[Monitor] recreate 清理旧 STRM 失败 %s: %v", oldStrmPath, err)
	}
	deletedRelated := deleteRelatedFiles(oldStrmPath)
	if config.RemoveEmptyDirs {
		removeEmptyParents(filepath.Dir(oldStrmPath), config.PathMappings)
	}
	if m.sqliteDB != nil {
		if derr := db.RemoveFilePathEntry(m.sqliteDB, account, event.FileID); derr != nil {
			logger.S().Warnf("[Monitor] recreate DB fileID 清理失败 %s: %v", event.FileID, derr)
		} else {
			logger.S().Infof("[Monitor] recreate DB fileID 已清理 (%s)", event.FileID)
		}
	}
	m.appendLog(ctx, account, "move-recreate", true, oldCloudPath, oldStrmPath,
		fmt.Sprintf("recreate 模式：旧 STRM 已清理 %s (关联 %d)", oldStrmPath, deletedRelated))
}

// resolveOldCloudPathByFileID 从 filePathDb 按 file_id 反查旧的 cloudPath
// 对齐 MoviePilot：file_item = _databasehelper.get_by_id(int(event["file_id"]))
// P0-2: 先查 files 表，未命中查 folders 表（文件夹路径持久化）
func (m *Monitor) resolveOldCloudPathByFileID(account, fileID string) (oldCloudPath string) {
	if m.sqliteDB == nil || fileID == "" || fileID == "0" {
		return ""
	}
	entry, err := db.GetFileOrFolderEntry(m.sqliteDB, account, fileID)
	if err != nil || entry == nil {
		return ""
	}
	return entry.Path
}

// findLocalStrmByFileName 当 DB 查不到旧路径时，在本地 Strm 目录中按文件名匹配查找
// 对齐参考项目 move 事件中 DB 无旧路径时的兜底策略
// extendedDirs: 额外搜索目录列表（如 MediaMountPath）
func (m *Monitor) findLocalStrmByFileName(account, fileName string, fileCategory int, mappings []model.MonitorPathMapping, extendedDirs ...string) string {
	if fileName == "" {
		return ""
	}
	// 先在映射目录中搜索
	for _, mp := range mappings {
		if mp.LocalPath == "" {
			continue
		}
		// 文件夹：在 LocalPath 下查找同名目录
		if fileCategory == 0 {
			candidate := filepath.Join(mp.LocalPath, fileName)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		} else {
			// 文件：在 LocalPath 下查找同名 .strm 文件
			strmName := getStrmFileName(fileName)
			entries, err := os.ReadDir(mp.LocalPath)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				strmPath := filepath.Join(mp.LocalPath, entry.Name(), strmName)
				if _, err := os.Stat(strmPath); err == nil {
					return strmPath
				}
			}
		}
	}

	// 再在扩展目录中搜索（如 MediaMountPath）
	for _, dir := range extendedDirs {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		if fileCategory == 0 {
			candidate := filepath.Join(dir, fileName)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		} else {
			strmName := getStrmFileName(fileName)
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				strmPath := filepath.Join(dir, entry.Name(), strmName)
				if _, err := os.Stat(strmPath); err == nil {
					return strmPath
				}
			}
		}
	}
	return ""
}

// resolveOldLocalPathByCloudPath 通过旧 cloudPath + 映射配置，计算旧的本地路径
func resolveOldLocalPath(oldCloudPath string, mappings []model.MonitorPathMapping, account string) (oldLocalPath string, oldMapping *pathMapping) {
	if oldCloudPath == "" {
		return "", nil
	}
	mp := matchPathMapping(oldCloudPath, mappings, account)
	if mp == nil {
		return "", nil
	}
	return mp.localPath, mp
}

// moveOrRenameRelatedAssets 本地重命名/移动关联的 .nfo/.jpg/.srt/.sub 等同名资源
// sourceStem: 旧文件名 stem（不含扩展名，含 .iso 时指原始文件名 stem + .iso）；targetStem: 新的 stem
// 对齐 MoviePilot: _move_local_related_asset + PathRemoveUtils.clean_related_files 的反向 rename
func moveOrRenameRelatedAssets(dir, sourceStem, targetStem string) int {
	if dir == "" || sourceStem == "" || targetStem == "" || sourceStem == targetStem {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	moved := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// .strm 文件本身已在主流程处理；此处跳过避免重复操作
		if strings.HasSuffix(strings.ToLower(name), ".strm") {
			continue
		}
		// 将 sourceStem.xxx 的资源重命名为 targetStem.xxx（保留原扩展名）
		if !strings.HasPrefix(name, sourceStem) {
			continue
		}
		rest := strings.TrimPrefix(name, sourceStem)
		// rest 必须是空或 .xxx（扩展名形式），避免 Movie.mkv.png 被 Movie.mkv 误匹配到 "Movie" stem
		if rest != "" && !strings.HasPrefix(rest, ".") {
			continue
		}
		targetName := targetStem + rest
		if targetName == name {
			continue
		}
		src := filepath.Join(dir, name)
		dst := filepath.Join(dir, targetName)
		if _, statErr := os.Stat(dst); statErr == nil {
			continue // 目标已存在，不覆盖
		}
		if mvErr := os.Rename(src, dst); mvErr == nil {
			moved++
		}
	}
	return moved
}

// handleMoveEvent 移动 STRM 到新路径
// 对齐 MoviePilot MonitorLife.move：
//   - 文件夹：直接 shutil_move（原子移动，保留内部 STRM/nfo/jpg/刮削缓存），失败 fallback 到递归 recreate
//   - 文件：通过 DB 反查旧路径 → 旧 strm/nfo 重命名；找不到旧路径 → fallback 到 recreate
func (m *Monitor) handleMoveEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
	lifeClient *client115.LifeClient,
) error {
	config := m.settingsFn()
	oldCloudPath := m.resolveOldCloudPathByFileID(account, event.FileID)
	oldLocalPath, _ := resolveOldLocalPath(oldCloudPath, config.PathMappings, account)

	// MoveMediaMode 决策：
	//   - "recreate"：先清理旧 STRM/目录 + DB，再在新位置重新生成（不依赖本地文件系统状态）
	//   - "local_move"/""：原子 rename 优先，失败 fallback 到 recreate（保留内部结构和刮削元数据）
	// P0-7: MoveMediaKeepOldStrm=true 时保留旧 STRM（不调用 cleanupOldStrmAssets）
	isRecreateMode := strings.EqualFold(config.MoveMediaMode, "recreate")
	if isRecreateMode && !config.MoveMediaKeepOldStrm {
		if oldLocalPath != "" {
			m.cleanupOldStrmAssets(ctx, account, event, oldCloudPath, oldLocalPath, config)
		} else if event.FileCategory != 0 {
			// DB 无旧路径记录 → 通过文件名查找本地旧 STRM 并删除
			// 确保"移动=删旧+生新"行为一致，不依赖 DB 是否有旧记录
			var oldStrmPath string
			if oldCloudPath != "" {
				// 从旧云路径提取旧文件名（e.g. "/电影/小王子 (2015).mkv" → "小王子 (2015).mkv"）
				oldFileName := filepath.Base(oldCloudPath)
				if oldFileName != "" && oldFileName != "." && oldFileName != "/" {
					oldStrmPath = m.findLocalStrmByFileName(account, oldFileName, event.FileCategory, config.PathMappings)
				}
			}
			// 备用：用事件新文件名查找（可能找到新 STRM，但如果它不存在则说明还没创建，可以安全删除同名旧 STRM）
			if oldStrmPath == "" {
				oldStrmPath = m.findLocalStrmByFileName(account, event.FileName, event.FileCategory, config.PathMappings)
			}
			if oldStrmPath != "" {
				if err := strmutil.SafeDeleteStrmFile(oldStrmPath, config.EnableHardDelete); err != nil {
					logger.S().Warnf("[Monitor] recreate DB无旧路径 清理旧STRM失败 %s: %v", oldStrmPath, err)
				} else {
					deletedRelated := deleteRelatedFiles(oldStrmPath)
					logger.S().Infof("[Monitor] recreate DB无旧路径 旧STRM已清理 %s (关联 %d)", oldStrmPath, deletedRelated)
				}
				if config.RemoveEmptyDirs {
					removeEmptyParents(filepath.Dir(oldStrmPath), config.PathMappings)
				}
				if m.sqliteDB != nil {
					if derr := db.RemoveFilePathEntry(m.sqliteDB, account, event.FileID); derr != nil {
						logger.S().Warnf("[Monitor] recreate DB无旧路径 清理DB失败 %s: %v", event.FileID, derr)
					}
				}
			}
		}
	}

	// —— 文件夹移动（对齐参考项目 move L966-1012 + _move_local_media_assets L1768-1788）——
	// 参考项目流程：
	//   ① recreate 模式 或 DB 无旧路径（其它目录→媒体目录）→ mkdir + 递归 create（L1006-1012 / L966-982）
	//   ② local_move 模式 + DB 有旧路径：对齐 _move_local_media_assets L1768-1788：
	//      a) old 目录不存在 → 跳过（L1769-1774）
	//      b) new 目录已存在 → 跳过（L1775-1780）
	//      c) mkdir 父目录 + shutil_move（L1781-1782）
	//      d) rename 失败直接抛异常，不 fallback（shutil_move 行为）
	//      e) DB 路径前缀批量更新（_sync_move_event_db_records L1964+）
	if event.FileCategory == 0 {
		// ① recreate 模式 或 DB 无旧路径：走 mkdir + 递归 create
		if isRecreateMode || oldLocalPath == "" {
			if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
				return fmt.Errorf("mkdir 失败: %w", err)
			}
			strmCreated := 0
			// recreate 模式强制创建新 STRM（删旧+生新是 recreate 的核心语义）
			// 非 recreate 模式（DB 无旧路径的 OTHER_TO_MEDIA）也创建新 STRM
			if lifeClient != nil {
				var rerr error
				strmCreated, rerr = m.handleCreateFolderRecursive(
					ctx, account, mapping, lifeClient, event.FileID, cloudPath, 0,
				)
				if rerr != nil {
					logger.S().Warnf("[Monitor] 移动事件 文件夹递归recreate失败 folder=%s: %v", cloudPath, rerr)
				}
			}
			actionWord := "recreate 模式"
			if !isRecreateMode {
				actionWord = "DB 无旧路径(其它→媒体)"
			}
			m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
				fmt.Sprintf("%s：文件夹已重建，生成 STRM %d 个", actionWord, strmCreated))
			m.notifyMove(ctx, account, cloudPath, "目录", mapping.localPath)
			return nil
		}

		// ② local_move 模式 + DB 有旧路径：对齐 _move_local_media_assets L1768-1788
		// a) 旧目录不存在 → 跳过（L1769-1774）
		if _, stErr := os.Stat(oldLocalPath); stErr != nil {
			m.appendLog(ctx, account, "move", false, cloudPath, mapping.localPath,
				fmt.Sprintf("跳过: 本地旧文件夹不存在 %s", oldLocalPath))
			return nil
		}
		// b) 新目录已存在 → 跳过（L1775-1780）
		if _, dstErr := os.Stat(mapping.localPath); dstErr == nil {
			m.appendLog(ctx, account, "move", false, cloudPath, mapping.localPath,
				fmt.Sprintf("跳过: 移动目标已存在 %s", mapping.localPath))
			return nil
		}
		// c) mkdir 父目录（L1781）
		if err := os.MkdirAll(filepath.Dir(mapping.localPath), 0o755); err != nil {
			return fmt.Errorf("创建父目录失败: %w", err)
		}
		// d) shutil_move（os.Rename），失败直接返回错误，不 fallback（对齐 L1782 + shutil_move 抛异常）
		if mvErr := os.Rename(oldLocalPath, mapping.localPath); mvErr != nil {
			m.appendLog(ctx, account, "move", false, cloudPath, oldLocalPath,
				fmt.Sprintf("文件夹 rename 失败 %s → %s: %v", oldLocalPath, mapping.localPath, mvErr))
			return fmt.Errorf("文件夹 rename 失败 %s → %s: %w", oldLocalPath, mapping.localPath, mvErr)
		}
		// e) DB 路径前缀批量更新（对齐 _sync_move_event_db_records + update_path_prefix_batch）
		if m.sqliteDB != nil && oldCloudPath != "" && oldCloudPath != cloudPath {
			if n, upErr := db.UpdatePathPrefixBatch(m.sqliteDB, account, oldCloudPath, cloudPath); upErr != nil {
				logger.S().Warnf("[Monitor] 文件夹move后DB路径前缀更新失败 %s→%s: %v",
					oldCloudPath, cloudPath, upErr)
			} else if n > 0 {
				logger.S().Infof("[Monitor] 文件夹move后DB路径前缀更新完成: %s→%s (%d条)",
					oldCloudPath, cloudPath, n)
			}
			// P0-2: 同时更新 folders 表
			if n, upErr := db.UpdateFolderPathPrefixBatch(m.sqliteDB, account, oldCloudPath, cloudPath); upErr == nil && n > 0 {
				logger.S().Infof("[Monitor] folders表路径前缀更新: %s→%s (%d条)",
					oldCloudPath, cloudPath, n)
			}
		}
		m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
			fmt.Sprintf("文件夹已原子移动: %s → %s", oldLocalPath, mapping.localPath))
		logger.S().Infof("[Monitor] 文件夹原子移动: %s → %s", oldLocalPath, mapping.localPath)
		m.notifyMove(ctx, account, cloudPath, "目录", mapping.localPath)
		if config.RemoveEmptyDirs {
			removeEmptyParents(filepath.Dir(oldLocalPath), config.PathMappings)
		}
		return nil
	}

	// —— 文件移动（对齐参考项目 _move_local_media_assets L1790-1913）——
	// 关键：mapping.localPath 已包含相对路径（如 dist\Strm\小王子），直接作为 STRM 目录
	newLocalDir := mapping.localPath
	newStrmPath := filepath.Join(newLocalDir, getStrmFileName(event.FileName))

	// recreate 模式：前面已 cleanupOldStrmAssets 删旧，此处直接走建新（对应参考项目 recreate 流程 L1006-1012）
	// recreate 模式强制创建新 STRM（删旧+生新是 recreate 的核心语义）
	if isRecreateMode {
		if !config.MoveMediaCreateNewStrm {
			// 配置虽然关闭了，但 recreate 模式必须创建 STRM
			// 记录日志但不跳过，继续执行创建
		}
		err := m.handleCreateEvent(ctx, account, event, mapping, cloudPath, lifeClient, false)
		if err == nil {
			m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
				"recreate 模式: 文件已重建")
			m.notifyMove(ctx, account, cloudPath, "文件", mapping.localPath)
		}
		return err
	}

	// local_move 模式：对齐参考项目 _move_local_media_assets 4 种组合
	// DB 无旧路径：等价于 old_strm_exists=false
	if oldLocalPath == "" {
		// !old_strm && new_strm → 仅同步内容（对齐 L1858-1865）
		if _, nerr := os.Stat(newStrmPath); nerr == nil {
			if event.PickCode != "" {
				if newContent := generateStrmContent(
					cloudPath, config.StrmPrefix, config.EnablePathEncoding,
					config.Enable302, account, event.PickCode, event.FileName,
				); newContent != "" {
					_ = writeStrmFile(newStrmPath, newContent)
				}
			}
			m.appendLog(ctx, account, "move", true, cloudPath, newStrmPath,
				"DB 无旧路径,新 STRM 已存在,同步内容")
			return nil
		}
		// !old_strm && !new_strm → 跳过（对齐 L1866-1871）
		m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
			"跳过: DB 无旧路径且新 STRM 不存在（源与目标均不存在）")
		return nil
	}

	// 计算旧 STRM 路径
	oldDir := filepath.Dir(oldLocalPath)
	oldFileName := filepath.Base(oldLocalPath)
	oldStrmPath := filepath.Join(oldDir, getStrmFileName(oldFileName))
	oldStrmExists := false
	if _, err := os.Stat(oldStrmPath); err == nil {
		oldStrmExists = true
	}
	newStrmExists := false
	if _, err := os.Stat(newStrmPath); err == nil {
		newStrmExists = true
	}
	// samefile 判断
	sameStrmPath := false
	if oldAbs, errA := filepath.Abs(oldStrmPath); errA == nil {
		if newAbs, errB := filepath.Abs(newStrmPath); errB == nil {
			sameStrmPath = oldAbs == newAbs
		}
	}
	// 公共 stem 计算（带 .iso 修正）
	oldStem := strings.TrimSuffix(oldFileName, filepath.Ext(oldFileName))
	newStem := strings.TrimSuffix(event.FileName, filepath.Ext(event.FileName))
	if strings.EqualFold(filepath.Ext(oldFileName), ".iso") {
		oldStem = oldStem + ".iso"
	}
	if strings.EqualFold(filepath.Ext(event.FileName), ".iso") {
		newStem = newStem + ".iso"
	}

	// ① old_strm_exists && !new_strm_exists: rename + 内容重同步（对齐 L1811-1826）
	if oldStrmExists && !newStrmExists {
		if err := os.MkdirAll(newLocalDir, 0o755); err != nil {
			return fmt.Errorf("创建父目录失败: %w", err)
		}
		if !sameStrmPath {
			if rmErr := os.Rename(oldStrmPath, newStrmPath); rmErr != nil {
				// 对齐参考项目: rename 失败直接返回错误，不 fallback
				m.appendLog(ctx, account, "move", false, cloudPath, oldStrmPath,
					fmt.Sprintf("STRM rename 失败 %s → %s: %v", oldStrmPath, newStrmPath, rmErr))
				return fmt.Errorf("STRM rename 失败 %s → %s: %w", oldStrmPath, newStrmPath, rmErr)
			}
			logger.S().Infof("[Monitor] local_move STRM rename 完成: %s → %s", oldStrmPath, newStrmPath)
		}
		// STRM 内容重同步（对齐 L1820-1826: _sync_strm_text_with_event）
		if event.PickCode != "" {
			if newContent := generateStrmContent(
				cloudPath, config.StrmPrefix, config.EnablePathEncoding,
				config.Enable302, account, event.PickCode, event.FileName,
			); newContent != "" {
				if werr := writeStrmFile(newStrmPath, newContent); werr != nil {
					logger.S().Warnf("[Monitor] move 后 STRM 内容重同步失败 %s: %v", newStrmPath, werr)
				}
			}
		}
		// 关联文件迁移（对齐 L1873-1906）
		if config.MoveLocalMoveRelatedFiles && !sameStrmPath {
			moveOrRenameRelatedAssets(newLocalDir, oldStem, newStem)
			if oldDir != newLocalDir {
				moveOrRenameRelatedAssets(oldDir, oldStem, newStem)
				_ = os.MkdirAll(newLocalDir, 0o755)
				if od, oerr := os.ReadDir(oldDir); oerr == nil {
					for _, e := range od {
						if e.IsDir() {
							continue
						}
						n := e.Name()
						if !strings.HasPrefix(n, oldStem+".") {
							continue
						}
						if strings.EqualFold(filepath.Ext(n), ".strm") {
							continue
						}
						src := filepath.Join(oldDir, n)
						dst := filepath.Join(newLocalDir, strings.Replace(n, oldStem, newStem, 1))
						if _, st := os.Stat(dst); st == nil {
							continue
						}
						_ = os.Rename(src, dst)
					}
				}
			}
		}
		// DB 更新
		if m.sqliteDB != nil && event.PickCode != "" {
			fid := event.FileID
			if fid == "" {
				fid = "0"
			}
			pid := event.ParentID
			if pid == "" {
				pid = "0"
			}
			_ = db.UpsertFilePathEntry(m.sqliteDB, account, db.FilePathEntry{
				FileID: fid, Path: cloudPath, FileName: event.FileName,
				ParentID: pid, PickCode: event.PickCode, UpdateTime: time.Now().Unix(),
			})
		}
		// 旧目录空则清理
		if config.RemoveEmptyDirs && oldDir != newLocalDir {
			removeEmptyParents(oldDir, config.PathMappings)
		}
		m.appendLog(ctx, account, "move", true, cloudPath, newStrmPath,
			fmt.Sprintf("STRM 已移动: %s → %s", oldStrmPath, newStrmPath))
		m.notifyMove(ctx, account, cloudPath, "文件", newStrmPath)
		return nil
	}

	// ② old_strm_exists && new_strm_exists: 合并场景（对齐 L1827-1857）
	if oldStrmExists && newStrmExists {
		if sameStrmPath {
			// 同文件 → 仅同步内容（对齐 L1828-1835）
			if event.PickCode != "" {
				if newContent := generateStrmContent(
					cloudPath, config.StrmPrefix, config.EnablePathEncoding,
					config.Enable302, account, event.PickCode, event.FileName,
				); newContent != "" {
					_ = writeStrmFile(newStrmPath, newContent)
				}
			}
			m.appendLog(ctx, account, "move", true, cloudPath, newStrmPath,
				"STRM 路径未变,已同步内容")
			return nil
		}
		// 非同文件 → 同步内容 + 删旧 STRM（对齐 L1837-1857）
		if event.PickCode != "" {
			if newContent := generateStrmContent(
				cloudPath, config.StrmPrefix, config.EnablePathEncoding,
				config.Enable302, account, event.PickCode, event.FileName,
			); newContent != "" {
				if werr := writeStrmFile(newStrmPath, newContent); werr != nil {
					m.appendLog(ctx, account, "move", false, cloudPath, newStrmPath,
						fmt.Sprintf("合并场景同步新 STRM 内容失败: %v", werr))
					return werr
				}
			}
		}
		if err := strmutil.SafeDeleteStrmFile(oldStrmPath, config.EnableHardDelete); err != nil {
			m.appendLog(ctx, account, "move", false, cloudPath, oldStrmPath,
				fmt.Sprintf("合并场景删除旧 STRM 失败: %v", err))
			return err
		}
		// 关联文件迁移
		if config.MoveLocalMoveRelatedFiles {
			moveOrRenameRelatedAssets(newLocalDir, oldStem, newStem)
		}
		// DB 更新
		if m.sqliteDB != nil && event.PickCode != "" {
			fid := event.FileID
			if fid == "" {
				fid = "0"
			}
			pid := event.ParentID
			if pid == "" {
				pid = "0"
			}
			_ = db.UpsertFilePathEntry(m.sqliteDB, account, db.FilePathEntry{
				FileID: fid, Path: cloudPath, FileName: event.FileName,
				ParentID: pid, PickCode: event.PickCode, UpdateTime: time.Now().Unix(),
			})
		}
		m.appendLog(ctx, account, "move", true, cloudPath, newStrmPath,
			fmt.Sprintf("合并场景: 删旧 STRM + 同步新 STRM: %s → %s", oldStrmPath, newStrmPath))
		m.notifyMove(ctx, account, cloudPath, "文件", newStrmPath)
		return nil
	}

	// ③ !old_strm_exists && new_strm_exists: 仅同步内容（对齐 L1858-1865）
	if !oldStrmExists && newStrmExists {
		if event.PickCode != "" {
			if newContent := generateStrmContent(
				cloudPath, config.StrmPrefix, config.EnablePathEncoding,
				config.Enable302, account, event.PickCode, event.FileName,
			); newContent != "" {
				_ = writeStrmFile(newStrmPath, newContent)
			}
		}
		m.appendLog(ctx, account, "move", true, cloudPath, newStrmPath,
			"本地无旧 STRM,新 STRM 已存在,同步内容")
		return nil
	}

	// ④ !old_strm_exists && !new_strm_exists: 跳过（对齐 L1866-1871: 源与目标均不存在）
	m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
		"跳过: 源与目标 STRM 均不存在")
	return nil
}

// ==================== handleRenameEvent：重命名 STRM ====================

// handleRenameEvent 重命名 STRM 文件
// 对齐 MoviePilot MonitorLife.rename：
//   - 文件夹 rename：shutil_move（保留内部所有文件结构和元数据），若失败 fallback recreate 递归
//   - 文件 rename：按 DB 反查旧路径 → old_strm → new_strm rename + 同名 nfo/jpg/srt rename；找不到 → fallback 重新生成
func (m *Monitor) handleRenameEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
	lifeClient *client115.LifeClient,
) error {
	config := m.settingsFn()
	oldCloudPath := m.resolveOldCloudPathByFileID(account, event.FileID)
	oldLocalPath, _ := resolveOldLocalPath(oldCloudPath, config.PathMappings, account)

	// MoveMediaMode 决策（与 handleMoveEvent 对齐）：
	//   - "recreate"：先清理旧 STRM + DB，再在新名称下重新生成（STRM 内容会重同步新 file_name）
	//   - "local_move"/""：原子 rename 优先，保留刮削元数据，失败 fallback recreate
	// P0-7: MoveMediaKeepOldStrm=true 时保留旧 STRM
	isRecreateMode := strings.EqualFold(config.MoveMediaMode, "recreate")
	if isRecreateMode && !config.MoveMediaKeepOldStrm {
		if oldLocalPath != "" {
			m.cleanupOldStrmAssets(ctx, account, event, oldCloudPath, oldLocalPath, config)
		} else if event.FileCategory != 0 {
			// DB 无旧路径记录 → 通过文件名查找本地旧 STRM 并删除
			// 确保"重命名=删旧+生新"行为一致，不依赖 DB 是否有旧记录
			var oldStrmPath string
			if oldCloudPath != "" {
				// 从旧云路径提取旧文件名（e.g. "/电影/小王子 (2015).mkv" → "小王子 (2015).mkv"）
				oldFileName := filepath.Base(oldCloudPath)
				if oldFileName != "" && oldFileName != "." && oldFileName != "/" {
					oldStrmPath = m.findLocalStrmByFileName(account, oldFileName, event.FileCategory, config.PathMappings)
				}
			}
			// 备用：用事件新文件名查找（可能找到新 STRM，但如果它不存在则说明还没创建，可以安全删除同名旧 STRM）
			if oldStrmPath == "" {
				oldStrmPath = m.findLocalStrmByFileName(account, event.FileName, event.FileCategory, config.PathMappings)
			}
			if oldStrmPath != "" {
				if err := strmutil.SafeDeleteStrmFile(oldStrmPath, config.EnableHardDelete); err != nil {
					logger.S().Warnf("[Monitor] recreate DB无旧路径 清理旧STRM失败 %s: %v", oldStrmPath, err)
				} else {
					deletedRelated := deleteRelatedFiles(oldStrmPath)
					logger.S().Infof("[Monitor] recreate DB无旧路径 旧STRM已清理 %s (关联 %d)", oldStrmPath, deletedRelated)
					m.appendLog(ctx, account, "rename", true, oldCloudPath, oldStrmPath,
						fmt.Sprintf("recreate DB无旧路径：旧STRM已清理 %s (关联 %d)", oldStrmPath, deletedRelated))
				}
				if config.RemoveEmptyDirs {
					removeEmptyParents(filepath.Dir(oldStrmPath), config.PathMappings)
				}
				if m.sqliteDB != nil {
					if derr := db.RemoveFilePathEntry(m.sqliteDB, account, event.FileID); derr != nil {
						logger.S().Warnf("[Monitor] recreate DB无旧路径 清理DB失败 %s: %v", event.FileID, derr)
					}
				}
			}
		}
	}

	// —— 文件夹重命名（type=20，对齐参考项目 rename() L1177-1217）——
	// 参考项目流程：
	//   ① old_path 不存在 → 跳过（L1179-1183）
	//   ② 父目录不一致 → 跳过（L1185-1189）
	//   ③ old_path 不是 dir → 跳过（L1191-1195）
	//   ④ new_path 已存在 → 跳过（L1197-1202）
	//   ⑤ shutil_move 重命名 → 失败直接 return，不 fallback（L1204-1217）
	// recreate 模式时前面已 cleanupOldStrmAssets 删旧目录，此处走 mkdir + 递归 recreate（对应参考项目 recreate 模式重建步骤）
	if event.FileCategory == 0 {
		if oldLocalPath == "" {
			// ① DB无旧路径 → 跳过（对齐 L1179-1183）
			logger.S().Warnf("[Monitor] 文件夹rename: DB无旧路径记录，跳过 fid=%s name=%s",
				event.FileID, event.FileName)
			m.appendLog(ctx, account, "rename", false, cloudPath, mapping.localPath,
				"跳过: DB无旧文件夹路径记录")
			return nil
		}

		// recreate 模式：前面已 cleanupOldStrmAssets 删旧目录，此处重建
		if isRecreateMode {
			if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
				return fmt.Errorf("mkdir 失败: %w", err)
			}
			strmCreated := 0
			// recreate 模式强制创建新 STRM（删旧+生新是 recreate 的核心语义）
			if lifeClient != nil {
				strmCreated, _ = m.handleCreateFolderRecursive(
					ctx, account, mapping, lifeClient, event.FileID, cloudPath, 0,
				)
			}
			m.appendLog(ctx, account, "rename", true, cloudPath, mapping.localPath,
				fmt.Sprintf("recreate 模式：文件夹已重建，生成 STRM %d 个", strmCreated))
			m.notifyRename(ctx, account, cloudPath, "目录", mapping.localPath)
			return nil
		}

		// local_move 模式：对齐 L1185-1217 严格条件 + 失败直接返回错误
		// ② 父目录不一致 → 跳过
		if filepath.Dir(oldLocalPath) != filepath.Dir(mapping.localPath) {
			m.appendLog(ctx, account, "rename", false, cloudPath, mapping.localPath,
				fmt.Sprintf("跳过: 旧文件夹父目录与新文件夹不一致 old=%s new=%s",
					filepath.Dir(oldLocalPath), filepath.Dir(mapping.localPath)))
			return nil
		}
		// ③ old_path 不存在 → 跳过
		if _, stErr := os.Stat(oldLocalPath); stErr != nil {
			m.appendLog(ctx, account, "rename", false, cloudPath, mapping.localPath,
				fmt.Sprintf("跳过: 本地旧文件夹不存在 %s", oldLocalPath))
			return nil
		}
		// ④ new_path 已存在 → 跳过
		if _, dstErr := os.Stat(mapping.localPath); dstErr == nil {
			m.appendLog(ctx, account, "rename", false, cloudPath, mapping.localPath,
				fmt.Sprintf("跳过: 重命名目标已存在 %s", mapping.localPath))
			return nil
		}
		// ⑤ 执行 rename（对齐 L1204-1210: shutil_move）
		if rmErr := os.Rename(oldLocalPath, mapping.localPath); rmErr != nil {
			// 对齐 L1211-1217: rename 失败直接返回错误，不 fallback
			m.appendLog(ctx, account, "rename", false, cloudPath, oldLocalPath,
				fmt.Sprintf("文件夹 rename 失败 %s → %s: %v", oldLocalPath, mapping.localPath, rmErr))
			return fmt.Errorf("文件夹 rename 失败 %s → %s: %w", oldLocalPath, mapping.localPath, rmErr)
		}
		// rename 成功 → DB 路径前缀批量更新（对齐 _sync_rename_path_records + update_path_prefix_batch）
		if m.sqliteDB != nil && oldCloudPath != "" && oldCloudPath != cloudPath {
			if n, upErr := db.UpdatePathPrefixBatch(m.sqliteDB, account, oldCloudPath, cloudPath); upErr != nil {
				logger.S().Warnf("[Monitor] 文件夹rename后DB路径前缀更新失败 %s→%s: %v",
					oldCloudPath, cloudPath, upErr)
			} else if n > 0 {
				logger.S().Infof("[Monitor] 文件夹rename后DB路径前缀更新完成: %s→%s (%d条)",
					oldCloudPath, cloudPath, n)
			}
			// P0-2: 同时更新 folders 表
			if n, upErr := db.UpdateFolderPathPrefixBatch(m.sqliteDB, account, oldCloudPath, cloudPath); upErr == nil && n > 0 {
				logger.S().Infof("[Monitor] folders表路径前缀更新(rename): %s→%s (%d条)",
					oldCloudPath, cloudPath, n)
			}
		}
		m.appendLog(ctx, account, "rename", true, cloudPath, mapping.localPath,
			fmt.Sprintf("文件夹已原子重命名: %s → %s", oldLocalPath, mapping.localPath))
		logger.S().Infof("[Monitor] 文件夹原子重命名: %s → %s", oldLocalPath, mapping.localPath)
		m.notifyRename(ctx, account, cloudPath, "目录", mapping.localPath)
		return nil
	}

	// —— 文件重命名（对齐参考项目 helper/life/client.py L1218-1395）——
	// 完整对齐 6 个分支：
	//   ① 非媒体 + 关闭关联 → return
	//   ② 非媒体 + 旧关联存在 → 关联文件 rename
	//   ③ 非媒体 + 旧关联不存在 + 新 STRM 不存在 → create
	//   ④ 媒体 + DB 无旧路径 → new 不存在才 create
	//   ⑤ 媒体 + 本地无旧 STRM → new 不存在才 create
	//   ⑥ 媒体 + new 已存在且非同文件 → 合并 (同步内容 + 删旧 + 关联 rename)
	//   ⑦ 媒体 + 正常 rename → shutil_move + 内容重同步 + 关联 rename（失败直接返回，不 fallback）
	newLocalDir := mapping.localPath
	newStrmPath := filepath.Join(newLocalDir, getStrmFileName(event.FileName))

	// ===== 非媒体扩展名分支（对齐 L1220-1241）=====
	if !isMediaFile(event.FileName, model.DefaultStrmExtensions) {
		if !config.RenameAutoRelatedFiles {
			m.appendLog(ctx, account, "rename", true, cloudPath, mapping.localPath,
				"跳过: 非媒体扩展名且未开启 RenameAutoRelatedFiles")
			return nil
		}
		if oldLocalPath != "" {
			if _, err := os.Stat(oldLocalPath); err == nil {
				// 旧关联文件存在 → 关联文件 rename
				oldDir := filepath.Dir(oldLocalPath)
				oldFileName := filepath.Base(oldLocalPath)
				oldStem := strings.TrimSuffix(oldFileName, filepath.Ext(oldFileName))
				newStem := strings.TrimSuffix(event.FileName, filepath.Ext(event.FileName))
				if strings.EqualFold(filepath.Ext(oldFileName), ".iso") {
					oldStem = oldStem + ".iso"
				}
				if strings.EqualFold(filepath.Ext(event.FileName), ".iso") {
					newStem = newStem + ".iso"
				}
				_ = os.MkdirAll(newLocalDir, 0o755)
				moveOrRenameRelatedAssets(oldDir, oldStem, newStem)
				moveOrRenameRelatedAssets(newLocalDir, oldStem, newStem)
				m.appendLog(ctx, account, "rename", true, cloudPath, newStrmPath,
					fmt.Sprintf("非媒体关联文件已重命名: %s → %s", oldLocalPath, newStrmPath))
				return nil
			}
			// 旧关联不存在 + 新 STRM 不存在 → create
			if _, nerr := os.Stat(newStrmPath); os.IsNotExist(nerr) && (config.MoveMediaCreateNewStrm || isRecreateMode) {
				return m.handleCreateEvent(ctx, account, event, mapping, cloudPath, lifeClient, false)
			}
		} else {
			// 无旧路径 + 新 STRM 不存在 → create
			if _, nerr := os.Stat(newStrmPath); os.IsNotExist(nerr) && (config.MoveMediaCreateNewStrm || isRecreateMode) {
				return m.handleCreateEvent(ctx, account, event, mapping, cloudPath, lifeClient, false)
			}
		}
		m.appendLog(ctx, account, "rename", true, cloudPath, mapping.localPath,
			"非媒体文件 rename 已处理")
		return nil
	}

	// ===== 媒体扩展名分支 =====
	newStrmExists := false
	if _, err := os.Stat(newStrmPath); err == nil {
		newStrmExists = true
	}

	// ④ DB 无旧路径（对齐 L1246-1262）
	if oldLocalPath == "" {
		if !newStrmExists && (config.MoveMediaCreateNewStrm || isRecreateMode) {
			return m.handleCreateEvent(ctx, account, event, mapping, cloudPath, lifeClient, false)
		}
		m.appendLog(ctx, account, "rename", true, cloudPath, newStrmPath,
			"跳过: DB 无旧路径且新 STRM 已存在")
		return nil
	}

	// 计算旧 STRM 路径
	oldDir := filepath.Dir(oldLocalPath)
	oldFileName := filepath.Base(oldLocalPath)
	oldStrmPath := filepath.Join(oldDir, getStrmFileName(oldFileName))
	oldStrmExists := false
	if _, err := os.Stat(oldStrmPath); err == nil {
		oldStrmExists = true
	}

	// ⑤ 本地无旧 STRM（对齐 L1267-1283）
	if !oldStrmExists {
		if !newStrmExists && (config.MoveMediaCreateNewStrm || isRecreateMode) {
			return m.handleCreateEvent(ctx, account, event, mapping, cloudPath, lifeClient, false)
		}
		m.appendLog(ctx, account, "rename", true, cloudPath, newStrmPath,
			"跳过: 本地无旧 STRM 且新 STRM 已存在")
		return nil
	}

	// 计算 same_strm_path（绝对路径比较）
	sameStrmPath := false
	if oldAbs, errA := filepath.Abs(oldStrmPath); errA == nil {
		if newAbs, errB := filepath.Abs(newStrmPath); errB == nil {
			sameStrmPath = oldAbs == newAbs
		}
	}

	// 公共：stem 计算（带 .iso 修正）
	oldStem := strings.TrimSuffix(oldFileName, filepath.Ext(oldFileName))
	newStem := strings.TrimSuffix(event.FileName, filepath.Ext(event.FileName))
	if strings.EqualFold(filepath.Ext(oldFileName), ".iso") {
		oldStem = oldStem + ".iso"
	}
	if strings.EqualFold(filepath.Ext(event.FileName), ".iso") {
		newStem = newStem + ".iso"
	}

	// ⑥ 合并场景：新 STRM 已存在且非同文件（对齐 L1285-1337）
	if newStrmExists && !sameStrmPath {
		// 1. 同步新 STRM 内容
		if event.PickCode != "" {
			if newContent := generateStrmContent(
				cloudPath, config.StrmPrefix, config.EnablePathEncoding,
				config.Enable302, account, event.PickCode, event.FileName,
			); newContent != "" {
				if werr := writeStrmFile(newStrmPath, newContent); werr != nil {
					m.appendLog(ctx, account, "rename", false, cloudPath, newStrmPath,
						fmt.Sprintf("合并场景同步新 STRM 内容失败: %v", werr))
					return werr
				}
			}
		}
		// 2. 删旧 STRM（对齐 L1305: old_strm_path.unlink）
		if err := strmutil.SafeDeleteStrmFile(oldStrmPath, config.EnableHardDelete); err != nil {
			m.appendLog(ctx, account, "rename", false, cloudPath, oldStrmPath,
				fmt.Sprintf("合并场景删除旧 STRM 失败: %v", err))
			return err
		}
		// 3. 关联文件 rename
		if config.RenameAutoRelatedFiles {
			moveOrRenameRelatedAssets(newLocalDir, oldStem, newStem)
		}
		// 4. DB 更新
		if m.sqliteDB != nil && event.PickCode != "" {
			fid := event.FileID
			if fid == "" {
				fid = "0"
			}
			pid := event.ParentID
			if pid == "" {
				pid = "0"
			}
			_ = db.UpsertFilePathEntry(m.sqliteDB, account, db.FilePathEntry{
				FileID: fid, Path: cloudPath, FileName: event.FileName,
				ParentID: pid, PickCode: event.PickCode, UpdateTime: time.Now().Unix(),
			})
		}
		m.appendLog(ctx, account, "rename", true, cloudPath, newStrmPath,
			fmt.Sprintf("合并场景: 删旧 STRM + 同步新 STRM: %s → %s", oldStrmPath, newStrmPath))
		m.notifyRename(ctx, account, cloudPath, "文件", newStrmPath)
		return nil
	}

	// ⑦ 正常 rename 场景（对齐 L1339-1395）
	// 1. mkdir 父目录
	if err := os.MkdirAll(newLocalDir, 0o755); err != nil {
		return fmt.Errorf("创建父目录失败: %w", err)
	}
	// 2. STRM rename（对齐 L1349: shutil_move(old_strm, new_strm)）
	if !sameStrmPath {
		if rmErr := os.Rename(oldStrmPath, newStrmPath); rmErr != nil {
			// 对齐 L1389-1395: rename 失败直接返回错误，不 fallback
			m.appendLog(ctx, account, "rename", false, cloudPath, oldStrmPath,
				fmt.Sprintf("STRM rename 失败 %s → %s: %v", oldStrmPath, newStrmPath, rmErr))
			return fmt.Errorf("STRM rename 失败 %s → %s: %w", oldStrmPath, newStrmPath, rmErr)
		}
		logger.S().Infof("[Monitor] 本地 STRM 重命名完成: %s → %s", oldStrmPath, newStrmPath)
	}
	// 3. STRM 内容重同步（对齐 L1360: _sync_strm_text_with_event）
	if event.PickCode != "" {
		if newContent := generateStrmContent(
			cloudPath, config.StrmPrefix, config.EnablePathEncoding,
			config.Enable302, account, event.PickCode, event.FileName,
		); newContent != "" {
			if werr := writeStrmFile(newStrmPath, newContent); werr != nil {
				logger.S().Warnf("[Monitor] rename 后 STRM 内容重同步失败 %s: %v", newStrmPath, werr)
			}
		}
	}
	// 4. 关联文件 rename（对齐 L1369-1388）
	var movedRel int
	if config.RenameAutoRelatedFiles && !sameStrmPath {
		movedRel = moveOrRenameRelatedAssets(newLocalDir, oldStem, newStem)
	}
	// 5. DB 更新
	if m.sqliteDB != nil && event.PickCode != "" {
		fid := event.FileID
		if fid == "" {
			fid = "0"
		}
		pid := event.ParentID
		if pid == "" {
			pid = "0"
		}
		_ = db.UpsertFilePathEntry(m.sqliteDB, account, db.FilePathEntry{
			FileID: fid, Path: cloudPath, FileName: event.FileName,
			ParentID: pid, PickCode: event.PickCode, UpdateTime: time.Now().Unix(),
		})
	}
	// 6. 旧目录空则清理
	if config.RemoveEmptyDirs && oldDir != newLocalDir {
		removeEmptyParents(oldDir, config.PathMappings)
	}
	m.appendLog(ctx, account, "rename", true, cloudPath, newStrmPath,
		fmt.Sprintf("STRM + 关联资源(%d) 已重命名: %s → %s", movedRel, oldStrmPath, newStrmPath))
	m.notifyRename(ctx, account, cloudPath, "文件", newStrmPath)
	return nil
}

// ==================== 辅助函数 ====================

// matchPathMapping 为云端路径匹配最佳路径映射
// 对齐 TS matchPathMapping
func matchPathMapping(cloudPath string, mappings []model.MonitorPathMapping, account string) *pathMapping {
	// 过滤账号并按 cloudPath 长度降序排序（最长匹配优先）
	var candidates []model.MonitorPathMapping
	for _, mp := range mappings {
		if mp.Account != "" && mp.Account != account {
			continue
		}
		candidates = append(candidates, mp)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return len(strings.TrimRight(candidates[i].CloudPath, "/")) >
			len(strings.TrimRight(candidates[j].CloudPath, "/"))
	})
	for _, mp := range candidates {
		key := strings.TrimRight(mp.CloudPath, "/")
		normalizedKey := key + "/"

		// 精确匹配
		if cloudPath == mp.CloudPath || cloudPath == key {
			return &pathMapping{
				cloudPath:    mp.CloudPath,
				localPath:    mp.LocalPath,
				relativePath: "",
			}
		}
		// 前缀匹配
		if strings.HasPrefix(cloudPath, normalizedKey) {
			relativePath := cloudPath[len(normalizedKey):]
			localPath := filepath.Join(mp.LocalPath, sanitizePathParts(relativePath))
			return &pathMapping{
				cloudPath:    mp.CloudPath,
				localPath:    localPath,
				relativePath: relativePath,
			}
		}
	}
	return nil
}

// cleanupOldStrmForOtherToMedia 在 OTHER_TO_MEDIA 象限清理旧 STRM
// 场景：文件从非媒体目录移动/重命名到媒体目录
// 旧 STRM 可能存在于以下位置：
//   1. 旧云路径映射的本地目录（如 oldCloudPath="小王子" → 本地旧目录）
//   2. 通过文件名在映射本地目录中查找
func (m *Monitor) cleanupOldStrmForOtherToMedia(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	oldCloudPath string,
	config model.LifeMonitorSettings,
) {
	if config.MoveMediaKeepOldStrm {
		return // 配置了保留旧 STRM
	}

	// 方式1：尝试通过 oldCloudPath 反查旧本地路径
	oldLocalPath, _ := resolveOldLocalPath(oldCloudPath, config.PathMappings, account)
	if oldLocalPath != "" {
		m.deleteOldStrmAssetsAtPath(ctx, account, event, oldCloudPath, oldLocalPath, config)
		return
	}

	// 方式2：通过文件名在映射目录中查找旧 STRM
	// 先尝试从 oldCloudPath 提取旧文件名
	var searchName string
	if oldCloudPath != "" {
		searchName = filepath.Base(oldCloudPath)
	}
	if searchName == "" || searchName == "." || searchName == "/" {
		searchName = event.FileName
	}
	if searchName == "" {
		return
	}

	foundPath := m.findLocalStrmByFileName(account, searchName, event.FileCategory, config.PathMappings)
	if foundPath == "" && searchName != event.FileName {
		// 备用：用事件当前文件名查找
		foundPath = m.findLocalStrmByFileName(account, event.FileName, event.FileCategory, config.PathMappings)
	}

	if foundPath != "" {
		logger.S().Infof("[Monitor] OTHER_TO_MEDIA 清理旧STRM: 找到路径=%s (searchName=%s)", foundPath, searchName)
		if err := strmutil.SafeDeleteStrmFile(foundPath, config.EnableHardDelete); err != nil {
			logger.S().Warnf("[Monitor] OTHER_TO_MEDIA 清理旧STRM失败 %s: %v", foundPath, err)
		} else {
			deletedRelated := deleteRelatedFiles(foundPath)
			logger.S().Infof("[Monitor] OTHER_TO_MEDIA 旧STRM已清理 %s (关联 %d)", foundPath, deletedRelated)
			if config.RemoveEmptyDirs {
				removeEmptyParents(filepath.Dir(foundPath), config.PathMappings)
			}
			if m.sqliteDB != nil {
				if derr := db.RemoveFilePathEntry(m.sqliteDB, account, event.FileID); derr != nil {
					logger.S().Warnf("[Monitor] OTHER_TO_MEDIA 清理DB失败 %s: %v", event.FileID, derr)
				}
			}
		}
	}
}

// deleteOldStrmAssetsAtPath 在指定路径删除旧 STRM 及其关联资源
func (m *Monitor) deleteOldStrmAssetsAtPath(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	oldCloudPath string,
	oldLocalPath string,
	config model.LifeMonitorSettings,
) {
	if event.FileCategory == 0 {
		// 文件夹：删除目录下所有 .strm 文件及关联资源
		oldDir := oldLocalPath
		if _, err := os.Stat(oldDir); os.IsNotExist(err) {
			return
		}
		strmFiles, err := filepath.Glob(filepath.Join(oldDir, "*.strm"))
		if err != nil {
			return
		}
		for _, sp := range strmFiles {
			if err := strmutil.SafeDeleteStrmFile(sp, config.EnableHardDelete); err == nil {
				deleteRelatedFiles(sp)
			}
		}
		if config.RemoveEmptyDirs {
			removeEmptyParents(oldDir, config.PathMappings)
		}
	} else {
		// 单文件：删除 .strm 及关联资源
		oldDir := filepath.Dir(oldLocalPath)
		oldFileName := filepath.Base(oldLocalPath)
		oldStrmPath := filepath.Join(oldDir, getStrmFileName(oldFileName))
		if _, err := os.Stat(oldStrmPath); err == nil {
			if err := strmutil.SafeDeleteStrmFile(oldStrmPath, config.EnableHardDelete); err == nil {
				deleteRelatedFiles(oldStrmPath)
			}
		}
		if config.RemoveEmptyDirs {
			removeEmptyParents(oldDir, config.PathMappings)
		}
	}
	// 清理 DB
	if m.sqliteDB != nil {
		if derr := db.RemoveFilePathEntry(m.sqliteDB, account, event.FileID); derr != nil {
			logger.S().Warnf("[Monitor] 清理DB失败 %s: %v", event.FileID, derr)
		}
	}
	logger.S().Infof("[Monitor] OTHER_TO_MEDIA 旧STRM已清理 path=%s", oldLocalPath)
}

// cleanupOldStrmByFileID 清理同 file_id 的旧 STRM 文件
// 对齐参考项目 create() L1590-1626：DB 中存在同 file_id 但不同路径的旧记录时，删除旧 STRM
// 搜索策略：
//   1. 通过旧 cloudPath 在当前映射中查找旧 localPath
//   2. 如果找不到，在 MediaMountPath 扩展目录中按文件名搜索
//   3. 如果还找不到，在当前映射目录中按文件名搜索
func (m *Monitor) cleanupOldStrmByFileID(
	account string,
	fileID string,
	oldCloudPath string,
	oldFileName string,
	newFileName string,
	config model.LifeMonitorSettings,
) {
	logger.S().Infof("[Monitor] cleanupOldStrmByFileID: fileID=%s oldPath=%s oldName=%s newName=%s",
		fileID, oldCloudPath, oldFileName, newFileName)

	// 方式1：通过旧 cloudPath 在当前映射中查找旧 localPath
	oldLocalPath, _ := resolveOldLocalPath(oldCloudPath, config.PathMappings, account)
	if oldLocalPath != "" {
		logger.S().Infof("[Monitor] cleanupOldStrm: 方式1命中 oldLocalPath=%s", oldLocalPath)
		m.deleteOldStrmAtLocalPath(account, fileID, oldLocalPath, config)
		return
	}

	// 方式2：在 MediaMountPath 扩展目录中按文件名搜索
	for _, mountDir := range config.MediaMountPath {
		if mountDir == "" {
			continue
		}
		// 搜索目录下的子目录
		if info, err := os.Stat(mountDir); err == nil && info.IsDir() {
			entries, err := os.ReadDir(mountDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				// 目录名匹配旧文件名
				if entry.Name() == oldFileName || entry.Name() == newFileName {
					candidate := filepath.Join(mountDir, entry.Name())
					// 检查目录下是否有 .strm 文件
					strmFiles, _ := filepath.Glob(filepath.Join(candidate, "*.strm"))
					if len(strmFiles) > 0 {
						logger.S().Infof("[Monitor] cleanupOldStrm: 方式2命中 MediaMountPath=%s candidate=%s", mountDir, candidate)
						m.deleteOldStrmAtLocalPath(account, fileID, candidate, config)
						return
					}
				}
			}
		}
	}

	// 方式3：在当前映射目录 + 扩展目录中按文件名搜索
	foundPath := m.findLocalStrmByFileName(account, oldFileName, 1, config.PathMappings, config.MediaMountPath...)
	if foundPath == "" && oldFileName != newFileName {
		foundPath = m.findLocalStrmByFileName(account, newFileName, 1, config.PathMappings, config.MediaMountPath...)
	}
	if foundPath != "" {
		logger.S().Infof("[Monitor] cleanupOldStrm: 方式3命中 foundPath=%s", foundPath)
		// foundPath 是 .strm 文件路径，取其父目录
		m.deleteOldStrmAtLocalPath(account, fileID, filepath.Dir(foundPath), config)
		return
	}

	logger.S().Infof("[Monitor] cleanupOldStrm: 未找到旧STRM fileID=%s", fileID)
}

// deleteOldStrmAtLocalPath 在指定本地路径删除旧 STRM 及其关联资源
func (m *Monitor) deleteOldStrmAtLocalPath(
	account string,
	fileID string,
	oldLocalPath string,
	config model.LifeMonitorSettings,
) {
	// 检查路径是否存在
	info, err := os.Stat(oldLocalPath)
	if err != nil {
		logger.S().Infof("[Monitor] deleteOldStrmAtLocalPath: 路径不存在 %s: %v", oldLocalPath, err)
		return
	}

	if info.IsDir() {
		// 文件夹：删除目录下所有 .strm 文件
		strmFiles, err := filepath.Glob(filepath.Join(oldLocalPath, "*.strm"))
		if err != nil {
			return
		}
		for _, sp := range strmFiles {
			if delErr := strmutil.SafeDeleteStrmFile(sp, config.EnableHardDelete); delErr == nil {
				deletedRelated := deleteRelatedFiles(sp)
				logger.S().Infof("[Monitor] deleteOldStrmAtLocalPath: 已删除 %s (关联 %d)", sp, deletedRelated)
			} else {
				logger.S().Warnf("[Monitor] deleteOldStrmAtLocalPath: 删除失败 %s: %v", sp, delErr)
			}
		}
		// 清理空目录
		if config.RemoveEmptyDirs {
			removeEmptyParents(oldLocalPath, config.PathMappings)
		}
	} else {
		// 单文件：直接删除
		if delErr := strmutil.SafeDeleteStrmFile(oldLocalPath, config.EnableHardDelete); delErr == nil {
			deleteRelatedFiles(oldLocalPath)
		}
	}

	// 清理 DB 记录
	if m.sqliteDB != nil {
		if derr := db.RemoveFilePathEntry(m.sqliteDB, account, fileID); derr != nil {
			logger.S().Warnf("[Monitor] deleteOldStrmAtLocalPath: 清理DB失败 fileID=%s: %v", fileID, derr)
		}
	}

	logger.S().Infof("[Monitor] deleteOldStrmAtLocalPath: 完成 %s", oldLocalPath)
}

// getStrmFileName 将文件名转换为 .strm 扩展名
// 对齐 TS// getStrmFileName 生成 .strm 文件名
// 对齐 MoviePilot StrmGenerater.get_strm_filename:
//   - 普通文件：movie.mkv → movie.strm
//   - ISO 镜像保留双扩展名：game.iso → game.iso.strm
func getStrmFileName(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return fileName + ".strm"
	}
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if ext == ".iso" {
		return stem + ".iso.strm"
	}
	return stem + ".strm"
}

// isValidPickcode 校验 115 pickcode 合法性
// 对齐 MoviePilot：17 位字母数字混合字符串
func isValidPickcode(pc string) bool {
	if len(pc) != 17 {
		return false
	}
	for i := 0; i < len(pc); i++ {
		c := pc[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// shouldGenerateStrm 统一判断文件是否应该生成 STRM（与 task 包同名函数逻辑保持一致）
// 对齐 MoviePilot StrmGenerater.should_generate_strm：
//  1. 黑名单关键词检查（不区分大小写子串匹配，若任一条目匹配则拒绝）
//  2. 最小文件大小检查（minFileSize>0 且 fileSize>0 且 fileSize<minFileSize 则拒绝）
//
// P2-2：matcher 非空时优先用 AC 自动机；matcher 为空但黑名单条目 ≥ concurrency.ACThreshold
// 时，会在本函数内临时构建 AC（建议外层批量调用场景传入预构建 matcher 减少重复开销）。
// 返回：(拒绝原因, 是否通过)；通过时拒绝原因为空
func shouldGenerateStrm(fileName string, fileSize, minFileSize int64, blacklist []string, matcher ...*concurrency.StringMatcher) (string, bool) {
	// 1) 黑名单检查
	if len(blacklist) > 0 {
		// 选 matcher：显式传入 > 阈值临时构建 > contains 线性扫
		var m *concurrency.StringMatcher
		if len(matcher) > 0 && matcher[0] != nil {
			m = matcher[0]
		} else if concurrency.ShouldUseAC(blacklist) {
			m = concurrency.NewStringMatcher(blacklist)
		}
		if m != nil {
			if kw, ok := m.MatchAny(fileName); ok {
				return fmt.Sprintf("匹配黑名单关键词 %q", kw), false
			}
		} else {
			lowerName := strings.ToLower(fileName)
			for _, kw := range blacklist {
				if kw == "" {
					continue
				}
				if strings.Contains(lowerName, strings.ToLower(kw)) {
					return fmt.Sprintf("匹配黑名单关键词 %q", kw), false
				}
			}
		}
	}
	// 2) 最小文件大小限制
	if minFileSize > 0 && fileSize > 0 && fileSize < minFileSize {
		return "小于最小文件大小", false
	}
	return "", true
}

// getRelPathStemStrmSuffix 基于相对路径（含扩展名的完整文件路径）计算 strm 输出路径
// 等价于：替换文件名为 getStrmFileName(name)
func replaceRelPathExtToStrm(relPath string) string {
	dir, name := filepath.Split(relPath)
	return filepath.Join(dir, getStrmFileName(name))
}

// isMediaFile 检查文件名是否为媒体文件扩展名
// extensions 可带或不带前导 "."
func isMediaFile(fileName string, extensions []string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return false
	}
	ext = strings.TrimPrefix(ext, ".")
	for _, e := range extensions {
		if strings.ToLower(strings.TrimPrefix(e, ".")) == ext {
			return true
		}
	}
	return false
}

// sanitizePathParts 清理路径中的非法字符（Windows 平台）
// 对齐 TS sanitizePathParts
func sanitizePathParts(relativePath string) string {
	if relativePath == "" {
		return ""
	}
	// 统一使用 / 作为分隔符进行分割（云端路径用 /）
	parts := strings.Split(relativePath, "/")
	for i, part := range parts {
		// 冒号替换为全角冒号
		part = strings.ReplaceAll(part, ":", "：")
		// Windows 非法字符替换为下划线
		for _, c := range []string{"<", ">", "\"", "|", "?", "*"} {
			part = strings.ReplaceAll(part, c, "_")
		}
		parts[i] = part
	}
	return strings.Join(parts, string(filepath.Separator))
}

// generateStrmContent 生成 .strm 文件内容（URL 到 115 文件）
// 302 模式：{prefix}/api/fs/get?account=xxx&pickcode=xxx&file_name=xxx
// 非 302：{prefix}/api/strm?account=xxx&pickcode=xxx&file_name=xxx
// P1-4：customTemplate 非空时优先用 model.RenderStrmUrlTemplate。
// 对齐 TS generateStrmContent
func generateStrmContent(cloudPath, strmPrefix string, enablePathEncoding, enable302 bool, account, pickcode, fileName string, customTemplate ...string) string {
	// —— P1-4 高级模板优先 ——
	if len(customTemplate) > 0 && customTemplate[0] != "" {
		ext := strings.ToLower(filepath.Ext(fileName))
		stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		if strings.EqualFold(ext, ".iso") {
			stem = stem + ".iso"
		}
		if rendered := model.RenderStrmUrlTemplate(customTemplate[0], strmPrefix, account, pickcode, fileName, ext, stem); rendered != "" {
			return rendered
		}
	}
	prefix := strings.TrimRight(strmPrefix, "/")
	// 兜底：prefix 为空时使用默认值
	if prefix == "" {
		prefix = "http://127.0.0.1:8090"
	}
	// pickcode 为空无法生成
	if pickcode == "" {
		logger.S().Warnf("[Monitor] generateStrmContent: pickcode 为空，跳过生成 cloudPath=%s account=%s", cloudPath, account)
		return ""
	}
	// 对参数进行 URL 编码
	encodedAccount := url.QueryEscape(account)
	var u string
	if enable302 {
		u = fmt.Sprintf("%s/api/fs/get?account=%s&pickcode=%s", prefix, encodedAccount, pickcode)
		if fileName != "" {
			u += "&file_name=" + url.QueryEscape(fileName)
		}
	} else {
		u = fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s", prefix, encodedAccount, pickcode)
		if fileName != "" {
			u += "&file_name=" + url.QueryEscape(fileName)
		}
	}
	return u + "\n"
}

// writeStrmFile 原子写入 STRM 文件（先写 tmp 再 rename）
// P2-10: 读-比较-写，仅在内容变化时覆盖（对齐参考项目 _sync_strm_text_with_event）
func writeStrmFile(strmPath, content string) error {
	// P2-10: 读取现有内容，相同则跳过写入
	if existing, err := os.ReadFile(strmPath); err == nil {
		if string(existing) == content {
			return nil // 内容未变化，跳过
		}
	}
	tmpPath := strmPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, strmPath)
}

// removeEmptyParents 向上递归删除空目录，直到遇到根目录或非空目录
// 对齐 TS removeEmptyParents
func removeEmptyParents(startDir string, mappings []model.MonitorPathMapping) {
	rootDirs := getRootDirs(mappings)
	currentDir := startDir
	for {
		// 检查是否命中根目录
		abs, err := filepath.Abs(currentDir)
		if err != nil {
			break
		}
		if _, ok := rootDirs[abs]; ok {
			break
		}
		// 检查目录是否为空
		entries, err := os.ReadDir(currentDir)
		if err != nil || len(entries) > 0 {
			break
		}
		// 删除空目录
		if err := os.Remove(currentDir); err != nil {
			break
		}
		logger.S().Debugf("[Monitor] 清理空目录: %s", currentDir)
		currentDir = filepath.Dir(currentDir)
	}
}

// getRootDirs 从路径映射中提取根目录集合（绝对路径）
func getRootDirs(mappings []model.MonitorPathMapping) map[string]bool {
	roots := make(map[string]bool)
	for _, m := range mappings {
		if abs, err := filepath.Abs(m.LocalPath); err == nil {
			roots[abs] = true
		}
	}
	return roots
}

// createRelatedAssetPlaceholders 在 STRM 同目录创建同名关联资源的空占位文件
// 对齐 MoviePilot auto_download_metadata 的轻量模式：先建好 nfo/jpg/png/srt/ass/sub 空文件，
// Emby/Jellyfin 识别为有刮削关联，真实内容由全量扫描 runDownloads 触发覆盖下载。
// strmPath: 刚写入的 STRM 路径；originalFileName: 原始媒体文件名（用于 .iso 双扩展名判断）
// extensions: 目标扩展名列表（带或不带 .），空则使用 model.DefaultDownloadExtensions
// 返回：实际创建的占位文件数量
func createRelatedAssetPlaceholders(strmPath, originalFileName string, extensions []string) int {
	dir := filepath.Dir(strmPath)
	base := filepath.Base(strmPath)
	// stem: STRM 文件名去掉 .strm（例如 movie.strm -> movie, game.iso.strm -> game.iso）
	stem := strings.TrimSuffix(base, ".strm")
	if stem == "" {
		return 0
	}
	// 媒体文件名 stem（movie.mkv -> movie），与 STRM stem 不同时（如 .iso）也作为额外匹配名
	mediaExt := strings.ToLower(filepath.Ext(originalFileName))
	mediaStem := strings.TrimSuffix(originalFileName, mediaExt)
	if strings.EqualFold(mediaExt, ".iso") {
		mediaStem = mediaStem + ".iso" // .iso 文件 mediaStem 与 STRM stem 对齐
	}
	// 扩展名集合（空则默认）
	exts := extensions
	if len(exts) == 0 {
		exts = model.DefaultDownloadExtensions
	}
	// 规范化：每个扩展名加 "." 前缀
	normExts := make([]string, 0, len(exts))
	for _, e := range exts {
		if e == "" {
			continue
		}
		e = strings.ToLower(e)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		normExts = append(normExts, e)
	}
	if len(normExts) == 0 {
		return 0
	}
	created := 0
	// 候选 stem 去重
	candidates := make(map[string]struct{})
	candidates[stem] = struct{}{}
	if mediaStem != "" && mediaStem != stem {
		candidates[mediaStem] = struct{}{}
	}
	for s := range candidates {
		for _, ext := range normExts {
			target := filepath.Join(dir, s+ext)
			if _, stErr := os.Stat(target); stErr == nil {
				continue // 已存在不覆盖（真实内容不丢失）
			}
			if f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644); err == nil {
				_ = f.Close()
				created++
			}
		}
	}
	return created
}

// deleteRelatedFiles 删除与 STRM 同名的相关文件（.nfo/.jpg/.srt 等）
// 匹配规则：同目录下、文件名以 STRM stem 为前缀、非 .strm 后缀
// 对齐 TS cleanRelatedFiles
func deleteRelatedFiles(strmPath string) int {
	dir := filepath.Dir(strmPath)
	base := filepath.Base(strmPath)
	stem := strings.TrimSuffix(base, ".strm")
	if stem == "" {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == base {
			continue // 跳过 STRM 文件本身
		}
		// 同名前缀匹配（如 movie.nfo, movie.srt 匹配 movie.strm 的 stem "movie"）
		if strings.HasPrefix(name, stem) {
			if err := os.Remove(filepath.Join(dir, name)); err == nil {
				deleted++
			}
		}
	}
	return deleted
}

// appendLog 追加事件处理日志
func (m *Monitor) appendLog(ctx context.Context, account, eventType string, success bool, filePath, localPath, message string) {
	if m.lifeEventLogRepo == nil {
		return
	}
	_, _ = m.lifeEventLogRepo.AppendLog(ctx, db.LifeEventLog{
		Timestamp: time.Now().UnixMilli(),
		Account:   account,
		EventType: eventType,
		Success:   success,
		FilePath:  filePath,
		LocalPath: localPath,
		Message:   message,
	})
}

// ==================== 通知辅助函数 ====================

// tryDispatchNotification 尝试通过新的 Dispatch 接口发送结构化通知
func (m *Monitor) tryDispatchNotification(ctx context.Context, n *notify.Notification) bool {
	if dispatcher, ok := m.notifier.(notify.NotificationDispatcher); ok {
		if err := dispatcher.Dispatch(ctx, n); err != nil {
			logger.S().Warnf("[Monitor] Dispatch 通知发送失败: %v", err)
		}
		return true
	}
	return false
}

// ==================== P2-8 通知合并（对齐参考项目 _schedule_notification） ====================

// notifyEntry 单条通知记录
type notifyEntry struct {
	kind      string // create / delete / move / rename
	account   string
	cloudPath string
	localPath string
	kindLabel string
	size      int64
}

// NotifyMerger 通知合并器：60秒窗口内累积通知，到期后合并发送一条摘要
// 对齐参考项目 _schedule_notification: Timer 60s 延迟，合并 strm_count + mediainfo_count
type NotifyMerger struct {
	notifier  Notifier
	mu        sync.Mutex
	entries   []notifyEntry
	timer     *time.Timer
	windowSec time.Duration
}

// NewNotifyMerger 创建通知合并器
func NewNotifyMerger(notifier Notifier) *NotifyMerger {
	return &NotifyMerger{
		notifier:  notifier,
		windowSec: 60 * time.Second,
	}
}

// Add 将一条通知加入合并队列，启动/重置60秒定时器
func (nm *NotifyMerger) Add(entry notifyEntry) {
	if nm == nil || nm.notifier == nil {
		return
	}
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.entries = append(nm.entries, entry)
	// 首次添加时启动定时器；已有定时器则保持不变（不推迟，保证60秒内必发）
	if nm.timer == nil {
		nm.timer = time.AfterFunc(nm.windowSec, nm.flush)
	}
}

// flush 合并发送所有累积的通知（定时器回调）
func (nm *NotifyMerger) flush() {
	nm.mu.Lock()
	entries := nm.entries
	nm.entries = nil
	nm.timer = nil
	nm.mu.Unlock()

	if len(entries) == 0 {
		return
	}

	// 按类型统计
	counts := map[string]int{"create": 0, "delete": 0, "move": 0, "rename": 0}
	for _, e := range entries {
		counts[e.kind]++
	}

	// 构建摘要消息
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>STRM 操作摘要</b>（%d 条事件）:\n", len(entries)))
	if counts["create"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0✅ 生成: %d\n", counts["create"]))
	}
	if counts["delete"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0🗑️ 删除: %d\n", counts["delete"]))
	}
	if counts["move"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0📁 移动: %d\n", counts["move"]))
	}
	if counts["rename"] > 0 {
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0✏️ 重命名: %d\n", counts["rename"]))
	}
	// 列出最多5条明细
	maxDetail := 5
	for i, e := range entries {
		if i >= maxDetail {
			sb.WriteString(fmt.Sprintf("\u00a0\u00a0...及其他 %d 条\n", len(entries)-maxDetail))
			break
		}
		sb.WriteString(fmt.Sprintf("\u00a0\u00a0· %s: %s\n", e.kind, e.cloudPath))
	}

	if err := nm.notifier.Notify(context.Background(), sb.String()); err != nil {
		logger.S().Warnf("[Monitor] 合并通知发送失败: %v", err)
	}
}

// ==================== STRM 通知（统一 Notification 对象 + 富文本 HTML 格式） ====================

// notifyCreate 发送创建通知
// 发送策略二选一（避免一条事件发两条）：
//   · 实现了 NotificationDispatcher（新路径）：富文本卡片 + 按钮，单独发送
//   · 回退到单纯 Notifier：进入合并器汇总成摘要消息后再推送
func (m *Monitor) notifyCreate(ctx context.Context, account, cloudPath, kindLabel, localPath string, size int64) {
	if m.notifier == nil {
		return
	}
	// 仅错误模式：正常操作不发通知（错误仍由 notifyPollError/notifyEventBatchError 推送）
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildCreateNotification(notify.STRMCreateInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
		FileSize:  size,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器，后续合并器 flush 时统一推送摘要（不再单独发一条，避免重复）
	m.notifyMerger.Add(notifyEntry{kind: "create", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel, size: size})
}

// notifyDelete 发送删除通知
func (m *Monitor) notifyDelete(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildDeleteNotification(notify.STRMDeleteInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器
	m.notifyMerger.Add(notifyEntry{kind: "delete", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel})
}

// notifyMove 发送移动通知
func (m *Monitor) notifyMove(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildMoveNotification(notify.STRMMoveInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器
	m.notifyMerger.Add(notifyEntry{kind: "move", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel})
}

// notifyRename 发送重命名通知
func (m *Monitor) notifyRename(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	if m.settingsFn().NotifyOnlyOnError {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildRenameNotification(notify.STRMRenameInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if m.tryDispatchNotification(ctx, n) {
		return
	}
	// 回退模式：只走合并器
	m.notifyMerger.Add(notifyEntry{kind: "rename", account: account, cloudPath: cloudPath, localPath: localPath, kindLabel: kindLabel})
}
