// Package client115 115 网盘 API 客户端
// life.go 实现 115 生活事件（Life）API 客户端
// 对齐 p115client life_behavior_detail / life_calendar_setoption
package client115

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 事件类型常量 ====================

// BehaviorTypeToName 行为类型(int) -> 名称(string) 映射
// 对齐 p115client BEHAVIOR_TYPE_TO_NAME
var BehaviorTypeToName = map[int]string{
	1:  "upload_image_file",
	2:  "upload_file",
	3:  "star_image",
	4:  "star_file",
	5:  "move_image_file",
	6:  "move_file",
	7:  "browse_image",
	8:  "browse_video",
	9:  "browse_audio",
	10: "browse_document",
	14: "receive_files",
	17: "new_folder",
	18: "copy_folder",
	19: "folder_label",
	20: "folder_rename",
	22: "delete_file",
	23: "copy_file",
	24: "file_rename",
}

// BehaviorNameToType 行为类型名称(string) -> 编号(int) 映射
var BehaviorNameToType = map[string]int{
	"upload_image_file":  1,
	"upload_file":        2,
	"star_image":         3,
	"star_file":          4,
	"move_image_file":    5,
	"move_file":          6,
	"browse_image":       7,
	"browse_video":       8,
	"browse_audio":       9,
	"browse_document":    10,
	"receive_files":      14,
	"new_folder":         17,
	"copy_folder":        18,
	"folder_label":       19,
	"folder_rename":      20,
	"delete_file":        22,
	"copy_file":          23,
	"file_rename":        24,
}

// CreateEventTypes 创建类事件（上传/新建/复制/接收）
var CreateEventTypes = map[int]bool{1: true, 2: true, 14: true, 17: true, 18: true, 23: true}

// MoveEventTypes 移动事件类型
var MoveEventTypes = map[int]bool{5: true, 6: true}

// RenameEventTypes 重命名事件类型
var RenameEventTypes = map[int]bool{20: true, 24: true}

// DeleteEventTypes 删除事件类型
var DeleteEventTypes = map[int]bool{22: true}

// IgnoreBehaviorTypes 忽略的行为类型（标星/浏览/标签等无操作事件）
// 对齐 p115client IGNORE_BEHAVIOR_TYPES
var IgnoreBehaviorTypes = map[int]bool{
	3:  true, // star_image 标星图片
	4:  true, // star_file 标星文件/目录
	7:  true, // browse_image 浏览图片
	8:  true, // browse_video 浏览视频
	9:  true, // browse_audio 浏览音频
	10: true, // browse_document 浏览文档
	19: true, // folder_label 标签文件夹
}

// ==================== LifeEventItem ====================

// LifeEventItem 115 生活事件条目
// 对齐 p115client life_behavior_detail 响应字段
type LifeEventItem struct {
	ID           string `json:"id"`
	Type         int    `json:"type"`
	BehaviorType string `json:"behavior_type,omitempty"`
	FileCategory int    `json:"file_category"`
	UpdateTime   int64  `json:"update_time"`
	FileID       string `json:"file_id"`
	FileName     string `json:"file_name"`
	ParentID     string `json:"parent_id"`
	PickCode     string `json:"pick_code"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"-"` // 由路径解析逻辑填充，不从 API 响应直接解析
}

// lifeEventRaw 用于灵活解析 115 life_behavior_detail API 响应
type lifeEventRaw struct {
	ID           any    `json:"id"`
	Type         any    `json:"type"`
	BehaviorType string `json:"behavior_type"`
	FileCategory any    `json:"file_category"`
	UpdateTime   any    `json:"update_time"`
	FileID       any    `json:"file_id"`
	FileName     string `json:"file_name"`
	ParentID     any    `json:"parent_id"`
	PickCode     string `json:"pick_code"`
	FileSize     any    `json:"file_size"`
	FilePath     string `json:"file_path,omitempty"`
}

// ==================== LifeClient ====================

// LifeClient 115 生活事件 API 客户端
type LifeClient struct {
	cookie     string
	httpClient *http.Client

	// 路径解析缓存
	pathCache   sync.Map      // key: parentID, value: cachedPathEntry
	pathCacheMu  sync.RWMutex
	pathCacheTTL time.Duration

	// API 域名轮换
	useAlternateHost bool
}

// NewLifeClient 创建生活事件客户端
func NewLifeClient(cookie string) *LifeClient {
	return &LifeClient{
		cookie: cookie,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		pathCacheTTL: 5 * time.Minute,
	}
}

// cachedPathEntry 路径缓存条目
type cachedPathEntry struct {
	path      string
	expiresAt time.Time
}

// getApiHost 返回当前使用的 API 域名
func (c *LifeClient) getApiHost() string {
	if c.useAlternateHost {
		return "https://proapi.115.com"
	}
	return "https://webapi.115.com"
}

