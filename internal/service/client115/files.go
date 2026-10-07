package client115

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
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

// ==================== 请求层重试策略（P0-1） ====================
//
// 目标：对 115 API 的「瞬态故障」做有限重试，提升拉取/查询稳定性，同时避免放大风控。
// 原则：
//  1. 仅幂等方法（GET/HEAD）自动重试；POST 等可能有副作用的请求保持单次调用。
//  2. 仅网络错误、超时、5xx、429 重试；4xx 与业务错误（HTTP 200 但 state=false）不重试。
//  3. 父级 ctx 取消/超时不重试，避免无谓延长调用方等待。

const (
	// requestMaxAttempts 幂等请求最大尝试次数（含首次）
	requestMaxAttempts = 3
	// requestRetryBaseDelay 首次重试退避基数
	requestRetryBaseDelay = 500 * time.Millisecond
	// requestRetryMaxDelay 单次退避上限
	requestRetryMaxDelay = 8 * time.Second
)

// isIdempotentMethod 仅幂等方法参与自动重试，避免副操作被重复执行
func isIdempotentMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// isRetryableStatus 5xx 服务端错误与 429 限流/风控可安全重试
func isRetryableStatus(code int) bool {
	return code >= 500 || code == http.StatusTooManyRequests
}

// retryBackoffDelay 计算第 attempt 次重试前的退避时长（attempt 从 0 计）
// 指数增长（×2）并附加 [0, d/2) 抖动，打散多账号同时重试形成的尖峰
func retryBackoffDelay(attempt int) time.Duration {
	d := requestRetryBaseDelay
	for i := 0; i < attempt && d < requestRetryMaxDelay; i++ {
		d *= 2
	}
	if d > requestRetryMaxDelay {
		d = requestRetryMaxDelay
	}
	if half := d / 2; half > 0 {
		d += time.Duration(rand.Int63n(int64(half))) //nolint:gosec // G404 — 退避抖动，非安全用途，仅用于打散多账号重试峰值
	}
	return d
}

// acquireAPI 获取一个 115 API 令牌（重试时每次尝试都需重新过限流）
func (c *Client) acquireAPI(ctx context.Context) error {
	lim := rate.GetRegistry().GetLimiter("global", rate.TypeAPI115)
	if err := lim.Acquire(ctx); err != nil {
		return fmt.Errorf("rate limited: %w", err)
	}
	return nil
}

