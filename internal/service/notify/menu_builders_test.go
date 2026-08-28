package notify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// === Menu Builders ===

func TestBuildMainMenu(t *testing.T) {
	text, buttons := BuildMainMenu()
	if text == "" {
		t.Fatal("text should not be empty")
	}
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
	// 主菜单应包含 状态、监控、任务、Emby、STRM、帮助 按钮
	// 至少 3 行按钮
	if len(buttons) < 3 {
		t.Fatalf("expected >= 3 button rows, got %d", len(buttons))
	}
	// 第一行应有 callbackMenuStatus
	if buttons[0][0].CallbackData != callbackMenuStatus {
		t.Fatalf("first button callback: got %q, want %q", buttons[0][0].CallbackData, callbackMenuStatus)
	}
	// 最后一行应有返回主菜单（帮助按钮）
	last := buttons[len(buttons)-1]
	if last[0].CallbackData != callbackMenuHelp {
		t.Fatalf("last row callback: got %q, want %q", last[0].CallbackData, callbackMenuHelp)
	}
}

func TestBuildStatusMenu_Empty(t *testing.T) {
	text, buttons := BuildStatusMenu(map[string]any{})
	if text == "" {
		t.Fatal("text should not be empty")
	}
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
}

func TestBuildStatusMenu_WithAccounts(t *testing.T) {
	status := map[string]any{
		"accounts": []map[string]any{
			{"name": "user1", "hasCookie": true},
			{"name": "user2", "hasCookie": false},
		},
		"monitors": []map[string]any{
			{"account": "user1", "running": true},
		},
		"runningTasks": []map[string]any{
			{"name": "task1", "progress": "50%"},
		},
		"emby": map[string]any{"connected": true},
	}
	text, buttons := BuildStatusMenu(status)
	if text == "" {
		t.Fatal("text should not be empty")
	}
	// Should mention user1 and user2
	if !strContains(text, "user1") {
		t.Fatal("text should contain user1")
	}
	if !strContains(text, "user2") {
		t.Fatal("text should contain user2")
	}
	// Should have back button
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
}

func TestBuildMonitorMenu_Empty(t *testing.T) {
	text, buttons := BuildMonitorMenu(map[string]any{})
	if text == "" {
		t.Fatal("text should not be empty")
	}
	if !strContains(text, "暂无") {
		t.Fatal("should mention '暂无' when no monitors")
	}
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
}

func TestBuildMonitorMenu_WithMonitors(t *testing.T) {
	status := map[string]any{
		"monitors": []map[string]any{
			{"account": "acc1", "running": true},
			{"account": "acc2", "running": false},
		},
		"eventTypes": map[string]bool{"create": true, "remove": false, "move": true, "rename": false},
	}
	text, buttons := BuildMonitorMenu(status)
	if !strContains(text, "acc1") {
		t.Fatal("text should contain acc1")
	}
	if !strContains(text, "acc2") {
		t.Fatal("text should contain acc2")
	}
	if len(buttons) < 3 {
		t.Fatalf("expected >= 3 button rows, got %d", len(buttons))
	}
}

func TestBuildTasksMenu_Empty(t *testing.T) {
	text, buttons := BuildTasksMenu(nil, nil)
	if !strContains(text, "暂无") {
		t.Fatal("should mention '暂无' when no tasks")
	}
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
}

func TestBuildTasksMenu_WithTasks(t *testing.T) {
	running := []map[string]any{
		{"name": "running1", "progress": "50%", "id": "r1"},
	}
	scheduled := []map[string]any{
		{"name": "sched1", "schedule": "daily 09:00", "id": "s1"},
	}
	text, buttons := BuildTasksMenu(running, scheduled)
	if !strContains(text, "running1") {
		t.Fatal("text should contain running1")
	}
	if !strContains(text, "sched1") {
		t.Fatal("text should contain sched1")
	}
	if len(buttons) < 3 {
		t.Fatalf("expected >= 3 button rows, got %d", len(buttons))
	}
}

func TestBuildEmbyMenu_Connected(t *testing.T) {
	text, buttons := BuildEmbyMenu(map[string]any{"connected": true, "pendingRefresh": 3})
	if !strContains(text, "已连接") {
		t.Fatal("should mention '已连接'")
	}
	if !strContains(text, "待刷新") {
		t.Fatal("should mention '待刷新' when pendingRefresh > 0")
	}
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
}

func TestBuildEmbyMenu_Disconnected(t *testing.T) {
	text, _ := BuildEmbyMenu(map[string]any{"connected": false})
	if !strContains(text, "未连接") {
		t.Fatal("should mention '未连接'")
	}
}

func TestBuildSTRMMenu(t *testing.T) {
	text, buttons := BuildSTRMMenu()
	if text == "" {
		t.Fatal("text should not be empty")
	}
	if len(buttons) < 2 {
		t.Fatalf("expected >= 2 button rows, got %d", len(buttons))
	}
	if buttons[0][0].CallbackData != callbackStrmSync {
		t.Fatalf("first button: got %q, want %q", buttons[0][0].CallbackData, callbackStrmSync)
	}
}

