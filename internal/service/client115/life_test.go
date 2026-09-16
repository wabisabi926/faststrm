// Package client115 unit tests for life.go (PullEvents / pickcode fallback)
package client115

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
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
func TestResolvePath_FolderEvent_UsesFileIDAsCid(t *testing.T) {
	var medialistCalled bool
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "888"},
		BodyString: `{"state":true,"ancestors":[{"id":1,"name":"根目录","parent_id":0},{"id":777,"name":"电影","parent_id":1}]}`,
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

// 文件事件(parent_id 合法)：与参考项目一致走 parentID 反查父目录，不触发 file_id 反查。
func TestResolvePath_FileEvent_WithParentID(t *testing.T) {
	var medialistCalled bool
	lc := newLifeClientWithTrips(t, []*mockTrip{{
		Path:       "/files/medialist",
		Query:      map[string]string{"cid": "555"},
		BodyString: `{"state":true,"ancestors":[{"id":1,"name":"根目录","parent_id":0},{"id":777,"name":"电影","parent_id":1}]}`,
		called:     &medialistCalled,
	}})
	// fsClient 用于 ResolveDirPath 在父目录(777)中列目录取文件夹 555 的自身名
	lc.fsClient = newMockClient(t, []*mockTrip{{
		Path:       "/files",
		Query:      map[string]string{"cid": "777"},
		BodyString: `{"state":true,"data":[{"fid":0,"cid":555,"n":"动作片","fc":0}]}`,
	}})
	got := lc.ResolvePath(context.Background(), "555", "100001", "movie.mkv", 1)
	if !medialistCalled {
		t.Fatal("medialist should be called for parentID resolution")
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
