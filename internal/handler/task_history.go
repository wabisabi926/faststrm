package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/db"
)

// TaskHistoryDeps 任务历史依赖
type TaskHistoryDeps struct {
	Repo *db.TaskHistoryRepo
}

// HandleTaskHistory GET /api/taskHistory
// query: taskId, account, status, before (unix ms), limit
func HandleTaskHistory(deps TaskHistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Repo == nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{"items": []db.TaskExecution{}})
			return
		}
		q := r.URL.Query()
		var query db.TaskHistoryQuery
		query.TaskID = q.Get("taskId")
		query.Account = q.Get("account")
		query.Status = q.Get("status")
		if b := q.Get("before"); b != "" {
			if n, err := strconv.ParseInt(b, 10, 64); err == nil {
				query.BeforeMs = n
			}
		}
		if l := q.Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				query.Limit = n
			}
		}
		items, err := deps.Repo.Query(r.Context(), query)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if items == nil {
			items = []db.TaskExecution{}
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{"items": items})
	}
}

// HandleTaskHistoryLogs GET /api/taskHistory/:executionId/logs
// 返回指定 execution 的日志行数组
func HandleTaskHistoryLogs(deps TaskHistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Repo == nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{"logs": []string{}})
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/taskHistory/")
		executionIDStr := strings.TrimSuffix(path, "/logs")
		executionID, err := strconv.ParseInt(executionIDStr, 10, 64)
		if err != nil || executionID <= 0 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid executionId"})
			return
		}

		limit := 20000
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}

		logs, err := deps.Repo.GetLogs(r.Context(), executionID, limit)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if logs == nil {
			logs = []string{}
		}

		httpx.WriteJson(w, http.StatusOK, map[string]any{"logs": logs})
	}
}

// HandleTaskHistoryDelete DELETE /api/taskHistory
// query:
//
//	executionId=xxx    删除单条执行记录（含日志）
//	action=cleanup     清空全部执行记录（含日志）
func HandleTaskHistoryDelete(deps TaskHistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Repo == nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		ctx := r.Context()
		q := r.URL.Query()
		if q.Get("action") == "cleanup" {
			if err := deps.Repo.DeleteAll(ctx); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			httpx.WriteJson(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if idStr := q.Get("executionId"); idStr != "" {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil || id <= 0 {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid executionId"})
				return
			}
			if derr := deps.Repo.DeleteExecution(ctx, id); derr != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": derr.Error()})
				return
			}
			httpx.WriteJson(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid parameter"})
	}
}
