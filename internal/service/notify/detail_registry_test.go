package notify

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDetailRegistry_BasicRoundTrip(t *testing.T) {
	path := "/媒体/电影/某部电影 (2024)/movie.strm"
	id := RegisterDetailPath(path)
	if len(id) != detailIDLen {
		t.Fatalf("id length: got %d, want %d", len(id), detailIDLen)
	}
	got, ok := LookupDetailPath(id)
	if !ok || got != path {
		t.Fatalf("round trip: got %q ok=%v, want %q", got, ok, path)
	}
}

func TestDetailRegistry_TTLExpiry(t *testing.T) {
	path := "/ttl-测试路径-" + fmt.Sprint(time.Now().UnixNano())
	id := RegisterDetailPath(path)
	// 人为把条目 lastSeen 拨到 TTL 之外
	detailMu.Lock()
	e := detailPaths[id]
	e.lastSeen = time.Now().Add(-detailEntryTTL - time.Minute)
	detailPaths[id] = e
	detailMu.Unlock()

	if _, ok := LookupDetailPath(id); ok {
		t.Fatal("expired entry should not be returned")
	}
	// 过期条目应被删除
	detailMu.Lock()
	_, exists := detailPaths[id]
	detailMu.Unlock()
	if exists {
		t.Fatal("expired entry should be deleted on lookup")
	}
}

func TestDetailRegistry_LRUEviction(t *testing.T) {
	// 记录注册前条目数，构造填满 + 淘汰的场景
	base := DetailRegistryLen()
	if base > detailMaxEntries-2 {
		t.Skip("registry nearly full, skip eviction test")
	}

	oldPath := "/lru-最旧路径-" + fmt.Sprint(time.Now().UnixNano())
	oldID := RegisterDetailPath(oldPath)
	// 把 old 条目 lastSeen 拨旧，确保它是「最旧」
	detailMu.Lock()
	e := detailPaths[oldID]
	e.lastSeen = time.Now().Add(-time.Hour)
	detailPaths[oldID] = e
	detailMu.Unlock()

	// 注册到超过容量，触发淘汰
	for i := 0; i < detailMaxEntries-base+8; i++ {
		RegisterDetailPath(fmt.Sprintf("/lru-填充-%d-%d", time.Now().UnixNano(), i))
	}

	// 最旧条目应被淘汰
	if _, ok := LookupDetailPath(oldID); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if n := DetailRegistryLen(); n >= detailMaxEntries+64 {
		t.Fatalf("registry should stay bounded, got %d", n)
	}
}

func TestDetailRegistry_LookupRefreshesLastSeen(t *testing.T) {
	path := "/lru-刷新测试-" + fmt.Sprint(time.Now().UnixNano())
	id := RegisterDetailPath(path)
	// 拨旧
	detailMu.Lock()
	e := detailPaths[id]
	e.lastSeen = time.Now().Add(-time.Hour)
	detailPaths[id] = e
	detailMu.Unlock()

	// Lookup 应刷新 lastSeen
	if _, ok := LookupDetailPath(id); !ok {
		t.Fatal("entry should exist")
	}
	detailMu.Lock()
	after := detailPaths[id]
	detailMu.Unlock()
	if time.Since(after.lastSeen) > time.Minute {
		t.Fatalf("Lookup should refresh lastSeen, got %v", after.lastSeen)
	}
}

func TestLimitPickerButtons(t *testing.T) {
	var sb strings.Builder
	buttons := make([][]InlineKeyboardButton, 0, 12)
	for i := 0; i < 12; i++ {
		buttons = append(buttons, []InlineKeyboardButton{{Text: fmt.Sprintf("按钮%d", i)}})
	}
	got := limitPickerButtons(&sb, buttons)
	if len(got) != maxPickerButtons {
		t.Fatalf("expected %d buttons, got %d", maxPickerButtons, len(got))
	}
	if !strings.Contains(sb.String(), "4 个请到 Web UI") {
		t.Fatalf("hint missing: %s", sb.String())
	}

	// 不超限时原样返回且无提示
	sb.Reset()
	ok := limitPickerButtons(&sb, buttons[:3])
	if len(ok) != 3 || sb.String() != "" {
		t.Fatalf("under limit should be unchanged, got %d, hint=%q", len(ok), sb.String())
	}
}

func TestBuildSyncTaskPicker_ButtonCap(t *testing.T) {
	tasks := make([]map[string]any, 0, 12)
	for i := 0; i < 12; i++ {
		tasks = append(tasks, map[string]any{
			"id": fmt.Sprintf("task-%d", i), "name": fmt.Sprintf("任务%d", i),
			"account": "acc", "schedule": "手动",
		})
	}
	text, buttons := BuildSyncTaskPicker(tasks)
	// 12 任务按钮 + 全部执行 = 13 → 截到 8，最后追加返回按钮
	if len(buttons) != maxPickerButtons+1 {
		t.Fatalf("buttons: got %d, want %d (cap + back)", len(buttons), maxPickerButtons+1)
	}
	if !strings.Contains(text, "请到 Web UI") {
		t.Fatalf("hint missing in text: %s", text)
	}
	// 返回按钮必须保留在最后一行
	if buttons[len(buttons)-1][0].CallbackData != callbackMenuSTRM {
		t.Fatal("back button should remain last")
	}
}

func TestBuildCleanupMappingPicker_ButtonCap(t *testing.T) {
	mappings := make([]map[string]string, 0, 10)
	for i := 0; i < 10; i++ {
		mappings = append(mappings, map[string]string{
			"account": "acc", "cloudPath": fmt.Sprintf("/云/%d", i), "localPath": fmt.Sprintf("/本/%d", i),
		})
	}
	text, buttons := BuildCleanupMappingPicker(mappings)
	// 10 映射按钮 → 截到 8，最后追加返回按钮
	if len(buttons) != maxPickerButtons+1 {
		t.Fatalf("buttons: got %d, want %d (cap + back)", len(buttons), maxPickerButtons+1)
	}
	if !strings.Contains(text, "请到 Web UI") {
		t.Fatalf("hint missing: %s", text)
	}
	if buttons[len(buttons)-1][0].CallbackData != callbackMenuSTRM {
		t.Fatal("back button should remain last")
	}
}

func TestEventLabelZH(t *testing.T) {
	cases := map[string]string{
		"poll":          "轮询",
		"create":        "创建",
		"move-out":      "移出",
		"move-recreate": "移动重建",
		"stall-timeout": "轮询超时",
		"new_folder":    "新建文件夹",
		"folder-sync":   "文件夹同步",
	}
	for in, want := range cases {
		if got := eventLabelZH(in); got != want {
			t.Fatalf("eventLabelZH(%q) = %q, want %q", in, got, want)
		}
	}
	if got := eventLabelZH("unknown-x"); got != "unknown-x" {
		t.Fatalf("fallback: got %q", got)
	}
}
