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
type WebhookEvent struct {
	Event  string      `json:"Event"`
	User   *UserInfo   `json:"User,omitempty"`
	Item   *ItemInfo   `json:"Item,omitempty"`
	Server *ServerInfo `json:"Server,omitempty"`
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
