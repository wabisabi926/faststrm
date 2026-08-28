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
				if err := strmutil.DeleteStrmFile(oldStrmPath); err != nil {
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
		// ④ new_path 已存在 → fallback 到 recreate（清理旧STRM + 重新生成）
		if _, dstErr := os.Stat(mapping.localPath); dstErr == nil {
			logger.S().Infof("[Monitor] rename目标已存在 %s, fallback到recreate模式", mapping.localPath)
			if oldLocalPath != "" {
				m.cleanupOldStrmAssets(ctx, account, event, oldCloudPath, oldLocalPath, config)
			}
			if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
				return fmt.Errorf("mkdir 失败: %w", err)
			}
			if lifeClient != nil {
				m.recreateStrmInDirectory(ctx, account, event, mapping, cloudPath, lifeClient)
			}
			if m.sqliteDB != nil && oldCloudPath != "" && oldCloudPath != cloudPath {
				if n, upErr := db.UpdatePathPrefixBatch(m.sqliteDB, account, oldCloudPath, cloudPath); upErr == nil && n > 0 {
					logger.S().Infof("[Monitor] rename fallback recreate DB更新: %s→%s (%d条)", oldCloudPath, cloudPath, n)
				}
			}
			m.appendLog(ctx, account, "rename", true, cloudPath, mapping.localPath,
				fmt.Sprintf("重命名(recreate): 旧目录已清理，新STRM已生成 %s", mapping.localPath))
			m.notifyRename(ctx, account, cloudPath, "目录", mapping.localPath)
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
		if err := strmutil.DeleteStrmFile(oldStrmPath); err != nil {
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
