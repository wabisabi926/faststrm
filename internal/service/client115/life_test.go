// Package client115 unit tests for life.go (PullEvents / pickcode fallback)
package client115

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ==================== parseCountInt / parseNextPage ====================

func TestParseCountInt(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"int", 100, 100},
		{"int64", int64(200), 200},
		{"float64", 300.0, 300},
		{"json-number", json.Number("400"), 400},
		{"string", "500", 500},
		{"string-invalid", "abc", 0},
		{"nil", nil, 0},
		{"bool", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCountInt(tc.in); got != tc.want {
				t.Errorf("parseCountInt(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseNextPage(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"bool-true", true, true},
		{"bool-false", false, false},
		{"int-1", 1, true},
		{"int-0", 0, false},
		{"float-1", 1.0, true},
		{"float-0", 0.0, false},
		{"string-1", "1", true},
		{"string-true", "true", true},
		{"string-True", "True", true},
		{"string-0", "0", false},
		{"string-false", "false", false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNextPage(tc.in); got != tc.want {
				t.Errorf("parseNextPage(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ==================== GetPickCodeByFileID ====================

func newLifeClientWithTrips(t *testing.T, trips []*mockTrip) *LifeClient {
	t.Helper()
	return &LifeClient{
		cookie:     "UID=x;CID=y;SEID=z;KID=w",
		httpClient: &http.Client{Transport: &mockRoundTripper{trips: trips, t: t}},
	}
}

func TestGetPickCodeByFileID_Success(t *testing.T) {
	var called bool
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/info",
		Query:      map[string]string{"file_id": "100"},
		BodyString: `{"state":true,"data":[{"pc":"abc123def456ghi7"}]}`,
		called:     &called,
	}})
	pc, err := lc.GetPickCodeByFileID(context.Background(), "100")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !called {
		t.Fatal("request not sent")
	}
	if pc != "abc123def456ghi7" {
		t.Errorf("pc want abc123def456ghi7, got %q", pc)
	}
}

func TestGetPickCodeByFileID_StateFalse(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/info",
		BodyString: `{"state":false,"errmsg":"file not found"}`,
	}})
	_, err := lc.GetPickCodeByFileID(context.Background(), "100")
	if err == nil || !strings.Contains(err.Error(), "state=false") {
		t.Errorf("want state=false error, got %v", err)
	}
}

func TestGetPickCodeByFileID_EmptyData(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/info",
		BodyString: `{"state":true,"data":[]}`,
	}})
	_, err := lc.GetPickCodeByFileID(context.Background(), "100")
	if err == nil || !strings.Contains(err.Error(), "empty data") {
		t.Errorf("want empty data error, got %v", err)
	}
}

func TestGetPickCodeByFileID_EmptyPickCode(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/info",
		BodyString: `{"state":true,"data":[{"pc":""}]}`,
	}})
	_, err := lc.GetPickCodeByFileID(context.Background(), "100")
	if err == nil || !strings.Contains(err.Error(), "无 pickcode") {
		t.Errorf("want no-pickcode error, got %v", err)
	}
}

func TestGetPickCodeByFileID_EmptyFileID(t *testing.T) {
	lc := &LifeClient{cookie: "x", httpClient: &http.Client{}}
	if _, err := lc.GetPickCodeByFileID(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "file_id is empty") {
		t.Errorf("empty file_id should error, got %v", err)
	}
	if _, err := lc.GetPickCodeByFileID(context.Background(), "0"); err == nil || !strings.Contains(err.Error(), "file_id is empty") {
		t.Errorf("file_id=0 should error, got %v", err)
	}
}

func TestGetPickCodeByFileID_HTTPError(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/info",
		Status:     http.StatusInternalServerError,
		BodyString: "oops",
	}})
	_, err := lc.GetPickCodeByFileID(context.Background(), "100")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("want HTTP 500 error, got %v", err)
	}
}

// ==================== ResolvePath（对齐参考项目：按 file_category 分流） ====================

// 文件事件(parent_id=0 + file_category!=0)：绝不把 file_id 当文件夹 cid 调 medialist，
// 直接兜底返回裸文件名。empty trips 意味着任何请求都会 t.Errorf。
func TestResolvePath_FileEvent_NoMedialist(t *testing.T) {
	lc := newLifeClientWithTrips(t, nil) // 不允许任何网络请求
	got := lc.ResolvePath(context.Background(), "0", "100001", "movie.mkv", 1)
	if got != "/movie.mkv" {
		t.Fatalf("want /movie.mkv, got %q", got)
	}
}

