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
	"github.com/wabisabi926/faststrm/pkg/concurrency"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

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

	// 4.5 先写 DB 记录（对齐参考项目 _create 先 upsert 再生成 STRM）
	// —— DB 写入必须在 overwrite=never 检查之前，确保已存在的老 STRM 也补录 DB，
	//       否则 move/rename 反查不到旧路径，旧 STRM 删不掉
	if m.sqliteDB != nil {
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

	// 4.6 对齐 MoviePilot overwrite_mode："never" 时已存在 STRM 则跳过
	// —— DB 已在 4.5 写入，此处跳过 STRM 生成不影响后续 move/rename 反查
	if strings.EqualFold(config.OverwriteMode, "never") {
		if _, statErr := os.Stat(strmPath); statErr == nil {
			logger.S().Debugf("[Monitor] createStrmForSingleFile overwrite=never，跳过已存在: %s", strmPath)
			return strmPath, nil
		}
	}

	// 5. 生成 STRM URL 内容（P1-4 高级模板优先）
	content := generateStrmContent(
		in.CloudPath, config.StrmPrefix, config.EnablePathEncoding,
		account, in.PickCode, in.FileName,
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

	// 7. 清理同 file_id 的旧 STRM（仅在确定生成新 STRM 时执行，对齐参考项目 create() L1590-1626）
	// 参考项目行为：检查 DB 中是否已有同 file_id 的旧记录，若旧路径 ≠ 新路径则删除旧 STRM
	if m.sqliteDB != nil {
		oldEntry, _ := db.GetFileOrFolderEntry(m.sqliteDB, account, in.FileID)
		if oldEntry != nil && oldEntry.Path != in.CloudPath {
			logger.S().Infof("[Monitor] createStrm: 检测到旧STRM fileID=%s oldPath=%s newPath=%s → 清理旧STRM",
				in.FileID, oldEntry.Path, in.CloudPath)
			m.cleanupOldStrmByFileID(account, in.FileID, oldEntry.Path, oldEntry.FileName, in.FileName, config)
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

// resolveEventPickCode 解析事件的有效 pickcode；事件自带缺失/无效时按 file_id 反查
// 回退链（对齐参考项目 get_pickcode_by_path）：
//  1. 事件自带 pickcode（proapi/ios 响应字段完整）
//  2. DB 按 file_id 反查（历史同步已写入，免 API 调用）
//  3. files/info API 按 file_id 反查（webapi 响应经常缺 pick_code）
func (m *Monitor) resolveEventPickCode(
	ctx context.Context,
	account string,
	lifeClient *client115.LifeClient,
	event client115.LifeEventItem,
) string {
	if isValidPickcode(event.PickCode) {
		return event.PickCode
	}
	if event.FileID == "" || event.FileID == "0" {
		return event.PickCode
	}
	// 2) DB 按 file_id 反查
	if m.sqliteDB != nil {
		if entry, err := db.GetFileOrFolderEntry(m.sqliteDB, account, event.FileID); err == nil && entry != nil && isValidPickcode(entry.PickCode) {
			logger.S().Infof("[Monitor] 事件缺 pickcode file_id=%s → DB 反查成功 pc=%s", event.FileID, entry.PickCode)
			return entry.PickCode
		}
	}
	// 3) files/info API 按 file_id 反查
	if lifeClient != nil {
		if pc, err := lifeClient.GetPickCodeByFileID(ctx, event.FileID); err == nil && isValidPickcode(pc) {
			logger.S().Infof("[Monitor] 事件缺 pickcode file_id=%s → files/info 反查成功 pc=%s", event.FileID, pc)
			return pc
		} else if err != nil {
			logger.S().Warnf("[Monitor] 事件缺 pickcode file_id=%s files/info 反查失败: %v", event.FileID, err)
		}
	}
	return event.PickCode
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
	// 文件夹事件：先 mkdir，写根文件夹到 DB，然后递归遍历内部媒体文件生成 STRM
	if event.FileCategory == 0 {
		if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
			m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
				fmt.Sprintf("mkdir 失败: %v", err))
			return fmt.Errorf("mkdir 失败: %w", err)
		}
		// 先写根文件夹到 folders 表（对齐参考项目 _create 第一步 upsert_batch 包含根目录）
		if m.sqliteDB != nil && event.FileID != "" && event.FileID != "0" {
			parentID := event.ParentID
			if parentID == "" {
				parentID = "0"
			}
			_ = db.UpsertFolderEntry(m.sqliteDB, account, db.FilePathEntry{
				FileID:     event.FileID,
				Path:       cloudPath,
				FileName:   event.FileName,
				ParentID:   parentID,
				UpdateTime: time.Now().Unix(),
			})
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
		PickCode:  m.resolveEventPickCode(ctx, account, lifeClient, event),
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
// nolint:cyclop // 递归 DFS 函数，多分支不可避免，拆分会破坏递归结构
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
	// depth==0 根文件夹：也写 folders 表（对齐 handleCreateEvent L186）
	// 确保 recreate 模式（rename/move）调 handleCreateFolderRecursive 时根文件夹路径被更新
	if depth == 0 && m.sqliteDB != nil && folderID != "" && folderID != "0" {
		_ = db.UpsertFolderEntry(m.sqliteDB, account, db.FilePathEntry{
			FileID:     folderID,
			Path:       folderCloudPath,
			FileName:   filepath.Base(folderCloudPath),
			ParentID:   "0",
			UpdateTime: time.Now().Unix(),
		})
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
	// 关联资源扩展名集合（生活监控 DownloadExtensions，空则继承全局默认）
	// 对齐全量生成：视频生成 STRM，关联资源扩展名匹配则真实下载
	downloadExts := buildDownloadExtSet(config.DownloadExtensions)
	autoDownload := config.AutoDownloadMetadata

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
			// 非目录：计算本地相对路径，按扩展名分类处理
			// 对齐全量生成 listAllFilesRecursive：媒体→生成 STRM；关联资源→真实下载；其它→忽略
			relFromRoot := strings.TrimPrefix(
				strings.TrimPrefix(entryCloudPath, rootMapping.cloudPath), "/")
			relLocal := sanitizePathParts(relFromRoot)
			localParentDir := filepath.Join(rootLocalPath, filepath.Dir(relLocal))

			if isMediaFile(entry.Name, model.DefaultStrmExtensions) {
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
					// P3-4: 该内部文件已由文件夹递归生成 STRM，注入意图抑制标记，
					// 抑制后续到达的同文件独立 create 事件（防同一文件 STRM 重复生成）
					if m.intent != nil {
						m.intent.Mark(fmt.Sprintf("%v", entry.FID))
					}
				}
			} else if autoDownload && isDownloadRelatedFile(entry.Name, downloadExts) {
				// 关联资源（.srt/.ass/.nfo/.jpg/.png 等）：真实下载云端内容，不再创建空占位符
				savePath := filepath.Join(rootLocalPath, relLocal)
				if err := m.downloadRelatedFile(ctx, lifeClient, entry.PickCode, entryCloudPath, savePath); err != nil {
					logger.S().Warnf("[Monitor] 关联资源下载失败 cloud=%s: %v", entryCloudPath, err)
				} else {
					logger.S().Debugf("[Monitor] 关联资源已下载: %s", savePath)
				}
			}
		}
		if len(resp.Data) < pageLimit {
			break
		}
		_ = config // 避免 config 未使用警告（后续可接入上限动态配置）
	}
	return totalCreated, nil
}
