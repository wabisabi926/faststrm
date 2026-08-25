// Package emby 封装 Emby REST API 客户端、Webhook 事件分发与删除同步
// 对齐 frontend/src/lib/emby/*.ts
package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

const (
	// DefaultTimeout HTTP 默认超时（对齐 TS DEFAULT_TIMEOUT）
	DefaultTimeout = 10 * time.Second
)

// ==================== 类型定义（对齐 TS emby/types.ts） ====================

// WebhookEvent Emby Webhook 事件载荷
// 参考：https://github.com/MediaBrowser/Wiki/blob/master/Webhook.md
type WebhookEvent struct {
	Event        string        `json:"Event"`
	User         *UserInfo     `json:"User,omitempty"`
	Item         *ItemInfo     `json:"Item,omitempty"`
	Server       *ServerInfo   `json:"Server,omitempty"`
	DeviceID     string        `json:"DeviceId,omitempty"`
	DeviceName   string        `json:"DeviceName,omitempty"`
	Client       string        `json:"Client,omitempty"`
	AppVersion   string        `json:"ApplicationVersion,omitempty"`
	PlaybackInfo *PlaybackInfo `json:"PlaybackInfo,omitempty"`
}

// PlaybackInfo 播放事件附带的播放状态信息（对齐 qmediasync EmbyPlaybackInfo）
type PlaybackInfo struct {
	PositionTicks  int64        `json:"PositionTicks,omitempty"`
	PlaySessionID  string       `json:"PlaySessionId,omitempty"`
	PlaybackMethod string       `json:"PlayMethod,omitempty"`
	IsPaused       bool         `json:"IsPaused,omitempty"`
	IsAutomated    bool         `json:"IsAutomated,omitempty"`
	MediaSource    *MediaSource `json:"MediaSource,omitempty"`
}

// MediaSource 媒体源信息（对齐 qmediasync EmbyMediaSource）
type MediaSource struct {
	RunTimeTicks int64 `json:"RunTimeTicks,omitempty"`
}

// UserInfo Emby 用户信息
type UserInfo struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// ItemInfo Emby Webhook 项基础信息（含 Path 用于删除同步）
type ItemInfo struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type,omitempty"`
	MediaType         string            `json:"MediaType,omitempty"`
	Path              string            `json:"Path,omitempty"`
	SeriesName        string            `json:"SeriesName,omitempty"`
	SeriesID          string            `json:"SeriesId,omitempty"`
	SeasonID          string            `json:"SeasonId,omitempty"`
	ParentIndexNumber int               `json:"ParentIndexNumber,omitempty"`
	IndexNumber       int               `json:"IndexNumber,omitempty"`
	ImageTags         map[string]string `json:"ImageTags,omitempty"`
}

// ServerInfo Emby 服务器信息
type ServerInfo struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// ItemDetail Emby Items/{id} 详情响应（对齐 TS EmbyItemDetail）
type ItemDetail struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type,omitempty"`
	MediaType         string            `json:"MediaType,omitempty"`
	Overview          string            `json:"Overview,omitempty"`
	DateCreated       string            `json:"DateCreated,omitempty"`
	RunTimeTicks      int64             `json:"RunTimeTicks,omitempty"`
	ImageTags         map[string]string `json:"ImageTags,omitempty"`
	SeriesName        string            `json:"SeriesName,omitempty"`
	SeasonName        string            `json:"SeasonName,omitempty"`
	IndexNumber       int               `json:"IndexNumber,omitempty"`
	ParentIndexNumber int               `json:"ParentIndexNumber,omitempty"`
	ProductionYear    int               `json:"ProductionYear,omitempty"`
	CommunityRating   float64           `json:"CommunityRating,omitempty"`
	Genres            []string          `json:"Genres,omitempty"`
	People            []Person          `json:"People,omitempty"`
}

// Person Emby 人物信息
type Person struct {
	Name string `json:"Name"`
	Type string `json:"Type"`
}

// UserPolicy Emby 用户权限
type UserPolicy struct {
	EnableAllFolders bool `json:"EnableAllFolders"`
}

// UserDto Emby 用户信息（对齐 qmediasync UserDto）
type UserDto struct {
	Name   string     `json:"Name"`
	ID     string     `json:"Id"`
	Policy UserPolicy `json:"Policy"`
}

// ==================== Client ====================

