package monitor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
)

// ======================================================================
// resolveEventPickCode 单元测试（P4：pickcode 反查回退链）
// 回退链：事件自带 → DB(file_id) → files/info API → 原样返回
// ======================================================================

// validPC 是合法的 17 位字母数字 pickcode（与 isValidPickcode 一致）
const validPC = "abc123def456ghi78"

// roundTripFunc 将函数适配为 http.RoundTripper
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestResolveEventPickCode_ValidEventPickCode 事件自带合法 pickcode → 直接使用，不触发任何反查
func TestResolveEventPickCode_ValidEventPickCode(t *testing.T) {
	m := &Monitor{}
	ev := client115.LifeEventItem{FileID: "100", PickCode: validPC}
	if got := m.resolveEventPickCode(context.Background(), "acc1", nil, ev); got != validPC {
		t.Fatalf("want %s got %s", validPC, got)
	}
}

// TestResolveEventPickCode_EmptyFileID 事件缺 file_id（"" 或 "0"）→ 跳过反查，原样返回
func TestResolveEventPickCode_EmptyFileID(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFilePathEntry(sqldb, "acc1", db.FilePathEntry{
		FileID: "100", Path: "电影/A", FileName: "A.mkv", ParentID: "0", PickCode: validPC, UpdateTime: 1,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	for _, fid := range []string{"", "0"} {
		ev := client115.LifeEventItem{FileID: fid, PickCode: "bad"}
		if got := m.resolveEventPickCode(context.Background(), "acc1", nil, ev); got != "bad" {
			t.Fatalf("file_id=%q 应原样返回 'bad', got %s", fid, got)
		}
	}
}

// TestResolveEventPickCode_DBFallback 事件 pickcode 无效 → 从 DB 按 file_id 反查命中
func TestResolveEventPickCode_DBFallback(t *testing.T) {
	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFilePathEntry(sqldb, "acc1", db.FilePathEntry{
		FileID: "100", Path: "电影/A", FileName: "A.mkv", ParentID: "0", PickCode: validPC, UpdateTime: 1,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	ev := client115.LifeEventItem{FileID: "100", PickCode: ""}
	if got := m.resolveEventPickCode(context.Background(), "acc1", nil, ev); got != validPC {
		t.Fatalf("DB 反查 want %s got %s", validPC, got)
	}
}

// TestResolveEventPickCode_APIFallback DB 无命中 → 走 files/info API 反查
func TestResolveEventPickCode_APIFallback(t *testing.T) {
	orig := http.DefaultTransport
	defer func() { http.DefaultTransport = orig }()

	var apiCalls int
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "webapi.115.com" && req.URL.Path == "/files/info" && req.URL.Query().Get("file_id") == "100" {
			apiCalls++
			return jsonResponse(200, `{"state":true,"data":[{"pc":"`+validPC+`"}]}`), nil
		}
		return jsonResponse(404, "not found"), nil
	})

	m := &Monitor{sqliteDB: newTestSqliteDB(t)}
	lifeClient := client115.NewLifeClient("UID=x;CID=y")
	ev := client115.LifeEventItem{FileID: "100", PickCode: ""}
	if got := m.resolveEventPickCode(context.Background(), "acc1", lifeClient, ev); got != validPC {
		t.Fatalf("API 反查 want %s got %s", validPC, got)
	}
	if apiCalls != 1 {
		t.Fatalf("files/info 应恰好调用 1 次, got %d", apiCalls)
	}
}

// TestResolveEventPickCode_DBInvalid_APIFallback DB 命中但 pickcode 非法 → 继续走 API 反查
func TestResolveEventPickCode_DBInvalid_APIFallback(t *testing.T) {
	orig := http.DefaultTransport
	defer func() { http.DefaultTransport = orig }()

	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "webapi.115.com" && req.URL.Path == "/files/info" {
			return jsonResponse(200, `{"state":true,"data":[{"pc":"`+validPC+`"}]}`), nil
		}
		return jsonResponse(404, "not found"), nil
	})

	sqldb := newTestSqliteDB(t)
	if err := db.UpsertFilePathEntry(sqldb, "acc1", db.FilePathEntry{
		FileID: "100", Path: "电影/A", FileName: "A.mkv", ParentID: "0", PickCode: "bad", UpdateTime: 1,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m := &Monitor{sqliteDB: sqldb}
	lifeClient := client115.NewLifeClient("UID=x;CID=y")
	ev := client115.LifeEventItem{FileID: "100", PickCode: "bad"}
	if got := m.resolveEventPickCode(context.Background(), "acc1", lifeClient, ev); got != validPC {
		t.Fatalf("DB pickcode 非法时应继续走 API, want %s got %s", validPC, got)
	}
}

// TestResolveEventPickCode_AllFallbackFail 无 DB 且无 client → 原样返回事件自带 pickcode
func TestResolveEventPickCode_AllFallbackFail(t *testing.T) {
	m := &Monitor{}
	ev := client115.LifeEventItem{FileID: "100", PickCode: "bad"}
	if got := m.resolveEventPickCode(context.Background(), "acc1", nil, ev); got != "bad" {
		t.Fatalf("全部回退失败应原样返回, got %s", got)
	}
}
