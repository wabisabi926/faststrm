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

	"github.com/wabisabi926/faststrm/internal/service/crypto115"
	"github.com/wabisabi926/faststrm/internal/service/rate"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 公共 request115 封装 ====================

// RequestOptions 115 API 请求选项
type RequestOptions struct {
	Cookie           string
	UseCommonHeaders bool
	ExtraHeaders     map[string]string
}

func (c *Client) buildCommonHeaders(opt RequestOptions) map[string]string {
	h := map[string]string{
		"User-Agent":   c.UserAgent,
		"Accept":       "application/json, text/plain, */*",
		"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
		"Referer":      "https://115.com/",
		"Origin":       "https://115.com",
	}
	if opt.Cookie != "" {
		h["Cookie"] = opt.Cookie
	}
	for k, v := range opt.ExtraHeaders {
		h[k] = v
	}
	return h
}

// request115 发送 115 API 请求，自动走 API 限流器
func (c *Client) request115(
	ctx context.Context,
	method string,
	urlStr string,
	body string,
	opt RequestOptions,
) ([]byte, error) {
	// 限流：API 115 令牌桶
	lim := rate.GetRegistry().GetLimiter("global", rate.TypeAPI115)
	if err := lim.Acquire(ctx); err != nil {
		return nil, fmt.Errorf("rate limited: %w", err)
	}

	headers := make(map[string]string)
	if opt.UseCommonHeaders {
		headers = c.buildCommonHeaders(opt)
	} else {
		headers["User-Agent"] = c.UserAgent
		if opt.Cookie != "" {
			headers["Cookie"] = opt.Cookie
		}
		for k, v := range opt.ExtraHeaders {
			headers[k] = v
		}
		if method == http.MethodPost {
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
	}

	var (
		reqBody io.Reader
	)
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ==================== 下载链接解析 ====================

// DownloadUrlMeta 下载链接元数据
type DownloadUrlMeta struct {
	URL      string `json:"url"`
	FileSize int64  `json:"fileSize,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

// GetDownloadUrlWebFull 根据 pickcode 获取 115 直链
// 对齐 lib/115.ts getDownloadUrlWebFull
// 走 POST http://pro.api.115.com/android/2.0/ufile/download + encrypt/decrypt
func (c *Client) GetDownloadUrlWebFull(
	ctx context.Context,
	pickcode string,
	cookie string,
) (*DownloadUrlMeta, error) {
	if pickcode == "" {
		return nil, fmt.Errorf("pickcode is empty")
	}
	if cookie == "" {
		return nil, fmt.Errorf("cookie is empty")
	}

	// 加密 payload: {"pick_code":"xxx"}
	payload := fmt.Sprintf(`{"pick_code":"%s"}`, pickcode)
	encrypted, encErr := crypto115.Encrypt([]byte(payload))
	if encErr != nil {
		return nil, fmt.Errorf("encrypt pick_code: %w", encErr)
	}
	data := "data=" + url.QueryEscape(encrypted)

	endpoint := "http://pro.api.115.com/android/2.0/ufile/download"
	headers := map[string]string{
		"User-Agent":     c.UserAgent,
		"Content-Type":   "application/x-www-form-urlencoded",
		"Content-Length": strconv.Itoa(len(data)),
	}

	respBody, err := c.request115(ctx, http.MethodPost, endpoint, data, RequestOptions{
		Cookie:           cookie,
		UseCommonHeaders: false,
		ExtraHeaders:     headers,
	})
	if err != nil {
		return nil, fmt.Errorf("115 download API request: %w", err)
	}

	var resp struct {
		State bool   `json:"state"`
		Data  string `json:"data"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse 115 download API response: %w (body=%s)", err, truncate(respBody, 512))
	}
	if !resp.State {
		return nil, fmt.Errorf("115 download API state=false: %s", firstNonEmpty(resp.Error, string(respBody)))
	}
	if resp.Data == "" {
		return nil, fmt.Errorf("115 download API returned empty data field")
	}

	// 解密
	decrypted, decErr := crypto115.Decrypt(resp.Data)
	if decErr != nil {
		return nil, fmt.Errorf("decrypt 115 download API response: %w", decErr)
	}
	var dm struct {
		URL      string `json:"url"`
		FileSize any    `json:"file_size"`
		FileName string `json:"fileName"`
		FileName2 string `json:"file_name"`
	}
	if err := json.Unmarshal([]byte(decrypted), &dm); err != nil {
		return nil, fmt.Errorf("parse decrypted download meta: %w", err)
	}
	if dm.URL == "" {
		return nil, fmt.Errorf("115 download API returned empty url (decrypted=%s)", truncate([]byte(decrypted), 256))
	}

	result := &DownloadUrlMeta{
		URL:      dm.URL,
		FileName: firstNonEmpty(dm.FileName, dm.FileName2),
	}
	switch v := dm.FileSize.(type) {
	case float64:
		result.FileSize = int64(v)
	case string:
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			result.FileSize = n
		}
	}

	return result, nil
}

