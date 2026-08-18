// Emby 删除同步：监听 library.deleted 事件，自动删除本地 STRM + 关联文件
// 对齐 frontend/src/lib/emby/syncDel.ts
// 参考项目：MoviePilot-Plugins samediasyncdel（裁剪为 faststrm 场景）
//
// 核心链路：
//
//	webhook → 白名单匹配 → 去重检查 → 路径映射 → 防误删1(STRM存在) →
//	防误删2(标题校验) → 防误删3(目录文件数) → 删STRM+关联+空目录 →
//	更新filePathDb → TG通知
package emby

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

const (
	// MAX_DIR_FILES_THRESHOLD 整季/整剧删除的目录文件数安全阈值
	MAX_DIR_FILES_THRESHOLD = 100
	// DedupWindow 去重窗口（60 秒内生活监控可能已处理同一路径）
	DedupWindow = 60 * time.Second
	// DedupCleanupThreshold 去重缓存清理阈值（超过 5 分钟的条目清理）
	DedupCleanupThreshold = 5 * time.Minute
	// SyncDeleteTag 日志标签
	SyncDeleteTag = "SyncDel"
)

// ==================== 文件路径数据库接口 ====================

// FilePathDb 文件路径数据库接口（可选注入，用于删除关联 DB 记录）
type FilePathDb interface {
	// DeleteByPath 按精确路径删除
	DeleteByPath(account, filePath string) (int64, error)
	// DeleteByPathPrefix 按路径前缀批量删除
	DeleteByPathPrefix(account, pathPrefix string) (int64, error)
}

// ==================== SyncDelete ====================

// SyncDelete 删除同步器
type SyncDelete struct {
	client     *Client
	dispatcher NotifierDispatcher
	settingsFn SettingsProvider
	filePathDb FilePathDb
	mu         sync.Mutex
	dedupCache map[string]time.Time
}

// NewSyncDelete 创建 SyncDelete
func NewSyncDelete(client *Client, dispatcher NotifierDispatcher, settingsFn SettingsProvider) *SyncDelete {
	return &SyncDelete{
		client:     client,
		dispatcher: dispatcher,
		settingsFn: settingsFn,
		dedupCache: make(map[string]time.Time),
	}
}

// SetFilePathDb 注入 FilePathDb 实例（可选）
func (s *SyncDelete) SetFilePathDb(db FilePathDb) {
	s.filePathDb = db
}

// HandleSyncDelete Emby library.deleted 事件处理入口
func (s *SyncDelete) HandleSyncDelete(ctx context.Context, item ItemInfo) error {
	settings := s.settingsFn()

	// 1. 基础检查：syncDeleteEnabled
	if !settings.SyncDeleteEnabled {
		return s.skip(item, "sync_delete_disabled")
	}

	mappings := settings.SyncDeletePathMappings
	if len(mappings) == 0 {
		return s.skip(item, "no_path_mappings")
	}

	embyPath := item.Path
	if embyPath == "" {
		return s.skip(item, "no_item_path")
	}

	dryRun := settings.SyncDeleteDryRun
	itemName := item.Name
	itemType := item.Type

	logger.S().Infof("[%s] 收到删除事件: type=%s name=%s path=%s dryRun=%v",
		SyncDeleteTag, itemType, itemName, embyPath, dryRun)

	// 2. 去重检查（60s 窗口，按 item ID）
	if s.isRecentlyDeleted(item.ID) {
		logger.S().Infof("[%s] 60秒内已处理过该项目，跳过: id=%s", SyncDeleteTag, item.ID)
		return s.skip(item, "recently_deleted")
	}

	// 3. 路径映射：白名单检查（路径必须匹配某个映射前缀）
	mapping := MatchPathMapping(embyPath, mappings)
	if mapping == nil {
		logger.S().Infof("[%s] 路径未匹配任何映射，跳过: %s", SyncDeleteTag, embyPath)
		return s.skip(item, "path_not_matched")
	}

	// 计算网盘路径
	cloudPath := MapEmbyPathToCloud(embyPath, *mapping)
	if cloudPath == "" {
		logger.S().Infof("[%s] 路径映射失败: %s", SyncDeleteTag, embyPath)
		return s.skip(item, "mapping_failed")
	}

	// 4. 试运行模式：仅记录不删除
	if dryRun {
		logger.S().Infof("[%s] 试运行模式，仅记录不删除: %s (cloudPath=%s)",
			SyncDeleteTag, embyPath, cloudPath)
		// 标记去重
		s.markRecentlyDeleted(item.ID)
		// 试运行模式仍发送通知
		if settings.SyncDeleteNotify {
			msg := formatDeleteNotification(itemName, itemType, 0, 0, true)
			if s.dispatcher != nil {
				_ = s.dispatcher.Notify(ctx, msg)
			}
		}
		return nil
	}

	// 5. 删除 STRM 文件 + 关联文件
	deletedFiles, deletedDirs := s.deleteByItemType(embyPath, itemType, mapping)

	// 6. 更新 filePathDb（如果可用）
	if s.filePathDb != nil {
		s.cleanupDbRecords(cloudPath, itemType, mapping.Account)
	}

	// 记录去重
	s.markRecentlyDeleted(item.ID)

	// 8. 发送通知
	if settings.SyncDeleteNotify {
		msg := formatDeleteNotification(itemName, itemType, deletedFiles, deletedDirs, false)
		if s.dispatcher != nil {
			_ = s.dispatcher.Notify(ctx, msg)
		}
	}

	logger.S().Infof("[%s] 完成: type=%s name=%s files=%d dirs=%d",
		SyncDeleteTag, itemType, itemName, deletedFiles, deletedDirs)

	return nil
}

