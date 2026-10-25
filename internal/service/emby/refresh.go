// Package emby 封装 Emby REST API 客户端、Webhook 事件分发与删除同步
// refresh.go 媒体服务器刷新服务：带防抖的 Emby 刷新
package emby

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// 刷库并发与排队上限：全量任务会一次安排大量目录，必须限制同时打向 Emby 的请求数。
const (
	refreshConcurrency  = 4
	refreshQueueTimeout = 30 * time.Second
)

// MediaServerRefresh 媒体服务器刷新服务
// 对齐 qmediasync：不缓存 Client，每次动态创建
type MediaServerRefresh struct {
	settingsFn func() model.EmbySettings

	debounceMap map[string]*time.Timer
	mu          sync.Mutex
	sem         chan struct{}
}

// NewMediaServerRefresh 创建刷新服务
// 注意：不再接收 client 参数，改为动态创建
func NewMediaServerRefresh(_ *Client, settingsFn func() model.EmbySettings) *MediaServerRefresh {
	return &MediaServerRefresh{
		settingsFn:  settingsFn,
		debounceMap: make(map[string]*time.Timer),
		sem:         make(chan struct{}, refreshConcurrency),
	}
}

// getClient 从当前设置动态创建 Emby Client
func (r *MediaServerRefresh) getClient() *Client {
	s := r.settingsFn()
	if s.URL == "" || s.APIKey == "" {
		return nil
	}
	return NewClient(s.URL, s.APIKey)
}

// RefreshByPath 按路径刷新（按目录防抖 + 有界并发）。
// 同一目录下的多个文件只保留一个 timer，避免全量任务生成上万个刷库请求；
// 刷新父目录时 Recursive=true 已覆盖其下所有新增/删除项。
func (r *MediaServerRefresh) RefreshByPath(ctx context.Context, filePath string) error {
	if r == nil {
		return nil
	}

	settings := r.settingsFn()
	if !settings.RefreshOnCreate && !settings.RefreshOnDelete {
		return nil
	}

	if filePath == "" {
		return nil
	}

	target := filepath.Dir(filepath.Clean(filePath))

	delay := 10 * time.Second
	if settings.DebounceSeconds > 0 {
		delay = time.Duration(settings.DebounceSeconds) * time.Second
	}

	r.mu.Lock()
	if oldTimer, ok := r.debounceMap[target]; ok {
		oldTimer.Stop()
	}

	timer := time.AfterFunc(delay, func() {
		r.runRefresh(target)
		r.mu.Lock()
		delete(r.debounceMap, target)
		r.mu.Unlock()
	})
	r.debounceMap[target] = timer
	r.mu.Unlock()

	logger.S().Debugf("[Emby] 已安排刷库(目录): %s 触发=%s (延迟 %v)", target, filePath, delay)
	return nil
}

// runRefresh 在并发上限内执行单个目录刷新；排队超时则跳过，避免 goroutine 堆积。
func (r *MediaServerRefresh) runRefresh(dir string) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-time.After(refreshQueueTimeout):
		logger.S().Warnf("[Emby] 刷库排队超时，跳过: %s", dir)
		return
	}
	r.doRefresh(context.Background(), dir)
}

// RefreshOnCreate 创建后刷新
func (r *MediaServerRefresh) RefreshOnCreate(ctx context.Context, filePath string) error {
	if r == nil {
		return nil
	}
	settings := r.settingsFn()
	if !settings.RefreshOnCreate {
		return nil
	}
	return r.RefreshByPath(ctx, filePath)
}

// RefreshOnDelete 删除后刷新
func (r *MediaServerRefresh) RefreshOnDelete(ctx context.Context, filePath string) error {
	if r == nil {
		return nil
	}
	settings := r.settingsFn()
	if !settings.RefreshOnDelete {
		return nil
	}
	return r.RefreshByPath(ctx, filePath)
}