// ==================== fs_files：列目录 ====================

// FsFileEntry 115 文件条目
type FsFileEntry struct {
	PickCode string `json:"pc,omitempty"`
	FID      any    `json:"fid"`
	CID      any    `json:"cid"`
	Name     string `json:"n"`
	Size     int64  `json:"s,omitempty"`
	SHA      string `json:"sha,omitempty"`
	FC       any    `json:"fc"` // 子文件数（目录）
	IsDir    bool   `json:"-"`
}

// FsFilesResp fs_files 返回结构
type FsFilesResp struct {
	State bool          `json:"state"`
	Data  []FsFileEntry `json:"data"`
	Count int           `json:"count"`
	ErrNo int           `json:"errno,omitempty"`
	ErrMsg string       `json:"errmsg,omitempty"`
}

// FsFiles 列目录
// GET https://webapi.115.com/files?aid=1&cid={cid}&o=user_ptime&asc=0&offset=0&show_dir=1&limit={limit}&code=&scid=&snap=0&natsort=1&record_open_time=1&source=&format=json&virtual=1
func (c *Client) FsFiles(
	ctx context.Context,
	cid string,
	limit int,
	offset int,
	cookie string,
) (*FsFilesResp, error) {
	if cookie == "" {
		return nil, fmt.Errorf("cookie is empty")
	}
	if cid == "" {
		cid = "0" // 根目录
	}
	if limit <= 0 {
		limit = 1000
	}

	params := url.Values{}
	params.Set("aid", "1")
	params.Set("cid", cid)
	params.Set("o", "user_ptime")
	params.Set("asc", "0")
	params.Set("offset", strconv.Itoa(offset))
	params.Set("show_dir", "1")
	params.Set("limit", strconv.Itoa(limit))
	params.Set("code", "")
	params.Set("scid", "")
	params.Set("snap", "0")
	params.Set("natsort", "1")
	params.Set("record_open_time", "1")
	params.Set("source", "")
	params.Set("format", "json")
	params.Set("virtual", "1")

	endpoint := "https://webapi.115.com/files?" + params.Encode()
	body, err := c.request115(ctx, http.MethodGet, endpoint, "", RequestOptions{
		Cookie:           cookie,
		UseCommonHeaders: true,
	})
	if err != nil {
		return nil, fmt.Errorf("fs_files request: %w", err)
	}
	var resp FsFilesResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse fs_files response: %w (body=%s)", err, truncate(body, 512))
	}
	// 标记目录
	// 115 web API: fc 字段为子项数量（目录>0，文件为1或0）
	// 正确判断：fc > 0 且 fid 为 0 → 目录
	for i := range resp.Data {
		fc, _ := toInt64(resp.Data[i].FC)
		fid, _ := toInt64(resp.Data[i].FID)
		resp.Data[i].IsDir = fc > 0 && fid == 0
	}
	return &resp, nil
}