// 文件夹事件(parent_id=0 + file_category==0)：file_id 是合法文件夹 cid，可用它反查祖先链。
// 真实 /files/medialist 响应字段为 path，元素 {cid,name,pid}，且**含目标目录自身作为最后一项**。
func TestResolvePath_FolderEvent_UsesFileIDAsCid(t *testing.T) {
	var medialistCalled bool
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":true,"path":[{"cid":"1","name":"根目录","pid":"0"},{"cid":"777","name":"电影","pid":"1"},{"cid":"888","name":"新文件夹","pid":"777"}]}`,
		called:     &medialistCalled,
	}})
	got := lc.ResolvePath(context.Background(), "0", "888", "新文件夹", 0)
	if !medialistCalled {
		t.Fatal("medialist should be called for folder event file_id-as-cid")
	}
	if got != "电影/新文件夹" {
		t.Fatalf("want 电影/新文件夹, got %q", got)
	}
}

// FsFilesMediaAncestors 解析真实 path 字段（{cid,name,pid}，含目录自身），
// 并兼容 id/cid/pid 为数字字面量或数字字符串两种形态。
func TestFsFilesMediaAncestors_ParsesPathIncludingSelf(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888", "aid": "1", "format": "json"},
		BodyString: `{"state":true,"path":[{"cid":"1","name":"根目录","pid":"0"},{"cid":777,"name":"电影","pid":1},{"cid":"888","name":"新文件夹","pid":777}]}`,
	}})
	nodes, err := lc.FsFilesMediaAncestors(context.Background(), "888")
	if err != nil {
		t.Fatalf("FsFilesMediaAncestors err: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d: %+v", len(nodes), nodes)
	}
	last := nodes[len(nodes)-1]
	if last.Name != "新文件夹" || last.ID.Int() != 888 || last.ParentID.Int() != 777 {
		t.Fatalf("last node mismatch: %+v", last)
	}
}

// 解析出错（返回非 JSON，如风控/登录页）时必须把原始响应带出，
// 供运维「最终判定」是接口不可靠还是解析问题。
func TestFsFilesMediaAncestors_ParseError_SurfacesRawBody(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `<html>风控验证页</html>`,
	}})
	_, err := lc.FsFilesMediaAncestors(context.Background(), "888")
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
	if !strings.Contains(err.Error(), "风控验证页") {
		t.Fatalf("parse error should surface raw body, got %v", err)
	}
}

// state=false（接口拒绝该 cid）时同样把 errmsg 与原始响应带出。
func TestFsFilesMediaAncestors_StateFalse_SurfacesRawBody(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":false,"errmsg":"cid 无效"}`,
	}})
	_, err := lc.FsFilesMediaAncestors(context.Background(), "888")
	if err == nil {
		t.Fatal("want state=false error, got nil")
	}
	if !strings.Contains(err.Error(), "state=false") || !strings.Contains(err.Error(), "cid 无效") {
		t.Fatalf("state=false error should surface errmsg, got %v", err)
	}
}

// state=true 且接口真的返回空数组（path:[]）→ 无可用祖先，返回 nil,nil（不报错），交由上层降级链。
func TestFsFilesMediaAncestors_EmptyPath_ReturnsNilNoError(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":true,"path":[]}`,
	}})
	nodes, err := lc.FsFilesMediaAncestors(context.Background(), "888")
	if err != nil {
		t.Fatalf("empty path should not error, got %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("want 0 nodes, got %d: %+v", len(nodes), nodes)
	}
}

// path 含目标自身时，ResolvePathByFileID 必须丢弃自身项，避免路径出现重复段。
func TestResolvePathByFileID_PathIncludesSelf(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":true,"path":[{"cid":1,"name":"根目录","pid":0},{"cid":777,"name":"电影","pid":1},{"cid":888,"name":"新文件夹","pid":777}]}`,
	}})
	got := lc.ResolvePathByFileID(context.Background(), "888", "新文件夹")
	if got != "电影/新文件夹" {
		t.Fatalf("want 电影/新文件夹, got %q", got)
	}
}

