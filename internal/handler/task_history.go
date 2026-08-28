package handler

import (
	"net/http"
	"strconv"

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