// FsDirGetID 根据路径获取目录 ID
// GET https://webapi.115.com/files/getid?path={path}
func (c *Client) FsDirGetID(
	ctx context.Context,
	path string,
	cookie string,
) (int64, error) {
	if cookie == "" {
		return 0, fmt.Errorf("cookie is empty")
	}
	// 路径格式处理：确保以 / 开头，去除末尾的 /
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimSuffix(path, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	endpoint := "https://webapi.115.com/files/getid?path=" + url.QueryEscape(path)
	body, err := c.request115(ctx, http.MethodGet, endpoint, "", RequestOptions{
		Cookie:           cookie,
		UseCommonHeaders: true,
	})
	if err != nil {
		return 0, fmt.Errorf("fs_dir_getid request: %w", err)
	}
	var resp struct {
		State bool `json:"state"`
		Data  struct {
			ID any `json:"id"`
		} `json:"data"`
		ErrMsg string `json:"errmsg,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("parse fs_dir_getid: %w", err)
	}
	if !resp.State {
		return 0, fmt.Errorf("fs_dir_getid state=false: %s (path=%s)", resp.ErrMsg, path)
	}
	id, err := toInt64(resp.Data.ID)
	if err != nil {
		return 0, fmt.Errorf("parse dir id: %w", err)
	}
	return id, nil
}

// ExportDirParse 提交并轮询目录导出任务，拿到 export_id 的完整解析结果
// 简化版：仅支持 exportFileIds 单个或多个提交，轮询间隔 1s，超时 10 分钟
func (c *Client) ExportDirParse(
	ctx context.Context,
	exportFileIDs string,
	cookie string,
	layerLimit int,
) (string, error) {
	if cookie == "" {
		return "", fmt.Errorf("cookie is empty")
	}
	if exportFileIDs == "" {
		return "", fmt.Errorf("exportFileIDs is empty")
	}

	// Step 1: 提交导出任务
	target := "U_1_0"
	form := url.Values{}
	form.Set("file_ids", exportFileIDs)
	form.Set("target", target)
	if layerLimit > 0 {
		form.Set("layer_limit", strconv.Itoa(layerLimit))
	}

	endpoint := "https://webapi.115.com/files/zip?format=json"
	body, err := c.request115(ctx, http.MethodPost, endpoint, form.Encode(), RequestOptions{
		Cookie:           cookie,
		UseCommonHeaders: true,
	})
	if err != nil {
		return "", fmt.Errorf("submit export: %w", err)
	}
	var submitResp struct {
		State bool `json:"state"`
		Data  struct {
			ExportID any `json:"export_id"`
		} `json:"data"`
		ErrMsg string `json:"errmsg,omitempty"`
	}
	if err := json.Unmarshal(body, &submitResp); err != nil {
		return "", fmt.Errorf("parse submit export: %w (body=%s)", err, truncate(body, 512))
	}
	if !submitResp.State {
		return "", fmt.Errorf("submit export state=false: %s", submitResp.ErrMsg)
	}
	exportID, err := toInt64(submitResp.Data.ExportID)
	if err != nil {
		return "", fmt.Errorf("parse export_id: %w", err)
	}

	// Step 2: 轮询导出状态（最多 10 分钟）
	statusEndpoint := fmt.Sprintf("https://webapi.115.com/files/zip_status?id=%d&format=json", exportID)
	deadline := time.After(10 * time.Minute)
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("export_dir_parse timeout after 10min")
		case <-tick.C:
			body, err := c.request115(ctx, http.MethodGet, statusEndpoint, "", RequestOptions{
				Cookie:           cookie,
				UseCommonHeaders: true,
			})
			if err != nil {
				logger.S().Warnf("[ExportDirParse] poll status err: %v", err)
				continue
			}
			var statusResp struct {
				State bool `json:"state"`
				Data  struct {
					Status    int    `json:"status"`
					PickCode  string `json:"pick_code"`
					FileName  string `json:"file_name"`
				} `json:"data"`
				ErrMsg string `json:"errmsg,omitempty"`
			}
			if err := json.Unmarshal(body, &statusResp); err != nil {
				logger.S().Warnf("[ExportDirParse] parse status err: %v", err)
				continue
			}
			if !statusResp.State {
				return "", fmt.Errorf("export status state=false: %s", statusResp.ErrMsg)
			}
			switch statusResp.Data.Status {
			case 2:
				// 完成
				logger.S().Infof("[ExportDirParse] done, export_id=%d", exportID)
				return statusResp.Data.PickCode, nil
			case -1:
				return "", fmt.Errorf("export failed, status=-1")
			default:
				// 0=排队, 1=处理中 → 继续轮询
			}
		}
	}
}

// ==================== 辅助 ====================

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func toInt64(v any) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case nil:
		return 0, fmt.Errorf("nil")
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}