// switchApiHost 切换 API 域名（用于风控规避）
func (c *LifeClient) switchApiHost() {
	c.useAlternateHost = !c.useAlternateHost
	logger.S().Warnf("[LifeClient] 切换 API 域名: useAlternate=%v", c.useAlternateHost)
}

// LifeShow 开启生活事件功能
// POST https://life.115.com/api/1.0/web/1.0/calendar/setoption
// 对齐 p115client life_calendar_setoption
func (c *LifeClient) LifeShow(ctx context.Context) error {
	if c.cookie == "" {
		return fmt.Errorf("cookie is empty")
	}

	endpoint := "https://life.115.com/api/1.0/web/1.0/calendar/setoption"
	form := url.Values{}
	form.Set("locus", "1")
	form.Set("open_life", "1")

	body, err := c.doRequest(ctx, http.MethodPost, endpoint, form.Encode())
	if err != nil {
		return fmt.Errorf("lifeShow request: %w", err)
	}

	var resp struct {
		State  bool   `json:"state"`
		ErrNo  int    `json:"errNo"`
		ErrNo2 int    `json:"errno"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse lifeShow response: %w (body=%s)", err, truncateBody(body, 256))
	}

	logger.S().Infof("[LifeClient] lifeShow resp: state=%v errNo=%d err=%s",
		resp.State, resp.ErrNo, resp.Error)

	if resp.State {
		return nil
	}

	errNo := resp.ErrNo
	if errNo == 0 {
		errNo = resp.ErrNo2
	}
	if errNo == 99 || errNo == 990001 {
		return fmt.Errorf("cookie 已过期 (errno=%d): %s", errNo, resp.Error)
	}
	if errNo != 0 {
		return fmt.Errorf("lifeShow 失败 errno=%d: %s", errNo, resp.Error)
	}
	return fmt.Errorf("lifeShow 失败: state=false, body=%s", truncateBody(body, 256))
}

// PullEvents 拉取生活事件列表
// GET https://webapi.115.com/behavior/detail?limit=1000&offset=0
// 对齐 p115client life_behavior_detail
func (c *LifeClient) PullEvents(ctx context.Context, account string, offset int64) ([]LifeEventItem, int64, error) {
	if c.cookie == "" {
		return nil, -1, fmt.Errorf("cookie is empty")
	}

	apiHost := c.getApiHost()
	endpoint := fmt.Sprintf(
		"%s/behavior/detail?limit=1000&offset=%d",
		apiHost,
		offset,
	)

	body, err := c.doRequest(ctx, http.MethodGet, endpoint, "")
	if err != nil {
		// 主域名失败时尝试备用域名
		if !c.useAlternateHost {
			c.switchApiHost()
			endpoint = fmt.Sprintf("%s/behavior/detail?limit=1000&offset=%d", c.getApiHost(), offset)
			body, err = c.doRequest(ctx, http.MethodGet, endpoint, "")
		}
		if err != nil {
			return nil, -1, fmt.Errorf("pullEvents request: %w", err)
		}
	}

	var resp struct {
		Code  int    `json:"code"`
		Error string `json:"error,omitempty"`
		Data  struct {
			List     []lifeEventRaw `json:"list"`
			Count    int            `json:"count"`
			NextPage bool           `json:"next_page"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, -1, fmt.Errorf("parse pullEvents response: %w (body=%s)", err, truncateBody(body, 512))
	}

	if resp.Code != 0 {
		if resp.Code == 99 || resp.Code == 990001 {
			return nil, -1, fmt.Errorf("cookie 已过期 (code=%d): %s", resp.Code, resp.Error)
		}
		return nil, -1, fmt.Errorf("pullEvents 失败 code=%d: %s", resp.Code, resp.Error)
	}

	items := make([]LifeEventItem, 0, len(resp.Data.List))
	for _, raw := range resp.Data.List {
		items = append(items, raw.toLifeEventItem())
	}

	nextOffset := int64(-1)
	if resp.Data.NextPage && len(items) > 0 {
		nextOffset = offset + int64(len(items))
	}
	_ = account

	logger.S().Infof("[LifeClient] pulled %d events, offset=%d, next_offset=%d, count=%d",
		len(items), offset, nextOffset, resp.Data.Count)
	return items, nextOffset, nil
}

// doRequest 发送 HTTP 请求，返回响应体
func (c *LifeClient) doRequest(ctx context.Context, method, urlStr, body string) ([]byte, error) {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", DefaultUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	req.Header.Set("Cookie", c.cookie)

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}

	return respBody, nil
}

// ==================== 路径解析 ====================

// fsMediaAncestorNode 文件祖先节点
type fsMediaAncestorNode struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ParentID int    `json:"parent_id"`
}

