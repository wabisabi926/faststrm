package handler

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wabisabi926/faststrm/internal/service/task"
)

func TestGenerateTaskID_Basic(t *testing.T) {
	cases := []struct {
		name       string
		account    string
		originPath string
		want       string
	}{
		{"普通中文路径", "aliyun", "/媒体/电影", "aliyun-电影"},
		{"带尾斜杠", "acc1", "/媒体/剧集/", "acc1-剧集"},
		{"英文路径", "quark", "/media/movies", "quark-movies"},
		{"路径含空格", "aliyun", "/我的 媒体/电影 库", "aliyun-电影-库"},
		{"路径含特殊字符", "aliyun", "/媒体/电影(4K)【新】", "aliyun-电影-4K-新"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := GenerateTaskID(c.account, c.originPath, nil)
			if got != c.want {
				t.Fatalf("GenerateTaskID(%q,%q) = %q, want %q", c.account, c.originPath, got, c.want)
			}
		})
	}
}

func TestGenerateTaskID_Conflict(t *testing.T) {
	existing := []task.Task{{ID: "aliyun-电影"}}
	got := GenerateTaskID("aliyun", "/媒体/电影", existing)
	if got != "aliyun-电影-2" {
		t.Fatalf("conflict: got %q, want %q", got, "aliyun-电影-2")
	}
	// 连续冲突
	existing = append(existing, task.Task{ID: "aliyun-电影-2"})
	if got := GenerateTaskID("aliyun", "/媒体/电影", existing); got != "aliyun-电影-3" {
		t.Fatalf("double conflict: got %q", got)
	}
}

func TestGenerateTaskID_ByteLimit(t *testing.T) {
	long := "/" + strings.Repeat("很长的中文目录名", 10)
	got := GenerateTaskID("aliyun", long, nil)
	if len(got) > maxTaskIDBytes {
		t.Fatalf("id too long: %d bytes (%q)", len(got), got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("id cut mid-rune: %q", got)
	}
}

func TestGenerateTaskID_Fallback(t *testing.T) {
	got := GenerateTaskID("", "", nil)
	if !strings.HasPrefix(got, "task-") {
		t.Fatalf("fallback should be task-<ts>, got %q", got)
	}
}

func TestGenerateTaskID_KeepsOldStyleUnaffected(t *testing.T) {
	// 前端显式传 ID 时不应被改名（逻辑在 HandleUpsertTask，这里验证生成器不干扰）
	got := GenerateTaskID("aliyun", "/电影", []task.Task{{ID: "task-1729300000000"}})
	if got != "aliyun-电影" {
		t.Fatalf("got %q", got)
	}
}