// doHTTPOnce 执行单次 HTTP 往返，返回响应体与状态码（不判定业务状态）
// 错误仅在传输/读取阶段产生；HTTP 状态码由调用方决定是否重试
func (c *Client) doHTTPOnce(
	ctx context.Context,
	method string,
	urlStr string,
	body string,
	headers map[string]string,
) ([]byte, int, error) {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// request115 发送 115 API 请求，自动走 API 限流器。
//
// P0-1：幂等请求（GET/HEAD）在网络错误、超时、5xx、429 时做有限重试
// （指数退避 + 抖动）；非幂等方法（POST 等）保持单次调用，行为与原实现一致。
// 业务错误（HTTP 200 但 state=false）由调用方解析处理，不在此重试。
func (c *Client) request115(
	ctx context.Context,
	method string,
	urlStr string,
	body string,
	opt RequestOptions,
) ([]byte, error) {
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

	// 非幂等：单次调用，不重试、不判定状态码（保持原行为）
	if !isIdempotentMethod(method) {
		if err := c.acquireAPI(ctx); err != nil {
			return nil, err
		}
		data, _, err := c.doHTTPOnce(ctx, method, urlStr, body, headers)
		return data, err
	}

	var lastErr error
	for attempt := 0; attempt < requestMaxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryBackoffDelay(attempt - 1)
			logger.S().Warnf("[115] 请求失败重试 %d/%d，等待 %v 后重试 url=%s err=%v",
				attempt, requestMaxAttempts-1, delay, urlStr, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		if err := c.acquireAPI(ctx); err != nil {
			return nil, err
		}

		data, status, err := c.doHTTPOnce(ctx, method, urlStr, body, headers)
		if err != nil {
			// 父级 ctx 取消/超时为终止性错误，不再重试
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		if isRetryableStatus(status) {
			lastErr = fmt.Errorf("HTTP %d", status)
			continue
		}
		return data, nil
	}

	return nil, fmt.Errorf("请求重试 %d 次仍失败: %w", requestMaxAttempts, lastErr)
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
// 走 POST http://proapi.115.com/android/2.0/ufile/download + encrypt/decrypt
func (c *Client) GetDownloadUrlWebFull(
	ctx context.Context,
	pickcode string,
	cookie string,
	userAgent string,
) (*DownloadUrlMeta, error) {
	if pickcode == "" {
		return nil, fmt.Errorf("pickcode is empty")
	}
	if cookie == "" {
		return nil, fmt.Errorf("cookie is empty")
	}

	// 对齐参考项目 api.py#L829: 115 API 要求 pickcode 小写
	pickcode = strings.ToLower(pickcode)

	// 加密 payload: {"pick_code":"xxx"}
	payload := fmt.Sprintf(`{"pick_code":"%s"}`, pickcode)
	encrypted, encErr := crypto115.Encrypt([]byte(payload))
	if encErr != nil {
		return nil, fmt.Errorf("encrypt pick_code: %w", encErr)
	}
	data := "data=" + url.QueryEscape(encrypted)

	endpoint := "http://proapi.115.com/android/2.0/ufile/download"
	// 对齐参考项目：用调用方传入的 userAgent（浏览器/播放器 UA），
	// 而非 c.UserAgent（115 客户端 UA）。115 API 返回的 CDN URL 可能有 UA 绑定。
	ua := userAgent
	if ua == "" {
		ua = c.UserAgent
	}
	headers := map[string]string{
		"User-Agent":     ua,
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

	// data 容错为 RawMessage：115 在 state=false 时可能返回 data:[]（数组），
	// 若直接声明为 string 会先报 JSON 解析错误，把真正的 errno/错误文案盖掉。
	var resp struct {
		State bool            `json:"state"`
		ErrNo int             `json:"errno,omitempty"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse 115 download API response: %w (body=%s)", err, truncate(respBody, 512))
	}
	if !resp.State {
		return nil, newAPIError(resp.ErrNo, firstNonEmpty(resp.Error, string(respBody)))
	}
	var encData string
	if err := json.Unmarshal(resp.Data, &encData); err != nil {
		return nil, fmt.Errorf("115 download API unexpected data field: %s (body=%s)",
			truncate(resp.Data, 128), truncate(respBody, 256))
	}
	if encData == "" {
		return nil, fmt.Errorf("115 download API returned empty data field")
	}

	// 解密
	decrypted, decErr := crypto115.Decrypt(encData)
	if decErr != nil {
		// 输出调试信息：原始响应体 + data字段前128字节，便于排查
		logger.S().Errorf("[115Download] decrypt failed: %v | respData[:128]=%q rawBody[:256]=%q",
			decErr, truncate([]byte(encData), 128), truncate(respBody, 256))
		return nil, fmt.Errorf("decrypt 115 download API response: %w", decErr)
	}
	var dm struct {
		URL       string `json:"url"`
		FileSize  any    `json:"file_size"`
		FileName  string `json:"fileName"`
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
	PID      any    `json:"pid,omitempty"` // 父目录 cid（115 /files 目录条目会带，文件条目通常不带）
	Name     string `json:"n"`
	Size     int64  `json:"s,omitempty"`
	SHA      string `json:"sha,omitempty"`
	FC       any    `json:"fc"` // 子文件数（目录）
	IsDir    bool   `json:"-"`
}

// FsFilesPathNode /files 响应顶层 path 字段的元素：从根到「当前被列出目录」的完整目录树。
// 115 实测字段为 {cid, name, pid}，其中 cid 为该层级目录自身 id，pid 为其父目录 id。
// path 描述的是「被列出目录」自身的祖先链（从根直到并包含该目录），不含其子项。
type FsFilesPathNode struct {
	Cid  flexInt `json:"cid"`
	Pid  flexInt `json:"pid"`
	Name string  `json:"name"`
}

// FsFilesResp fs_files 返回结构
type FsFilesResp struct {
	State  bool              `json:"state"`
	Data   []FsFileEntry     `json:"data"`
	Path   []FsFilesPathNode `json:"path,omitempty"` // 当前列目录的祖先链（含被列出目录自身）
	Count  int               `json:"count"`
	ErrNo  int               `json:"errno,omitempty"`
	ErrMsg string            `json:"errmsg,omitempty"`
	Error  string            `json:"error,omitempty"`
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
	// state=false 且带错误码/文案：按类型化错误返回，避免调用方把失败当成「空目录」而漏扫。
	// 用 ErrNo/ErrMsg/Error 守卫：兼容部分测试/旧响应缺失 state 字段（缺省 false）的情况。
	if !resp.State && (resp.ErrNo != 0 || resp.ErrMsg != "" || resp.Error != "") {
		return nil, newAPIError(resp.ErrNo, firstNonEmpty(resp.ErrMsg, resp.Error))
	}
	// 标记目录
	// 115 web API:
	//   - 目录: cid（有效非零）为该目录子内容访问id，fid 为 nil / 0 / 不存在（目录没有文件ID）
	//   - 文件: fid（有效非零）为该文件的唯一ID，cid 等于父目录的cid
	// 判定优先级：
	//   1. 有有效 fid → 文件（即使 fc>0 也不能覆盖，115 某些文件 fc 也大于0）
	//   2. 无有效 fid 但有有效 cid → 目录
	//   3. 都没有 → 用 fc 兜底
	for i := range resp.Data {
		cid, cidErr := toInt64(resp.Data[i].CID)
		fid, fidErr := toInt64(resp.Data[i].FID)
		hasCID := cidErr == nil && cid > 0
		hasFID := fidErr == nil && fid > 0
		fc, fcErr := toInt64(resp.Data[i].FC)
		hasChildren := fcErr == nil && fc > 0
		switch {
		case hasFID:
			resp.Data[i].IsDir = false
		case hasCID:
			resp.Data[i].IsDir = true
		default:
			resp.Data[i].IsDir = hasChildren
		}
	}
	return &resp, nil
}

// FsDirGetID 根据路径获取目录 ID
// GET https://webapi.115.com/files/getid?path={path}
// 115 实际响应存在两种格式：
//
//	新版（实测）: {"state":true, "id":"3491751436709005103", "is_private":"0"}
//	旧版（文档）: {"state":true, "data":{"id":"..."}}
//
// 两种都要兼容
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
		logger.S().Errorf("[115/FsDirGetID] path=%s request error: %v", path, err)
		return 0, fmt.Errorf("fs_dir_getid request: %w", err)
	}
	logger.S().Infof("[115/FsDirGetID] path=%s raw response: %s", path, truncate(body, 1024))
	var resp struct {
		State  bool   `json:"state"`
		ErrMsg string `json:"errmsg,omitempty"`
		// 新版格式：id 直接在顶层（string）
		ID any `json:"id"`
		// 旧版兼容：data.id
		Data struct {
			ID any `json:"id"`
		} `json:"data,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		logger.S().Errorf("[115/FsDirGetID] path=%s parse failed: %v, body=%s", path, err, truncate(body, 1024))
		return 0, fmt.Errorf("parse fs_dir_getid: %w (body=%s)", err, truncate(body, 512))
	}
	if !resp.State {
		logger.S().Warnf("[115/FsDirGetID] path=%s state=false, errmsg=%s, body=%s", path, resp.ErrMsg, truncate(body, 1024))
		return 0, fmt.Errorf("115 目录不存在:path=%q，请在任务编辑中重新选择远程路径 (errmsg=%s)", path, resp.ErrMsg)
	}
	// 兼容两种 id 位置：优先顶层 id，回退到 data.id
	idAny := resp.ID
	if idAny == nil || isEmptyString(idAny) {
		idAny = resp.Data.ID
	}
	id, err := toInt64(idAny)
	if err != nil {
		logger.S().Errorf("[115/FsDirGetID] path=%s parse id failed: %v, top.id=%v, data.id=%v, body=%s",
			path, err, resp.ID, resp.Data.ID, truncate(body, 1024))
		return 0, fmt.Errorf("parse dir id: %w (top.id=%v, data.id=%v, body=%s)", err, resp.ID, resp.Data.ID, truncate(body, 512))
	}
	return id, nil
}

// isEmptyString 判断 any 类型是否为空字符串形式（nil、空串、全空格）
func isEmptyString(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
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
		Error  string `json:"error,omitempty"`
		Errno  any    `json:"errno,omitempty"`
	}
	logger.S().Infof("[ExportDirParse] submit body=%s", truncate(body, 1024))
	if err := json.Unmarshal(body, &submitResp); err != nil {
		return "", fmt.Errorf("parse submit export: %w (body=%s)", err, truncate(body, 512))
	}
	if !submitResp.State {
		errMsg := firstNonEmpty(submitResp.ErrMsg, submitResp.Error, fmt.Sprint(submitResp.Errno))
		return "", fmt.Errorf("submit export state=false: %s (body=%s)", errMsg, truncate(body, 512))
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
					Status   int    `json:"status"`
					PickCode string `json:"pick_code"`
					FileName string `json:"file_name"`
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
