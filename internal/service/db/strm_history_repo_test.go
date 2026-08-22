package db

import (
	"database/sql"
	"testing"
)

// TestStrmHistoryInsertAndGet 测试插入和按ID查询
func TestStrmHistoryInsertAndGet(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	entry := StrmHistoryEntry{
		TaskID:       "task-001",
		Kind:         StrmHistoryKindFull,
		Account:      "acc1",
		Success:      true,
		TotalFiles:   100,
		SuccessFiles: 95,
		FailedFiles:  5,
		ElapsedMs:    5000,
		APIRequests:  200,
		ErrorMsg:     "",
	}
	id, err := InsertStrmHistory(db, entry)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id should be > 0, got %d", id)
	}

	got, err := GetStrmHistoryByID(db, id)
	if err != nil || got == nil {
		t.Fatalf("GetByID: err=%v got=%v", err, got)
	}
	if got.TaskID != "task-001" {
		t.Fatalf("taskId mismatch: %s", got.TaskID)
	}
	if got.Kind != StrmHistoryKindFull {
		t.Fatalf("kind mismatch: %s", got.Kind)
	}
	if !got.Success {
		t.Fatalf("success should be true")
	}
	if got.TotalFiles != 100 {
		t.Fatalf("totalFiles mismatch: %d", got.TotalFiles)
	}
	if got.SuccessFiles != 95 {
		t.Fatalf("successFiles mismatch: %d", got.SuccessFiles)
	}
}

// TestStrmHistoryInsertFailure 测试失败记录
func TestStrmHistoryInsertFailure(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	entry := StrmHistoryEntry{
		TaskID:       "task-002",
		Kind:         StrmHistoryKindMonitor,
		Account:      "acc2",
		Success:      false,
		TotalFiles:   1,
		SuccessFiles: 0,
		FailedFiles:  1,
		ElapsedMs:    500,
		ErrorMsg:     "pickcode 无效",
	}
	id, err := InsertStrmHistory(db, entry)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id should be > 0")
	}

	got, err := GetStrmHistoryByID(db, id)
	if err != nil || got == nil {
		t.Fatalf("GetByID: err=%v", err)
	}
	if got.Success {
		t.Fatalf("success should be false")
	}
	if got.ErrorMsg != "pickcode 无效" {
		t.Fatalf("errorMsg mismatch: %s", got.ErrorMsg)
	}
}

// TestStrmHistoryListByKind 测试按kind过滤
func TestStrmHistoryListByKind(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	// 插入3条不同kind
	kinds := []StrmHistoryKind{StrmHistoryKindFull, StrmHistoryKindMonitor, StrmHistoryKindDelete}
	for i, k := range kinds {
		_, err := InsertStrmHistory(db, StrmHistoryEntry{
			TaskID: "task-list",
			Kind:   k,
			Account: "acc1",
			Success: true,
			TotalFiles: i + 1,
			SuccessFiles: i + 1,
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", k, err)
		}
	}

	// 查全部
	all, err := ListStrmHistory(db, "", "", 0)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 records, got %d", len(all))
	}

	// 按kind=monitor过滤
	monitorOnly, err := ListStrmHistory(db, string(StrmHistoryKindMonitor), "", 0)
	if err != nil {
		t.Fatalf("List monitor: %v", err)
	}
	if len(monitorOnly) != 1 {
		t.Fatalf("expected 1 monitor record, got %d", len(monitorOnly))
	}
	if monitorOnly[0].Kind != StrmHistoryKindMonitor {
		t.Fatalf("kind mismatch")
	}
}

