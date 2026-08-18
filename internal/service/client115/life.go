// Package client115 115 网盘 API 客户端
// life.go 实现 115 生活事件（Life）API 客户端
// 对齐 frontend/src/lib/115Life.ts
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
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 事件类型常量 ====================

// BehaviorTypeToName 行为类型 -> 名称映射
// 对齐 TS BEHAVIOR_TYPE_TO_NAME
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
	24: "rename_file",
}

// CreateEventTypes 创建类事件（上传/新建/复制/接收）
// 对齐 TS CREATE_EVENT_TYPES
var CreateEventTypes = map[int]bool{1: true, 2: true, 14: true, 17: true, 18: true, 23: true}

// MoveEventTypes 移动事件类型
// 对齐 TS MOVE_EVENT_TYPES
var MoveEventTypes = map[int]bool{5: true, 6: true}

// RenameEventTypes 重命名事件类型
// 对齐 TS RENAME_EVENT_TYPES
var RenameEventTypes = map[int]bool{20: true, 24: true}

// DeleteEventTypes 删除事件类型
// 对齐 TS DELETE_EVENT_TYPES
var DeleteEventTypes = map[int]bool{22: true}

// ==================== LifeEventItem ====================

// LifeEventItem 115 生活事件条目
// 对齐 TS LifeEvent，从 115 life API JSON 响应映射
// 注意：FileID/ParentID 使用 string 以避免 19 位数字精度丢失
type LifeEventItem struct {
	Category     int    `json:"file_category"` // 0=文件夹, 1=文件
	Type         int    `json:"type"`          // 事件类型编号（见 BehaviorTypeToName）
	Area         int    `json:"area,omitempty"`
	OpTime       int64  `json:"op_time"`
	FileID       string `json:"file_id"`
	FileName     string `json:"file_name"`
	ParentID     string `json:"parent_id"`
	PickCode     string `json:"pick_code"`
	FilePath     string `json:"file_path,omitempty"`
	Size         int64  `json:"file_size,omitempty"`
	BehaviorType int    `json:"behavior_type,omitempty"`
}

// lifeEventRaw 用于灵活解析 115 API 响应中的事件字段
// file_id / parent_id 在 API 响应中可能是 number 或 string，统一用 any 解析后转字符串
type lifeEventRaw struct {
	Category     any    `json:"file_category"`
	Type         any    `json:"type"`
	Area         any    `json:"area"`
	OpTime       any    `json:"op_time"`
	FileID       any    `json:"file_id"`
	FileName     string `json:"file_name"`
	ParentID     any    `json:"parent_id"`
	PickCode     string `json:"pick_code"`
	FilePath     string `json:"file_path"`
	Path         string `json:"path"`
	Size         any    `json:"file_size"`
	BehaviorType any    `json:"behavior_type"`
}

// ==================== LifeClient ====================

// LifeClient 115 生活事件 API 客户端
type LifeClient struct {
	cookie     string
	httpClient *http.Client
}

// NewLifeClient 创建生活事件客户端
func NewLifeClient(cookie string) *LifeClient {
	return &LifeClient{
		cookie: cookie,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// LifeShow 开启生活事件功能
// POST https://life.115.com/api/1.0/web/1.0/calendar/setoption
// 表单: locus=1&open_life=1
// 检查响应 errNo 字段（0=成功）
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
		return fmt.Errorf("parse lifeShow response: %w (body=%s)", err, truncate(body, 256))
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
	return fmt.Errorf("lifeShow 失败: state=false, body=%s", truncate(body, 256))
}

// PullEvents 拉取生活事件列表
// POST https://life.115.com/api/1.0/web/1.0/live/listhistory
// 表单: cursor=xxx&limit=1000
// 返回事件列表 + 下一页游标（0 表示无更多数据）
func (c *LifeClient) PullEvents(ctx context.Context, account string, cursor int64) ([]LifeEventItem, int64, error) {
	if c.cookie == "" {
		return nil, 0, fmt.Errorf("cookie is empty")
	}

	endpoint := "https://life.115.com/api/1.0/web/1.0/live/listhistory"
	form := url.Values{}
	form.Set("cursor", strconv.FormatInt(cursor, 10))
	form.Set("limit", "1000")
	if account != "" {
		form.Set("account", account)
	}

	body, err := c.doRequest(ctx, http.MethodPost, endpoint, form.Encode())
	if err != nil {
		return nil, 0, fmt.Errorf("pullEvents request: %w", err)
	}

	var resp struct {
		State  bool   `json:"state"`
		ErrNo  int    `json:"errNo"`
		ErrNo2 int    `json:"errno"`
		Error  string `json:"error,omitempty"`
		Data   struct {
			List       []lifeEventRaw `json:"list"`
			NextCursor any            `json:"next_cursor"`
			Count      int            `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("parse pullEvents response: %w (body=%s)", err, truncate(body, 512))
	}

	// 登录失效检测
	errNo := resp.ErrNo
	if errNo == 0 {
		errNo = resp.ErrNo2
	}
	if errNo == 99 || errNo == 990001 {
		return nil, 0, fmt.Errorf("cookie 已过期 (errno=%d): %s", errNo, resp.Error)
	}
	if !resp.State && errNo != 0 {
		return nil, 0, fmt.Errorf("pullEvents 失败 errno=%d: %s", errNo, resp.Error)
	}

	// 解析 next_cursor（可能是 number/string/null）
	nextCursor := parseCursor(resp.Data.NextCursor)

	// 转换 raw -> LifeEventItem
	items := make([]LifeEventItem, 0, len(resp.Data.List))
	for _, raw := range resp.Data.List {
		items = append(items, raw.toLifeEventItem())
	}

	logger.S().Infof("[LifeClient] pulled %d events, next_cursor=%d", len(items), nextCursor)
	return items, nextCursor, nil
}

// doRequest 发送 HTTP 请求，返回响应体
// 自动注入 cookie、UA、115 标准 headers
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
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	req.Header.Set("Cookie", c.cookie)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// toLifeEventItem 将灵活解析的 raw 转为强类型 LifeEventItem
func (r lifeEventRaw) toLifeEventItem() LifeEventItem {
	item := LifeEventItem{
		FileName: r.FileName,
		PickCode: r.PickCode,
	}
	item.Category = lifeToInt(r.Category)
	item.Type = lifeToInt(r.Type)
	item.Area = lifeToInt(r.Area)
	item.OpTime = lifeToInt64(r.OpTime)
	item.Size = lifeToInt64(r.Size)
	item.BehaviorType = lifeToInt(r.BehaviorType)
	item.FileID = lifeToString(r.FileID)
	item.ParentID = lifeToString(r.ParentID)
	// 优先 file_path，回退 path
	if r.FilePath != "" {
		item.FilePath = r.FilePath
	} else {
		item.FilePath = r.Path
	}
	return item
}

// parseCursor 解析游标（可能是 number/string/null）
func parseCursor(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		if x == "" {
			return 0
		}
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n
		}
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
	}
	return 0
}

// lifeToInt 将 any 转为 int（兼容 number/string）
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

// lifeToString 将 any 转为 string（兼容 number/string）
func lifeToString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// 整数化，避免出现 "1.234e+18" 这种科学计数法
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

// TypeNumberToString 115 数字类型 -> 分类字符串
// 对齐 TS typeNumberToString
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
