package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
)

// 分片并发代理（P0-1，对齐 LitePan range_proxy）：
// 大原盘（50-100GB）单连接从 115 CDN 拉流时带宽不足，播放器表现为卡顿、
// 首帧超时或直接「无法播放」。这里把客户端请求的字节区间按 chunkSize 切分，
// 用 concurrency 路并发拉取、按原顺序写出，首片由调用方直写（缩短首字节）。
//
// 约束：
//   - 只对 GET + 上游 206 生效；HEAD/200/不支持 Range 走原单连接逻辑
//   - 分片请求严格校验 206 + Content-Range + Content-Length，任何不符视为失败
//     （避免写出错位数据，Lavf 对 Content-Range 零容忍）
//   - 分片失败时换链重试一次；并发分片共享同一次换链（singleflight），
//     避免 4 路分片同时打 115 download API 触发风控
//   - 客户端断开（w 写失败 / ctx 取消）立即取消所有在途分片并关闭 body
type rangeChunkFetcher struct {
	mu       sync.Mutex
	cdnURL   string
	headers  map[string]string
	client   *http.Client
	chunk    int64
	parallel int
	refresh  func(context.Context) (string, error)

	// refreshMu/refreshSeq 实现换链 singleflight：
	// 只有持有旧 seq 的第一个分片真正执行 refresh，其余直接复用新 URL。
	refreshMu  sync.Mutex
	refreshSeq uint64
}

func newRangeChunkFetcher(
	cdnURL string,
	headers map[string]string,
	client *http.Client,
	chunkSize int64,
	parallel int,
	refresh func(context.Context) (string, error),
) *rangeChunkFetcher {
	if chunkSize <= 0 {
		chunkSize = 10 << 20
	}
	if parallel < 1 {
		parallel = 1
	}
	if parallel > 4 {
		parallel = 4
	}
	return &rangeChunkFetcher{
		cdnURL:   cdnURL,
		headers:  headers,
		client:   client,
		chunk:    chunkSize,
		parallel: parallel,
		refresh:  refresh,
	}
}

func (f *rangeChunkFetcher) snapshot() (string, map[string]string, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cdnURL, f.headers, f.refreshSeq
}

// refreshIfStale 串行化换链：同一代 URL 只允许一个分片真正刷新，
// 其余分片发现 seq 已变化后直接复用新 URL。
func (f *rangeChunkFetcher) refreshIfStale(ctx context.Context, seq uint64) (string, error) {
	if f.refresh == nil {
		return "", errors.New("refresh not configured")
	}
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()

	f.mu.Lock()
	if f.refreshSeq != seq {
		u := f.cdnURL
		f.mu.Unlock()
		return u, nil
	}
	f.mu.Unlock()

	newURL, err := f.refresh(ctx)
	if err != nil {
		return "", err
	}
	if newURL == "" {
		return "", errors.New("refresh returned empty url")
	}
	f.mu.Lock()
	f.cdnURL = newURL
	f.refreshSeq++
	f.mu.Unlock()
	return newURL, nil
}

// fetch 拉取 [start,end] 分片，要求 206；失败时换链重试一次。
func (f *rangeChunkFetcher) fetch(ctx context.Context, start, end int64) (*http.Response, error) {
	url, headers, seq := f.snapshot()
	resp, status, err := f.fetchOnce(ctx, url, headers, start, end)
	if err == nil {
		return resp, nil
	}
	if !isRetryableChunk(status) || f.refresh == nil || ctx.Err() != nil {
		return nil, err
	}
	if _, rerr := f.refreshIfStale(ctx, seq); rerr != nil {
		return nil, err
	}
	url, headers, _ = f.snapshot()
	resp, _, err = f.fetchOnce(ctx, url, headers, start, end)
	return resp, err
}

// fetchOnce 发起一次分片请求并校验响应区间。
// status 为 0 表示网络层错误；返回 err 时响应体已关闭。
func (f *rangeChunkFetcher) fetchOnce(
	ctx context.Context,
	url string,
	headers map[string]string,
	start, end int64,
) (*http.Response, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusPartialContent {
		status := resp.StatusCode
		drainAndCloseResponse(resp)
		return nil, status, fmt.Errorf("分片请求失败: status=%d", status)
	}
	if verr := validateChunkResponse(resp, start, end); verr != nil {
		status := resp.StatusCode
		drainAndCloseResponse(resp)
		return nil, status, verr
	}
	return resp, resp.StatusCode, nil
}

// validateChunkResponse 校验区间与长度：CDN 忽略 Range 或返回错位数据时必须拒绝，
// 否则客户端会收到错位字节（表现为花屏、seek 后黑屏）。
func validateChunkResponse(resp *http.Response, start, end int64) error {
	expected := end - start + 1
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		var unit string
		var rs, re, total int64
		if _, err := fmt.Sscanf(cr, "%s %d-%d/%d", &unit, &rs, &re, &total); err == nil {
			if unit != "bytes" || rs != start || re != end {
				return fmt.Errorf("Content-Range 不匹配: got %q, want bytes %d-%d", cr, start, end)
			}
		}
	}
	if resp.ContentLength > 0 && resp.ContentLength != expected {
		return fmt.Errorf("Content-Length 不匹配: got %d, want %d", resp.ContentLength, expected)
	}
	return nil
}

