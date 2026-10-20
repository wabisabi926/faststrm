package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	AccountStore     *store.AccountStore
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

// validateLifeMonitorAccounts 保存前校验监控配置里的账户引用。
// 账户改名/删除后 settings.json 可能残留旧名字：监控会每轮刷新报错且永不产生事件，
// 因此保存时直接拒绝并列出缺失账户，让用户先修正配置（账号改名联动会自动同步）。
func validateLifeMonitorAccounts(cfg model.LifeMonitorSettings, accountStore *store.AccountStore) string {
	if accountStore == nil {
		return ""
	}
	missing := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || accountStore.Has(name) {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		missing = append(missing, name)
	}
	for _, a := range cfg.Accounts {
		add(a)
	}
	for _, pm := range cfg.PathMappings {
		add(pm.Account)
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return fmt.Sprintf("生活监控引用了不存在的账户：%s（账户可能已改名或删除，请先修正配置，否则监控会持续报错）", strings.Join(missing, "、"))
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
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少操作类型"})
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
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少账号"})
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
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少账号"})
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
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少配置内容"})
				return
			}
			// 合并语义：以现有配置为基准，仅覆盖 config 中出现的字段，
			// 避免未提供字段被零值清空（与 updateConfig 保持一致）。
			badRequest := false
			_, err := deps.SettingsStore.Update(func(settings *model.Settings) error {
				merged := settings.LifeMonitor
				if err := json.Unmarshal(req.Config, &merged); err != nil {
					badRequest = true
					return fmt.Errorf("invalid config: %w", err)
				}
				if msg := validateLifeMonitorAccounts(merged, deps.AccountStore); msg != "" {
					badRequest = true
					return fmt.Errorf("%s", msg)
				}
				settings.LifeMonitor = merged
				return nil
			})
			if err != nil {
				if badRequest {
					httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				} else {
					httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
				}
				return
			}
			deps.Monitor.Refresh()
			httpx.OkJson(w, map[string]any{
				"success": true,
				"message": "配置已保存",
			})

		case "verify":
			if req.Account == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少账号"})
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
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少更新内容"})
				return
			}
			badRequest := false
			_, err := deps.SettingsStore.Update(func(settings *model.Settings) error {
				merged := settings.LifeMonitor
				// 在现有结构上 unmarshal updates：仅覆盖 updates 中存在的字段
				if err := json.Unmarshal(req.Updates, &merged); err != nil {
					badRequest = true
					return fmt.Errorf("invalid updates: %w", err)
				}
				if msg := validateLifeMonitorAccounts(merged, deps.AccountStore); msg != "" {
					badRequest = true
					return fmt.Errorf("%s", msg)
				}
				settings.LifeMonitor = merged
				return nil
			})
			if err != nil {
				if badRequest {
					httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				} else {
					httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存配置失败"})
				}
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
// query: id=xxx / action=cleanup / action=clear / eventType=poll
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

		// 按事件类型清理（用于清掉历史轮询噪音等）：?eventType=poll
		if eventType := q.Get("eventType"); eventType != "" {
			removed, err := deps.LifeEventLogRepo.DeleteByEventType(ctx, eventType)
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
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "id or action (clear/cleanup) or eventType is required"})
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