// Client Emby REST API 客户端
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	// 缓存的有权限用户 ID（对齐 qmediasync embyUserId）
	// 避免每次获取详情都重复请求用户列表
	mu         sync.Mutex
	embyUserID string

	// onUserIDChange 用户ID变更回调（Notifier用于跨Client实例缓存userID）
	// 获取到userID时回调传入userID，失效时回调传入空字符串
	onUserIDChange func(userID string)
}

// NewClient 创建 Emby 客户端
// 自动剥离 URL 末尾的 /emby 路径（用户可能配置 http://host/emby 或 http://host:port）
func NewClient(baseURL, apiKey string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	// 移除可能的 /emby 前缀（Emby API 路径已由各方法拼接）
	baseURL = strings.TrimSuffix(baseURL, "/emby")
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// GetItemDetail 查询媒体详情（对齐 qmediasync GetEmbyItemDetail 实现）
// 1. 获取有权限用户（EnableAllFolders=true）
// 2. 使用用户上下文查询详情
// 3. 若没有权限用户，回退尝试所有用户
// 4. 最终降级到无用户上下文
func (c *Client) GetItemDetail(ctx context.Context, itemID string) (*ItemDetail, error) {
	if c.baseURL == "" || c.apiKey == "" || itemID == "" {
		return nil, fmt.Errorf("invalid params: baseURL empty=%v apiKey empty=%v itemID=%q", c.baseURL == "", c.apiKey == "", itemID)
	}

	// 1. 获取有权限的用户（对齐 qmediasync）
	userID, err := c.getEmbyUserID(ctx)
	if err == nil && userID != "" {
		detail, err := c.GetItemDetailByUser(ctx, itemID, userID)
		if err == nil {
			return detail, nil
		}
		// 用户上下文失败，清除缓存
		logger.S().Warnf("[Emby] 用户 %s 获取详情失败: %v", userID, err)
		c.InvalidateUserCache()
	}

	// 2. 回退：尝试所有用户（不强制要求 EnableAllFolders）
	detail, err := c.tryGetDetailWithAnyUser(ctx, itemID)
	if err == nil {
		return detail, nil
	}

	// 3. 最终降级：使用无用户上下文
	logger.S().Warnf("[Emby] 用户上下文获取详情失败，降级到无用户端点: %v", err)
	return c.getItemDetailWithoutUser(ctx, itemID)
}

// getItemDetailWithoutUser 使用无用户上下文的端点获取详情
// 与 qmediasync 保持一致：不使用 url 编码
func (c *Client) getItemDetailWithoutUser(ctx context.Context, itemID string) (*ItemDetail, error) {
	u := fmt.Sprintf("%s/emby/Items/%s?api_key=%s",
		c.baseURL,
		itemID,
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request emby: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("emby status %d for item %s (no-user mode)", resp.StatusCode, itemID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var detail ItemDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return &detail, nil
}

// tryGetDetailWithAnyUser 尝试使用任何可用用户获取详情
// 当没有 EnableAllFolders=true 的用户时，回退尝试所有用户
func (c *Client) tryGetDetailWithAnyUser(ctx context.Context, itemID string) (*ItemDetail, error) {
	// 获取所有用户（不筛选权限）
	users, err := c.GetAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all users: %w", err)
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("no users found")
	}

	// 尝试每个用户获取详情
	var lastErr error
	for _, user := range users {
		detail, err := c.GetItemDetailByUser(ctx, itemID, user.ID)
		if err == nil && detail != nil {
			logger.S().Debugf("[Emby] 使用用户 %s 获取详情成功", user.ID)
			return detail, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all %d users failed: %v", len(users), lastErr)
}

// GetItemDetailWithRetry 查询媒体详情，带重试机制
// 对齐 qmediasync 实现：
// - 处理 Emby webhook 先于 item 入库的时序问题
// - 处理临时网络错误、用户上下文暂时失效等场景
// - 失败时返回 nil（qmediasync 风格：不发送降级通知）
func (c *Client) GetItemDetailWithRetry(ctx context.Context, itemID string) (*ItemDetail, error) {
	const maxRetries = 5
	const initialDelay = 500 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		detail, err := c.GetItemDetail(ctx, itemID)
		if err == nil && detail != nil {
			if i > 0 {
				logger.S().Infof("[Emby] 重试获取详情成功 itemID=%s (第%d次)", itemID, i+1)
			}
			return detail, nil
		}

		lastErr = err
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "emby status 404") {
				logger.S().Debugf("[Emby] 详情暂不可用(404)，重试 itemID=%s (第%d次): %v", itemID, i+1, err)
			} else {
				logger.S().Debugf("[Emby] 获取详情失败，重试 itemID=%s (第%d次): %v", itemID, i+1, err)
			}
		}

		// 最后一次失败
		if i == maxRetries-1 {
			logger.S().Errorf("[Emby] 获取详情最终失败 itemID=%s: %v", itemID, lastErr)
			return nil, lastErr
		}

		// 指数退避
		delay := initialDelay * time.Duration(1<<uint(i))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// BuildImageURL 构造主图 URL：/emby/Items/{id}/Images/Primary?maxWidth=...
// 注意：无论该 Item 是否实际拥有 Primary 图都会返回 URL，外层需调用 BuildImageURLIfAvailable 做存在性判断
func (c *Client) BuildImageURL(itemID string, maxWidth int) string {
	if c.baseURL == "" || c.apiKey == "" || itemID == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 400
	}
	// 与 qmediasync 保持一致：不使用 url.QueryEscape 编码 apiKey
	return fmt.Sprintf("%s/emby/Items/%s/Images/Primary?maxWidth=%d&api_key=%s",
		c.baseURL,
		itemID,
		maxWidth,
		c.apiKey,
	)
}

// BuildImageURLIfAvailable 仅当 imageTags 里存在图片时才返回合法 URL（避免 Telegram 404 后降级成纯文本）
// 优先级：Backdrop（横版背景，通知里视觉效果更好） > Primary（竖版海报）
// 注意：Emby ImageTags 的 key 大小写不固定（有 "backdrop"/"Backdrop"/"primary"/"Primary" 等变体），因此做大小写不敏感查找
//
// 适用场景：入库/删除通知（横版背景图视觉更佳）。
// 播放通知请用 BuildPrimaryImageURL（只取竖版海报，对齐 QMS 播放通知图片策略）。
func (c *Client) BuildImageURLIfAvailable(itemID string, imageTags map[string]string, maxWidth int) string {
	if len(imageTags) == 0 {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 400
	}
	// 通用：大小写不敏感取 imageTag
	getTag := func(keys ...string) string {
		for _, k := range keys {
			for mk, mv := range imageTags {
				if strings.EqualFold(mk, k) && mv != "" {
					return mv
				}
			}
		}
		return ""
	}
	// 优先 Backdrop 背景图（Telegram 大图展示更好看）
	if tag := getTag("Backdrop", "backdrop"); tag != "" {
		return fmt.Sprintf("%s/emby/Items/%s/Images/Backdrop?tag=%s&maxWidth=%d&api_key=%s",
			c.baseURL,
			itemID,
			tag,
			maxWidth,
			c.apiKey,
		)
	}
	// 其次 Primary 海报
	if tag := getTag("Primary", "primary", "Thumb"); tag != "" {
		return fmt.Sprintf("%s/emby/Items/%s/Images/Primary?tag=%s&maxWidth=%d&api_key=%s",
			c.baseURL,
			itemID,
			tag,
			maxWidth,
			c.apiKey,
		)
	}
	return ""
}

// BuildPrimaryImageURL 仅当 imageTags 里存在 Primary 时才返回竖版海报 URL
// 对齐 QMS 播放通知图片策略：播放通知只用 Primary（竖版海报），不用 Backdrop（横版背景）
// 理由：播放通知含季集/进度等文字信息，竖版海报视觉更协调；Backdrop 横版图在播放通知里不搭
func (c *Client) BuildPrimaryImageURL(itemID string, imageTags map[string]string, maxWidth int) string {
	if len(imageTags) == 0 {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 400
	}
	// 大小写不敏感取 Primary/Thumb
	for mk, mv := range imageTags {
		if (strings.EqualFold(mk, "Primary") || strings.EqualFold(mk, "Thumb")) && mv != "" {
			return fmt.Sprintf("%s/emby/Items/%s/Images/Primary?tag=%s&maxWidth=%d&api_key=%s",
				c.baseURL,
				itemID,
				mv,
				maxWidth,
				c.apiKey,
			)
		}
	}
	return ""
}

// ==================== 媒体库刷新 ====================

// RefreshOptions 刷新选项
type RefreshOptions struct {
	Recursive          bool   // 是否递归刷新子项
	MetadataMode       string // MetadataRefreshMode: Default/FullRefresh
	ImageMode          string // ImageRefreshMode: Default/FullRefresh
	ReplaceAllMetadata bool   // 是否替换所有元数据
	ReplaceAllImages   bool   // 是否替换所有图片
}

// DefaultRefreshOptions 返回默认刷新选项
func DefaultRefreshOptions() *RefreshOptions {
	return &RefreshOptions{
		Recursive:    true,
		MetadataMode: "FullRefresh",
		ImageMode:    "FullRefresh",
	}
}

// FindItemByPath 根据路径查找 Emby Item
// GET /emby/Items?Path={path}&Recursive=true&Fields=Path&IncludeItemTypes=Movie,Episode,Series,Folder
func (c *Client) FindItemByPath(ctx context.Context, path string) (*ItemInfo, error) {
	if c.baseURL == "" || c.apiKey == "" || path == "" {
		return nil, fmt.Errorf("invalid params: baseURL empty=%v apiKey empty=%v path=%q", c.baseURL == "", c.apiKey == "", path)
	}

	// 路径需要编码（包含 /, \, 空格等特殊字符），但 apiKey 不应编码
	u := fmt.Sprintf("%s/emby/Items?Path=%s&Recursive=true&Fields=Path&IncludeItemTypes=Movie,Episode,Series,Folder&api_key=%s",
		c.baseURL,
		url.QueryEscape(path),
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request emby: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("emby find item status %d for path %s", resp.StatusCode, path)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Emby 返回格式: { "Items": [...] }
	var result struct {
		Items []ItemInfo `json:"Items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	// 精确匹配路径
	for _, item := range result.Items {
		if item.Path == path {
			return &item, nil
		}
	}

	// 没有找到精确匹配
	return nil, nil
}

// RefreshItem 刷新单个 Item
// POST /emby/Items/{id}/Refresh
func (c *Client) RefreshItem(ctx context.Context, itemID string, opts *RefreshOptions) error {
	if c.baseURL == "" || c.apiKey == "" || itemID == "" {
		return fmt.Errorf("invalid params: baseURL empty=%v apiKey empty=%v itemID=%q", c.baseURL == "", c.apiKey == "", itemID)
	}

	if opts == nil {
		opts = DefaultRefreshOptions()
	}

	// 与 qmediasync 保持一致：不使用 url.PathEscape 编码 itemID
	u := fmt.Sprintf("%s/emby/Items/%s/Refresh",
		c.baseURL,
		itemID,
	)

	// 构建查询参数（api_key 直接设置，不编码）
	params := url.Values{}
	params.Set("Recursive", fmt.Sprintf("%t", opts.Recursive))
	params.Set("MetadataRefreshMode", opts.MetadataMode)
	params.Set("ImageRefreshMode", opts.ImageMode)
	params.Set("ReplaceAllMetadata", fmt.Sprintf("%t", opts.ReplaceAllMetadata))
	params.Set("ReplaceAllImages", fmt.Sprintf("%t", opts.ReplaceAllImages))
	params.Set("api_key", c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request emby: %w", err)
	}
	defer resp.Body.Close()

	// 200 OK 或 204 No Content 都算成功
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, bread := io.ReadAll(resp.Body)
		if bread != nil {
			return fmt.Errorf("emby refresh failed: status=%d", resp.StatusCode)
		}
		return fmt.Errorf("emby refresh failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	return nil
}

// RefreshLibrary 刷新指定媒体库
// POST /emby/Library/Refresh?LibraryId={id}
func (c *Client) RefreshLibrary(ctx context.Context, libraryID string) error {
	if c.baseURL == "" || c.apiKey == "" {
		return fmt.Errorf("invalid params: baseURL=%v apiKey=%v", c.baseURL == "", c.apiKey == "")
	}

	// 与 qmediasync 保持一致：不使用 url.QueryEscape 编码 libraryID 和 apiKey
	u := fmt.Sprintf("%s/emby/Library/Refresh?LibraryId=%s&api_key=%s",
		c.baseURL,
		libraryID,
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request emby: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, bread := io.ReadAll(resp.Body)
		if bread != nil {
			return fmt.Errorf("emby library refresh failed: status=%d", resp.StatusCode)
		}
		return fmt.Errorf("emby library refresh failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	return nil
}

// Ping 检测 Emby 服务器连通性
// GET /emby/System/Info/Public
func (c *Client) Ping(ctx context.Context) error {
	if c.baseURL == "" || c.apiKey == "" {
		return fmt.Errorf("emby not configured")
	}

	u := fmt.Sprintf("%s/emby/System/Info/Public", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("emby ping failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("emby ping failed: status=%d", resp.StatusCode)
	}

	return nil
}

// ==================== 用户上下文（对齐 qmediasync GetEmbyItemDetail 逻辑） ====================

// GetUsersWithAllLibrariesAccess 获取有权访问所有媒体库的用户列表
// GET /emby/Users?api_key=... 筛选 Policy.EnableAllFolders=true 的用户
// 对齐 qmediasync 实现：不额外编码 API Key
func (c *Client) GetUsersWithAllLibrariesAccess(ctx context.Context) ([]UserDto, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return nil, fmt.Errorf("emby not configured")
	}

	// 与 qmediasync 保持一致：直接拼接，不使用 url.QueryEscape
	u := fmt.Sprintf("%s/emby/Users?api_key=%s", c.baseURL, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request emby users: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("emby get users status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var users []UserDto
	if err := json.Unmarshal(body, &users); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	// 筛选有权限的用户
	var usersWithAllAccess []UserDto
	for _, user := range users {
		if user.Policy.EnableAllFolders {
			usersWithAllAccess = append(usersWithAllAccess, user)
		}
	}
	return usersWithAllAccess, nil
}

// GetAllUsers 获取所有用户列表（不筛选权限）
// 用于回退场景：当没有 EnableAllFolders=true 的用户时，尝试所有用户
func (c *Client) GetAllUsers(ctx context.Context) ([]UserDto, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return nil, fmt.Errorf("emby not configured")
	}

	// 与 qmediasync 保持一致：直接拼接，不使用 url.QueryEscape
	u := fmt.Sprintf("%s/emby/Users?api_key=%s", c.baseURL, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request emby users: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("emby get users status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var users []UserDto
	if err := json.Unmarshal(body, &users); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	// 排除禁用的用户（Policy 中 Enabled=false）
	var activeUsers []UserDto
	for _, user := range users {
		activeUsers = append(activeUsers, user) // 简单起见，包含所有用户
	}
	return activeUsers, nil
}

// getEmbyUserID 获取缓存的有权限用户 ID
// 首次调用时查询用户列表并缓存，后续直接返回缓存值
func (c *Client) getEmbyUserID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.embyUserID != "" {
		return c.embyUserID, nil
	}

	users, err := c.GetUsersWithAllLibrariesAccess(ctx)
	if err != nil {
		return "", fmt.Errorf("get emby users: %w", err)
	}
	if len(users) == 0 {
		return "", fmt.Errorf("no user with access to all libraries found")
	}

	c.embyUserID = users[0].ID
	// 回调通知 Notifier 缓存（跨 Client 实例复用，对齐 qmediasync 全局 embyUserId）
	if c.onUserIDChange != nil {
		c.onUserIDChange(c.embyUserID)
	}
	return c.embyUserID, nil
}

// InvalidateUserCache 清除用户缓存（用户权限变更时调用）
func (c *Client) InvalidateUserCache() {
	c.mu.Lock()
	c.embyUserID = ""
	c.mu.Unlock()
	// 回调通知 Notifier 清除缓存
	if c.onUserIDChange != nil {
		c.onUserIDChange("")
	}
}

// GetItemDetailByUser 使用用户上下文查询媒体详情
// GET /emby/Users/{userID}/Items/{id}?api_key=...
// 对齐 qmediasync GetItemDetailByUser 实现：不额外编码 ID 和 API Key
func (c *Client) GetItemDetailByUser(ctx context.Context, itemID, userID string) (*ItemDetail, error) {
	if c.baseURL == "" || c.apiKey == "" || itemID == "" {
		return nil, fmt.Errorf("invalid params: baseURL empty=%v apiKey empty=%v itemID=%q", c.baseURL == "", c.apiKey == "", itemID)
	}

	// 与 qmediasync 保持一致：直接拼接字符串，不使用 url 编码
	// 注意：不要使用 url.PathEscape/QueryEscape，否则会改变 ID 和 API Key 的值
	u := fmt.Sprintf("%s/emby/Users/%s/Items/%s?api_key=%s",
		c.baseURL,
		userID,
		itemID,
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request emby: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("emby status %d for item %s", resp.StatusCode, itemID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var detail ItemDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return &detail, nil
}
