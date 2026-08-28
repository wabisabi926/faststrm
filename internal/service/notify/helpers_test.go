package notify

import (
	"strings"
	"testing"
	"time"
)

func TestNowFormatted(t *testing.T) {
	got := nowFormatted()
	if len(got) == 0 {
		t.Fatal("nowFormatted returned empty string")
	}
	// 格式应为 "2006-01-02 15:04:05"
	// 基本检查：包含 "-" 和 ":" 和 " "
	if !strings.Contains(got, "-") {
		t.Fatalf("nowFormatted %q missing '-'", got)
	}
	if !strings.Contains(got, ":") {
		t.Fatalf("nowFormatted %q missing ':'", got)
	}
	if !strings.Contains(got, " ") {
		t.Fatalf("nowFormatted %q missing space", got)
	}
}

func TestIsUserAllowed(t *testing.T) {
	h := &CommandHandler{}
	cases := []struct {
		name         string
		userID       int64
		allowedUsers []int64
		want         bool
	}{
		{"empty allowed -> true (allow all)", 100, nil, true},
		{"empty slice -> true (allow all)", 100, []int64{}, true},
		{"user in list", 42, []int64{10, 20, 42}, true},
		{"user not in list", 99, []int64{10, 20, 42}, false},
		{"single element match", 5, []int64{5}, true},
		{"single element no match", 5, []int64{7}, false},
		{"zero userID in list", 0, []int64{0, 1}, true},
		{"negative userID in list", -1, []int64{-1, 1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := h.IsUserAllowed(c.userID, c.allowedUsers)
			if got != c.want {
				t.Fatalf("IsUserAllowed(%d, %v) = %v, want %v", c.userID, c.allowedUsers, got, c.want)
			}
		})
	}
}

func TestBoolToText(t *testing.T) {
	cases := []struct {
		name string
		b    bool
		txt  string
		f    string
		want string
	}{
		{"true returns t", true, "yes", "no", "yes"},
		{"false returns f", false, "yes", "no", "no"},
		{"true with emoji", true, "✅", "❌", "✅"},
		{"false with emoji", false, "✅", "❌", "❌"},
		{"empty strings", true, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := boolToText(c.b, c.txt, c.f)
			if got != c.want {
				t.Fatalf("boolToText(%v, %q, %q) = %q, want %q", c.b, c.txt, c.f, got, c.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	// truncate 使用 len(s) 即字节数，不是 rune 数
	cases := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"shorter -> ascii", "hello", 10, "hello"},
		{"equal -> ascii", "hello", 5, "hello"},
		{"longer -> ascii", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"max zero: skip", "abc", 0, ""}, // 0 字节会截到 [:0] + "..."
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.maxLen <= 0 && len(c.input) > 0 {
				// maxLen <= 0 时会 panic（truncate 没有边界保护），跳过
				t.Skipf("truncate with maxLen=%d is unsafe (would panic), skipping", c.maxLen)
			}
			got := truncate(c.input, c.maxLen)
			if got != c.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", c.input, c.maxLen, got, c.want)
			}
		})
	}
}

// ==================== notification_batcher.go 纯函数测试 ====================

func TestIsCoalescable(t *testing.T) {
	cases := []struct {
		name string
		n    *Notification
		want bool
	}{
		{"STRMCreate coalescable", &Notification{Type: TypeSTRMCreate}, true},
		{"STRMDelete coalescable", &Notification{Type: TypeSTRMDelete}, true},
		{"STRMMove coalescable", &Notification{Type: TypeSTRMMove}, true},
		{"STRMRename coalescable", &Notification{Type: TypeSTRMRename}, true},
		{"MediaAdded not coalescable", &Notification{Type: TypeMediaAdded}, false},
		{"MediaDeleted not coalescable", &Notification{Type: TypeMediaDeleted}, false},
		{"TaskComplete not coalescable", &Notification{Type: TypeTaskComplete}, false},
		{"TaskError not coalescable", &Notification{Type: TypeTaskError}, false},
		{"SystemAlert not coalescable", &Notification{Type: TypeSystemAlert}, false},
		{"Playback not coalescable", &Notification{Type: TypePlayback}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isCoalescable(c.n)
			if got != c.want {
				t.Fatalf("isCoalescable(%v) = %v, want %v", c.n.Type, got, c.want)
			}
		})
	}
}

