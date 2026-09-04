package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