// doRefresh 执行实际刷新
func (r *MediaServerRefresh) doRefresh(ctx context.Context, filePath string) {
	logger.S().Infof("[Emby] 开始刷库: %s", filePath)

	client := r.getClient()
	if client == nil {
		logger.S().Warnf("[Emby] Emby 未配置，跳过刷库: %s", filePath)
		return
	}

	itemID := r.findItemByPathRecursive(ctx, filePath)
	if itemID == "" {
		logger.S().Warnf("[Emby] 无法找到 Item: %s", filePath)
		return
	}

	opts := DefaultRefreshOptions()
	if err := client.RefreshItem(ctx, itemID, opts); err != nil {
		logger.S().Warnf("[Emby] 刷新失败 item=%s path=%s: %v", itemID, filePath, err)
		return
	}

	logger.S().Infof("[Emby] 刷新成功: %s → item=%s", filePath, itemID)
}

// findItemByPathRecursive 递归父目录查找 Item
func (r *MediaServerRefresh) findItemByPathRecursive(ctx context.Context, filePath string) string {
	client := r.getClient()
	if client == nil {
		return ""
	}
	path := filePath
	for {
		item, err := client.FindItemByPath(ctx, path)
		if err != nil {
			logger.S().Debugf("[Emby] 查找 Item 失败 path=%s: %v", path, err)
		} else if item != nil {
			return item.ID
		}

		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return ""
}

// CancelPending 取消所有待执行的刷新
func (r *MediaServerRefresh) CancelPending() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for path, timer := range r.debounceMap {
		timer.Stop()
		delete(r.debounceMap, path)
	}
	logger.S().Debug("[Emby] 已取消所有待执行的刷库任务")
}

// PendingCount 获取待执行刷新数量
func (r *MediaServerRefresh) PendingCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.debounceMap)
}

// String 返回调试信息
func (r *MediaServerRefresh) String() string {
	if r == nil {
		return "MediaServerRefresh{nil}"
	}
	return fmt.Sprintf("MediaServerRefresh{pending=%d}", r.PendingCount())
}

// RefreshLibrary 按库类型刷新媒体库
// libType: "movie" | "tv" | "all"
func (r *MediaServerRefresh) RefreshLibrary(ctx context.Context, libType string) error {
	if r == nil {
		return fmt.Errorf("media server refresh not initialized")
	}

	settings := r.settingsFn()
	if settings.URL == "" || settings.APIKey == "" {
		return fmt.Errorf("emby not configured")
	}

	client := r.getClient()
	if client == nil {
		return fmt.Errorf("emby client not configured")
	}

	libraryID := settings.LibraryID
	switch libType {
	case "movie", "tv", "series":
		if libraryID == "" {
			if err := client.RefreshLibrary(ctx, ""); err != nil {
				return fmt.Errorf("refresh %s library failed: %w", libType, err)
			}
			logger.S().Infof("[Emby] %s 库刷新已触发 (全部库)", libType)
			return nil
		}
		if err := client.RefreshLibrary(ctx, libraryID); err != nil {
			return fmt.Errorf("refresh %s library failed: %w", libType, err)
		}
		logger.S().Infof("[Emby] %s 库刷新已触发 (id=%s)", libType, libraryID)
		return nil
	case "all":
		if err := client.RefreshLibrary(ctx, ""); err != nil {
			return fmt.Errorf("refresh all libraries failed: %w", err)
		}
		logger.S().Infof("[Emby] 全部媒体库刷新已触发")
		return nil
	default:
		return fmt.Errorf("unknown library type: %s", libType)
	}
}

// GetStatus 获取 Emby 连接状态
func (r *MediaServerRefresh) GetStatus() map[string]any {
	result := map[string]any{
		"connected": false,
		"url":       "",
	}

	if r == nil {
		return result
	}

	settings := r.settingsFn()
	result["url"] = settings.URL

	if settings.URL == "" || settings.APIKey == "" {
		return result
	}

	client := r.getClient()
	if client != nil {
		if err := client.Ping(context.Background()); err == nil {
			result["connected"] = true
		}
	}

	return result
}
