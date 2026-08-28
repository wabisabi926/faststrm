package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// LifeMonitorDeps 生活监控相关 handler 依赖
type LifeMonitorDeps struct {
	SettingsStore    *store.SettingsStore
	Monitor          *monitor.Monitor
	LifeEventLogRepo *db.LifeEventLogRepo
}

// ==================== Life Monitor Status ====================

// HandleLifeMonitorGET GET /api/lifeMonitor 返回监控配置、状态与账号列表
func HandleLifeMonitorGET(deps LifeMonitorDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
			return
		}
		config := settings.LifeMonitor

		var states map[string]monitor.AccountMonitorStatus
		if deps.Monitor != nil {
			states = deps.Monitor.Status()
		}
		// 转为数组，前端期望数组而非 map
		stateList := make([]monitor.AccountMonitorStatus, 0, len(states))
		for _, s := range states {
			stateList = append(stateList, s)
		}
		// accounts 为当前监控配置中的账号列表
		accounts := config.Accounts
		if accounts == nil {
			accounts = []string{}
		}
		httpx.OkJson(w, map[string]any{
			"config":   config,
			"states":   stateList,
			"accounts": accounts,
		})
	}
}

// lifeMonitorRequest POST /api/lifeMonitor 请求体
type lifeMonitorRequest struct {
	Action  string          `json:"action"`
	Account string          `json:"account"`
	Config  json.RawMessage `json:"config"`
	Updates json.RawMessage `json:"updates"`
}

// HandleLifeMonitorPOST POST /api/lifeMonitor 按动作执行监控操作
// action: start / stop / startAll / stopAll / saveConfig / verify / updateConfig
func HandleLifeMonitorPOST(deps LifeMonitorDeps) http.HandlerFunc { //nolint:cyclop // complexity: 26
	return func(w http.ResponseWriter, r *http.Request) {
		var req lifeMonitorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		if req.Action == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "action is required"})
			return
		}
		if deps.Monitor == nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "Monitor 未初始化"})
			return
		}

		ctx := r.Context()

		switch req.Action {
		case "start":
			if req.Account == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account is required"})
				return
			}
			if err := deps.Monitor.Start(ctx, req.Account); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "监控已启动: " + req.Account,
			})

		case "stop":
			if req.Account == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account is required"})
				return
			}
			deps.Monitor.Stop(req.Account)
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "监控已停止: " + req.Account,
			})

		case "startAll":
			if err := deps.Monitor.StartAll(ctx); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "已启动所有配置账号的监控",
			})

		case "stopAll":
			deps.Monitor.StopAll()
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "所有监控已停止",
			})

		case "saveConfig":
			if len(req.Config) == 0 {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "config is required"})
				return
			}
			var newCfg model.LifeMonitorSettings
			if err := json.Unmarshal(req.Config, &newCfg); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid config"})
				return
			}
			settings, err := deps.SettingsStore.ReadSettings()
			if err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
				return
			}
			settings.LifeMonitor = newCfg
			if err := deps.SettingsStore.SaveSettings(settings); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
				return
			}
			deps.Monitor.Refresh()
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "配置已保存",
			})

		case "verify":
			if req.Account == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account is required"})
				return
			}
			vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := deps.Monitor.VerifyAccount(vctx, req.Account); err != nil {
				logger.S().Warnf("[lifeMonitor/verify] %s: %v", req.Account, err)
				httpx.OkJson(w, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "账号验证成功: " + req.Account,
			})

		case "updateConfig":
			if len(req.Updates) == 0 {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "updates is required"})
				return
			}
			settings, err := deps.SettingsStore.ReadSettings()
			if err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取配置失败"})
				return
			}
			merged := settings.LifeMonitor
			// 在现有结构上 unmarshal updates：仅覆盖 updates 中存在的字段
			if err := json.Unmarshal(req.Updates, &merged); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid updates"})
				return
			}
			settings.LifeMonitor = merged
			if err := deps.SettingsStore.SaveSettings(settings); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
				return
			}
			deps.Monitor.Refresh()
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "配置已更新",
			})

		default:
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Unknown action: " + req.Action})
		}
	}
}

// ==================== Life Events ====================

// HandleLifeEventsGET GET /api/lifeEvents 列出生活事件处理日志
// query: account, eventType, success, since, until, limit
func HandleLifeEventsGET(deps LifeMonitorDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.LifeEventLogRepo == nil {
			httpx.OkJson(w, map[string]any{"total": 0, "items": []db.LifeEventLog{}})
			return
		}
		q := r.URL.Query()
		var query db.LifeEventLogQuery
		query.Account = q.Get("account")
		query.EventType = q.Get("eventType")
		if s := q.Get("success"); s != "" {
			var success bool
			switch s {
			case "true", "1":
				success = true
			case "false", "0":
				success = false
			default:
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid success param"})
				return
			}
			query.Success = &success
		}
		if s := q.Get("since"); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				query.SinceMs = n
			}
		}
		if u := q.Get("until"); u != "" {
			if n, err := strconv.ParseInt(u, 10, 64); err == nil {
				query.UntilMs = n
			}
		}
		if l := q.Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				query.Limit = n
			}
		}

		items, err := deps.LifeEventLogRepo.Query(r.Context(), query)
		if err != nil {
			logger.S().Errorf("[lifeEvents GET] query: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if items == nil {
			items = []db.LifeEventLog{}
		}
		httpx.OkJson(w, map[string]any{
			"total": len(items),
			"items": items,
		})
	}
}

// HandleLifeEventsDELETE DELETE /api/lifeEvents 删除日志
// query: id=xxx / action=cleanup / action=clear
func HandleLifeEventsDELETE(deps LifeMonitorDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.LifeEventLogRepo == nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "LifeEventLogRepo 未初始化"})
			return
		}
		q := r.URL.Query()
		action := q.Get("action")
		idStr := q.Get("id")
		ctx := r.Context()

		if action == "cleanup" {
			// 清理 30 天前的日志
			before := time.Now().AddDate(0, 0, -30).UnixMilli()
			removed, err := deps.LifeEventLogRepo.CleanupOld(ctx, before)
			if err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			httpx.OkJson(w, map[string]any{
				"success": true,
				"removed": removed,
			})
			return
		}

		if action == "clear" {
			removed, err := deps.LifeEventLogRepo.ClearAll(ctx)
			if err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			httpx.OkJson(w, map[string]any{
				"success": true,
				"removed": removed,
			})
			return
		}

		if idStr == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "id or action (clear/cleanup) is required"})
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Invalid id"})
			return
		}
		ok, err := deps.LifeEventLogRepo.DeleteByID(ctx, id)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !ok {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}
		httpx.OkJson(w, map[string]bool{"success": true})
	}
}
