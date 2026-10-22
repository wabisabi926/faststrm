package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
)

func TestHandleStartTask_BlockedReturnsConflict(t *testing.T) {
	dir := t.TempDir()
	const account = "blocked-account-test"
	tasksJSON := `[{"id":"blocked-task","name":"blocked","account":"` + account + `","originPath":"/origin","targetPath":"/target"}]`
	if err := os.WriteFile(filepath.Join(dir, "tasks.json"), []byte(tasksJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	tasksStore := store.NewTasksStore(dir)

	rt := task.GetRuntime()
	if ok, conflict := rt.TryEnter(account, "already-running"); !ok {
		t.Fatalf("test setup failed: account already locked by %s", conflict)
	}
	defer rt.Exit(account)

	deps := TaskHandlerDeps{
		ExecutorDeps: task.ExecutorDeps{TasksStore: tasksStore},
		TasksStore:   tasksStore,
		Runtime:      rt,
		Scheduler:    task.GetScheduler(),
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/startTask", strings.NewReader(`{"taskId":"blocked-task"}`))
	HandleStartTask(deps)(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var result task.ExecuteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Success || !result.Blocked || result.Reason != "account_running" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