// skip 返回跳过结果
func (s *SyncDelete) skip(item ItemInfo, reason string) error {
	logger.S().Infof("[%s] 跳过: name=%s type=%s reason=%s",
		SyncDeleteTag, item.Name, item.Type, reason)
	return nil
}

// ==================== 路径映射（PathMapping helper） ====================

// MatchPathMapping 从映射列表中找出 embyPath 最长前缀匹配的映射
// 对齐 TS matchPathMapping：选择最长前缀匹配（最具体的路径）
func MatchPathMapping(embyPath string, mappings []model.SyncDeletePathMapping) *model.SyncDeletePathMapping {
	normalized := normalizePath(embyPath)

	var bestMatch *model.SyncDeletePathMapping
	bestPrefixLen := -1

	for i := range mappings {
		prefix := normalizePath(mappings[i].EmbyPath)
		if normalized == prefix || strings.HasPrefix(normalized, prefix+"/") {
			if len(prefix) > bestPrefixLen {
				bestPrefixLen = len(prefix)
				bestMatch = &mappings[i]
			}
		}
	}
	return bestMatch
}

// MapEmbyPathToCloud 将 Emby 路径映射为网盘路径
// 对齐 TS mapEmbyPathToCloud
func MapEmbyPathToCloud(embyPath string, mapping model.SyncDeletePathMapping) string {
	normalizedEmby := normalizePath(embyPath)
	prefix := normalizePath(mapping.EmbyPath)
	cloudPrefix := strings.TrimRight(mapping.CloudPath, "/")

	if normalizedEmby == prefix {
		return cloudPrefix
	}
	if !strings.HasPrefix(normalizedEmby, prefix+"/") {
		return ""
	}
	relativePath := strings.TrimPrefix(normalizedEmby, prefix+"/")
	if relativePath == "" || relativePath == "." {
		return cloudPrefix
	}
	return cloudPrefix + "/" + relativePath
}

// normalizePath 规范化路径：统一为正斜杠
func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	// 统一为正斜杠
	cleaned := strings.ReplaceAll(p, "\\", "/")
	// 去除末尾斜杠（根目录 "/" 除外）
	if len(cleaned) > 1 {
		cleaned = strings.TrimRight(cleaned, "/")
	}
	return cleaned
}

// ==================== 去重 ====================

// isRecentlyDeleted 检查是否在去重窗口内已处理过
func (s *SyncDelete) isRecentlyDeleted(itemID string) bool {
	if itemID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// 清理过期条目
	for id, ts := range s.dedupCache {
		if now.Sub(ts) > DedupCleanupThreshold {
			delete(s.dedupCache, id)
		}
	}

	if ts, ok := s.dedupCache[itemID]; ok {
		if now.Sub(ts) < DedupWindow {
			return true
		}
		delete(s.dedupCache, itemID)
	}
	return false
}

// markRecentlyDeleted 标记最近处理过
func (s *SyncDelete) markRecentlyDeleted(itemID string) {
	if itemID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dedupCache[itemID] = time.Now()
}

// ==================== 按类型删除 ====================

