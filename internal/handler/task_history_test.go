package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/db"
)

// newTestTaskHistoryRepo 创建基于临时目录 SQLite 的 TaskHistoryRepo
func newTestTaskHistoryRepo(t *testing.T) *db.TaskHistoryRepo {
	t.Helper()
	sqlDB, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatalf("OpenNew err: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo, err := db.NewTaskHistoryRepo(sqlDB)
	if err != nil {
		t.Fatalf("NewTaskHistoryRepo err: %v", err)
	}
	return repo
}

// ================================================================
// HandleTaskHistoryLogs — GET /api/taskHistory/:executionId/logs
// ================================================================

func TestHandleTaskHistoryLogs_NilRepo(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/1/logs", nil)
	HandleTaskHistoryLogs(TaskHistoryDeps{Repo: nil}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("nil repo status: got %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	logs, _ := body["logs"].([]any)
	if logs == nil || len(logs) != 0 {
		t.Errorf("nil repo logs: want empty array, got %v", body["logs"])
	}
}

func TestHandleTaskHistoryLogs_InvalidExecutionID(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/not-a-number/logs", nil)
	HandleTaskHistoryLogs(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status: got %d, want 400", w.Code)
	}
}

func TestHandleTaskHistoryLogs_NotFoundExecution(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/999999/logs", nil)
	HandleTaskHistoryLogs(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("not found status: got %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	logs, _ := body["logs"].([]any)
	if logs == nil || len(logs) != 0 {
		t.Errorf("not found logs: want empty array, got %v", body["logs"])
	}
}

func TestHandleTaskHistoryLogs_ReturnsLogsInOrder(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)
	ctx := context.Background()

	id, err := repo.CreateExecution(ctx, db.TaskExecution{
		TaskID: "task-1", Status: "completed", CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("CreateExecution err: %v", err)
	}

	// 乱序写入，验证按 seq asc 返回
	lines := []struct {
		seq  int
		line string
	}{
		{0, `{"filePath":"/a","done":true}`},
		{2, `{"filePath":"/c","done":true}`},
		{1, `{"filePath":"/b","done":true}`},
	}
	for _, l := range lines {
		if err := repo.AddLog(ctx, id, l.seq, l.line); err != nil {
			t.Fatalf("AddLog err: %v", err)
		}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/1/logs", nil)
	HandleTaskHistoryLogs(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var body struct {
		Logs []string `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Logs) != 3 {
		t.Fatalf("logs len: got %d, want 3: %v", len(body.Logs), body.Logs)
	}
	if body.Logs[0] != `{"filePath":"/a","done":true}` || body.Logs[1] != `{"filePath":"/b","done":true}` {
		t.Errorf("logs order wrong: %v", body.Logs)
	}
}

// ================================================================
// HandleTaskHistoryDelete — DELETE /api/taskHistory
// ================================================================

func TestHandleTaskHistoryDelete_NilRepo(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/taskHistory?action=cleanup", nil)
	HandleTaskHistoryDelete(TaskHistoryDeps{Repo: nil}).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("nil repo clean status: got %d, want 200", w.Code)
	}
}

func TestHandleTaskHistoryDelete_DeleteOne(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)
	id1, err := repo.CreateExecution(context.Background(), db.TaskExecution{TaskID: "t1", Status: "completed", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateExecution t1: %v", err)
	}
	if _, err := repo.CreateExecution(context.Background(), db.TaskExecution{TaskID: "t2", Status: "completed", CreatedAt: 2}); err != nil {
		t.Fatalf("CreateExecution t2: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/taskHistory?executionId="+strconv.FormatInt(id1, 10), nil)
	HandleTaskHistoryDelete(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	items, err := repo.Query(context.Background(), db.TaskHistoryQuery{})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	if len(items) != 1 || items[0].TaskID != "t2" {
		t.Fatalf("expected 1 remaining (t2), got %+v", items)
	}
}

func TestHandleTaskHistoryDelete_Cleanup(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)
	if _, err := repo.CreateExecution(context.Background(), db.TaskExecution{TaskID: "t1", Status: "completed", CreatedAt: 1}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/taskHistory?action=cleanup", nil)
	HandleTaskHistoryDelete(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cleanup status: got %d, want 200", w.Code)
	}

	items, err := repo.Query(context.Background(), db.TaskHistoryQuery{})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty after cleanup, got %d items", len(items))
	}
}

func TestHandleTaskHistoryDelete_InvalidParams(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)
	cases := []string{
		"/api/taskHistory",                 // 无参数
		"/api/taskHistory?executionId=abc", // 非数字
	}
	for _, path := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, path, nil)
		HandleTaskHistoryDelete(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path %s: got %d, want 400", path, w.Code)
		}
	}
}

// ================================================================
// HandleTaskHistoryStats — GET /api/taskHistory/stats
// ================================================================

func TestHandleTaskHistoryStats_NilRepo(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/stats", nil)
	HandleTaskHistoryStats(TaskHistoryDeps{Repo: nil}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("nil repo status: got %d, want 200", w.Code)
	}
	var stats db.TaskHistoryStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.Total != 0 {
		t.Errorf("nil repo total: got %d, want 0", stats.Total)
	}
}

func TestHandleTaskHistoryStats_AggregatesAllRecords(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)
	ctx := context.Background()

	records := []db.TaskExecution{
		{
			TaskID: "t1", Account: "acc-a", Status: "completed", CreatedAt: 1000, DurationMs: 2000,
			Summary: db.TaskExecutionSummary{TotalFiles: 10, DownloadedFiles: 8},
		},
		{
			TaskID: "t2", Account: "acc-a", Status: "failed", CreatedAt: 2000, DurationMs: 4000,
			Summary: db.TaskExecutionSummary{TotalFiles: 5, DownloadedFiles: 2},
		},
		{
			TaskID: "t3", Account: "acc-b", Status: "cancelled", CreatedAt: 3000,
			Summary: db.TaskExecutionSummary{TotalFiles: 3, DownloadedFiles: 1},
		},
	}
	for _, rec := range records {
		if _, err := repo.CreateExecution(ctx, rec); err != nil {
			t.Fatalf("CreateExecution %s: %v", rec.TaskID, err)
		}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/stats", nil)
	HandleTaskHistoryStats(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var stats db.TaskHistoryStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.Total != 3 || stats.Completed != 1 || stats.Failed != 1 || stats.Cancelled != 1 {
		t.Errorf("counts wrong: %+v", stats)
	}
	if stats.FilesDownloaded != 11 || stats.FilesTotal != 18 {
		t.Errorf("file totals wrong: %+v", stats)
	}
	// 只有 2 条记录有耗时：(2000+4000)/2 = 3000
	if stats.AvgDurationMs != 3000 {
		t.Errorf("avg duration: got %d, want 3000", stats.AvgDurationMs)
	}
}

func TestHandleTaskHistoryStats_FilterByAccount(t *testing.T) {
	repo := newTestTaskHistoryRepo(t)
	ctx := context.Background()

	for _, rec := range []db.TaskExecution{
		{TaskID: "t1", Account: "acc-a", Status: "completed", CreatedAt: 1000, DurationMs: 1000},
		{TaskID: "t2", Account: "acc-b", Status: "completed", CreatedAt: 2000, DurationMs: 1000},
	} {
		if _, err := repo.CreateExecution(ctx, rec); err != nil {
			t.Fatalf("CreateExecution %s: %v", rec.TaskID, err)
		}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/taskHistory/stats?account=acc-b", nil)
	HandleTaskHistoryStats(TaskHistoryDeps{Repo: repo}).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var stats db.TaskHistoryStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.Total != 1 || stats.Completed != 1 {
		t.Errorf("filtered stats wrong: %+v", stats)
	}
}

func TestSSEFrameLine(t *testing.T) {
	if got, ok := sseFrameLine("data: {\"event\":\"log\",\"message\":\"x\"}\n\n"); !ok || got != `{"event":"log","message":"x"}` {
		t.Fatalf("sse frame decode = %q, %v", got, ok)
	}
	if got, ok := sseFrameLine(`{"plain":1}`); !ok || got != `{"plain":1}` {
		t.Fatalf("plain json line = %q, %v", got, ok)
	}
	if _, ok := sseFrameLine("  \n"); ok {
		t.Fatal("empty frame should be skipped")
	}
}
