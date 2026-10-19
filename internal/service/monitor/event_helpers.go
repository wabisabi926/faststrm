package monitor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/strm"
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

// singleFileParentDir 单文件事件下，把匹配到的本地路径回收一级到父目录。
// 对齐参考项目 p115strmhelper client.py：先拼完整相对路径再取 file_path.parent。
// 前缀匹配时 relativePath 末段就是文件名（如 "叶问.iso"），会被当成目录多拼一层，
// 这里截去最后一级，STRM 才落在正确父目录（电影本地/叶问.iso.strm，
// 而非 电影本地/叶问.iso/叶问.iso.strm）。精确匹配（relativePath==""）返回映射根 localPath。
func singleFileParentDir(m *pathMapping) string {
	if m == nil {
		return ""
	}
	if m.relativePath == "" {
		return m.localPath
	}
	return filepath.Dir(m.localPath)
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

// getStrmFileName / isValidPickcode / shouldGenerateStrm 的实现在 pkg/strmutil，
// 与 task 侧共用同一份（此前两个包各写了一遍，逐行相同）。

func getStrmFileName(fileName string) string { return strmutil.StrmFileName(fileName) }

func isValidPickcode(pc string) bool { return strmutil.IsValidPickcode(pc) }

func shouldGenerateStrm(fileName string, fileSize, minFileSize int64, blacklist []string, matcher ...*concurrency.StringMatcher) (string, bool) {
	return strmutil.ShouldGenerateStrm(fileName, fileSize, minFileSize, blacklist, matcher...)
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
// 实现已统一到 pkg/strmutil.SanitizeRelPath，与 task 全量生成共用同一套规则；
// 否则同一个文件会被事件侧与全量侧写到两个不同路径。
func sanitizePathParts(relativePath string) string {
	return strmutil.SanitizeRelPath(relativePath)
}

// generateStrmContent 生成 .strm 文件内容（URL 到 115 文件）
// 302 模式：{prefix}/api/fs/get?account=xxx&pickcode=xxx&file_name=xxx
// 统一输出：{prefix}/api/strm?account=xxx&pickcode=xxx&file_name=xxx
// P1-4：customTemplate 非空时优先用 model.RenderStrmUrlTemplate。
// 对齐 TS generateStrmContent
func generateStrmContent(cloudPath, strmPrefix, account, pickcode, fileName string, customTemplate ...string) string {
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
	// 前缀不可用（空 / 回环 / 0.0.0.0）时先用本机内网 IP 兜底。
	// 历史实现直接回落到 http://127.0.0.1:8090，结果只有本机能播，用户很难定位原因。
	if strm.IsUnusablePrefix(prefix) {
		if p, source := strm.ResolveStrmPrefix([]string{prefix}, 0); p != "" {
			logger.S().Warnf("[Monitor] STRM 前缀不可用(%q)，已改用本机内网地址 %s（source=%s）cloudPath=%s",
				prefix, p, source, cloudPath)
			prefix = p
		} else {
			logger.S().Warnf("[Monitor] STRM 前缀不可用(%q) 且无法探测内网地址，跳过生成 cloudPath=%s；请在设置中配置可访问的 STRM 前缀",
				prefix, cloudPath)
			return ""
		}
	}
	// pickcode 为空无法生成
	if pickcode == "" {
		logger.S().Warnf("[Monitor] generateStrmContent: pickcode 为空，跳过生成 cloudPath=%s account=%s", cloudPath, account)
		return ""
	}
	// 对参数进行 URL 编码
	encodedAccount := url.QueryEscape(account)
	// 统一硬编码 /api/strm（enable302 已废弃）
	u := fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s", prefix, encodedAccount, pickcode)
	if fileName != "" {
		u += "&file_name=" + url.QueryEscape(fileName)
	}
	return u + "\n"
}

// writeStrmFile 原子写入 STRM 文件（先写 tmp 再 rename）
// P2-10: 读-比较-写，仅在内容变化时覆盖（对齐参考项目 _sync_strm_text_with_event）
func writeStrmFile(strmPath, content string) error {
	_, err := strmutil.WriteStrmFile(strmPath, content)
	return err
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

// resolvedStrmExtensions 返回生活监控生效的 STRM 视频扩展名列表。
//
// 优先使用 LifeMonitor.StrmExtensions（由 server 层从全局 settings.StrmExtensions 继承），
// 为空时回落内置默认值。历史实现直接硬编码 model.DefaultStrmExtensions，
// 导致用户自定义扩展名对生活事件完全无效。
func resolvedStrmExtensions(config model.LifeMonitorSettings) []string {
	if len(config.StrmExtensions) > 0 {
		return config.StrmExtensions
	}
	return model.DefaultStrmExtensions
}

// buildDownloadExtSet 规范化 DownloadExtensions 为带 "." 前缀、小写的扩展名集合
// 空列表时回退到 model.DefaultDownloadExtensions
func buildDownloadExtSet(extensions []string) map[string]struct{} {
	exts := extensions
	if len(exts) == 0 {
		exts = model.DefaultDownloadExtensions
	}
	set := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		if e == "" {
			continue
		}
		e = strings.ToLower(e)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		set[e] = struct{}{}
	}
	return set
}

// isDownloadRelatedFile 判断扩展名是否属于关联资源扩展名集合
// 对齐全量生成 listAllFilesRecursive：仅视频生成 STRM，DownloadExtensions 匹配的关联文件真实下载
func isDownloadRelatedFile(fileName string, downloadExts map[string]struct{}) bool {
	if len(downloadExts) == 0 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return false
	}
	_, ok := downloadExts[ext]
	return ok
}

// downloadRelatedFile 真实下载 115 云端关联资源文件（.srt/.ass/.nfo/.jpg/.png 等）到 savePath
// 对齐全量生成 runDownloads：只下载云端真实存在的文件，不再创建空占位符。
// 先写 .tmp 再原子 rename，保证不残留半截文件；已存在的文件会被覆盖为真实内容。
func (m *Monitor) downloadRelatedFile(
	ctx context.Context,
	lifeClient *client115.LifeClient,
	pickcode, cloudPath, savePath string,
) error {
	if !isValidPickcode(pickcode) {
		return fmt.Errorf("关联资源 pickcode 无效(需17位字母数字): %q cloud=%s", pickcode, cloudPath)
	}
	fs := lifeClient.FsClient()
	if fs == nil {
		return fmt.Errorf("fsClient 未初始化")
	}
	meta, err := fs.GetDownloadUrlWebFull(ctx, pickcode, lifeClient.Cookie(), client115.DefaultUA)
	if err != nil {
		return fmt.Errorf("解析直链失败 %s: %w", cloudPath, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求失败 %s: %w", cloudPath, err)
	}
	req.Header.Set("User-Agent", client115.DefaultUA)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	if cookie := lifeClient.Cookie(); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败 %s: %w", cloudPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("下载失败 %s: http status %d", cloudPath, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(savePath), 0o755); err != nil {
		return fmt.Errorf("创建目录失败 %s: %w", filepath.Dir(savePath), err)
	}
	tmpPath := savePath + ".tmp"
	fp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("创建临时文件失败 %s: %w", tmpPath, err)
	}
	defer func() {
		fp.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err := io.Copy(fp, resp.Body); err != nil {
		return fmt.Errorf("写入失败 %s: %w", tmpPath, err)
	}
	if err := fp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, savePath); err != nil {
		return fmt.Errorf("落盘失败 %s: %w", savePath, err)
	}
	return nil
}

// deleteRelatedFiles 删除 STRM 同名的关联文件（nfo/海报/字幕）。
// 实现在 pkg/strmutil，带扩展名白名单：只删元数据与字幕，绝不碰 .mkv/.mp4 等视频文件
// （用户可能把真实文件与 STRM 放在同一目录）。
func deleteRelatedFiles(strmPath string) int {
	return strmutil.DeleteRelatedFiles(strmPath, strmutil.DefaultRelatedExts)
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
