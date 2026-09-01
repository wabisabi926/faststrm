package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// TaskHandlerDeps 任务 handler 依赖
type TaskHandlerDeps struct {
	ExecutorDeps    task.ExecutorDeps
	TasksStore      *store.TasksStore
	Runtime         *task.Runtime
	Scheduler       *task.Scheduler
	TaskHistoryRepo *db.TaskHistoryRepo
}

// StartTaskRequest POST /api/startTask { "taskId": "xxx" }
type StartTaskRequest struct {
	TaskID string `json:"taskId"`
}

// HandleStartTask 启动任务（异步）
func HandleStartTask(deps TaskHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StartTaskRequest
		if r.Body != nil {
			// 兼容 query 参数：?taskId=xxx 也接受
			if q := r.URL.Query().Get("taskId"); q != "" {
				req.TaskID = q
			} else {
				_ = json.NewDecoder(r.Body).Decode(&req)
			}
		}
		if req.TaskID == "" {
			httpx.WriteJson(w, http.StatusBadRequest, task.ExecuteResult{
				Success: false, Reason: "missing_task_id", Message: "taskId required",
			})
			return
		}

		// 前置快速检查：确保任务存在（避免异步启动后立即失败）
		tasks, err := deps.TasksStore.ReadTasks()
		if err != nil {
			httpx.WriteJson(w, http.StatusOK, task.ExecuteResult{
				Success: false, Reason: "store_error", Message: err.Error(),
			})
			return
		}
		var foundTask *task.Task
		for i := range tasks {
			if tasks[i].ID == req.TaskID {
				foundTask = &tasks[i]
				break
			}
		}
		if foundTask == nil {
			httpx.WriteJson(w, http.StatusOK, task.ExecuteResult{
				Success: false, Reason: "not_found", Message: "Task not found",
			})
			return
		}

		// 异步执行任务（立即返回，实际执行通过 SSE 广播进度）
		// 注意：独占锁由 task.ExecuteTask 内部处理，这里不重复获取
		go func() {
			res := task.ExecuteTask(context.Background(), req.TaskID, deps.ExecutorDeps)
			// 无论成功失败，重新读取任务刷新调度
			go deps.Scheduler.RefreshAll(deps.TasksStore)
			_ = res
		}()

		// 立即返回启动成功
		httpx.WriteJson(w, http.StatusOK, task.ExecuteResult{
			Success: true, TaskID: req.TaskID, Message: "Task started",
		})
	}
}

// CancelTaskRequest POST /api/cancelTask { "id": "xxx" | "taskId": "xxx" }
type CancelTaskRequest struct {
	ID     string `json:"id"`
	TaskID string `json:"taskId"`
}

// HandleCancelTask 取消正在运行的任务
// 兼容 query 参数 id 或 taskId
func HandleCancelTask(deps TaskHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CancelTaskRequest
		// 优先 query
		if q := r.URL.Query().Get("taskId"); q != "" {
			req.TaskID = q
		}
		if q := r.URL.Query().Get("id"); q != "" && req.TaskID == "" {
			req.TaskID = q
		}
		if req.TaskID == "" && r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		tid := req.TaskID
		if tid == "" {
			tid = req.ID
		}
		if tid == "" {
			httpx.WriteJson(w, http.StatusBadRequest, task.CancelResult{
				Success: false, Reason: "missing_task_id", Message: "id/taskId required",
			})
			return
		}
		if !deps.Runtime.Cancel(tid) {
			// 如果不是运行中，但尝试 cancel 也 OK（返回成功但提示 not running）
			httpx.WriteJson(w, http.StatusOK, task.CancelResult{
				Success: true, Reason: "not_running", Message: "task not running",
			})
			return
		}
		// SSE 广播取消
		sse.GetServer().EmitComplete(sse.CompletePayload{TaskID: tid, Status: string(task.StatusCancelled)})
		httpx.WriteJson(w, http.StatusOK, task.CancelResult{Success: true, Message: "cancelled"})
	}
}

// HandleListTasks GET /api/tasks
func HandleListTasks(deps TaskHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var tasks []task.Task
		if deps.TasksStore != nil {
			read, err := deps.TasksStore.ReadTasks()
			if err != nil {
				logger.S().Warnf("[HandleListTasks] ReadTasks failed: %v, returning empty list", err)
			} else {
				tasks = read
			}
		}
		var running map[string]*task.RuntimeState
		if deps.Runtime != nil {
			running = deps.Runtime.RunningTasks()
		} else {
			running = make(map[string]*task.RuntimeState)
		}
		var schedStatus task.SchedulerStatus
		if deps.Scheduler != nil {
			schedStatus = deps.Scheduler.Status()
		}
		schedNext := make(map[string]any, len(schedStatus.Tasks))
		for _, t := range schedStatus.Tasks {
			tid, _ := t["taskId"].(string)
			if tid == "" {
				continue
			}
			schedNext[tid] = map[string]any{
				"cron":      t["cron"],
				"nextRunAt": t["nextRunAt"],
			}
		}
		type enriched struct {
			task.Task
			Runtime      *task.RuntimeState `json:"runtime,omitempty"`
			ScheduleNext any                `json:"scheduleNext,omitempty"`
		}
		out := make([]enriched, 0, len(tasks))
		for _, t := range tasks {
			en := enriched{Task: t}
			if rs, ok := running[t.ID]; ok {
				en.Runtime = rs
			}
			if t.Schedule != nil && t.Schedule.Enabled {
				en.ScheduleNext = schedNext[t.ID]
			}
			out = append(out, en)
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"tasks":     out,
			"scheduler": schedStatus,
		})
	}
}

