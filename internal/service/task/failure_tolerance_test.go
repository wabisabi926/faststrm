package task

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// flakyRT 按 cid 注入失败：指定 cid 的前 failTimes 次 RoundTrip 返回网络错误（之后成功）。
// 用于验证「列目录失败 → 重试 → 成功」路径。
type flakyRT struct {
	fixtures  map[string]string // cid → JSON body
	failCIDs  map[string]int    // cid → 剩余失败次数
	failState bool              // true=返回 state=false 响应；false=网络错误
	mu        sync.Mutex
	requests  map[string]int
}

func (r *flakyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cid := req.URL.Query().Get("cid")
	r.mu.Lock()
	r.requests[cid]++
	remain := r.failCIDs[cid]
	if remain > 0 {
		r.failCIDs[cid] = remain - 1
		r.mu.Unlock()
		if r.failState {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"state":false,"error":"mocked"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return nil, errors.New("mocked network error")
	}
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

func (r *flakyRT) requestCount(cid string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[cid]
}

// TestListAllFilesRecursive_RetriesFailedDir 子目录列目录失败一次后重试成功：
// 任务不失败，重试后条目照常产出，FailedDirs 为空。
func TestListAllFilesRecursive_RetriesFailedDir(t *testing.T) {
	oldWait := scanRetryWait
	scanRetryWait = 1 // ms
	t.Cleanup(func() { scanRetryWait = oldWait })

	rt := &flakyRT{
		fixtures: map[string]string{
			"100": `{"state":true,"data":[` +
				`{"n":"subdir","cid":200,"fc":2},` +
				`{"n":"root.mkv","fid":11,"pc":"abcdefghij1234567","s":100000,"cid":100}]}`,
			"200": `{"state":true,"data":[` +
				`{"n":"child.mkv","fid":22,"pc":"bcdefghijklmnopq7","s":100000,"cid":200}]}`,
		},
		failCIDs: map[string]int{"200": 1},
		requests: map[string]int{},
	}

	var stats ScanStats
	out, err := listAllFilesRecursive(
		context.Background(), newMockClient(rt), "cookie", 100, "电影",
		map[string]struct{}{".mkv": {}}, map[string]struct{}{},
		0, nil, "", nil, nil, nil, "", &stats,
	)
	if err != nil {
		t.Fatalf("失败后重试成功，任务不应失败: %v", err)
	}
	if got := rt.requestCount("200"); got != 2 {
		t.Fatalf("cid=200 应被请求 2 次（首失败+重试）, got %d", got)
	}
	if len(out) != 2 {
		t.Fatalf("重试成功后应命中 2 个文件, got %d", len(out))
	}
	if len(stats.FailedDirs) != 0 {
		t.Fatalf("重试成功后 FailedDirs 应为空, got %v", stats.FailedDirs)
	}
}

// TestListAllFilesRecursive_FailedDirRecorded 子目录始终失败：
// 任务不失败，该子树零产出，FailedDirs 记录 relPath。
func TestListAllFilesRecursive_FailedDirRecorded(t *testing.T) {
	oldWait := scanRetryWait
	scanRetryWait = 1
	t.Cleanup(func() { scanRetryWait = oldWait })

	rt := &flakyRT{
		fixtures: map[string]string{
			"100": `{"state":true,"data":[` +
				`{"n":"subdir","cid":200,"fc":2},` +
				`{"n":"root.mkv","fid":11,"pc":"abcdefghij1234567","s":100000,"cid":100}]}`,
		},
		failCIDs: map[string]int{"200": 999}, // 始终失败
		requests: map[string]int{},
	}

	var stats ScanStats
	out, err := listAllFilesRecursive(
		context.Background(), newMockClient(rt), "cookie", 100, "电影",
		map[string]struct{}{".mkv": {}}, map[string]struct{}{},
		0, nil, "", nil, nil, nil, "", &stats,
	)
	if err != nil {
		t.Fatalf("非根目录失败不应中断任务: %v", err)
	}
	if len(out) != 1 || out[0].Name != "root.mkv" {
		t.Fatalf("根下正常文件应照常命中, got %d", len(out))
	}
	if len(stats.FailedDirs) != 1 || stats.FailedDirs[0] != "subdir" {
		t.Fatalf("FailedDirs 应为 [subdir], got %v", stats.FailedDirs)
	}
}

// TestListAllFilesRecursive_RootFailureFatal 根目录失败仍是致命错误。
func TestListAllFilesRecursive_RootFailureFatal(t *testing.T) {
	oldWait := scanRetryWait
	scanRetryWait = 1
	t.Cleanup(func() { scanRetryWait = oldWait })

	rt := &flakyRT{
		failCIDs: map[string]int{"100": 999},
		requests: map[string]int{},
	}
	_, err := listAllFilesRecursive(
		context.Background(), newMockClient(rt), "cookie", 100, "电影",
		map[string]struct{}{".mkv": {}}, map[string]struct{}{},
		0, nil, "", nil, nil, nil, "", nil,
	)
	if err == nil {
		t.Fatal("根目录失败应返回错误")
	}
	if !strings.Contains(err.Error(), "root list failed") {
		t.Fatalf("错误信息应说明根目录失败, got %v", err)
	}
}

// TestWriteStrmFile_Dedup 内容未变化时跳过写盘（unchanged=true），变化时正常写入。
func TestWriteStrmFile_Dedup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.strm")

	unchanged, err := writeStrmFile(p, "http://x/1")
	if err != nil || unchanged {
		t.Fatalf("首次写入应成功且 unchanged=false, got unchanged=%v err=%v", unchanged, err)
	}
	info1, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	unchanged, err = writeStrmFile(p, "http://x/1")
	if err != nil || !unchanged {
		t.Fatalf("相同内容应 unchanged=true, got unchanged=%v err=%v", unchanged, err)
	}
	info2, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("跳过写入时 mtime 不应变化（否则触发 Emby 假重扫）")
	}

	unchanged, err = writeStrmFile(p, "http://x/2")
	if err != nil || unchanged {
		t.Fatalf("内容变化应正常写入 unchanged=false, got unchanged=%v err=%v", unchanged, err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "http://x/2" {
		t.Fatalf("内容应更新为新值, got %q", string(got))
	}
}

// TestCleanupOrphanStrms_FailedDirProtected 位于失败目录前缀下的 STRM 不参与对账。
func TestCleanupOrphanStrms_FailedDirProtected(t *testing.T) {
	target := t.TempDir()
	failedDir := filepath.Join(target, "电影", "失败目录")
	if err := os.MkdirAll(failedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphanPath := filepath.Join(failedDir, "x.strm")
	// faststrm 生成的 STRM 内容是 URL（ExtractPickcode 从内容提取 pickcode=17位）
	orphanContent := `http://192.168.1.5:8090/api/strm?account=a&pickcode=abcdefghij1234567&file_name=x`
	if err := os.WriteFile(orphanPath, []byte(orphanContent), 0o600); err != nil {
		t.Fatal(err)
	}
	// 同目录树之外放一个正常命中孤儿的文件，验证保护只作用于前缀内
	otherDir := filepath.Join(target, "电影", "正常目录")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "y.strm"), []byte(orphanContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// 本次清单：只含一个无关 pickcode，两个本地 STRM 都应判孤儿
	cloud := map[string]string{"zzzzzzzzzzzzzzz99": "无关/x.strm"}

	// 带失败目录前缀：前缀内的 x.strm 被跳过，前缀外的 y.strm 仍判孤儿
	n, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"task1", target, cloud, nil, []string{"电影/失败目录"}, "mark_only",
		model.CleanupSettings{}, nil)
	if err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if n != 1 {
		t.Fatalf("前缀保护生效时应只剩 1 个孤儿, got %d", n)
	}

	// 不带前缀：两个都算孤儿
	n2, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"task1", target, cloud, nil, nil, "mark_only",
		model.CleanupSettings{}, nil)
	if err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if n2 != 2 {
		t.Fatalf("无保护时应检出 2 个孤儿, got %d", n2)
	}
}

// TestCleanupOrphanStrms_StalePathDetected pickcode 仍在云端但本地路径变了（云端改名/移动），
// 旧 STRM 必须被判为残留——这是原来只有路径级清理能做的事。
func TestCleanupOrphanStrms_StalePathDetected(t *testing.T) {
	target := t.TempDir()
	oldDir := filepath.Join(target, "电影", "旧片名")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pc := "abcdefghij1234567"
	content := `http://x/api/strm?account=a&pickcode=` + pc
	oldPath := filepath.Join(oldDir, "movie.strm")
	if err := os.WriteFile(oldPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// 云端期望路径：电影/新片名/movie.strm
	cloud := map[string]string{pc: filepath.Join("电影", "新片名", "movie.strm")}
	n, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, cloud, nil, nil, "mark_only", model.CleanupSettings{}, nil)
	if err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if n != 1 {
		t.Fatalf("路径不一致的旧 STRM 应判残留, got %d", n)
	}

	// 期望路径与本地一致 → 不算孤儿
	same := map[string]string{pc: filepath.Join("电影", "旧片名", "movie.strm")}
	n2, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, same, nil, nil, "mark_only", model.CleanupSettings{}, nil)
	if err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("路径一致不应判孤儿, got %d", n2)
	}
}

// TestCleanupOrphanStrms_SnapshotOnlyKept 仅 DB 快照知道的 pickcode 一律保留（路径未知，防误删）
func TestCleanupOrphanStrms_SnapshotOnlyKept(t *testing.T) {
	target := t.TempDir()
	dir := filepath.Join(target, "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pc := "abcdefghij1234567"
	if err := os.WriteFile(filepath.Join(dir, "movie.strm"),
		[]byte(`http://x/api/strm?account=a&pickcode=`+pc), 0o600); err != nil {
		t.Fatal(err)
	}

	n, _, err := cleanupOrphanStrms(context.Background(), nil, nil,
		"t", target, nil, map[string]struct{}{pc: {}}, nil, "mark_only", model.CleanupSettings{}, nil)
	if err != nil {
		t.Fatalf("cleanupOrphanStrms: %v", err)
	}
	if n != 0 {
		t.Fatalf("快照命中的 STRM 应保留, got %d", n)
	}
}
