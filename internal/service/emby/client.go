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
	"time"
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
	Event        string      `json:"Event"`
	User         *UserInfo   `json:"User,omitempty"`
	Item         *ItemInfo   `json:"Item,omitempty"`
	Server       *ServerInfo `json:"Server,omitempty"`
	DeviceID     string      `json:"DeviceId,omitempty"`
	DeviceName   string      `json:"DeviceName,omitempty"`
	Client       string      `json:"Client,omitempty"`
	AppVersion   string      `json:"ApplicationVersion,omitempty"`
	PlaybackInfo *PlaybackInfo `json:"PlaybackInfo,omitempty"`
}

// PlaybackInfo 播放事件附带的播放状态信息（部分事件会带）
type PlaybackInfo struct {
	PositionTicks    int64  `json:"PositionTicks,omitempty"`
	PlaybackMethod   string `json:"PlayMethod,omitempty"`
	IsPaused         bool   `json:"IsPaused,omitempty"`
	IsAutomated      bool   `json:"IsAutomated,omitempty"`
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

// ==================== Client ====================

// Client Emby REST API 客户端
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient 创建 Emby 客户端
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// GetItemDetail 查询媒体详情：GET /emby/Items/{id}?api_key=...
func (c *Client) GetItemDetail(ctx context.Context, itemID string) (*ItemDetail, error) {
	if c.baseURL == "" || c.apiKey == "" || itemID == "" {
		return nil, fmt.Errorf("invalid params: baseURL empty=%v apiKey empty=%v itemID=%q", c.baseURL == "", c.apiKey == "", itemID)
	}

	u := fmt.Sprintf("%s/emby/Items/%s?api_key=%s",
		c.baseURL,
		url.PathEscape(itemID),
		url.QueryEscape(c.apiKey),
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

// BuildImageURL 构造主图 URL：/emby/Items/{id}/Images/Primary?maxWidth=...
// 注意：无论该 Item 是否实际拥有 Primary 图都会返回 URL，外层需调用 BuildImageURLIfAvailable 做存在性判断
func (c *Client) BuildImageURL(itemID string, maxWidth int) string {
	if c.baseURL == "" || c.apiKey == "" || itemID == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 400
	}
	return fmt.Sprintf("%s/emby/Items/%s/Images/Primary?maxWidth=%d&api_key=%s",
		c.baseURL,
		url.PathEscape(itemID),
		maxWidth,
		url.QueryEscape(c.apiKey),
	)
}

// BuildImageURLIfAvailable 仅当 imageTags 里存在图片时才返回合法 URL（避免 Telegram 404 后降级成纯文本）
// 优先级：Backdrop（横版背景，通知里视觉效果更好） > Primary（竖版海报）
// 注意：Emby ImageTags 的 key 大小写不固定（有 "backdrop"/"Backdrop"/"primary"/"Primary" 等变体），因此做大小写不敏感查找
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
			url.PathEscape(itemID),
			url.QueryEscape(tag),
			maxWidth,
			url.QueryEscape(c.apiKey),
		)
	}
	// 其次 Primary 海报
	if tag := getTag("Primary", "primary", "Thumb"); tag != "" {
		return fmt.Sprintf("%s/emby/Items/%s/Images/Primary?tag=%s&maxWidth=%d&api_key=%s",
			c.baseURL,
			url.PathEscape(itemID),
			url.QueryEscape(tag),
			maxWidth,
			url.QueryEscape(c.apiKey),
		)
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

	u := fmt.Sprintf("%s/emby/Items?Path=%s&Recursive=true&Fields=Path&IncludeItemTypes=Movie,Episode,Series,Folder&api_key=%s",
		c.baseURL,
		url.QueryEscape(path),
		url.QueryEscape(c.apiKey),
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

	u := fmt.Sprintf("%s/emby/Items/%s/Refresh",
		c.baseURL,
		url.PathEscape(itemID),
	)

	// 构建查询参数
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

	u := fmt.Sprintf("%s/emby/Library/Refresh?LibraryId=%s&api_key=%s",
		c.baseURL,
		url.QueryEscape(libraryID),
		url.QueryEscape(c.apiKey),
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
