package task

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
)

// fullListRT 假 115：cur=0 请求按 offset 返回分页数据，普通列目录请求按 cid 返回祖先链。
type fullListRT struct {
	mu      sync.Mutex
	pages   map[string]string // offset → JSON body
	chains  map[string]string // cid → JSON body
	repeat  string            // 非空时所有 offset 都返回它（模拟服务端忽略 offset）
	reqURLs []url.URL
}

func (r *fullListRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqURLs = append(r.reqURLs, *req.URL)
	r.mu.Unlock()

	q := req.URL.Query()
	body := ""
	if q.Get("cur") == "0" {
		if r.repeat != "" {
			body = r.repeat
		} else {
			body = r.pages[q.Get("offset")]
		}
	} else {
		body = r.chains[q.Get("cid")]
	}
	if body == "" {
		body = `{"state":true,"data":[],"count":0}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (r *fullListRT) lastQuery(t *testing.T) url.Values {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqURLs) == 0 {
		t.Fatal("没有发出任何请求")
	}
	return r.reqURLs[0].Query()
}

func pageJSON(count int, entries ...string) string {
	return `{"state":true,"count":` + itoaTest(count) + `,"data":[` + strings.Join(entries, ",") + `]}`
}

func itoaTest(n int) string { return strconv.Itoa(n) }

func TestFetchFullListPages_TerminatesOnEmptyPage(t *testing.T) {
	calls := 0
	fetch := func(ctx context.Context, offset, limit int) (*client115.FsFilesAllResp, error) {
		calls++
		switch offset {
		case 0:
			return &client115.FsFilesAllResp{State: true, Count: 3, Data: mkEntries(0, 1)}, nil
		case 2:
			return &client115.FsFilesAllResp{State: true, Count: 3, Data: mkEntries(2)}, nil
		default:
			return &client115.FsFilesAllResp{State: true, Count: 3}, nil
		}
	}
	seen, expected, err := fetchFullListPages(context.Background(), fetch, 2, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if seen != 3 || expected != 3 {
		t.Fatalf("seen=%d expected=%d, want 3/3", seen, expected)
	}
	if calls < 3 {
		t.Fatalf("应至少调用 3 次（含结束空页），实际 %d", calls)
	}
}

func TestFetchFullListPages_IncompleteListFails(t *testing.T) {
	fetch := func(ctx context.Context, offset, limit int) (*client115.FsFilesAllResp, error) {
		if offset == 0 {
			return &client115.FsFilesAllResp{State: true, Count: 100, Data: mkEntries(0, 1, 2)}, nil
		}
		return &client115.FsFilesAllResp{State: true, Count: 100}, nil
	}
	_, _, err := fetchFullListPages(context.Background(), fetch, 100, nil)
	if err == nil || !strings.Contains(err.Error(), "不完整") {
		t.Fatalf("清单不完整应报错，实际 err=%v", err)
	}
}

func TestFetchFullListPages_RepeatedPageFails(t *testing.T) {
	rt := &fullListRT{repeat: pageJSON(2, entryJSON(1, 100, "a.mkv"), entryJSON(2, 100, "b.mkv"))}
	c := client115.NewClient("ua")
	c.HTTP = &http.Client{Transport: rt}
	fetch := func(ctx context.Context, offset, limit int) (*client115.FsFilesAllResp, error) {
		return c.FsFilesAll(ctx, "100", limit, offset, "cookie")
	}
	_, _, err := fetchFullListPages(context.Background(), fetch, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复分页应报错，实际 err=%v", err)
	}
}

func TestFsFilesAll_RequestParams(t *testing.T) {
	rt := &fullListRT{pages: map[string]string{"0": pageJSON(0)}}
	c := client115.NewClient("ua")
	c.HTTP = &http.Client{Transport: rt}

	if _, err := c.FsFilesAll(context.Background(), "100", 1000, 0, "cookie"); err != nil {
		t.Fatalf("FsFilesAll: %v", err)
	}
	q := rt.lastQuery(t)
	for k, want := range map[string]string{"cur": "0", "show_dir": "0", "count_folders": "0", "fc_mix": "1", "cid": "100"} {
		if got := q.Get(k); got != want {
			t.Errorf("参数 %s = %q, want %q", k, got, want)
		}
	}
}

func TestFullListEntry_AliasFields(t *testing.T) {
	cases := []string{
		// web 短名
		`{"fid":11,"pid":100,"n":"a.mkv","pc":"1q2w3e4r5t6y7u8i9","s":123,"aid":1}`,
		// 长名
		`{"file_id":11,"parent_id":100,"file_name":"a.mkv","pick_code":"1q2w3e4r5t6y7u8i9","size_byte":123,"aid":1}`,
		// 文件条目的 cid 即父目录
		`{"fid":"11","cid":"100","fn":"a.mkv","pickcode":"1q2w3e4r5t6y7u8i9","size":"123","aid":1}`,
	}
	for i, raw := range cases {
		var e client115.FullListEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if e.FID != 11 || e.PID != 100 || e.Name != "a.mkv" || e.PickCode != "1q2w3e4r5t6y7u8i9" || e.Size != 123 {
			t.Fatalf("case %d 解析错误: %+v", i, e)
		}
	}
}

func TestResolveRelPathFromChain(t *testing.T) {
	chain := mkChain()
	cases := []struct {
		root int64
		want string
		ok   bool
	}{
		{100, "合集", true},
		{0, "电影/合集", true},
		{200, "", true},
		{999, "", false},
	}
	for _, c := range cases {
		got, ok := resolveRelPathFromChain(chain, c.root)
		if ok != c.ok || got != c.want {
			t.Errorf("root=%d got=(%q,%v), want (%q,%v)", c.root, got, ok, c.want, c.ok)
		}
	}
}

func TestCloudParent(t *testing.T) {
	cases := map[string]string{
		"/电影/合集":  "/电影",
		"/电影/合集/": "/电影",
		"/电影":     "/",
		"":        "",
	}
	for in, want := range cases {
		if got := cloudParent(in); got != want {
			t.Errorf("cloudParent(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestDirPathResolver_CacheConsistency 云端改名后（父路径与 parent_id 对不上）缓存不可信
func TestDirPathResolver_CacheConsistency(t *testing.T) {
	r := &dirPathResolver{
		originPath: "/电影",
		rootCID:    100,
		cache:      map[int64]string{},
		folders: map[int64]db.FolderPath{
			100: {Path: "/电影", ParentID: 0},
			200: {Path: "/电影/合集", ParentID: 100},
			300: {Path: "/电影/旧名/子目录", ParentID: 200}, // 父路径与 parent_id 不一致
		},
	}
	if rel, ok := r.fromCacheTable(200); !ok || rel != "合集" {
		t.Fatalf("正常缓存应命中: rel=%q ok=%v", rel, ok)
	}
	if _, ok := r.fromCacheTable(300); ok {
		t.Fatal("父路径与 parent_id 不一致时应判定缓存陈旧")
	}
}

// TestListAllFilesViaFullList_EndToEnd 全量清单 → pid 路径还原 → fileItem
func TestListAllFilesViaFullList_EndToEnd(t *testing.T) {
	rt := &fullListRT{
		pages: map[string]string{
			"0": pageJSON(3,
				entryJSON(11, 200, "电影:1.mkv"),
				entryJSON(12, 202, "00001.m2ts"),
				entryJSON(13, 200, "bad.mkv"),
			),
			// 第二页为空 → 结束
		},
		chains: map[string]string{
			"200": `{"state":true,"data":[],"path":[{"cid":0,"pid":0,"name":"根目录"},{"cid":100,"pid":0,"name":"电影"},{"cid":200,"pid":100,"name":"合集"}]}`,
			"202": `{"state":true,"data":[],"path":[{"cid":0,"pid":0,"name":"根目录"},{"cid":100,"pid":0,"name":"电影"},{"cid":200,"pid":100,"name":"合集"},{"cid":201,"pid":200,"name":"BDMV"},{"cid":202,"pid":201,"name":"STREAM"}]}`,
		},
	}
	c := client115.NewClient("ua")
	c.HTTP = &http.Client{Transport: rt}

	strmExts := map[string]struct{}{".mkv": {}, ".m2ts": {}}
	downloadExts := map[string]struct{}{}
	var stats ScanStats
	items, err := listAllFilesViaFullList(context.Background(), c, "cookie", 100, "/电影",
		strmExts, downloadExts, 0, nil, "", nil, nil, nil, "", &stats)
	if err != nil {
		t.Fatalf("listAllFilesViaFullList: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应只保留 1 个有效条目（BDMV 跳过 + pickcode 无效跳过），实际 %d: %+v", len(items), items)
	}
	it := items[0]
	if it.RelPath != "合集/电影:1.mkv" || it.CloudPath != "/电影/合集/电影:1.mkv" {
		t.Fatalf("路径还原错误: %+v", it)
	}
	if it.ParentID != "200" || it.FID != "11" || it.Kind != kindStrm {
		t.Fatalf("条目字段错误: %+v", it)
	}
	if stats.BdmvSkipped != 1 {
		t.Fatalf("BDMV 跳过计数=%d, want 1", stats.BdmvSkipped)
	}
}

// ---------- 测试辅助 ----------

func mkEntries(ids ...int64) []client115.FullListEntry {
	out := make([]client115.FullListEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, client115.FullListEntry{FID: id, PID: 100, Name: "a.mkv", PickCode: "1q2w3e4r5t6y7u8i9"})
	}
	return out
}

func mkChain() []client115.FsFilesPathNode {
	return []client115.FsFilesPathNode{
		{Cid: 0, Pid: 0, Name: "根目录"},
		{Cid: 100, Pid: 0, Name: "电影"},
		{Cid: 200, Pid: 100, Name: "合集"},
	}
}

func entryJSON(fid, pid int64, name string) string {
	pc := "1q2w3e4r5t6y7u8i9"
	if name == "bad.mkv" {
		pc = "short"
	}
	b, _ := json.Marshal(map[string]any{"fid": fid, "pid": pid, "n": name, "pc": pc, "s": 1024, "aid": 1})
	return string(b)
}