// 祖先链为空时走「语义优先」降级链：先用 ResolveDirPath(fileID) 做完整祖先链解析（支持任意深度），
// 而不是盲目假设「父目录是根目录」并浪费地遍历根目录一级子目录。
func TestResolvePathByFileID_SemanticFirstViaResolveDirPath(t *testing.T) {
	// medialist(cid=888) 返回 state=true 但 path/ancestors 皆空 → ancestors=0，进入降级链。
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":true}`,
	}})
	// 预置 ResolveDirPath 的 cid 缓存（key=888），模拟该目录此前已被其它事件解析并缓存：
	// 命中缓存即得完整路径「电影/动作片/新文件夹」，无需再发任何 FsFiles 请求。
	lc.pathCache.Store("888", cachedPathEntry{
		path:      "电影/动作片/新文件夹",
		expiresAt: time.Now().Add(time.Minute),
	})
	got := lc.ResolvePathByFileID(context.Background(), "888", "新文件夹")
	if got != "电影/动作片/新文件夹" {
		t.Fatalf("want 电影/动作片/新文件夹, got %q", got)
	}
}

// 语义优先未命中且不在根目录时，仍保留「根目录一级子目录浅扫」作为末端兜底（受 maxRootSubdirScan 限制）。
func TestResolvePathByFileID_RootSubdirShallowScanFallback(t *testing.T) {
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":true}`,
	}})
	lc.fsClient = newMockClient(t, []*mockTrip{
		{
			Path:       "/files",
			Query:      map[string]string{"cid": "888"},
			BodyString: `{"state":true,"data":[]}`,
		},
		{
			Path:       "/files",
			Query:      map[string]string{"cid": "0"},
			BodyString: `{"state":true,"data":[{"fid":0,"cid":777,"n":"电影","fc":0}]}`,
		},
		{
			Path:       "/files",
			Query:      map[string]string{"cid": "777"},
			BodyString: `{"state":true,"data":[{"fid":0,"cid":888,"n":"新文件夹","fc":0}]}`,
		},
	})
	got := lc.ResolvePathByFileID(context.Background(), "888", "新文件夹")
	if got != "电影/新文件夹" {
		t.Fatalf("want 电影/新文件夹, got %q", got)
	}
}

// 文件事件(parent_id 合法)：与参考项目一致走 parentID 反查父目录，不触发 file_id 反查。
// 第5项后：ResolveDirPath 首选 /files 列目录响应的 path 祖先链，故不再调用 medialist。
func TestResolvePath_FileEvent_WithParentID(t *testing.T) {
	var medialistCalled bool
	// medialist 作为「不应被命中」的守卫：新逻辑应优先 /files path。
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "555"},
		BodyString: `{"state":true,"ancestors":[{"id":1,"name":"根目录","parent_id":0},{"id":777,"name":"电影","parent_id":1}]}`,
		called:     &medialistCalled,
	}})
	// fsClient 提供 /files?cid=555 的 path（含目标自身），一次请求即得祖先段 + 自身名。
	lc.fsClient = newMockClient(t, []*mockTrip{{
		Path:       "/files",
		Query:      map[string]string{"cid": "555"},
		BodyString: `{"state":true,"data":[],"path":[{"cid":1,"name":"根目录","pid":0},{"cid":777,"name":"电影","pid":1},{"cid":555,"name":"动作片","pid":777}]}`,
	}})
	got := lc.ResolvePath(context.Background(), "555", "100001", "movie.mkv", 1)
	if medialistCalled {
		t.Fatal("新逻辑应优先 /files path 解析祖先链，不应回退调用 medialist")
	}
	if got != "电影/动作片/movie.mkv" {
		t.Fatalf("want 电影/动作片/movie.mkv, got %q", got)
	}
}

// ==================== PullEvents ====================

func TestPullEvents_EmptyCookie(t *testing.T) {
	lc := &LifeClient{cookie: ""}
	if _, err := lc.PullEvents(context.Background(), "acc1", 0, 0); err == nil || !strings.Contains(err.Error(), "cookie is empty") {
		t.Errorf("empty cookie should error, got %v", err)
	}
}

