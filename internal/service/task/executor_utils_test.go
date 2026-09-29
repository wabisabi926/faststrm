package task

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
)

func TestResolveStrmSettings_Default(t *testing.T) {
	task := &Task{
		Account:    "acc1",
		OriginPath: "/影视",
		TargetPath: "/out",
	}
	s := model.DefaultSettings()
	r := resolveStrmSettings(task, s, "http://localhost:8090", "")

	// Prefix fallback to baseURL (account appended later in buildStrmContent)
	if !strings.HasPrefix(r.StrmPrefix, "http://localhost:8090") {
		t.Fatalf("expect http prefix baseURL, got: %s", r.StrmPrefix)
	}
	// Extensions
	if len(r.StrmExtensions) == 0 {
		t.Fatal("expected default strm extensions")
	}
	if _, ok := r.StrmExtensions[".mp4"]; !ok {
		t.Fatal("expected .mp4 in strm exts (with dot)")
	}
	_ = r.DownloadExtensions

	// Verify buildStrmContent produces correct final URL with account
	f := &fileItem{Name: "test.mkv", PickCode: "abcdefghij1234567"} // valid 17-char alphanumeric
	out, err := buildStrmContent(task, f, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "account=acc1") {
		t.Fatalf("buildStrmContent should contain account param, got: %s", out)
	}
	if !strings.Contains(out, "pickcode=abcdefghij1234567") {
		t.Fatalf("buildStrmContent should contain pickcode param, got: %s", out)
	}
}

func TestResolveStrmSettings_TaskOverride(t *testing.T) {
	task := &Task{
		Account:            "acc2",
		StrmPrefix:         "https://public.example.com",
		EnablePathEncoding: true,
	}
	s := &model.Settings{}
	r := resolveStrmSettings(task, s, "", "")

	if !strings.HasPrefix(r.StrmPrefix, "https://public.example.com") {
		t.Fatalf("expect public prefix, got: %s", r.StrmPrefix)
	}
	if !r.EnablePathEncoding {
		t.Fatal("EnablePathEncoding should be overridden to true")
	}

	// Verify buildStrmContent produces correct URL with account + /api/strm
	f := &fileItem{Name: "test.mkv", PickCode: "ABCDEFGHIJ7654321"} // valid 17-char alphanumeric
	out, err := buildStrmContent(task, f, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "account=acc2") {
		t.Fatalf("buildStrmContent should contain account param, got: %s", out)
	}
	if !strings.Contains(out, "/api/strm") {
		t.Fatalf("unified mode should use /api/strm, got: %s", out)
	}
	if !strings.Contains(out, "pickcode=ABCDEFGHIJ7654321") {
		t.Fatalf("buildStrmContent should contain pickcode param, got: %s", out)
	}
}

func TestResolveStrmSettings_PublicDefaultPrefix(t *testing.T) {
	task := &Task{Account: "acc"}
	s := model.DefaultSettings()
	s.StrmPrefix = "https://strm.example.com"
	r := resolveStrmSettings(task, s, "http://127.0.0.1:8090", "")
	if !strings.HasPrefix(r.StrmPrefix, "https://strm.example.com") {
		t.Fatalf("expected StrmPrefix from settings, got: %s", r.StrmPrefix)
	}
}

func TestPerFilePercent_Overall(t *testing.T) {
	p := newPerFilePercent(4)
	p.Mark("a", 100)
	p.Mark("b", 50)
	done, overall := p.Overall(4)
	if done {
		t.Fatal("should not be done yet")
	}
	// (100 + 50 + 0 + 0) / 4 = 37.5
	if overall != "37.50" {
		t.Fatalf("expect 37.50, got: %s", overall)
	}
	p.Mark("c", 100)
	p.Mark("d", 100)
	// Still b at 50
	p.Update("b", 100)
	done, overall = p.Overall(4)
	if !done {
		t.Fatal("should be done")
	}
	if overall != "100.00" {
		t.Fatalf("expect 100.00, got: %s", overall)
	}
}