func TestBuildHelpMenu(t *testing.T) {
	text, buttons := BuildHelpMenu()
	if text == "" {
		t.Fatal("text should not be empty")
	}
	if len(buttons) == 0 {
		t.Fatal("buttons should not be empty")
	}
	if buttons[len(buttons)-1][0].CallbackData != callbackMenuMain {
		t.Fatal("last button should be 'menu_back'")
	}
}

// === NotificationBatcher ===

func TestNewNotificationBatcher(t *testing.T) {
	var called int32
	b := NewNotificationBatcher(
		func(ctx context.Context, n *Notification) error {
			atomic.AddInt32(&called, 1)
			return nil
		},
		100*time.Millisecond,
		5,
	)
	if b == nil {
		t.Fatal("batcher should not be nil")
	}
	defer b.Stop()
}

func TestNotificationBatcher_NonCoalescableImmediate(t *testing.T) {
	var called int32
	b := NewNotificationBatcher(
		func(ctx context.Context, n *Notification) error {
			atomic.AddInt32(&called, 1)
			return nil
		},
		10*time.Second, // long window, won't flush
		100,
	)
	defer b.Stop()

	// TypeSTRMCreate is coalescable, but non-STRM types are not
	ret := b.Enqueue(context.Background(), &Notification{
		Type:    NotificationType("error"),
		Content: "error!",
	})
	if ret {
		t.Fatal("non-coalescable should return false (immediate send)")
	}
}

func TestNotificationBatcher_CoalescableEnqueued(t *testing.T) {
	var called int32
	b := NewNotificationBatcher(
		func(ctx context.Context, n *Notification) error {
			atomic.AddInt32(&called, 1)
			return nil
		},
		10*time.Second, // long window, won't auto-flush
		100,            // high limit, won't exceed
	)
	defer b.Stop()

	ret := b.Enqueue(context.Background(), &Notification{
		Type:     TypeSTRMCreate,
		Content:  "file1.strm",
		Metadata: map[string]string{"account": "testacc"},
	})
	if !ret {
		t.Fatal("coalescable should return true (enqueued)")
	}
}

func TestNotificationBatcher_FlushOnStop(t *testing.T) {
	var called int32
	b := NewNotificationBatcher(
		func(ctx context.Context, n *Notification) error {
			atomic.AddInt32(&called, 1)
			return nil
		},
		10*time.Second,
		100,
	)

	// Enqueue 3 entries
	for i := 0; i < 3; i++ {
		b.Enqueue(context.Background(), &Notification{
			Type:     TypeSTRMCreate,
			Content:  "file.strm",
			Metadata: map[string]string{"account": "testacc"},
		})
	}

	// Stop should flush remaining
	b.Stop()

	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("expected 1 flush call after stop, got %d", atomic.LoadInt32(&called))
	}
}

func TestNotificationBatcher_MaxPerExceed(t *testing.T) {
	var called int32
	b := NewNotificationBatcher(
		func(ctx context.Context, n *Notification) error {
			atomic.AddInt32(&called, 1)
			return nil
		},
		10*time.Second,
		3, // maxPer = 3
	)
	defer b.Stop()

	// Enqueue exactly 3 entries (>= maxPer triggers flush)
	for i := 0; i < 3; i++ {
		b.Enqueue(context.Background(), &Notification{
			Type:     TypeSTRMCreate,
			Content:  "file.strm",
			Metadata: map[string]string{"account": "testacc"},
		})
	}

	// Allow flush to execute
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("expected 1 flush on exceed, got %d", atomic.LoadInt32(&called))
	}
}

func TestNotificationBatcher_NilSafety(t *testing.T) {
	var b *NotificationBatcher
	// nil batcher should not panic
	ret := b.Enqueue(context.Background(), &Notification{Type: TypeSTRMCreate})
	if ret {
		t.Fatal("nil batcher Enqueue should return false")
	}
	b.Stop() // should not panic
}

func TestNotificationBatcher_NilNotification(t *testing.T) {
	b := NewNotificationBatcher(nil, 10*time.Second, 10)
	defer b.Stop()
	ret := b.Enqueue(context.Background(), nil)
	if ret {
		t.Fatal("nil notification should return false")
	}
}

// === commands.go: parseCleanupCallbackData ===

func TestParseCleanupCallbackData(t *testing.T) {
	cases := []struct {
		name        string
		data        string
		wantID      string
		wantApprove bool
		wantOK      bool
	}{
		{"empty", "", "", false, false},
		{"approve", "cleanup_confirm|abc123|y", "abc123", true, true},
		{"reject", "cleanup_confirm|xyz789|n", "xyz789", false, true},
		{"invalid action", "cleanup_confirm|abc|x", "", false, false},
		{"wrong prefix", "something|abc|y", "", false, false},
		{"too few parts", "cleanup_confirm|abc", "", false, false},
		{"too many parts", "cleanup_confirm|abc|y|extra", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, approve, ok := parseCleanupCallbackData(c.data)
			if id != c.wantID || approve != c.wantApprove || ok != c.wantOK {
				t.Fatalf("parseCleanupCallbackData(%q) = (%q, %v, %v), want (%q, %v, %v)",
					c.data, id, approve, ok, c.wantID, c.wantApprove, c.wantOK)
			}
		})
	}
}

// === helper ===

func strContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