// FsFilesMediaAncestors 通过 fs_files_media API 获取指定目录的祖先路径链
// GET https://webapi.115.com/files/medialist?cid={cid}&limit=1&type=6&nf=1
// 对齐 p115client fs_files_media
func (c *LifeClient) FsFilesMediaAncestors(ctx context.Context, cid string) ([]fsMediaAncestorNode, error) {
	if cid == "" || cid == "0" {
		return nil, nil // 根目录无祖先
	}

	apiHost := c.getApiHost()
	endpoint := fmt.Sprintf("%s/files/medialist?cid=%s&limit=1&type=6&nf=1", apiHost, url.QueryEscape(cid))

	body, err := c.doRequest(ctx, http.MethodGet, endpoint, "")
	if err != nil {
		// 如果主域名失败，尝试切换到备用域名
		if !c.useAlternateHost {
			c.switchApiHost()
			endpoint = fmt.Sprintf("%s/files/medialist?cid=%s&limit=1&type=6&nf=1", c.getApiHost(), url.QueryEscape(cid))
			body, err = c.doRequest(ctx, http.MethodGet, endpoint, "")
		}
		if err != nil {
			return nil, fmt.Errorf("fs_files_media request: %w", err)
		}
	}

	var resp struct {
		State    bool                 `json:"state"`
		Ancestors []fsMediaAncestorNode `json:"ancestors"`
		ErrMsg   string               `json:"errmsg,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse fs_files_media response: %w (body=%s)", err, truncateBody(body, 256))
	}
	if !resp.State {
		return nil, fmt.Errorf("fs_files_media state=false: %s", resp.ErrMsg)
	}
	return resp.Ancestors, nil
}

// ResolvePath 通过 parent_id 解析文件的完整云端路径
// 三级降级：缓存 → fs_files_media API → 仅使用文件名
func (c *LifeClient) ResolvePath(ctx context.Context, parentID, fileName string) string {
	if fileName == "" {
		return ""
	}

	// 根目录下的文件
	if parentID == "0" || parentID == "" {
		return "/" + fileName
	}

	cacheKey := parentID
	now := time.Now()

	// 一级：检查缓存
	if cached, ok := c.pathCache.Load(cacheKey); ok {
		entry := cached.(cachedPathEntry)
		if now.Before(entry.expiresAt) {
			return entry.path + "/" + fileName
		}
		c.pathCache.Delete(cacheKey)
	}

	// 二级：调用 API 解析祖先链
	ancestors, err := c.FsFilesMediaAncestors(ctx, parentID)
	if err != nil {
		logger.S().Warnf("[LifeClient] ResolvePath API 失败 parentID=%s: %v, 使用降级路径", parentID, err)
		// 三级降级：仅使用文件名（无法构建完整路径，但不至于完全失败）
		return "/unknown/" + fileName
	}

	// 构建路径
	var pathBuilder strings.Builder
	for _, node := range ancestors {
		if pathBuilder.Len() > 0 {
			pathBuilder.WriteString("/")
		}
		pathBuilder.WriteString(node.Name)
	}
	parentPath := pathBuilder.String()
	if parentPath == "" {
		parentPath = "/"
	}

	fullPath := parentPath + "/" + fileName

	// 写入缓存
	c.pathCache.Store(cacheKey, cachedPathEntry{
		path:      parentPath,
		expiresAt: now.Add(c.pathCacheTTL),
	})

	return fullPath
}

// toLifeEventItem 将灵活解析的 raw 转为强类型 LifeEventItem
func (r lifeEventRaw) toLifeEventItem() LifeEventItem {
	item := LifeEventItem{
		BehaviorType: r.BehaviorType,
		FileName:     r.FileName,
		PickCode:     r.PickCode,
		FilePath:     r.FilePath,
	}
	item.ID = lifeToString(r.ID)
	item.Type = lifeToInt(r.Type)
	item.FileCategory = lifeToInt(r.FileCategory)
	item.UpdateTime = lifeToInt64(r.UpdateTime)
	item.FileID = lifeToString(r.FileID)
	item.ParentID = lifeToString(r.ParentID)
	item.FileSize = lifeToInt64(r.FileSize)

	if item.BehaviorType == "" {
		if name, ok := BehaviorTypeToName[item.Type]; ok {
			item.BehaviorType = name
		}
	}
	return item
}

// lifeToInt 将 any 转为 int
func lifeToInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return 0
}

// lifeToInt64 将 any 转为 int64
func lifeToInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// lifeToString 将 any 转为 string
func lifeToString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// truncateBody 截断字节为字符串，超出长度截断
func truncateBody(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen])
}

// TypeNumberToString 115 数字类型 -> 分类字符串
func TypeNumberToString(t int) string {
	switch {
	case CreateEventTypes[t]:
		return "create"
	case DeleteEventTypes[t]:
		return "delete"
	case MoveEventTypes[t]:
		return "move"
	case RenameEventTypes[t]:
		return "rename"
	default:
		return "folder-sync"
	}
}
