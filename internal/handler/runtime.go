package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/journal"
)

// HandleRuntimeStatus GET /api/runtime/status
// 返回当前进程运行状态与最近系统事件；不是任务执行日志。
func HandleRuntimeStatus(deps EmbyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := journal.StartedAt()
		httpx.OkJson(w, map[string]any{
			"status":        "ok",
			"version":       appVersion,
			"startedAt":     started,
			"uptimeSeconds": int64(time.Since(started).Seconds()),
			"proxy":         buildProxyStatus(deps),
			"journal":       journal.Default().List(30),
		})
	}
}

// HandleRuntimeJournal GET /api/runtime/journal?limit=&level=&category=
func HandleRuntimeJournal() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 500 {
			limit = 500
		}
		level := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level")))
		category := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))

		items := journal.Default().List(0)
		out := make([]journal.Event, 0, limit)
		for _, ev := range items {
			if level != "" && string(ev.Level) != level {
				continue
			}
			if category != "" && strings.ToLower(ev.Category) != category {
				continue
			}
			out = append(out, ev)
			if len(out) >= limit {
				break
			}
		}
		httpx.OkJson(w, map[string]any{"items": out, "count": len(out)})
	}
}
