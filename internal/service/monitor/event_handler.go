// Package monitor 生活事件监控与 STRM 同步
// event_handler.go 事件处理 handler（对齐 frontend/src/lib/eventMonitorHandlers.ts）
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
	"github.com/wabisabi926/faststrm/internal/service/notify"
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
// 对齐 TS processEvent
func (m *Monitor) processEvent(ctx context.Context, account string, event client115.LifeEventItem) error {
	config := m.settingsFn()
	eventType := event.Type

	// 解析云端路径
	cloudPath := event.FilePath
	if cloudPath == "" {
		return fmt.Errorf("event file_path 为空，无法解析云端路径")
	}

	// 匹配路径映射
	mapping := matchPathMapping(cloudPath, config.PathMappings, account)
	if mapping == nil {
		logger.S().Debugf("[Monitor] 事件跳过，无路径映射: cloudPath=%s account=%s type=%d",
			cloudPath, account, eventType)
		return nil
	}

	// 根据事件类型分发
	switch {
	case client115.CreateEventTypes[eventType]:
		if !config.EventTypes.Create {
			return nil
		}
		return m.handleCreateEvent(ctx, account, event, mapping, cloudPath, true)

	case client115.DeleteEventTypes[eventType]:
		if !config.EventTypes.Remove {
			return nil
		}
		return m.handleDeleteEvent(ctx, account, event, mapping, cloudPath)

	case client115.MoveEventTypes[eventType]:
		if !config.EventTypes.Move {
			return nil
		}
		return m.handleMoveEvent(ctx, account, event, mapping, cloudPath)

	case client115.RenameEventTypes[eventType]:
		if !config.EventTypes.Rename {
			return nil
		}
		return m.handleRenameEvent(ctx, account, event, mapping, cloudPath)

	default:
		// 未处理的事件类型，记录为 folder-sync 并跳过
		m.appendLog(ctx, account, "folder-sync", true, cloudPath, mapping.localPath,
			fmt.Sprintf("未处理的事件类型 type=%d", eventType))
		return nil
	}
}

// ==================== handleCreateEvent：创建 STRM ====================

// handleCreateEvent 在映射的本地路径创建 STRM 文件
// 对齐 TS handleCreateEvent（简化版：不递归扫描文件夹）
func (m *Monitor) handleCreateEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
	notify bool,
) error {
	config := m.settingsFn()

	// 文件夹事件：仅创建目录
	if event.Category == 0 {
		if err := os.MkdirAll(mapping.localPath, 0o755); err != nil {
			m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
				fmt.Sprintf("mkdir 失败: %v", err))
			return fmt.Errorf("mkdir 失败: %w", err)
		}
		m.appendLog(ctx, account, "create", true, cloudPath, mapping.localPath, "文件夹已创建")
		logger.S().Infof("[Monitor] 文件夹已创建: %s", mapping.localPath)
		if notify {
			m.notifyCreate(ctx, account, cloudPath, "目录", mapping.localPath, 0)
		}
		return nil
	}

	// 文件事件：检查扩展名
	// LifeMonitorSettings 不含 StrmExtensions，使用 model.DefaultStrmExtensions
	if !isMediaFile(event.FileName, model.DefaultStrmExtensions) {
		m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
			fmt.Sprintf("非媒体文件: %s", event.FileName))
		return nil
	}

	// 检查 pickcode
	if event.PickCode == "" {
		m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
			fmt.Sprintf("pick_code 为空: %s", event.FileName))
		return fmt.Errorf("pick_code 为空: %s", event.FileName)
	}

	// 检查最小文件大小
	if config.MinFileSize > 0 && event.Size > 0 && event.Size < config.MinFileSize {
		m.appendLog(ctx, account, "create", false, cloudPath, mapping.localPath,
			fmt.Sprintf("文件过小 (%d < %d): %s", event.Size, config.MinFileSize, event.FileName))
		return nil
	}

	// 构建 STRM 文件路径
	strmFileName := getStrmFileName(event.FileName)
	strmPath := filepath.Join(filepath.Dir(mapping.localPath), strmFileName)

	// 生成 STRM 内容
	content := generateStrmContent(cloudPath, config.StrmPrefix, config.EnablePathEncoding,
		config.Enable302, account, event.PickCode, event.FileName)
	if content == "" {
		m.appendLog(ctx, account, "create", false, cloudPath, strmPath, "生成 STRM 内容失败")
		return fmt.Errorf("生成 STRM 内容失败: %s", event.FileName)
	}

	// 确保父目录存在
	if err := os.MkdirAll(filepath.Dir(strmPath), 0o755); err != nil {
		m.appendLog(ctx, account, "create", false, cloudPath, strmPath,
			fmt.Sprintf("创建父目录失败: %v", err))
		return fmt.Errorf("创建父目录失败: %w", err)
	}

	// 原子写入 STRM 文件（先写 tmp 再 rename，避免写一半损坏）
	if err := writeStrmFile(strmPath, content); err != nil {
		m.appendLog(ctx, account, "create", false, cloudPath, strmPath,
			fmt.Sprintf("写入 STRM 失败: %v", err))
		return fmt.Errorf("写入 STRM 失败: %w", err)
	}

	m.appendLog(ctx, account, "create", true, cloudPath, strmPath,
		fmt.Sprintf("STRM 已创建: %s", strmPath))
	logger.S().Infof("[Monitor] STRM 已创建: %s", strmPath)

	if notify {
		m.notifyCreate(ctx, account, cloudPath, "文件", strmPath, event.Size)
	}

	// 通知 Emby 刷库
	if m.embyRefresh != nil {
		if err := m.embyRefresh.RefreshOnCreate(ctx, strmPath); err != nil {
			logger.S().Warnf("[Monitor] Emby 刷库安排失败 path=%s: %v", strmPath, err)
		}
	}

	return nil
}