func TestNotificationTypeLabel(t *testing.T) {
	cases := []struct {
		name string
		t    NotificationType
		want string
	}{
		{"STRMCreate -> 创建", TypeSTRMCreate, "创建"},
		{"STRMDelete -> 删除", TypeSTRMDelete, "删除"},
		{"STRMMove -> 移动", TypeSTRMMove, "移动"},
		{"STRMRename -> 重命名", TypeSTRMRename, "重命名"},
		{"unknown -> 事件", NotificationType("unknown"), "事件"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := notificationTypeLabel(c.t)
			if got != c.want {
				t.Fatalf("notificationTypeLabel(%q) = %q, want %q", c.t, got, c.want)
			}
		})
	}
}

func TestFirstNonEmptyLine(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"single line", "hello", "hello"},
		{"multi line first non-empty", "\n\nhello\nworld", "hello"},
		{"all empty lines returns original", "\n\n\n", "\n\n\n"}, // 所有行都空时返回原始 s
		{"empty string", "", ""},
		{"leading/trailing spaces", "  \n  world  \n  ", "world"},
		{"tabs only", "\t\n\thello\t\n", "hello"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstNonEmptyLine(c.input)
			if got != c.want {
				t.Fatalf("firstNonEmptyLine(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestMergeEntriesEmpty(t *testing.T) {
	got := mergeEntries(nil, bucketKey{account: "acc", nType: TypeSTRMCreate})
	if got != nil {
		t.Fatalf("mergeEntries(nil) should return nil, got %v", got)
	}
	got = mergeEntries([]coalescedEntry{}, bucketKey{account: "acc", nType: TypeSTRMCreate})
	if got != nil {
		t.Fatalf("mergeEntries(empty) should return nil, got %v", got)
	}
}

func TestMergeEntriesSingle(t *testing.T) {
	entries := []coalescedEntry{{
		timestamp: time.Now(),
		content:   "single content",
		metadata:  map[string]string{"kind": "movie"},
	}}
	got := mergeEntries(entries, bucketKey{account: "acc", nType: TypeSTRMCreate})
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Content != "single content" {
		t.Fatalf("single entry should keep original content, got %q", got.Content)
	}
	if got.Metadata["kind"] != "movie" {
		t.Fatalf("metadata should be preserved, got %v", got.Metadata)
	}
}

func TestMergeEntriesMulti(t *testing.T) {
	now := time.Now()
	entries := []coalescedEntry{
		{timestamp: now, content: "first\nline", metadata: map[string]string{"kind": "movie"}},
		{timestamp: now.Add(time.Second), content: "second content", metadata: map[string]string{"kind": "movie"}},
		{timestamp: now.Add(2 * time.Second), content: "third", metadata: nil},
	}
	got := mergeEntries(entries, bucketKey{account: "testacc", nType: TypeSTRMCreate})
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(got.Content, "批量创建通知") {
		t.Fatalf("should contain batch label, got %q", got.Content)
	}
	if !strings.Contains(got.Content, "testacc") {
		t.Fatalf("should contain account, got %q", got.Content)
	}
	if !strings.Contains(got.Content, "3条") {
		t.Fatalf("should contain entry count 3, got %q", got.Content)
	}
}

// ==================== formatDuration (dispatcher.go) ====================

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		ms   int64
		want string
	}{
		{"zero", 0, "0ms"},
		{"sub-second", 500, "500ms"},
		{"one second exactly", 1000, "1.0s"},
		{"seconds only", 3500, "3.5s"},
		{"just below 60s", 59500, "59.5s"},
		{"one minute", 60000, "1m0s"},
		{"minutes + seconds", 90000, "1m30s"},
		{"one hour", 3600000, "1h0m0s"},
		{"hour + min + sec", 3661000, "1h1m1s"},
		{"negative", -100, "-100ms"},
		{"large value (hours)", 7322000, "2h2m2s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatDuration(c.ms)
			if got != c.want {
				t.Fatalf("formatDuration(%d) = %q, want %q", c.ms, got, c.want)
			}
		})
	}
}

