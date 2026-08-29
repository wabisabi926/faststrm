// Package monitor 生活事件监控与 STRM 同步
// event_handler.go 事件处理 handler（对齐 frontend/src/lib/eventMonitorHandlers.ts）
package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/logger"
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
//
//	① 先调用 preProcessEvent 做决策/日志/Write-Ahead DB/type=17 拦截；
//	② 再按 skipReason / ShouldAct 进入 legacy handler；
//	③ 返回值通过 ctx 中携带的 *PollCounts（若有）累计 effective/skipped。
func (m *Monitor) processEvent(ctx context.Context, account string, event client115.LifeEventItem, lifeClient *client115.LifeClient) error { //nolint:cyclop // complexity: 58
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
		srcEventRaw   = "EVENT_RAW"
		srcDB         = "DB"
		srcDBRejected = "DB_SINGLE_REJECTED" // DB 有记录但仅单段+未命中根映射，强制丢弃重查
		srcAPIPID     = "API_PARENT_ID"
		srcAPIFID     = "API_FILE_ID"
		srcAPIRootLs  = "API_ROOT_FSLIST"
		srcFallback   = "BARE_FILENAME"
	)
	_ = srcAPIPID
	_ = srcAPIFID
	_ = srcAPIRootLs
	_ = srcFallback

	rawCloudPath := event.FilePath
	source := srcEventRaw

	// rename/move 事件：跳过 DB 缓存，强制走 API 刷新新路径（对齐参考项目 rename() refresh=True）
	// 原因：rename/move 事件到达时 DB 存的还是旧路径（上一个 create/move 写入的），
	// 若信任 DB 会算错 mapping.localPath → recreate 到旧目录名（看似没变）
	// 注意：oldCloudPath（旧路径）由 handler 内部 resolveOldCloudPathByFileID 单独查 DB，不受此处影响
	isRenameOrMove := client115.RenameEventTypes[eventType] || client115.MoveEventTypes[eventType]

	if !isRenameOrMove && strings.TrimSpace(rawCloudPath) == "" && m.sqliteDB != nil && strings.TrimSpace(event.FileID) != "" {
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
	stallTimeout := config.TransferStallTimeout()
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
	} else { //nolint:staticcheck // SA9003: 空分支为有意设计，保留错误给 pollOnce 内部累计 Errors
		// pollOnce 返回错误时不做额外处理
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

	warnMsg := fmt.Sprintf("事件处理无进展超时(%v) account=%s file=%s cloudPath=%s",
		config.TransferStallTimeout(), account, event.FileName, cloudPath)
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