// HandleTaskLog GET /api/taskLog/:taskId
func HandleTaskLog(deps TaskHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// params: go-zero mux vars 里取，这里兼容 query
		tid := r.URL.Query().Get("taskId")
		if tid == "" {
			// 路径参数占位：/api/taskLog/xxx 通过后缀切
			p := r.URL.Path
			idx := lastSeg(p)
			if idx != "" {
				tid = idx
			}
		}
		if tid == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "taskId required"})
			return
		}
		// 先从运行时内存取，再从 SSE 缓冲，最后才从 DB 取
		runtimeLogs := deps.Runtime.GetLogs(tid)
		sseLogs := sse.GetServer().GetTaskLogs(tid)
		if len(runtimeLogs) > 0 || len(sseLogs) > 0 {
			merged := append([]string(nil), runtimeLogs...)
			merged = append(merged, sseLogs...)
			WriteTextLines(w, merged)
			return
		}
		// 从 DB 读取：先查最近 execution
		if deps.TaskHistoryRepo != nil {
			ctx := r.Context()
			execs, err := deps.TaskHistoryRepo.Query(ctx, db.TaskHistoryQuery{
				TaskID: tid, Limit: 1,
			})
			if err != nil {
				logger.S().Warnf("[taskLog] query %s: %v", tid, err)
			} else if len(execs) > 0 {
				lines, err := deps.TaskHistoryRepo.GetLogs(ctx, execs[0].ID, 20000)
				if err != nil {
					logger.S().Warnf("[taskLog] get logs %d: %v", execs[0].ID, err)
				} else {
					WriteTextLines(w, lines)
					return
				}
			}
		}
		WriteTextLines(w, []string{})
	}
}

// lastSeg 取 URL.Path 最后一段
func lastSeg(p string) string {
	for len(p) > 0 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	idx := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}

// WriteTextLines 写纯文本多行到响应
func WriteTextLines(w http.ResponseWriter, lines []string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	for _, ln := range lines {
		_, _ = w.Write([]byte(ln))
		_ = w
		_, _ = w.Write([]byte{'\n'})
	}
}

// UpsertTaskRequest 新建/更新任务请求（兼容 form + JSON）
type UpsertTaskRequest struct {
	ID             string             `json:"id" form:"id"`
	Name           string             `json:"name" form:"name"`
	Account        string             `json:"account" form:"account"`
	AccountType    string             `json:"accountType" form:"accountType"`
	SourcePath     string             `json:"sourcePath" form:"sourcePath"`
	OriginPath     string             `json:"originPath" form:"originPath"` // alias
	TargetPath     string             `json:"targetPath" form:"targetPath"`
	Enabled        string             `json:"enabled" form:"enabled"` // "on"/"true"/"1" 表示 true
	ScheduleMode   string             `json:"scheduleMode" form:"scheduleMode"`
	ScheduleValue  string             `json:"scheduleValue" form:"scheduleValue"`
	Schedule       *task.TaskSchedule `json:"schedule,omitempty"` // 嵌套 schedule 对象（前端 TaskScheduleDialog 发）
	StrmType       string             `json:"strmType" form:"strmType"`
	StrmPrefix     string             `json:"strmPrefix" form:"strmPrefix"`
	RemoveExtra    string             `json:"removeExtraFiles" form:"removeExtraFiles"`
	EnableEnc      string             `json:"enablePathEncoding" form:"enablePathEncoding"`
	AccountCookie  string             `json:"cookie" form:"cookie"` // 兼容旧前端
	AccountAccount string             `json:"account_" form:"account_"`
}

func parseBoolAny(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "true", "1", "yes", "checked":
		return true
	}
	return false
}

// parseSchedule 将 (scheduleMode, scheduleValue) 解析成 TaskSchedule
func parseSchedule(mode, value string) *task.TaskSchedule {
	mode = strings.ToLower(strings.TrimSpace(mode))
	value = strings.TrimSpace(value)
	if mode == "" || mode == "manual" {
		return nil
	}
	s := &task.TaskSchedule{Enabled: true, Mode: mode}
	switch mode {
	case "interval":
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			s.IntervalMinutes = n
		} else {
			s.IntervalMinutes = 30
		}
	case "daily":
		s.Time = value
	case "weekly":
		// value 形如 "Mon-02:00"
		parts := strings.SplitN(value, "-", 2)
		if len(parts) == 2 {
			s.Time = parts[1]
			days := map[string]int{
				"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
			}
			if d, ok := days[strings.ToLower(parts[0])]; ok {
				s.Weekdays = []int{d}
			} else {
				s.Weekdays = []int{1}
			}
		} else {
			s.Time = value
			s.Weekdays = []int{1}
		}
	}
	return s
}