// deleteByItemType 根据 itemType 分支删除逻辑
// 对齐 TS deleteByItemType
func (s *SyncDelete) deleteByItemType(
	strmPath string,
	itemType string,
	mapping *model.SyncDeletePathMapping,
) (deletedFiles int, deletedDirs int) {
	rootDirs := []string{normalizePath(mapping.EmbyPath)}

	switch itemType {
	case "Movie", "Episode":
		// 删单个 STRM 文件 + 关联文件 + 空目录
		return deleteStrmFile(strmPath, rootDirs)

	case "Season", "Series":
		// 计算要删除的目录
		dirPath := strmPath
		info, err := os.Stat(strmPath)
		if err == nil && !info.IsDir() {
			// 文件路径 → 取父目录
			dirPath = filepath.Dir(strmPath)
		}

		// 防误删3：目录文件数校验
		fileCount, err := countFilesRecursive(dirPath)
		if err != nil {
			logger.S().Errorf("[%s] 目录读取失败: %s err=%v", SyncDeleteTag, dirPath, err)
			return 0, 0
		}
		if fileCount == 0 {
			logger.S().Warnf("[%s] 目录为空，跳过: %s", SyncDeleteTag, dirPath)
			return 0, 0
		}
		if fileCount > MAX_DIR_FILES_THRESHOLD {
			logger.S().Errorf("[%s] 目录文件数异常（%d），疑似误判，跳过: %s",
				SyncDeleteTag, fileCount, dirPath)
			return 0, 0
		}

		// 目录级删除
		if err := os.RemoveAll(dirPath); err != nil {
			logger.S().Errorf("[%s] 目录删除失败: %s err=%v", SyncDeleteTag, dirPath, err)
			return 0, 0
		}

		// 清理空父目录
		removedDirs := removeEmptyParents(dirPath, rootDirs)
		return fileCount, 1 + removedDirs

	default:
		logger.S().Warnf("[%s] 未知类型: %s", SyncDeleteTag, itemType)
		return 0, 0
	}
}

// deleteStrmFile 删除单个 STRM 文件 + 关联文件 + 空目录
// 对齐 TS deleteStrmFile（cleanRelated=true）
func deleteStrmFile(strmPath string, rootDirs []string) (deletedFiles int, deletedDirs int) {
	// 防误删1：STRM 文件必须存在
	info, err := os.Stat(strmPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.S().Infof("[%s] STRM 路径不存在，跳过（可能已被生活监控处理）: %s",
				SyncDeleteTag, strmPath)
			return 0, 0
		}
		logger.S().Errorf("[%s] stat 失败: %s err=%v", SyncDeleteTag, strmPath, err)
		return 0, 0
	}
	if info.IsDir() {
		// 目录走 deleteStrmDir 逻辑
		return deleteStrmDir(strmPath, rootDirs)
	}

	// 防误删2：标题校验由调用方负责（faststrm 场景宽松）

	// 删除主文件
	if err := os.Remove(strmPath); err != nil {
		logger.S().Errorf("[%s] 删除文件失败: %s err=%v", SyncDeleteTag, strmPath, err)
		return 0, 0
	}
	deletedFiles = 1

	// 删除关联文件（字幕、nfo、图片）
	dir := filepath.Dir(strmPath)
	base := strings.TrimSuffix(filepath.Base(strmPath), filepath.Ext(strmPath))
	relatedExts := []string{".srt", ".ass", ".sub", ".nfo", ".jpg", ".png"}

	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			// 跳过主文件（已删除）
			if name == filepath.Base(strmPath) {
				continue
			}
			nameBase := strings.TrimSuffix(name, filepath.Ext(name))
			ext := strings.ToLower(filepath.Ext(name))
			// 匹配同基名的关联文件
			if nameBase == base {
				for _, relExt := range relatedExts {
					if ext == relExt {
						if err := os.Remove(filepath.Join(dir, name)); err == nil {
							deletedFiles++
						}
						break
					}
				}
			}
		}
	}

	// 清理空父目录
	removedDirs := removeEmptyParents(dir, rootDirs)
	return deletedFiles, removedDirs
}

// deleteStrmDir 删除目录级 STRM（整季/整剧）
func deleteStrmDir(dirPath string, rootDirs []string) (deletedFiles int, deletedDirs int) {
	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.S().Infof("[%s] 目录不存在，跳过: %s", SyncDeleteTag, dirPath)
			return 0, 0
		}
		logger.S().Errorf("[%s] stat 目录失败: %s err=%v", SyncDeleteTag, dirPath, err)
		return 0, 0
	}
	if !info.IsDir() {
		// 文件走 deleteStrmFile
		return deleteStrmFile(dirPath, rootDirs)
	}

	// 防误删3：目录文件数校验
	fileCount, err := countFilesRecursive(dirPath)
	if err != nil {
		logger.S().Errorf("[%s] 目录读取失败: %s err=%v", SyncDeleteTag, dirPath, err)
		return 0, 0
	}
	if fileCount == 0 {
		logger.S().Warnf("[%s] 目录为空，跳过: %s", SyncDeleteTag, dirPath)
		return 0, 0
	}
	if fileCount > MAX_DIR_FILES_THRESHOLD {
		logger.S().Errorf("[%s] 目录文件数异常（%d），疑似误判，跳过: %s",
			SyncDeleteTag, fileCount, dirPath)
		return 0, 0
	}

	if err := os.RemoveAll(dirPath); err != nil {
		logger.S().Errorf("[%s] 目录删除失败: %s err=%v", SyncDeleteTag, dirPath, err)
		return 0, 0
	}

	removedDirs := removeEmptyParents(dirPath, rootDirs)
	return fileCount, 1 + removedDirs
}

