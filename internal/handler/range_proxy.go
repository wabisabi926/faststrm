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
//   - 分片请求严格校验 206，任何非 206 视为失败（避免写出错位数据）
//   - 分片失败时换链重试一次；仍失败则中断本次响应（客户端会重试）
//   - 客户端断开（w 写失败 / ctx 取消）立即取消所有在途分片
type rangeChunkFetcher struct {
	mu       sync.Mutex
	cdnURL   string
	headers  map[string]string
	client   *http.Client
	chunk    int64
	parallel int
	refresh  func(context.Context) (string, error)
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

func (f *rangeChunkFetcher) snapshot() (string, map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cdnURL, f.headers
}

func (f *rangeChunkFetcher) setURL(u string) {
	f.mu.Lock()
	f.cdnURL = u
	f.mu.Unlock()
}

// fetch 拉取 [start,end] 分片，要求 206；网络错误/可重试状态码换链重试一次。
func (f *rangeChunkFetcher) fetch(ctx context.Context, start, end int64) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		url, headers := f.snapshot()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt == 0 && f.refresh != nil && ctx.Err() == nil {
				if newURL, rerr := f.refresh(ctx); rerr == nil && newURL != "" {
					f.setURL(newURL)
					continue
				}
			}
			return nil, err
		}
		if resp.StatusCode == http.StatusPartialContent {
			return resp, nil
		}
		lastErr = fmt.Errorf("分片请求失败: status=%d", resp.StatusCode)
		retryable := resp.StatusCode >= 500 || resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound ||
			resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		if attempt == 0 && retryable && f.refresh != nil && ctx.Err() == nil {
			if newURL, rerr := f.refresh(ctx); rerr == nil && newURL != "" {
				f.setURL(newURL)
				continue
			}
		}
		return nil, lastErr
	}
	if lastErr == nil {
		lastErr = errors.New("分片请求重试后仍失败")
	}
	return nil, lastErr
}

type chunkResult struct {
	idx  int
	resp *http.Response
	err  error
}

// streamRemaining 从 nextStart 到 end 按序写出分片（有界并发预取）。
// 返回已写出字节数与错误；错误包含客户端断开与上游失败。
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
					if resp != nil {
						resp.Body.Close()
					}
				}
			}(i)
		}
	}()

	pending := make(map[int]chunkResult, f.parallel)
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
				return written, ctx.Err()
			}
			break
		}
		if cur.err != nil {
			return written, cur.err
		}
		copied, err := io.Copy(w, cur.resp.Body)
		cur.resp.Body.Close()
		written += copied
		if err != nil {
			return written, err
		}
	}
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
