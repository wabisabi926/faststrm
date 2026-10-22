package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCDN 模拟 115 CDN：支持 Range，返回 206 + Content-Range。
type fakeCDN struct {
	data     []byte
	requests atomic.Int32
	// failFirst 前 N 次请求返回 500（测试换链重试）
	failFirst int32
	// ignoreRange 返回 200 全量（测试降级）
	ignoreRange bool
}

func (f *fakeCDN) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := f.requests.Add(1)
		if n <= f.failFirst {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if f.ignoreRange {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(f.data)
			return
		}
		rangeHdr := r.Header.Get("Range")
		if rangeHdr == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(f.data)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(rangeHdr, "bytes=%d-%d", &start, &end); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if start < 0 || end >= int64(len(f.data)) || end < start {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		chunk := f.data[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(f.data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(chunk)
	}
}

// openUpstream 模拟 handleProxy 的首次请求（Range 透传）。
func openUpstream(t *testing.T, cdnURL, rangeHdr string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, cdnURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestStreamRangeProxy_ChunkAssembly(t *testing.T) {
	// 10MB 数据，分片 1MB、并发 2
	data := make([]byte, 10<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	cdn := &fakeCDN{data: data}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	upstream := openUpstream(t, srv.URL, fmt.Sprintf("bytes=0-%d", len(data)-1))
	defer upstream.Body.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/strm?account=a&pickcode=b", nil)
	opts := rangeProxyOptions{Enabled: true, Chunk: 1 << 20, Parallel: 2}

	if !streamRangeProxy(rec, req, upstream, srv.URL, nil, http.DefaultClient, nil, opts) {
		t.Fatal("streamRangeProxy should handle 206 response")
	}
	got := rec.Body.Bytes()
	if len(got) != len(data) {
		t.Fatalf("length: got %d, want %d", len(got), len(data))
	}
	if !bytes.Equal(got, data) {
		t.Fatal("chunk assembly produced different bytes")
	}
	// 首片 + 9 个分片 = 至少 10 次请求
	if cdn.requests.Load() < 10 {
		t.Fatalf("expected >=10 chunk requests, got %d", cdn.requests.Load())
	}
}

func TestStreamRangeProxy_MidRange(t *testing.T) {
	// 从中间位置开始的分片（seek 场景）：bytes=3MB-7MB
	data := make([]byte, 10<<20)
	for i := range data {
		data[i] = byte(i % 199)
	}
	cdn := &fakeCDN{data: data}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	start, end := 3<<20, 7<<20
	upstream := openUpstream(t, srv.URL, fmt.Sprintf("bytes=%d-%d", start, end))
	defer upstream.Body.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/strm", nil)
	opts := rangeProxyOptions{Enabled: true, Chunk: 1 << 20, Parallel: 2}
	if !streamRangeProxy(rec, req, upstream, srv.URL, nil, http.DefaultClient, nil, opts) {
		t.Fatal("should handle mid-range request")
	}
	want := data[start : end+1]
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("mid-range bytes mismatch: got %d want %d", rec.Body.Len(), len(want))
	}
}

func TestStreamRangeProxy_SmallRangeFallsBack(t *testing.T) {
	cdn := &fakeCDN{data: make([]byte, 4096)}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	upstream := openUpstream(t, srv.URL, "bytes=0-4095")
	defer upstream.Body.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/strm", nil)
	// 区间小于一片 → 不分片
	opts := rangeProxyOptions{Enabled: true, Chunk: 10 << 20, Parallel: 2}
	if streamRangeProxy(rec, req, upstream, srv.URL, nil, http.DefaultClient, nil, opts) {
		t.Fatal("small range should fall back to single-connection copy")
	}
}

func TestStreamRangeProxy_Disabled(t *testing.T) {
	cdn := &fakeCDN{data: make([]byte, 20<<20)}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	upstream := openUpstream(t, srv.URL, fmt.Sprintf("bytes=0-%d", len(cdn.data)-1))
	defer upstream.Body.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/strm", nil)
	if streamRangeProxy(rec, req, upstream, srv.URL, nil, http.DefaultClient, nil,
		rangeProxyOptions{Enabled: false, Chunk: 1 << 20, Parallel: 2}) {
		t.Fatal("disabled should fall back")
	}
}

func TestStreamRangeProxy_Upstream200FallsBack(t *testing.T) {
	cdn := &fakeCDN{data: make([]byte, 20<<20), ignoreRange: true}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	upstream := openUpstream(t, srv.URL, "bytes=0-100")
	defer upstream.Body.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/strm", nil)
	if streamRangeProxy(rec, req, upstream, srv.URL, nil, http.DefaultClient, nil,
		rangeProxyOptions{Enabled: true, Chunk: 1 << 20, Parallel: 2}) {
		t.Fatal("200 response must fall back to single-connection copy")
	}
}

func TestStreamRangeProxy_ClientGone(t *testing.T) {
	data := make([]byte, 10<<20)
	cdn := &fakeCDN{data: data}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	upstream := openUpstream(t, srv.URL, fmt.Sprintf("bytes=0-%d", len(data)-1))
	defer upstream.Body.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/strm", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	// 写 1KB 后取消（模拟客户端断开）
	w := &failAfterWriter{limit: 1024, cancel: cancel}
	opts := rangeProxyOptions{Enabled: true, Chunk: 1 << 20, Parallel: 2}
	// 不应 panic / 不应阻塞
	streamRangeProxy(w, req, upstream, srv.URL, nil, http.DefaultClient, nil, opts)
}

type failAfterWriter struct {
	limit   int
	written int
	cancel  context.CancelFunc
	header  http.Header
	status  int
}

func (f *failAfterWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failAfterWriter) WriteHeader(status int) { f.status = status }
func (f *failAfterWriter) Write(p []byte) (int, error) {
	remain := f.limit - f.written
	if remain <= 0 {
		f.cancel()
		return 0, io.ErrClosedPipe
	}
	if len(p) > remain {
		f.written = f.limit
		f.cancel()
		return remain, io.ErrClosedPipe
	}
	f.written += len(p)
	return len(p), nil
}

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		in              string
		start, end, tot int64
		ok              bool
	}{
		{"bytes 0-1023/4096", 0, 1023, 4096, true},
		{"bytes 1024-2047/4096", 1024, 2047, 4096, true},
		{"bytes 0-0/1", 0, 0, 1, true},
		{"", 0, 0, 0, false},
		{"bytes */4096", 0, 0, 0, false},
		{"items 0-10/100", 0, 0, 0, false},
		{"bytes 10-5/100", 0, 0, 0, false},
	}
	for _, c := range cases {
		s, e, tot, ok := parseContentRange(c.in)
		if ok != c.ok || s != c.start || e != c.end || tot != c.tot {
			t.Fatalf("parseContentRange(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
				c.in, s, e, tot, ok, c.start, c.end, c.tot, c.ok)
		}
	}
}

func TestStreamRangeProxy_RefreshRetry(t *testing.T) {
	// 首次请求（openUpstream）成功后，让下一个分片请求失败一次，验证换链重试
	data := make([]byte, 6<<20)
	cdn := &fakeCDN{data: data}
	srv := httptest.NewServer(cdn.handler())
	defer srv.Close()

	upstream := openUpstream(t, srv.URL, fmt.Sprintf("bytes=0-%d", len(data)-1))
	defer upstream.Body.Close()
	// 已发生 1 次请求 → 让第 2 次（首个分片）返回 500
	cdn.failFirst = cdn.requests.Load() + 1

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/strm", nil)
	refreshCalls := 0
	refresh := func(ctx context.Context) (string, error) {
		refreshCalls++
		return srv.URL, nil
	}
	opts := rangeProxyOptions{Enabled: true, Chunk: 1 << 20, Parallel: 2}
	if !streamRangeProxy(rec, req, upstream, srv.URL, nil, http.DefaultClient, refresh, opts) {
		t.Fatal("should handle 206")
	}
	if rec.Body.Len() != len(data) {
		t.Fatalf("bytes: got %d want %d", rec.Body.Len(), len(data))
	}
	if refreshCalls == 0 {
		t.Fatal("expected refresh to be called after 500 chunk")
	}
}

func TestRangeProxyOptions_Normalize(t *testing.T) {
	f := newRangeChunkFetcher("http://x", map[string]string{"User-Agent": "test"}, http.DefaultClient, 0, 0, nil)
	if f.chunk != 10<<20 {
		t.Fatalf("default chunk: got %d", f.chunk)
	}
	if f.parallel != 1 {
		t.Fatalf("default parallel: got %d", f.parallel)
	}
	f2 := newRangeChunkFetcher("http://x", nil, http.DefaultClient, 1<<20, 99, nil)
	if f2.parallel != 4 {
		t.Fatalf("parallel should cap at 4, got %d", f2.parallel)
	}
	if !strings.HasPrefix(f.headers["User-Agent"], "test") {
		t.Fatal("headers should be preserved")
	}
}

func TestRangeChunkFetcher_RejectsUnexpectedContentRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-9/100")
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 10))
	}))
	defer srv.Close()

	f := newRangeChunkFetcher(srv.URL, nil, http.DefaultClient, 1<<20, 2, nil)
	if _, err := f.fetch(context.Background(), 10, 19); err == nil {
		t.Fatal("mismatched Content-Range must be rejected")
	}
}

