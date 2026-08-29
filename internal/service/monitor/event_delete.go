package monitor

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

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

	// 文件夹事件：递归删除目录（P1-5 Delete 安全兜底）
	if event.FileCategory == 0 {
		if err := strmutil.DeletePath(mapping.localPath); err != nil {
			m.appendLog(ctx, account, "delete", false, cloudPath, mapping.localPath,
				fmt.Sprintf("删除目录失败: %v", err))
			return fmt.Errorf("删除目录失败: %w", err)
		}
		// 清理空父目录
		if config.RemoveEmptyDirs {
			removeEmptyParents(filepath.Dir(mapping.localPath), config.PathMappings)
		}
		m.appendLog(ctx, account, "delete", true, cloudPath, mapping.localPath, "文件夹已删除")
		m.notifyDelete(ctx, account, cloudPath, "目录", mapping.localPath)
		logger.S().Infof("[Monitor] 文件夹已删除: %s", mapping.localPath)
		return nil
	}

	// 文件事件：删除 STRM + 相关文件（P1-5 Delete 安全兜底）
	strmFileName := getStrmFileName(event.FileName)
	// 关键：mapping.localPath 已包含相对路径（如 dist\Strm\小王子），直接拼接文件名
	strmPath := filepath.Join(mapping.localPath, strmFileName)

	// 删除 STRM 文件
	if err := strmutil.DeleteStrmFile(strmPath); err != nil {
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
	// 收集到批次收集器，oncePoll 结束时按父目录聚合发送（避免整季每集一条）
	m.collectFileDelete(ctx, account, cloudPath, strmPath)
	logger.S().Infof("[Monitor] STRM 已删除: %s (关联 %d)", strmPath, deletedRelated)

	// 通知 Emby 刷库
	if m.embyRefresh != nil {
		if err := m.embyRefresh.RefreshOnDelete(ctx, strmPath); err != nil {
			logger.S().Warnf("[Monitor] Emby 刷库安排失败 path=%s: %v", strmPath, err)
		}
	}

	return nil
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