// TestStrmHistoryListByTaskID 测试按taskID过滤
func TestStrmHistoryListByTaskID(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	// 插入2条taskA, 1条taskB
	entries := []StrmHistoryEntry{
		{TaskID: "taskA", Kind: StrmHistoryKindFull, Account: "acc1", Success: true, TotalFiles: 10, SuccessFiles: 10},
		{TaskID: "taskA", Kind: StrmHistoryKindIncrement, Account: "acc1", Success: true, TotalFiles: 5, SuccessFiles: 5},
		{TaskID: "taskB", Kind: StrmHistoryKindFull, Account: "acc2", Success: false, TotalFiles: 3, FailedFiles: 3},
	}
	for _, e := range entries {
		_, err := InsertStrmHistory(db, e)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// 按taskA过滤
	taskARecords, err := ListStrmHistory(db, "", "taskA", 0)
	if err != nil {
		t.Fatalf("List taskA: %v", err)
	}
	if len(taskARecords) != 2 {
		t.Fatalf("expected 2 taskA records, got %d", len(taskARecords))
	}
	for _, r := range taskARecords {
		if r.TaskID != "taskA" {
			t.Fatalf("taskId mismatch: %s", r.TaskID)
		}
	}
}

// TestStrmHistoryListLimit 测试limit上限
func TestStrmHistoryListLimit(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	// 插入10条
	for i := 0; i < 10; i++ {
		_, err := InsertStrmHistory(db, StrmHistoryEntry{
			TaskID: "task-limit",
			Kind:   StrmHistoryKindFull,
			Account: "acc1",
			Success: true,
			TotalFiles: i,
		})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	// limit=5
	limited, err := ListStrmHistory(db, "", "task-limit", 5)
	if err != nil {
		t.Fatalf("List limit=5: %v", err)
	}
	if len(limited) != 5 {
		t.Fatalf("expected 5 records, got %d", len(limited))
	}
}

// TestStrmHistoryStats 测试统计聚合
func TestStrmHistoryStats(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	// 插入2成功1失败
	entries := []StrmHistoryEntry{
		{TaskID: "s1", Kind: StrmHistoryKindFull, Account: "acc1", Success: true, TotalFiles: 100, SuccessFiles: 100},
		{TaskID: "s2", Kind: StrmHistoryKindFull, Account: "acc1", Success: true, TotalFiles: 50, SuccessFiles: 50},
		{TaskID: "s3", Kind: StrmHistoryKindMonitor, Account: "acc2", Success: false, TotalFiles: 1, FailedFiles: 1},
	}
	for _, e := range entries {
		_, err := InsertStrmHistory(db, e)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// 全部统计
	stats, err := GetStrmHistoryStats(db, "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalCount != 3 {
		t.Fatalf("totalCount: expected 3, got %d", stats.TotalCount)
	}
	if stats.SuccessCount != 2 {
		t.Fatalf("successCount: expected 2, got %d", stats.SuccessCount)
	}
	if stats.FailedCount != 1 {
		t.Fatalf("failedCount: expected 1, got %d", stats.FailedCount)
	}
	if stats.TotalFiles != 151 {
		t.Fatalf("totalFiles: expected 151, got %d", stats.TotalFiles)
	}
	if stats.SuccessFiles != 150 {
		t.Fatalf("successFiles: expected 150, got %d", stats.SuccessFiles)
	}

	// 按 kind=full 统计
	fullStats, err := GetStrmHistoryStats(db, string(StrmHistoryKindFull))
	if err != nil {
		t.Fatalf("Stats full: %v", err)
	}
	if fullStats.TotalCount != 2 {
		t.Fatalf("full totalCount: expected 2, got %d", fullStats.TotalCount)
	}
	if fullStats.SuccessCount != 2 {
		t.Fatalf("full successCount: expected 2, got %d", fullStats.SuccessCount)
	}
}

// TestStrmHistoryGetByIDNotFound 测试查询不存在的ID
func TestStrmHistoryGetByIDNotFound(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	got, err := GetStrmHistoryByID(db, 99999)
	if err != nil {
		t.Fatalf("GetByID nonexist should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for non-existent ID")
	}
}

// TestStrmHistoryNilDB 测试nil DB防御
func TestStrmHistoryNilDB(t *testing.T) {
	var nilDB *sql.DB

	_, err := InsertStrmHistory(nilDB, StrmHistoryEntry{})
	if err == nil {
		t.Fatalf("Insert with nil db should error")
	}

	_, err = ListStrmHistory(nilDB, "", "", 0)
	if err == nil {
		t.Fatalf("List with nil db should error")
	}

	_, err = GetStrmHistoryByID(nilDB, 1)
	if err == nil {
		t.Fatalf("GetByID with nil db should error")
	}

	_, err = GetStrmHistoryStats(nilDB, "")
	if err == nil {
		t.Fatalf("Stats with nil db should error")
	}
}