func TestRangeChunkFetcher_RejectsShortContentLength(t *testing.T) {
	body := io.NopCloser(bytes.NewReader(make([]byte, 10)))
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			Header:        http.Header{"Content-Range": []string{"bytes 0-9/100"}},
			Body:          body,
			ContentLength: 5,
			Request:       req,
		}, nil
	})
	f := newRangeChunkFetcher("http://cdn", nil, &http.Client{Transport: rt}, 1<<20, 2, nil)
	if _, err := f.fetch(context.Background(), 0, 9); err == nil {
		t.Fatal("short Content-Length must be rejected")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRangeChunkFetcher_RefreshIsSingleflight(t *testing.T) {
	var mu sync.Mutex
	failures := 4
	var refreshCalls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		fail := failures > 0
		if fail {
			failures--
		}
		mu.Unlock()
		if fail {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("busy")),
				Request:    req,
			}, nil
		}
		var start, end int64
		if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			return nil, err
		}
		data := make([]byte, end-start+1)
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header: http.Header{
				"Content-Range": []string{fmt.Sprintf("bytes %d-%d/1000", start, end)},
			},
			Body:          io.NopCloser(bytes.NewReader(data)),
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	refresh := func(context.Context) (string, error) {
		mu.Lock()
		refreshCalls++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return "http://cdn", nil
	}
	f := newRangeChunkFetcher("http://cdn", nil, &http.Client{Transport: rt}, 10, 4, refresh)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := f.fetch(context.Background(), int64(i*10), int64(i*10+9))
			if err == nil {
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	mu.Lock()
	got := refreshCalls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("concurrent refresh should be singleflight, got %d calls", got)
	}
}

