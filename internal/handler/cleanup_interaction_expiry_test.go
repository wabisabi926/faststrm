package handler

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestInteraction 创建带临时文件 SQLite 的 StrmCleanupInteraction
func newTestInteraction(t *testing.T) *StrmCleanupInteraction {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	i, err := NewStrmCleanupInteraction(db, nil, nil)
	if err != nil {
		t.Fatalf("new interaction: %v", err)
	}
	return i
}

func TestBatchExpiry_RejectedOnConfirm(t *testing.T) {
	i := newTestInteraction(t)
	ctx := context.Background()

	old := time.Now().Add(-25 * time.Hour)
	batch := CleanupBatch{
		RequestID:  "expired-batch",
		CreatedAt:  old.UnixMilli(),
		Paths:      []string{"/tmp/a.strm"},
		PathCount:  1,
		RemoveStrm: true,
	}
	if err := i.AppendBatch(ctx, batch); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	// PopBatch 取出（从队列移除），回调确认时应因过期被拒绝
	popped, err := i.PopBatch(ctx, "expired-batch")
	if err != nil || popped == nil {
		t.Fatalf("PopBatch: %v, %v", popped, err)
	}
	age := time.Since(time.UnixMilli(popped.CreatedAt))
	if age <= BatchMaxAge {
		t.Fatalf("expected age > BatchMaxAge, got %v", age)
	}

	// 重新入队，走 HandleTelegramCallback 验证拒绝消息
	if err := i.AppendBatch(ctx, batch); err != nil {
		t.Fatalf("re-AppendBatch: %v", err)
	}
	msg, err := i.HandleTelegramCallback(ctx, "expired-batch", true)
	if err != nil {
		t.Fatalf("HandleTelegramCallback: %v", err)
	}
	if !strings.Contains(msg, "已过期") {
		t.Fatalf("expected expiry message, got: %s", msg)
	}
	// 过期批次应已从队列移除
	left, err := i.ListBatches(ctx)
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("expired batch should be removed from queue, got %d left", len(left))
	}
}

func TestBatchExpiry_FreshBatchStillWorks(t *testing.T) {
	i := newTestInteraction(t)
	ctx := context.Background()

	batch := CleanupBatch{
		RequestID:  "fresh-batch",
		CreatedAt:  time.Now().UnixMilli(),
		Paths:      []string{"/tmp/b.strm"},
		PathCount:  1,
		RemoveStrm: true,
	}
	if err := i.AppendBatch(ctx, batch); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	// 新批次不会触发过期逻辑——确认删除会真正执行（这里只验证不被过期拒绝）。
	// executePendingBatch 会真的删文件，路径不存在时删除失败但流程走「已执行删除」，
	// 关键断言：消息里不含「已过期」。
	msg, err := i.HandleTelegramCallback(ctx, "fresh-batch", true)
	if err != nil {
		t.Fatalf("HandleTelegramCallback: %v", err)
	}
	if strings.Contains(msg, "已过期") {
		t.Fatalf("fresh batch should not be expired, got: %s", msg)
	}
}

func TestBatchExpiry_ListBatchesCleansUp(t *testing.T) {
	i := newTestInteraction(t)
	ctx := context.Background()

	old := CleanupBatch{
		RequestID:  "old-batch",
		CreatedAt:  time.Now().Add(-48 * time.Hour).UnixMilli(),
		Paths:      []string{"/tmp/old.strm"},
		PathCount:  1,
		RemoveStrm: true,
	}
	fresh := CleanupBatch{
		RequestID:  "fresh-batch",
		CreatedAt:  time.Now().UnixMilli(),
		Paths:      []string{"/tmp/fresh.strm"},
		PathCount:  1,
		RemoveStrm: true,
	}
	if err := i.AppendBatch(ctx, old); err != nil {
		t.Fatalf("AppendBatch old: %v", err)
	}
	if err := i.AppendBatch(ctx, fresh); err != nil {
		t.Fatalf("AppendBatch fresh: %v", err)
	}

	batches, err := i.ListBatches(ctx)
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("expected only 1 (fresh) batch after expiry cleanup, got %d", len(batches))
	}
	if batches[0].RequestID != "fresh-batch" {
		t.Fatalf("expected fresh-batch to survive, got %q", batches[0].RequestID)
	}
}