// isRetryableChunk 判断是否值得换链重试：网络错误（0）与可能由过期/限流引起的状态码。
func isRetryableChunk(status int) bool {
	if status == 0 {
		return true
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

// drainAndCloseResponse 读掉少量 body 以便连接复用，然后关闭。
func drainAndCloseResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}

type chunkResult struct {
	idx  int
	resp *http.Response
	err  error
}

// streamRemaining 从 nextStart 到 end 按序写出分片（有界并发预取）。
// 返回已写出字节数与错误；任何退出路径都会关闭已打开的响应体，避免连接泄漏。
func (f *rangeChunkFetcher) streamRemaining(ctx context.Context, w io.Writer, nextStart, end int64) (int64, error) {
	if nextStart > end {
		return 0, nil
	}
	chunk := f.chunk
	total := end - nextStart + 1
	n := int((total + chunk - 1) / chunk)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan chunkResult, f.parallel)
	sem := make(chan struct{}, f.parallel)
	go func() {
		for i := 0; i < n; i++ {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(i int) {
				defer func() { <-sem }()
				s := nextStart + int64(i)*chunk
				e := s + chunk - 1
				if e > end {
					e = end
				}
				resp, err := f.fetch(ctx, s, e)
				select {
				case results <- chunkResult{idx: i, resp: resp, err: err}:
				case <-ctx.Done():
					drainAndCloseResponse(resp)
				}
			}(i)
		}
	}()

	pending := make(map[int]chunkResult, f.parallel)
	closePending := func() {
		for _, r := range pending {
			drainAndCloseResponse(r.resp)
		}
	}
	var written int64
	for i := 0; i < n; i++ {
		var cur chunkResult
		for {
			if r, ok := pending[i]; ok {
				cur = r
				delete(pending, i)
				break
			}
			select {
			case r := <-results:
				if r.idx != i {
					pending[r.idx] = r
					continue
				}
				cur = r
			case <-ctx.Done():
				closePending()
				return written, ctx.Err()
			}
			break
		}
		if cur.err != nil {
			closePending()
			return written, cur.err
		}
		copied, err := io.Copy(w, cur.resp.Body)
		_ = cur.resp.Body.Close()
		written += copied
		if err != nil {
			closePending()
			return written, err
		}
	}
	closePending()
	return written, nil
}

// parseContentRange 解析 "bytes start-end/total"。
func parseContentRange(cr string) (start, end, total int64, ok bool) {
	var unit string
	if _, err := fmt.Sscanf(cr, "%s %d-%d/%d", &unit, &start, &end, &total); err != nil {
		return 0, 0, 0, false
	}
	if unit != "bytes" || end < start || total <= 0 {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

// rangeProxyOptions 分片并发参数（来自设置页）。
type rangeProxyOptions struct {
	Enabled  bool
	Chunk    int64
	Parallel int
}

// streamRangeProxy 首片直写 + 剩余分片并发。
// 调用方必须已把响应头写给客户端；upstream 必须是 206 且 body 定位在 start。
// 返回 false 表示未启用/不适用，调用方应回退单连接 io.Copy。
func streamRangeProxy(
	w http.ResponseWriter,
	r *http.Request,
	upstream *http.Response,
	cdnURL string,
	reqHeaders map[string]string,
	client *http.Client,
	refresh func(context.Context) (string, error),
	opts rangeProxyOptions,
) bool {
	if !opts.Enabled || r.Method != http.MethodGet || upstream.StatusCode != http.StatusPartialContent {
		return false
	}
	start, end, total, ok := parseContentRange(upstream.Header.Get("Content-Range"))
	if !ok {
		return false
	}
	_ = total
	remaining := end - start + 1
	if remaining <= opts.Chunk || opts.Parallel <= 1 {
		// 小文件/单并发：不值得分片，走原逻辑
		return false
	}

	firstLen := opts.Chunk
	if firstLen > remaining {
		firstLen = remaining
	}
	n, err := io.CopyN(w, upstream.Body, firstLen)
	if err != nil && !errors.Is(err, io.EOF) {
		log.Printf("[STRM][range-proxy] 首片写出失败（客户端可能已断开）: copied=%d err=%v", n, err)
		return true
	}
	if start+firstLen-1 >= end {
		return true
	}

	fetcher := newRangeChunkFetcher(cdnURL, reqHeaders, client, opts.Chunk, opts.Parallel, refresh)
	written, err := fetcher.streamRemaining(r.Context(), w, start+firstLen, end)
	if err != nil {
		log.Printf("[STRM][range-proxy] 分片中断: first=%d rest=%d err=%v", firstLen, written, err)
		return true
	}
	log.Printf("[STRM][range-proxy] 完成: range=%d-%d chunk=%d parallel=%d wrote=%d",
		start, end, opts.Chunk, opts.Parallel, firstLen+written)
	return true
}