type trackedBody struct {
	reader io.Reader
	closed *atomic.Int32
	once   sync.Once
}

func (b *trackedBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *trackedBody) Close() error {
	b.once.Do(func() { b.closed.Add(1) })
	return nil
}

func TestStreamRemaining_ClosesBodiesWhenChunkFails(t *testing.T) {
	var opened, closed atomic.Int32
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var start, end int64
		if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			return nil, err
		}
		opened.Add(1)
		if start == 20 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       &trackedBody{reader: strings.NewReader("fail"), closed: &closed},
				Request:    req,
			}, nil
		}
		data := make([]byte, end-start+1)
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header: http.Header{
				"Content-Range": []string{fmt.Sprintf("bytes %d-%d/40", start, end)},
			},
			Body:          &trackedBody{reader: bytes.NewReader(data), closed: &closed},
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	f := newRangeChunkFetcher("http://cdn", nil, &http.Client{Transport: rt}, 10, 4, nil)
	if _, err := f.streamRemaining(context.Background(), io.Discard, 0, 39); err == nil {
		t.Fatal("chunk failure should abort stream")
	}
	if got, want := closed.Load(), opened.Load(); got != want {
		t.Fatalf("all fetched bodies must be closed: opened=%d closed=%d", want, got)
	}
}

func TestCopyChunkExactly(t *testing.T) {
	var dst bytes.Buffer
	n, err := copyChunkExactly(&dst, strings.NewReader("abcdEXTRA"), 4)
	if err == nil {
		t.Fatal("overlong chunk should fail")
	}
	if n != 4 || dst.String() != "abcd" {
		t.Fatalf("n=%d body=%q", n, dst.String())
	}

	dst.Reset()
	if _, err := copyChunkExactly(&dst, strings.NewReader("abc"), 4); err == nil {
		t.Fatal("short chunk should fail")
	}

	dst.Reset()
	if _, err := copyChunkExactly(&dst, strings.NewReader("abcd"), 4); err != nil {
		t.Fatalf("exact chunk should pass: %v", err)
	}
}