func fillUpsertFromBody(r *http.Request, req *UpsertTaskRequest) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		_ = json.NewDecoder(r.Body).Decode(req)
		return
	}
	_ = r.ParseForm()
	req.ID = r.FormValue("id")
	req.Name = r.FormValue("name")
	req.Account = r.FormValue("account")
	req.AccountType = r.FormValue("accountType")
	req.SourcePath = r.FormValue("sourcePath")
	req.TargetPath = r.FormValue("targetPath")
	req.Enabled = r.FormValue("enabled")
	req.ScheduleMode = r.FormValue("scheduleMode")
	req.ScheduleValue = r.FormValue("scheduleValue")
	req.StrmPrefix = r.FormValue("strmPrefix")
	req.RemoveExtra = r.FormValue("removeExtraFiles")
	req.EnableEnc = r.FormValue("enablePathEncoding")
	req.StrmPrefix = r.FormValue("strmPrefix")
}

func (req *UpsertTaskRequest) toTask(existing *task.Task) task.Task {
	var t task.Task
	if existing != nil {
		t = *existing
	}
	if req.ID != "" {
		t.ID = req.ID
	}
	if t.ID == "" {
		t.ID = fmt.Sprintf("task-%d", time.Now().UnixMilli())
	}
	if req.Name != "" {
		t.Name = req.Name
	}
	if req.Account != "" {
		t.Account = req.Account
	}
	if req.AccountType != "" {
		t.AccountType = req.AccountType
	}
	origin := req.OriginPath
	if origin == "" {
		origin = req.SourcePath
	}
	if origin != "" {
		t.OriginPath = origin
	}
	if req.TargetPath != "" {
		t.TargetPath = req.TargetPath
	}
	if req.StrmType != "" {
		t.StrmType = req.StrmType
	}
	if req.StrmPrefix != "" {
		t.StrmPrefix = req.StrmPrefix
	}
	t.RemoveExtraFiles = parseBoolAny(req.RemoveExtra)
	t.EnablePathEncoding = parseBoolAny(req.EnableEnc)

	// 优先处理前端 TaskScheduleDialog 发的嵌套 schedule 对象
	if req.Schedule != nil {
		if !req.Schedule.Enabled {
			t.Schedule = nil
		} else {
			t.Schedule = req.Schedule
		}
	} else {
		// 向后兼容旧的扁平字段
		sched := parseSchedule(req.ScheduleMode, req.ScheduleValue)
		if sched != nil {
			sched.Enabled = parseBoolAny(req.Enabled)
			t.Schedule = sched
		} else if req.ScheduleMode == "manual" {
			t.Schedule = nil
		}
	}
	return t
}

// HandleUpsertTask POST /api/task 创建新任务；PUT /api/task 更新任务
func HandleUpsertTask(deps TaskHandlerDeps, isUpdate bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req UpsertTaskRequest
		fillUpsertFromBody(r, &req)

		if isUpdate {
			if req.ID == "" {
				req.ID = r.URL.Query().Get("id")
			}
			if req.ID == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "task id required for update"})
				return
			}
		}

		// 必填检查（新建）
		if !isUpdate {
			if req.Account == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account required"})
				return
			}
			origin := req.OriginPath
			if origin == "" {
				origin = req.SourcePath
			}
			if origin == "" || req.TargetPath == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "sourcePath and targetPath required"})
				return
			}
		}

		var existing *task.Task
		if req.ID != "" {
			list, err := deps.TasksStore.ReadTasks()
			if err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			for i := range list {
				if list[i].ID == req.ID {
					existing = &list[i]
					break
				}
			}
			if isUpdate && existing == nil {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "task not found"})
				return
			}
		}

		t := req.toTask(existing)
		if err := deps.TasksStore.UpsertTask(t); err != nil {
			logger.S().Errorf("[UpsertTask] save %s: %v", t.ID, err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		go deps.Scheduler.RefreshAll(deps.TasksStore)

		w.Header().Set("HX-Trigger", "tasks-changed")
		if isUpdate {
			httpx.WriteJson(w, http.StatusOK, map[string]any{"success": true, "task": t})
		} else {
			httpx.WriteJson(w, http.StatusCreated, map[string]any{"success": true, "task": t})
		}
	}
}

// HandleDeleteTask DELETE /api/task?id=xxx
func HandleDeleteTask(deps TaskHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			// 兼容路径参数 /api/task/{id}
			id = lastSeg(r.URL.Path)
		}
		if id == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "task id required"})
			return
		}
		// 先尝试 cancel 避免残留
		_ = deps.Runtime.Cancel(id)
		deleted, err := deps.TasksStore.DeleteTask(id)
		if err != nil {
			logger.S().Errorf("[DeleteTask] %s: %v", id, err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !deleted {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "task not found"})
			return
		}
		go deps.Scheduler.RefreshAll(deps.TasksStore)
		w.Header().Set("HX-Trigger", "tasks-changed")
		httpx.WriteJson(w, http.StatusOK, map[string]any{"success": true, "message": "task deleted"})
	}
}
