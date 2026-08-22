// Package handler P0-4 STRM 执行历史 HTTP 接口
// 暴露 GET /api/history/strm 查询 STRM 生成/删除的细粒度历史，
// 用于失败定位、API 配额监控、局部重试依据。
package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// StrmHistoryDeps STRM 执行历史依赖
type StrmHistoryDeps struct {
	DB *sql.DB
}

// HandleStrmHistoryList GET /api/history/strm
// query: kind (full/increment/monitor/rename/move/delete), taskId, limit (默认100，上限500)
func HandleStrmHistoryList(deps StrmHistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{"items": []db.StrmHistoryEntry{}})
			return
		}
		q := r.URL.Query()
		kind := strings.TrimSpace(q.Get("kind"))
		taskID := strings.TrimSpace(q.Get("taskId"))
		limit := 100
		if l := q.Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		items, err := db.ListStrmHistory(deps.DB, kind, taskID, limit)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if items == nil {
			items = []db.StrmHistoryEntry{}
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{"items": items})
	}
}

// HandleStrmHistoryDetail GET /api/history/strm/:id
func HandleStrmHistoryDetail(deps StrmHistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "db unavailable"})
			return
		}
		idStr := strings.TrimPrefix(r.URL.Path, "/api/history/strm/")
		// 兼容 /api/history/strm?id=xxx 形式
		if idStr == "" || idStr == "/api/history/strm" {
			idStr = r.URL.Query().Get("id")
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		entry, err := db.GetStrmHistoryByID(deps.DB, id)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if entry == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		httpx.WriteJson(w, http.StatusOK, entry)
	}
}

// HandleStrmHistoryStats GET /api/history/strm/stats
// query: kind (可选)
func HandleStrmHistoryStats(deps StrmHistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			httpx.WriteJson(w, http.StatusOK, &db.StrmHistoryStats{})
			return
		}
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		stats, err := db.GetStrmHistoryStats(deps.DB, kind)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httpx.WriteJson(w, http.StatusOK, stats)
	}
}
