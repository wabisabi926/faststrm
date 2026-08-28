package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// 阶段 3 集成测试：SQLite TaskHistoryRepo + LifeEventRepo 端到端链路
//   - 表首次初始化正确（无 DDL 错）
//   - TaskHistory：创建执行 → 追加日志 → patch 完成 → 分页查询（按 taskId / account / status）→ 读日志 limit
//   - LifeEvent：写入一批 → ListPending(account/all) → MarkProcessed 验证幂等
func TestPhase3_RepoIntegration(t *testing.T) {
	root := t.TempDir()

	db, err := OpenNew(root)
	if err != nil {
		t.Fatalf("OpenNew err: %v", err)
	}
	defer db.Close()

	t.Run("SQLite PRAGMA sanity", func(t *testing.T) {
		// 验证 WAL + busy_timeout 生效（防止测试偶发锁超时）
		checkPragma(t, db, "journal_mode", "wal")
	})

	t.Run("TaskHistoryRepo CRUD + pagination", func(t *testing.T) {
		repo, err := NewTaskHistoryRepo(db)
		if err != nil {
			t.Fatalf("NewTaskHistoryRepo err: %v", err)
		}
		ctx := context.Background()

		// —— 初始为空
		qEmpty, err := repo.Query(ctx, TaskHistoryQuery{Limit: 10})
		if err != nil {
			t.Fatalf("Query empty err: %v", err)
		}
		if len(qEmpty) != 0 {
			t.Errorf("empty want 0 rows, got %d", len(qEmpty))
		}

		// —— 创建 3 条 execution（不同状态/账号）
		id1, err := repo.CreateExecution(ctx, TaskExecution{
			TaskID: "task_A", Account: "alice@115.com", OriginPath: "/电影", TargetPath: "/nas/alice",
			Status: "running", StartedAt: 1000, CreatedAt: 1000,
		})
		if err != nil {
			t.Fatalf("Create exec1 err: %v", err)
		}

		id2, err := repo.CreateExecution(ctx, TaskExecution{
			TaskID: "task_B", Account: "bob@115.com", OriginPath: "/TV", TargetPath: "/nas/bob",
			Status: "pending", CreatedAt: 900,
		})
		_ = id2

		id3, err := repo.CreateExecution(ctx, TaskExecution{
			TaskID: "task_A", Account: "alice@115.com", OriginPath: "/电影2", TargetPath: "/nas/alice2",
			Status: "failed", Error: "timeout reach", CreatedAt: 800,
		})
		_ = id3

		// —— 追加日志到 id1
		for i := 0; i < 5; i++ {
			line := fmt.Sprintf("%04d-startup line %d", i, i*100)
			if err := repo.AddLog(ctx, id1, i, line); err != nil {
				t.Fatalf("AddLog[%d] err: %v", i, err)
			}
		}

		// —— CompleteExecution 完成 id1（成功，duration=1234ms, summary）
		if err := repo.CompleteExecution(ctx, id1, "completed",
			TaskExecutionSummary{TotalFiles: 10, DownloadedFiles: 10, DeletedFiles: 3},
			"", 1234); err != nil {
			t.Fatalf("CompleteExecution err: %v", err)
		}

		// —— patch id1 局部更新
		if err := repo.UpdateExecution(ctx, id1, map[string]any{"error": ""}); err != nil {
			t.Fatalf("UpdateExecution err: %v", err)
		}

		// —— Query：按 taskId=task_A 应为 2 条（completed + failed），created desc
		byTask, err := repo.Query(ctx, TaskHistoryQuery{TaskID: "task_A", Limit: 20})
		if err != nil {
			t.Fatalf("Query by task err: %v", err)
		}
		if len(byTask) != 2 {
			t.Fatalf("Query task_A want 2, got %d: %+v", len(byTask), byTask)
		}
		if byTask[0].Status != "completed" || byTask[1].Status != "failed" {
			t.Errorf("order expect [completed, failed] (DESC by createdAt), got [%s, %s]",
				byTask[0].Status, byTask[1].Status)
		}
		if byTask[0].DurationMs != 1234 {
			t.Errorf("duration want 1234, got %d", byTask[0].DurationMs)
		}
		if byTask[0].Summary.TotalFiles != 10 || byTask[0].Summary.DeletedFiles != 3 {
			t.Errorf("summary round-trip failed: %+v", byTask[0].Summary)
		}

		// —— Query：按 status=failed 只 1 条
		failed, err := repo.Query(ctx, TaskHistoryQuery{Status: "failed"})
		if err != nil {
			t.Fatalf("Query status failed err: %v", err)
		}
		if len(failed) != 1 || failed[0].Error != "timeout reach" {
			t.Errorf("expect 1 failed with err, got %d: %+v", len(failed), failed)
		}

		// —— Query：按 account=bob 只 1 条
		bobList, err := repo.Query(ctx, TaskHistoryQuery{Account: "bob@115.com"})
		if err != nil {
			t.Fatalf("Query bob err: %v", err)
		}
		if len(bobList) != 1 || bobList[0].TaskID != "task_B" {
			t.Errorf("bob expect task_B, got %+v", bobList)
		}

		// —— Query：limit=1（仅最近 1 条，createdAt 最大 id1=1000，alice）
		lim, err := repo.Query(ctx, TaskHistoryQuery{Limit: 1})
		if err != nil {
			t.Fatalf("Query limit=1 err: %v", err)
		}
		if len(lim) != 1 || lim[0].CreatedAt != 1000 {
			t.Errorf("limit 1 want createdAt=1000, got %+v", lim)
		}

		// —— GetLogs：id1 limit=3 → seq0~2，升序
		logs, err := repo.GetLogs(ctx, id1, 3)
		if err != nil {
			t.Fatalf("GetLogs err: %v", err)
		}
		if len(logs) != 3 {
			t.Errorf("GetLogs limit=3 want 3, got %d", len(logs))
		}
		for i, l := range logs {
			want := fmt.Sprintf("%04d-startup line %d", i, i*100)
			if l != want {
				t.Errorf("log[%d] mismatch: want %q, got %q", i, want, l)
			}
		}

		// —— GetLogs no limit：默认 20000，取 5
		logsAll, err := repo.GetLogs(ctx, id1, 0)
		if err != nil {
			t.Fatalf("GetLogs(0) err: %v", err)
		}
		if len(logsAll) != 5 {
			t.Errorf("GetLogs(0) want 5, got %d", len(logsAll))
		}
	})

	t.Run("LifeEventRepo insert + ListPending + MarkProcessed", func(t *testing.T) {
		repo, err := NewLifeEventRepo(db)
		if err != nil {
			t.Fatalf("NewLifeEventRepo err: %v", err)
		}
		ctx := context.Background()

		// 插入 5 条：alice×3 + bob×2
		events := []LifeEvent{
			{Account: "alice@115.com", EventType: "folderChanged", FolderPath: "/电影/科幻", PayloadJSON: `{"n":1}`, Processed: 0, CreatedAt: 10},
			{Account: "bob@115.com", EventType: "fileRenamed", FolderPath: "/TV/Drama", PayloadJSON: `{"n":2}`, Processed: 0, CreatedAt: 20},
			{Account: "alice@115.com", EventType: "fileRemoved", FolderPath: "/电影/科幻/X", PayloadJSON: `{"n":3}`, Processed: 0, CreatedAt: 30},
			{Account: "bob@115.com", EventType: "folderChanged", FolderPath: "/TV/Anime", PayloadJSON: `{"n":4}`, Processed: 0, CreatedAt: 40},
			{Account: "alice@115.com", EventType: "fileRenamed", FolderPath: "/电影/科幻/Y", PayloadJSON: `{"n":5}`, Processed: 0, CreatedAt: 50},
		}
		ids := make([]int64, 0, len(events))
		for _, e := range events {
			id, err := repo.Insert(ctx, e)
			if err != nil {
				t.Fatalf("Insert err: %v", err)
			}
			ids = append(ids, id)
		}
		if len(ids) != 5 {
			t.Fatalf("ids len: %d", len(ids))
		}

		// ListPending(alice) → 3 条，按 createdAt ASC → 10/30/50
		alice, err := repo.ListPending(ctx, "alice@115.com", 20)
		if err != nil {
			t.Fatalf("ListPending alice err: %v", err)
		}
		if len(alice) != 3 {
			t.Fatalf("alice pending want 3, got %d", len(alice))
		}
		if alice[0].CreatedAt != 10 || alice[1].CreatedAt != 30 || alice[2].CreatedAt != 50 {
			t.Errorf("alice order expect createdAt [10,30,50], got [%d,%d,%d]",
				alice[0].CreatedAt, alice[1].CreatedAt, alice[2].CreatedAt)
		}
		if alice[0].EventType != "folderChanged" || alice[2].EventType != "fileRenamed" {
			t.Errorf("alice event types mismatch: %+v", alice)
		}

		// 标记前 2 条 alice 已处理（批量）
		if err := repo.MarkProcessed(ctx, []int64{alice[0].ID, alice[1].ID}); err != nil {
			t.Fatalf("MarkProcessed [%d,%d] err: %v", alice[0].ID, alice[1].ID, err)
		}
		// 幂等：再次标记同一批（不应报错）
		if err := repo.MarkProcessed(ctx, []int64{alice[0].ID}); err != nil {
			t.Fatalf("MarkProcessed idempotent err: %v", err)
		}

		// 再次 ListPending(alice) → 只剩 1 条（created=50）
		alice2, err := repo.ListPending(ctx, "alice@115.com", 20)
		if err != nil {
			t.Fatalf("ListPending alice after mark err: %v", err)
		}
		if len(alice2) != 1 || alice2[0].CreatedAt != 50 {
			t.Errorf("alice after mark expect [50], got %+v", alice2)
		}

		// ListPending("") = 全账号 pending → bob×2 + alice×1 = 3
		all, err := repo.ListPending(ctx, "", 20)
		if err != nil {
			t.Fatalf("ListPending all err: %v", err)
		}
		if len(all) != 3 {
			t.Errorf("all pending want 3, got %d", len(all))
		}
		// ASC：20(bob), 40(bob), 50(alice)
		for i, want := range []int64{20, 40, 50} {
			if all[i].CreatedAt != want {
				t.Errorf("all[%d].CreatedAt want %d, got %d", i, want, all[i].CreatedAt)
			}
		}

		// ListPending limit=1 → 仅最早 (bob createdAt=20)
		small, err := repo.ListPending(ctx, "", 1)
		if err != nil {
			t.Fatalf("ListPending limit 1 err: %v", err)
		}
		if len(small) != 1 || small[0].CreatedAt != 20 || small[0].Account != "bob@115.com" {
			t.Errorf("limit 1 expect bob@20, got %+v", small)
		}
	})
}

// checkPragma 验证 PRAGMA 值（忽略大小写）
func checkPragma(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()
	row := db.QueryRowContext(context.Background(), "PRAGMA "+name)
	var got string
	if err := row.Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s scan err: %v", name, err)
	}
	equalFold := func(a, b string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := 0; i < len(a); i++ {
			ca, cb := a[i], b[i]
			if 'A' <= ca && ca <= 'Z' {
				ca += 32
			}
			if 'A' <= cb && cb <= 'Z' {
				cb += 32
			}
			if ca != cb {
				return false
			}
		}
		return true
	}
	if !equalFold(got, want) {
		t.Errorf("PRAGMA %s want %q, got %q", name, want, got)
	}
}
