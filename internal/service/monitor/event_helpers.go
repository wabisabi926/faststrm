package monitor

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/concurrency"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

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
//  1. 旧云路径映射的本地目录（如 oldCloudPath="小王子" → 本地旧目录）
//  2. 通过文件名在映射本地目录中查找
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
		if err := strmutil.DeleteStrmFile(foundPath); err != nil {
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
			if err := strmutil.DeleteStrmFile(sp); err == nil {
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
			if err := strmutil.DeleteStrmFile(oldStrmPath); err == nil {
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
//  1. 通过旧 cloudPath 在当前映射中查找旧 localPath
//  2. 如果找不到，在 MediaMountPath 扩展目录中按文件名搜索
//  3. 如果还找不到，在当前映射目录中按文件名搜索
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
			if delErr := strmutil.DeleteStrmFile(sp); delErr == nil {
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
		if delErr := strmutil.DeleteStrmFile(oldLocalPath); delErr == nil {
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
// 匹配规则：同目录下、文件名以 STRM stem 为前缀、且后缀为扩展名（.xxx）
// 对齐 TS cleanRelatedFiles + moveOrRenameRelatedAssets 的安全匹配逻辑
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
		// 同名前缀匹配 + 安全校验：rest 必须为空或以 . 开头（扩展名形式）
		// 避免 "movie" 误匹配到 "movie_trailer.mp4" 等非关联文件
		if strings.HasPrefix(name, stem) {
			rest := strings.TrimPrefix(name, stem)
			if rest == "" || strings.HasPrefix(rest, ".") {
				if err := os.Remove(filepath.Join(dir, name)); err == nil {
					deleted++
				}
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