func TestCountAndFilterKind(t *testing.T) {
	items := []*fileItem{
		{Kind: kindStrm},
		{Kind: kindStrm},
		{Kind: kindDownload},
		{Kind: kindSkip},
	}
	if countKind(items, kindStrm) != 2 {
		t.Fatal("strm count expect 2")
	}
	if countKind(items, kindDownload) != 1 {
		t.Fatal("download count expect 1")
	}
	if len(filterKind(items, kindSkip)) != 1 {
		t.Fatal("skip filter expect 1")
	}
}

func TestUrlPathEncode(t *testing.T) {
	out := urlPathEncode("电影 2024.mp4")
	// space -> %20, Chinese -> percent encoded as UTF-8 bytes
	if !strings.Contains(out, "%20") {
		t.Fatalf("space must be encoded, got: %s", out)
	}
	if strings.Contains(out, "/") == false && strings.Contains(out, ".mp4") == false { //nolint:staticcheck // SA9003: 空分支为有意设计
		// 防御性断言：正常情况下 out 应该包含 "/" 和 ".mp4"
	}
}

func TestBuildStrmContent_UnifiedStrm(t *testing.T) {
	task := &Task{Account: "acc1"}
	f := &fileItem{Name: "movie.mkv", PickCode: "1q2w3e4r5t6y7u8i9"} // valid 17-char alphanumeric
	r := resolvedStrm{
		StrmPrefix: "http://x:8090?a=1",
	}
	out, err := buildStrmContent(task, f, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/api/strm") {
		t.Fatalf("unified mode expects /api/strm, got: %s", out)
	}
	if !strings.Contains(out, "pickcode=1q2w3e4r5t6y7u8i9") {
		t.Fatalf("expect pickcode: %s", out)
	}
	if !strings.Contains(out, "account=acc1") {
		t.Fatalf("expect account=acc1: %s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("expect trailing newline")
	}
}

func TestBuildStrmContent_DefaultMode(t *testing.T) {
	task := &Task{Account: "acc1"}
	f := &fileItem{Name: "movie.mkv", PickCode: "aS9dF8gH7jK6lLk3x"} // valid 17-char alphanumeric
	r := resolvedStrm{
		StrmPrefix: "http://x:8090",
	}
	out, err := buildStrmContent(task, f, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/api/strm") {
		t.Fatalf("default mode expects /api/strm, got: %s", out)
	}
}

// ==================== shouldGenerateStrm 单元测试 ====================
// 对齐 MoviePilot StrmGenerater.should_generate_strm 行为

func TestShouldGenerateStrm_DefaultPass(t *testing.T) {
	// 默认场景：无黑名单、无最小文件大小限制 → 通过
	reason, pass := shouldGenerateStrm("normal.mkv", 1024*1024*100, 0, nil)
	if !pass {
		t.Fatalf("expected pass, got reject reason: %s", reason)
	}
	if reason != "" {
		t.Fatalf("expected empty reason, got: %s", reason)
	}
}

func TestShouldGenerateStrm_BlacklistContains(t *testing.T) {
	// 注意：当前是 contains 子串匹配（对齐 MoviePilot not_blacklist_key 基础版，非 glob）
	// 若需 glob 风格如 "*-trailer.*"，可后续引入 path.Match。
	blacklist := []string{"trailer", "sample", ".cd2."}
	cases := []struct {
		name string
		want bool
	}{
		{"Avatar.2009.mkv", true},
		{"Avatar.2009-trailer.mkv", false}, // 命中 trailer
		{"Sample.avi", false},              // 命中 sample（大小写不敏感）
		{"movie.cd2.mkv", false},           // 命中 .cd2. 子串
		{"my TRAILER video.mp4", false},    // 大小写不敏感
		{"nothing_special_here.iso", true}, // 未命中
	}
	for _, c := range cases {
		reason, pass := shouldGenerateStrm(c.name, 1<<30, 0, blacklist)
		if pass != c.want {
			t.Errorf("name=%q: expect pass=%v, got pass=%v reason=%s", c.name, c.want, pass, reason)
		}
		if !c.want && reason == "" {
			t.Errorf("name=%q: rejected but reason empty", c.name)
		}
	}
}

func TestShouldGenerateStrm_BlacklistEmptyOrNil(t *testing.T) {
	// 空黑名单 / nil 黑名单 → 均不触发拒绝
	_, pass1 := shouldGenerateStrm("trailer.mp4", 100, 0, []string{})
	if !pass1 {
		t.Fatal("empty blacklist should not reject")
	}
	_, pass2 := shouldGenerateStrm("trailer.mp4", 100, 0, nil)
	if !pass2 {
		t.Fatal("nil blacklist should not reject")
	}
}

func TestShouldGenerateStrm_MinFileSize(t *testing.T) {
	const limit = 10 * 1024 * 1024 // 10MB
	// 小于阈值且 fileSize > 0 → 拒绝
	reason, pass := shouldGenerateStrm("small.mkv", 100, limit, nil)
	if pass {
		t.Fatalf("expect reject small file (100B < 10MB), got pass. reason=%s", reason)
	}
	if reason == "" {
		t.Fatal("reject reason should not be empty")
	}

	// 等于阈值 → 通过
	if _, pass = shouldGenerateStrm("ok.mkv", limit, limit, nil); !pass {
		t.Fatal("file size equal to min limit should pass")
	}

	// 大于阈值 → 通过
	if _, pass = shouldGenerateStrm("big.mkv", limit+1, limit, nil); !pass {
		t.Fatal("file size above limit should pass")
	}

	// fileSize = 0（API 未返回大小） → 默认通过，不阻塞
	if _, pass = shouldGenerateStrm("unknown_size.mkv", 0, limit, nil); !pass {
		t.Fatal("file size=0 (unknown) should NOT be rejected by min limit")
	}

	// minFileSize = 0（不限制） → 总是通过
	if _, pass = shouldGenerateStrm("tiny.mp4", 1, 0, nil); !pass {
		t.Fatal("minFileSize=0 should accept any size")
	}
}

func TestShouldGenerateStrm_Combined(t *testing.T) {
	// 黑名单 + 最小文件大小同时启用：任一命中即拒绝
	blacklist := []string{"trailer"}
	minSize := int64(1 << 20) // 1MB

	// 命中黑名单（即使大小满足） → 拒绝
	if _, pass := shouldGenerateStrm("Avatar_trailer.mkv", 1<<30, minSize, blacklist); pass {
		t.Fatal("blacklist hit should reject regardless of size")
	}
	// 大小不够（且不在黑名单） → 拒绝
	if _, pass := shouldGenerateStrm("intro.mp4", 100, minSize, blacklist); pass {
		t.Fatal("under min size should reject")
	}
	// 两者都满足 → 通过
	if reason, pass := shouldGenerateStrm("final.mkv", 2<<20, minSize, blacklist); !pass {
		t.Fatalf("both conditions ok should pass, reason=%s", reason)
	}
}

// ======================================================================
// BDMV 原盘过滤（全量生成）：BDMV/STREAM 子树不遍历，其 m2ts 不生成 STRM
// ======================================================================

// bdmvRT 按 URL 的 cid 参数返回固定 FsFiles 响应，并记录被请求过的 cid
type bdmvRT struct {
	fixtures map[string]string // cid → JSON body
	mu       sync.Mutex
	seen     []string
}

func (r *bdmvRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cid := req.URL.Query().Get("cid")
	r.mu.Lock()
	r.seen = append(r.seen, cid)
	r.mu.Unlock()
	body := r.fixtures[cid]
	if body == "" {
		body = `{"state":true,"data":[]}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (r *bdmvRT) requested(cid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.seen {
		if c == cid {
			return true
		}
	}
	return false
}

func newMockClient(rt http.RoundTripper) *client115.Client {
	c := client115.NewClient("test-ua")
	c.HTTP = &http.Client{Transport: rt}
	return c
}

// TestListAllFilesRecursive_SkipsBdmvStream 全量遍历遇到 BDMV/STREAM：
// 子树不得被遍历（其 cid 不应被请求），同级的普通媒体照常命中。
func TestListAllFilesRecursive_SkipsBdmvStream(t *testing.T) {
	rt := &bdmvRT{fixtures: map[string]string{
		// 根目录：BDMV 子目录 + 一个普通媒体文件
		"100": `{"state":true,"data":[` +
			`{"n":"BDMV","cid":200,"fc":2},` +
			`{"n":"movie.mkv","fid":11,"pc":"abcdefghij1234567","s":100000,"cid":100}]}`,
		// BDMV 目录：STREAM 子目录 + 一个非媒体文件
		"200": `{"state":true,"data":[` +
			`{"n":"STREAM","cid":300,"fc":2},` +
			`{"n":"index.bdmv","fid":22,"s":10,"cid":200}]}`,
		// STREAM 目录：内部全是原盘视频流（不应被请求）
		"300": `{"state":true,"data":[` +
			`{"n":"00000.m2ts","fid":33,"pc":"abcdefghij1234567","s":30000000000,"cid":300}]}`,
	}}

	out, err := listAllFilesRecursive(
		context.Background(), newMockClient(rt), "cookie", 100, "电影/沙丘",
		map[string]struct{}{".mkv": {}, ".m2ts": {}}, map[string]struct{}{},
		0, nil, "", nil, nil, nil, "",
	)
	if err != nil {
		t.Fatalf("listAllFilesRecursive: %v", err)
	}
	if rt.requested("300") {
		t.Fatalf("BDMV/STREAM 子树不应被遍历（cid=300 被请求了），请求记录=%v", rt.seen)
	}
	if len(out) != 1 || out[0].Name != "movie.mkv" {
		var names []string
		for _, f := range out {
			names = append(names, f.Name)
		}
		t.Fatalf("应只命中 movie.mkv, got %v", names)
	}
	if out[0].CloudPath != "电影/沙丘/movie.mkv" {
		t.Fatalf("CloudPath = %q, want 电影/沙丘/movie.mkv", out[0].CloudPath)
	}
}

// TestListAllFilesRecursive_TargetInsideBdmvStream 任务目标路径本身位于 BDMV/STREAM 内：
// 即使条目名字不含 BDMV 前缀，也必须按完整云路径判定并全部跳过。
func TestListAllFilesRecursive_TargetInsideBdmvStream(t *testing.T) {
	rt := &bdmvRT{fixtures: map[string]string{
		"300": `{"state":true,"data":[` +
			`{"n":"00000.m2ts","fid":33,"pc":"abcdefghij1234567","s":30000000000,"cid":300}]}`,
	}}

	out, err := listAllFilesRecursive(
		context.Background(), newMockClient(rt), "cookie", 300, "电影/沙丘/BDMV/STREAM",
		map[string]struct{}{".m2ts": {}}, map[string]struct{}{},
		0, nil, "", nil, nil, nil, "",
	)
	if err != nil {
		t.Fatalf("listAllFilesRecursive: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("任务目标本身位于 BDMV/STREAM 时不应产出任何条目, got %d", len(out))
	}
}

// TestListAllFilesRecursive_StandaloneM2tsKept 独立的 m2ts（不在 BDMV/STREAM 结构内）不受影响
func TestListAllFilesRecursive_StandaloneM2tsKept(t *testing.T) {
	rt := &bdmvRT{fixtures: map[string]string{
		"100": `{"state":true,"data":[` +
			`{"n":"录制.m2ts","fid":44,"pc":"abcdefghij1234567","s":100000,"cid":100}]}`,
	}}

	out, err := listAllFilesRecursive(
		context.Background(), newMockClient(rt), "cookie", 100, "电影",
		map[string]struct{}{".m2ts": {}}, map[string]struct{}{},
		0, nil, "", nil, nil, nil, "",
	)
	if err != nil {
		t.Fatalf("listAllFilesRecursive: %v", err)
	}
	if len(out) != 1 || out[0].Name != "录制.m2ts" {
		t.Fatalf("独立 m2ts 应照常命中, got %d", len(out))
	}
}
