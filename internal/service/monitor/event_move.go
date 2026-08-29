package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

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
		if err := strmutil.DeletePath(oldLocalPath); err != nil {
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
		m.appendLog(ctx, account, "move-out", true, oldCloudPath, oldLocalPath,
			fmt.Sprintf("移出映射：旧目录已删除 %s → %s", oldCloudPath, newCloudPath))
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
	if err := strmutil.DeleteStrmFile(strmPath); err != nil {
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
		if err := strmutil.DeletePath(oldLocalPath); err != nil {
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
		m.appendLog(ctx, account, "move-recreate", true, oldCloudPath, oldLocalPath,
			fmt.Sprintf("recreate 模式：旧目录已清理 %s", oldLocalPath))
		return
	}
	// 文件：删除旧 STRM + 关联资源 + DB 按 fileID 清理（P1-5 Delete 安全兜底）
	oldStrmPath := filepath.Join(filepath.Dir(oldLocalPath), getStrmFileName(filepath.Base(oldLocalPath)))
	if err := strmutil.DeleteStrmFile(oldStrmPath); err != nil {
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

// recreateStrmInDirectory 在目标目录重新生成STRM（用于move/rename目标已存在时的recreate fallback）
func (m *Monitor) recreateStrmInDirectory(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
	lifeClient *client115.LifeClient,
) {
	if event.FileCategory == 0 {
		strmCreated, createErr := m.handleCreateFolderRecursive(
			ctx, account, mapping, lifeClient, event.FileID, cloudPath, 0,
		)
		if createErr != nil {
			logger.S().Warnf("[Monitor] recreate文件夹递归部分失败 folder=%s: %v", cloudPath, createErr)
		}
		m.appendLog(ctx, account, "create", true, cloudPath, mapping.localPath,
			fmt.Sprintf("recreate: 文件夹已重新创建，内部生成 STRM %d 个", strmCreated))
		logger.S().Infof("[Monitor] recreate文件夹已创建: %s (内部STRM: %d)", mapping.localPath, strmCreated)
	} else {
		in := singleFileCreateInput{
			CloudPath: cloudPath,
			FileName:  event.FileName,
			PickCode:  event.PickCode,
			FileSize:  event.FileSize,
			FileID:    event.FileID,
			ParentID:  event.ParentID,
		}
		localParentDir := mapping.localPath
		strmPath, err := m.createStrmForSingleFile(ctx, account, in, localParentDir, "文件")
		if err != nil {
			m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath, err.Error())
			return
		}
		if strmPath == "" {
			m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
				fmt.Sprintf("recreate: 未生成 STRM: %s", event.FileName))
			return
		}
		m.appendLog(ctx, account, "create", true, cloudPath, strmPath, "recreate: STRM 已重新创建")
		logger.S().Infof("[Monitor] recreate STRM已创建: %s", strmPath)
	}
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
// sourceStem: 旧文件名 stem（仅去掉最后一个扩展名，对齐参考项目 Path.stem + glob 行为）；targetStem: 新的 stem
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
func (m *Monitor) handleMoveEvent( //nolint:cyclop // complexity: 96
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
				if err := strmutil.DeleteStrmFile(oldStrmPath); err != nil {
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
		// b) 新目录已存在 → fallback 到 recreate（先清理旧STRM，再在新位置重新生成）
		// 原因：115 可能先触发创建事件在目标位置生成STRM，再触发move事件，
		//       此时目标已存在不能直接跳过，需要走recreate逻辑保证STRM正确
		if _, dstErr := os.Stat(mapping.localPath); dstErr == nil {
			logger.S().Infof("[Monitor] move目标已存在 %s, fallback到recreate模式重新生成STRM", mapping.localPath)
			// 清理旧路径的STRM
			if oldLocalPath != "" {
				m.cleanupOldStrmAssets(ctx, account, event, oldCloudPath, oldLocalPath, config)
			}
			// 走recreate: 在新位置创建STRM
			if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
				return fmt.Errorf("mkdir 失败: %w", err)
			}
			if lifeClient != nil {
				m.recreateStrmInDirectory(ctx, account, event, mapping, cloudPath, lifeClient)
			}
			// DB路径前缀更新
			if m.sqliteDB != nil && oldCloudPath != "" && oldCloudPath != cloudPath {
				if n, upErr := db.UpdatePathPrefixBatch(m.sqliteDB, account, oldCloudPath, cloudPath); upErr == nil && n > 0 {
					logger.S().Infof("[Monitor] move fallback recreate DB更新: %s→%s (%d条)", oldCloudPath, cloudPath, n)
				}
			}
			m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
				fmt.Sprintf("移动(recreate): 旧目录已清理，新STRM已生成 %s", mapping.localPath))
			m.notifyMove(ctx, account, cloudPath, "目录", mapping.localPath)
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
		if !config.MoveMediaCreateNewStrm { //nolint:staticcheck // SA9003: 空分支为有意设计
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
	// 公共 stem 计算（原始文件名 stem，对齐参考项目 Path.stem + glob）
	oldStem := strings.TrimSuffix(oldFileName, filepath.Ext(oldFileName))
	newStem := strings.TrimSuffix(event.FileName, filepath.Ext(event.FileName))

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
		if err := strmutil.DeleteStrmFile(oldStrmPath); err != nil {
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