func TestPullEvents_CountTermination(t *testing.T) {
	var called bool
	trips := []*mockTrip{{
		Path: "/ios/behavior/detail",
		BodyString: `{"code":0,"data":{"count":2,"next_page":1,"list":[
			{"id":"1","type":2,"file_id":"100","file_name":"a.mkv","update_time":2000},
			{"id":"2","type":2,"file_id":"101","file_name":"b.mkv","update_time":2001}
		]}}`,
		called: &called,
	}}
	lc := newLifeClientWithTrips(t, trips)
	events, err := lc.PullEvents(context.Background(), "acc1", 0, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !called {
		t.Fatal("request not sent")
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
}

func TestPullEvents_FileIDDedup(t *testing.T) {
	trips := []*mockTrip{{
		Path: "/ios/behavior/detail",
		BodyString: `{"code":0,"data":{"count":2,"next_page":0,"list":[
			{"id":"2","type":2,"file_id":"100","file_name":"a.mkv","update_time":2001},
			{"id":"1","type":2,"file_id":"100","file_name":"a.mkv","update_time":2000}
		]}}`,
	}}
	lc := newLifeClientWithTrips(t, trips)
	events, err := lc.PullEvents(context.Background(), "acc1", 0, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event after file_id dedup, got %d", len(events))
	}
	if events[0].ID != "2" {
		t.Errorf("should keep the latest event id=2, got %s", events[0].ID)
	}
}

func TestPullEvents_CursorFilter_HitOldBreak(t *testing.T) {
	trips := []*mockTrip{{
		Path: "/ios/behavior/detail",
		BodyString: `{"code":0,"data":{"count":10,"next_page":1,"list":[
			{"id":"2","type":2,"file_id":"101","file_name":"b.mkv","update_time":2000},
			{"id":"1","type":2,"file_id":"100","file_name":"a.mkv","update_time":500}
		]}}`,
	}}
	lc := newLifeClientWithTrips(t, trips)
	events, err := lc.PullEvents(context.Background(), "acc1", 1000, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event (hit old -> break), got %d", len(events))
	}
	if events[0].ID != "2" {
		t.Errorf("want keep id=2, got %s", events[0].ID)
	}
}

func TestPullEvents_HostRotation(t *testing.T) {
	var proCalled, webCalled bool
	trips := []*mockTrip{
		{
			Path:       "/ios/behavior/detail",
			BodyString: `{"code":0,"data":{"count":10,"next_page":1,"list":[{"id":"1","type":2,"file_id":"100","file_name":"a.mkv","update_time":2000}]}}`,
			called:     &proCalled,
		},
		{
			Path:       "/behavior/detail",
			BodyString: `{"code":0,"data":{"count":10,"next_page":0,"list":[{"id":"2","type":2,"file_id":"101","file_name":"b.mkv","update_time":2001}]}}`,
			called:     &webCalled,
		},
	}
	lc := newLifeClientWithTrips(t, trips)
	events, err := lc.PullEvents(context.Background(), "acc1", 0, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !proCalled {
		t.Error("page0 should hit proapi (even page)")
	}
	if !webCalled {
		t.Error("page1 should hit webapi (odd page)")
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	if events[0].ID != "1" || events[1].ID != "2" {
		t.Errorf("page order mismatch: got %s,%s want 1,2", events[0].ID, events[1].ID)
	}
}

func TestPullEvents_CookieExpired(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/ios/behavior/detail",
		BodyString: `{"code":99,"error":"session expired"}`,
	}}
	lc := newLifeClientWithTrips(t, trips)
	_, err := lc.PullEvents(context.Background(), "acc1", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "cookie 已过期") {
		t.Errorf("want cookie expired error, got %v", err)
	}
}

func TestPullEvents_RequestError_SwitchHostRetry(t *testing.T) {
	trips := []*mockTrip{
		{
			Path:       "/ios/behavior/detail",
			Status:     http.StatusInternalServerError,
			BodyString: "oops",
		},
		{
			Path:       "/behavior/detail",
			BodyString: `{"code":0,"data":{"count":1,"next_page":0,"list":[{"id":"1","type":2,"file_id":"100","file_name":"a.mkv","update_time":2000}]}}`,
		},
	}
	lc := newLifeClientWithTrips(t, trips)
	events, err := lc.PullEvents(context.Background(), "acc1", 0, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event after host switch retry, got %d", len(events))
	}
}

func TestPullEvents_EmptyList_Break(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/ios/behavior/detail",
		BodyString: `{"code":0,"data":{"count":0,"next_page":1,"list":[]}}`,
	}}
	lc := newLifeClientWithTrips(t, trips)
	events, err := lc.PullEvents(context.Background(), "acc1", 0, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want 0 events, got %d", len(events))
	}
}
