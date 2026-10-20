package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
)

func TestHandleListTasks_UsesLatestHistoryWhenRuntimeMissing(t *testing.T) {
	tasksStore := store.NewTasksStore(t.TempDir())
	if err := tasksStore.SaveTasks([]task.Task{
		{ID: "done-task", Account: "a", OriginPath: "/o", TargetPath: "/t"},
		{ID: "stale-running-task", Account: "a", OriginPath: "/o", TargetPath: "/t2"},
	}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}

	repo := newTestTaskHistoryRepo(t)
	ctx := context.Background()
	if _, err := repo.CreateExecution(ctx, db.TaskExecution{
		TaskID: "done-task", Status: "completed",
		StartedAt: 1000, EndedAt: 2000,
		Summary: db.TaskExecutionSummary{TotalFiles: 3, DownloadedFiles: 2, DeletedFiles: 1},
	}); err != nil {
		t.Fatalf("CreateExecution completed: %v", err)
	}
	if _, err := repo.CreateExecution(ctx, db.TaskExecution{
		TaskID: "stale-running-task", Status: "running", StartedAt: 3000,
	}); err != nil {
		t.Fatalf("CreateExecution running: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	HandleListTasks(TaskHandlerDeps{
		TasksStore:      tasksStore,
		TaskHistoryRepo: repo,
	}).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Tasks []struct {
			ID      string `json:"id"`
			Runtime *struct {
				Status          string `json:"status"`
				TotalFiles      int    `json:"totalFiles"`
				DownloadedFiles int    `json:"downloadedFiles"`
				DeletedFiles    int    `json:"deletedFiles"`
			} `json:"runtime"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	type runtimeView struct {
		Status          string
		TotalFiles      int
		DownloadedFiles int
		DeletedFiles    int
	}
	byID := make(map[string]runtimeView)
	for _, item := range body.Tasks {
		if item.Runtime == nil {
			t.Fatalf("task %s missing runtime backfill", item.ID)
		}
		byID[item.ID] = runtimeView{
			item.Runtime.Status, item.Runtime.TotalFiles,
			item.Runtime.DownloadedFiles, item.Runtime.DeletedFiles,
		}
	}

	done := byID["done-task"]
	if done.Status != "completed" || done.TotalFiles != 3 || done.DownloadedFiles != 2 || done.DeletedFiles != 1 {
		t.Fatalf("completed task backfill = %+v", done)
	}
	if stale := byID["stale-running-task"]; stale.Status != "pending" {
		t.Fatalf("stale running history should fall back to pending, got %q", stale.Status)
	}
}
