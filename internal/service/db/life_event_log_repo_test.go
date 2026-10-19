package db

import (
	"context"
	"testing"
)

// TestLifeEventLogRepo_DeleteByEventType 按类型清理：只删 poll，业务事件保留
func TestLifeEventLogRepo_DeleteByEventType(t *testing.T) {
	sqldb, cleanup := setupTmpDB(t)
	defer cleanup()
	repo, err := NewLifeEventLogRepo(sqldb)
	if err != nil {
		t.Fatalf("NewLifeEventLogRepo: %v", err)
	}
	ctx := context.Background()

	seed := []LifeEventLog{
		{Account: "a", EventType: "poll", Success: false, Message: "获取 cookie 失败: 账号 x 不存在"},
		{Account: "a", EventType: "poll", Success: false, Message: "获取 cookie 失败: 账号 y 不存在"},
		{Account: "a", EventType: "create", Success: true, Message: "生成 STRM"},
		{Account: "a", EventType: "move", Success: true, Message: "移动"},
	}
	for _, l := range seed {
		if _, aerr := repo.AppendLog(ctx, l); aerr != nil {
			t.Fatalf("AppendLog: %v", aerr)
		}
	}

	removed, err := repo.DeleteByEventType(ctx, "poll")
	if err != nil {
		t.Fatalf("DeleteByEventType: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}

	left, err := repo.Query(ctx, LifeEventLogQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("剩余 %d 条, want 2", len(left))
	}
	for _, l := range left {
		if l.EventType == "poll" {
			t.Fatalf("poll 日志应已删除: %+v", l)
		}
	}

	// 空类型参数不做任何删除
	if n, derr := repo.DeleteByEventType(ctx, ""); derr != nil || n != 0 {
		t.Fatalf("空 eventType 应删除 0 条, got n=%d err=%v", n, derr)
	}
}