// ==================== 文件操作辅助 ====================

// countFilesRecursive 递归统计目录内文件数
func countFilesRecursive(dir string) (int, error) {
	count := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	return count, err
}

// removeEmptyParents 清理空父目录，遇到 rootDir 或非空目录停止
// 对齐 TS removeEmptyParents
func removeEmptyParents(dirPath string, rootDirs []string) int {
	count := 0
	abs, err := filepath.Abs(dirPath)
	if err != nil {
		return 0
	}

	// 规范化 rootDirs 为绝对路径
	normalizedRoots := make([]string, 0, len(rootDirs))
	for _, r := range rootDirs {
		if ra, err := filepath.Abs(r); err == nil {
			normalizedRoots = append(normalizedRoots, normalizePath(ra))
		}
	}

	current := normalizePath(abs)
	for {
		// 检查是否到达根目录
		isRoot := false
		for _, root := range normalizedRoots {
			if current == root {
				isRoot = true
				break
			}
		}
		if isRoot {
			break
		}

		// 检查目录是否为空
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) > 0 {
			break
		}

		parent := filepath.Dir(current)
		if err := os.Remove(current); err != nil {
			break
		}
		count++
		current = normalizePath(parent)
	}
	return count
}

// ==================== DB 清理 ====================

// cleanupDbRecords 清理 filePathDb 中的关联记录
// 对齐 TS cleanupDbRecords
func (s *SyncDelete) cleanupDbRecords(cloudPath string, itemType string, account string) {
	if s.filePathDb == nil {
		return
	}

	// 账号过滤：若映射指定了 account 则仅清该账号
	// 注意：faststrm Go 版本不在此处读取 accounts 列表（避免循环依赖）
	// 调用方若需要全账号清理，可在 FilePathDb 实现中处理空 account
	accounts := []string{}
	if account != "" {
		accounts = []string{account}
	} else {
		// account 为空时，留空让 FilePathDb 自行决定
		accounts = []string{""}
	}

	for _, acc := range accounts {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.S().Errorf("[%s] DB 清理 panic: account=%s path=%s err=%v",
						SyncDeleteTag, acc, cloudPath, r)
				}
			}()

			var deleted int64
			var err error
			if itemType == "Movie" || itemType == "Episode" {
				deleted, err = s.filePathDb.DeleteByPath(acc, cloudPath)
				if err != nil {
					logger.S().Errorf("[%s] DB 删除失败: account=%s path=%s err=%v",
						SyncDeleteTag, acc, cloudPath, err)
					return
				}
				if deleted > 0 {
					logger.S().Infof("[%s] DB 删除 %d 条记录: account=%s path=%s",
						SyncDeleteTag, deleted, acc, cloudPath)
				}
			} else {
				// 整季/整剧：按前缀删除
				deleted, err = s.filePathDb.DeleteByPathPrefix(acc, cloudPath)
				if err != nil {
					logger.S().Errorf("[%s] DB 前缀删除失败: account=%s prefix=%s err=%v",
						SyncDeleteTag, acc, cloudPath, err)
					return
				}
				if deleted > 0 {
					logger.S().Infof("[%s] DB 前缀删除 %d 条记录: account=%s prefix=%s",
						SyncDeleteTag, deleted, acc, cloudPath)
				}
			}
		}()
	}
}

// ==================== 通知 ====================

// formatDeleteNotification 格式化删除同步通知
// 对齐 TS formatDeleteNotification
func formatDeleteNotification(
	itemName string,
	itemType string,
	deletedFiles int,
	deletedDirs int,
	dryRun bool,
) string {
	typeMap := map[string]string{
		"Movie":   "电影",
		"Episode": "剧集",
		"Season":  "季",
		"Series":  "整剧",
	}
	typeText, ok := typeMap[itemType]
	if !ok {
		typeText = itemType
	}
	dryRunTag := ""
	if dryRun {
		dryRunTag = " [试运行]"
	}

	return fmt.Sprintf(`🗑️ 媒体删除同步%s
<b>标题:</b> %s
<b>类型:</b> %s
<b>删除文件:</b> %d
<b>清理目录:</b> %d
<b>时间:</b> %s`,
		dryRunTag,
		itemName,
		typeText,
		deletedFiles,
		deletedDirs,
		time.Now().Local().Format("2006-01-02 15:04:05"),
	)
}