// ==================== handleDeleteEvent：删除 STRM ====================

// handleDeleteEvent 删除 STRM 文件及相关文件
// 对齐 TS handleDeleteEvent（简化版：不做网盘二次验证）
func (m *Monitor) handleDeleteEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
) error {
	config := m.settingsFn()

	// 文件夹事件：递归删除目录
	if event.Category == 0 {
		if err := os.RemoveAll(mapping.localPath); err != nil {
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

	// 文件事件：删除 STRM + 相关文件
	strmFileName := getStrmFileName(event.FileName)
	strmPath := filepath.Join(filepath.Dir(mapping.localPath), strmFileName)

	// 删除 STRM 文件
	if err := os.Remove(strmPath); err != nil && !os.IsNotExist(err) {
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

// ==================== handleMoveEvent：移动 STRM ====================

// handleMoveEvent 移动 STRM 到新路径
// 简化版：采用 recreate 模式（在新路径创建，旧路径由一致性检查清理）
// 对齐 TS handleMoveEvent（recreate 模式）
func (m *Monitor) handleMoveEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
) error {
	err := m.handleCreateEvent(ctx, account, event, mapping, cloudPath, false)
	if err == nil {
		m.appendLog(ctx, account, "move", true, cloudPath, mapping.localPath,
			"移动事件已处理（recreate 模式）")
		m.notifyMove(ctx, account, cloudPath, "文件", mapping.localPath)
	}
	return err
}

// ==================== handleRenameEvent：重命名 STRM ====================

// handleRenameEvent 重命名 STRM 文件
// 简化版：采用 recreate 模式（在新路径创建，旧路径由一致性检查清理）
// 对齐 TS handleRenameEvent（recreate 模式）
func (m *Monitor) handleRenameEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	mapping *pathMapping,
	cloudPath string,
) error {
	err := m.handleCreateEvent(ctx, account, event, mapping, cloudPath, false)
	if err == nil {
		m.appendLog(ctx, account, "rename", true, cloudPath, mapping.localPath,
			"重命名事件已处理（recreate 模式）")
		m.notifyRename(ctx, account, cloudPath, "文件", mapping.localPath)
	}
	return err
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

// getStrmFileName 将文件名转换为 .strm 扩展名
// 对齐 TS getStrmFileName
func getStrmFileName(fileName string) string {
	ext := filepath.Ext(fileName)
	if ext == "" {
		return fileName + ".strm"
	}
	// 大小写不敏感地替换扩展名
	return strings.TrimSuffix(fileName, ext) + ".strm"
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
// 对齐 TS generateStrmContent
func generateStrmContent(cloudPath, strmPrefix string, enablePathEncoding, enable302 bool, account, pickcode, fileName string) string {
	prefix := strings.TrimRight(strmPrefix, "/")
	var u string
	if enable302 {
		u = fmt.Sprintf("%s/api/fs/get?account=%s&pickcode=%s", prefix, account, pickcode)
		if fileName != "" {
			u += "&file_name=" + url.QueryEscape(fileName)
		}
	} else {
		u = fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s", prefix, account, pickcode)
		if fileName != "" {
			u += "&file_name=" + url.QueryEscape(fileName)
		}
	}
	return u + "\n"
}

// writeStrmFile 原子写入 STRM 文件（先写 tmp 再 rename）
func writeStrmFile(strmPath, content string) error {
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

// ==================== STRM 通知（统一 Notification 对象 + 富文本 HTML 格式） ====================

// notifyCreate 发送创建通知
func (m *Monitor) notifyCreate(ctx context.Context, account, cloudPath, kindLabel, localPath string, size int64) {
	if m.notifier == nil {
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
	if !m.tryDispatchNotification(ctx, n) {
		if err := m.notifier.Notify(ctx, n.Content); err != nil {
			logger.S().Warnf("[Monitor] 创建通知发送失败: %v", err)
		}
	}
}

// notifyDelete 发送删除通知
func (m *Monitor) notifyDelete(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildDeleteNotification(notify.STRMDeleteInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if !m.tryDispatchNotification(ctx, n) {
		if err := m.notifier.Notify(ctx, n.Content); err != nil {
			logger.S().Warnf("[Monitor] 删除通知发送失败: %v", err)
		}
	}
}

// notifyMove 发送移动通知
func (m *Monitor) notifyMove(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildMoveNotification(notify.STRMMoveInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if !m.tryDispatchNotification(ctx, n) {
		if err := m.notifier.Notify(ctx, n.Content); err != nil {
			logger.S().Warnf("[Monitor] 移动通知发送失败: %v", err)
		}
	}
}

// notifyRename 发送重命名通知
func (m *Monitor) notifyRename(ctx context.Context, account, cloudPath, kindLabel, localPath string) {
	if m.notifier == nil {
		return
	}
	builder := notify.NewStrmNotifyBuilder()
	n := builder.BuildRenameNotification(notify.STRMRenameInput{
		Account:   account,
		Kind:      kindLabel,
		CloudPath: cloudPath,
		LocalPath: localPath,
	})
	if !m.tryDispatchNotification(ctx, n) {
		if err := m.notifier.Notify(ctx, n.Content); err != nil {
			logger.S().Warnf("[Monitor] 重命名通知发送失败: %v", err)
		}
	}
}
