package db

import (
	"context"
	"testing"
)

func TestMarkInterrupted(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	repo, err := NewTaskHistoryRepo(db)
	if err != nil {
		t.Fatalf("NewTaskHistoryRepo: %v", err)
	}
	ctx := context.Background()
	now := NowMs()

	runningID, err := repo.CreateExecution(ctx, TaskExecution{
		TaskID: "t-running", Status: "running", StartedAt: now - 5000, CreatedAt: now - 5000,
	})
	if err != nil {
		t.Fatalf("CreateExecution running: %v", err)
	}
	pendingID, err := repo.CreateExecution(ctx, TaskExecution{
		TaskID: "t-pending", Status: "pending", CreatedAt: now - 3000,
	})
	if err != nil {
		t.Fatalf("CreateExecution pending: %v", err)
	}
	doneID, err := repo.CreateExecution(ctx, TaskExecution{
		TaskID: "t-done", Status: "completed", StartedAt: now - 9000, CreatedAt: now - 9000,
	})
	if err != nil {
		t.Fatalf("CreateExecution completed: %v", err)
	}

	n, err := repo.MarkInterrupted(ctx, "server restarted")
	if err != nil {
		t.Fatalf("MarkInterrupted: %v", err)
	}
	if n != 2 {
		t.Fatalf("MarkInterrupted rows = %d, want 2", n)
	}

	for _, id := range []int64{runningID, pendingID} {
		var status, errStr string
		var endedAt int64
		if err := db.QueryRow(
			`SELECT status, error, ended_at FROM task_executions WHERE id = ?`, id,
		).Scan(&status, &errStr, &endedAt); err != nil {
			t.Fatalf("scan execution %d: %v", id, err)
		}
		if status != "failed" || errStr != "server restarted" || endedAt <= 0 {
			t.Errorf("execution %d = status %q error %q endedAt %d，应为 failed/已中断/%d",
				id, status, errStr, endedAt, now)
		}
	}

	var runningDuration int64
	if err := db.QueryRow(`SELECT duration_ms FROM task_executions WHERE id = ?`, runningID).Scan(&runningDuration); err != nil {
		t.Fatalf("scan duration: %v", err)
	}
	if runningDuration < 4000 {
		t.Errorf("running duration = %d, want >= 4000", runningDuration)
	}

	var doneStatus string
	if err := db.QueryRow(`SELECT status FROM task_executions WHERE id = ?`, doneID).Scan(&doneStatus); err != nil {
		t.Fatalf("scan completed: %v", err)
	}
	if doneStatus != "completed" {
		t.Errorf("completed execution status = %q，不应被改写", doneStatus)
	}
}
