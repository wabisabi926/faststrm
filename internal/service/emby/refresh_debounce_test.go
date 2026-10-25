package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
)

// 同一目录下的多个文件应合并为一次刷库，避免全量任务生成上万次 Emby API 调用。
func TestRefreshByPathCoalescesByDirectory(t *testing.T) {
	var mu sync.Mutex
	var findPaths []string
	refreshCount := 0
	refreshDone := make(chan struct{})
	var doneOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/emby/Items":
			p := r.URL.Query().Get("Path")
			mu.Lock()
			findPaths = append(findPaths, p)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Items": []map[string]any{{"Id": "item-1", "Path": p}},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/Refresh"):
			mu.Lock()
			refreshCount++
			n := refreshCount
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			if n >= 2 {
				doneOnce.Do(func() { close(refreshDone) })
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	settings := model.EmbySettings{
		URL: srv.URL, APIKey: "test-key", RefreshOnCreate: true, DebounceSeconds: 1,
	}
	r := NewMediaServerRefresh(nil, func() model.EmbySettings { return settings })
	defer r.CancelPending()

	root := t.TempDir()
	dirs := []string{filepath.Join(root, "d1"), filepath.Join(root, "d2")}
	for _, dir := range dirs {
		for i := 0; i < 25; i++ {
			if err := r.RefreshOnCreate(context.Background(), filepath.Join(dir, fmt.Sprintf("f%d.mkv", i))); err != nil {
				t.Fatalf("RefreshOnCreate: %v", err)
			}
		}
	}

	select {
	case <-refreshDone:
	case <-time.After(5 * time.Second):
		t.Fatal("等待刷库请求超时")
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if refreshCount != 2 {
		t.Fatalf("Refresh 请求 = %d, want 2（每个目录一次）", refreshCount)
	}
	if len(findPaths) != 2 {
		t.Fatalf("Find 请求 = %d, want 2（每个目录一次）", len(findPaths))
	}
	for _, p := range findPaths {
		if p != dirs[0] && p != dirs[1] {
			t.Errorf("刷库路径应为目录，got %q", p)
		}
	}
}