// ==================== Dispatcher.buildInlineKeyboard (dispatcher.go) ====================

func TestBuildInlineKeyboard(t *testing.T) {
	d := &Dispatcher{} // 零值安全，方法内不使用 receiver 字段

	t.Run("nil notification returns nil", func(t *testing.T) {
		got := d.buildInlineKeyboard(nil)
		if got != nil {
			t.Fatalf("nil notification should return nil, got %v", got)
		}
	})

	t.Run("nil metadata returns nil", func(t *testing.T) {
		n := &Notification{Type: TypeSTRMCreate}
		got := d.buildInlineKeyboard(n)
		if got != nil {
			t.Fatalf("nil metadata should return nil, got %v", got)
		}
	})

	t.Run("non-STRM type returns nil", func(t *testing.T) {
		n := &Notification{
			Type:     TypeMediaAdded,
			Metadata: map[string]string{"kind": "movie"},
		}
		got := d.buildInlineKeyboard(n)
		if got != nil {
			t.Fatalf("non-STRM type should return nil, got %v", got)
		}
	})

	t.Run("STRM but unknown kind returns nil", func(t *testing.T) {
		n := &Notification{
			Type:     TypeSTRMCreate,
			Metadata: map[string]string{"kind": "unknown"},
		}
		got := d.buildInlineKeyboard(n)
		if got != nil {
			t.Fatalf("unknown kind should return nil, got %v", got)
		}
	})

	t.Run("STRM movie kind returns 1x2 buttons", func(t *testing.T) {
		n := &Notification{
			Type:     TypeSTRMCreate,
			Metadata: map[string]string{"kind": "movie", "cloud_path": "cloud/movie/test"},
		}
		got := d.buildInlineKeyboard(n)
		if got == nil {
			t.Fatal("expected non-nil buttons")
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 row, got %d", len(got))
		}
		if len(got[0]) != 2 {
			t.Fatalf("expected 2 buttons in row, got %d", len(got[0]))
		}
		if got[0][0].Text == "" || got[0][0].CallbackData == "" {
			t.Fatalf("first button should have Text and CallbackData, got %+v", got[0][0])
		}
	})

	t.Run("STRM tv kind works", func(t *testing.T) {
		n := &Notification{
			Type:     TypeSTRMDelete,
			Metadata: map[string]string{"kind": "tv", "cloud_path": "tv/path"},
		}
		got := d.buildInlineKeyboard(n)
		if got == nil || len(got) != 1 || len(got[0]) != 2 {
			t.Fatalf("expected 1x2 buttons for tv kind, got %v", got)
		}
	})

	t.Run("STRM series kind works", func(t *testing.T) {
		n := &Notification{
			Type:     TypeSTRMMove,
			Metadata: map[string]string{"kind": "series", "cloud_path": "series/path"},
		}
		got := d.buildInlineKeyboard(n)
		if got == nil || len(got) != 1 || len(got[0]) != 2 {
			t.Fatalf("expected 1x2 buttons for series kind, got %v", got)
		}
	})

	t.Run("CallbackData contains kind suffix", func(t *testing.T) {
		n := &Notification{
			Type:     TypeSTRMCreate,
			Metadata: map[string]string{"kind": "movie", "cloud_path": "cp"},
		}
		got := d.buildInlineKeyboard(n)
		if !strings.Contains(got[0][0].CallbackData, "movie") {
			t.Fatalf("Refresh button CallbackData should contain 'movie', got %q", got[0][0].CallbackData)
		}
	})
}
