package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
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
				Success: false, Reason: "missing_task_id", Message: "缺少任务 ID",
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
				Success: false, Reason: "not_found", Message: "任务不存在",
			})
			return
		}

		// 异步执行任务，但等待执行器真实拿到账号锁后再返回，
		// 避免同账号已有任务时前端误报“已启动”（实际被 blocked）。
		startedCh := make(chan task.ExecuteResult, 1)
		go func() {
			res := task.ExecuteTask(context.Background(), req.TaskID, deps.ExecutorDeps, func(started task.ExecuteResult) {
				startedCh <- started
			})
			// 无论成功失败，重新读取任务刷新调度
			go deps.Scheduler.RefreshAll(deps.TasksStore)
			_ = res
		}()

		select {
		case res := <-startedCh:
			if !res.Success {
				httpx.WriteJson(w, http.StatusConflict, res)
				return
			}
			httpx.WriteJson(w, http.StatusOK, res)
		case <-time.After(5 * time.Second):
			httpx.WriteJson(w, http.StatusGatewayTimeout, task.ExecuteResult{
				Success: false, Reason: "start_timeout", Message: "任务启动超时，请稍后刷新状态",
			})
		}
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
			httpx.WriteJson(w, http.StatusOK, task.CancelResult{
				Success: false, Reason: "not_running", Message: "任务当前未在运行",
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
			} else if deps.TaskHistoryRepo != nil {
				// 服务重启后内存运行态会丢失：用最近一次执行历史回填状态，
				// 否则前端会把所有历史任务都显示成“待处理”。
				if execs, err := deps.TaskHistoryRepo.Query(r.Context(), db.TaskHistoryQuery{
					TaskID: t.ID, Limit: 1,
				}); err != nil {
					logger.S().Warnf("[HandleListTasks] query latest execution for %s: %v", t.ID, err)
				} else if len(execs) > 0 {
					e := execs[0]
					status := task.Status(e.Status)
					if status == task.StatusRunning {
						// 没有运行态却有 running 历史：要么刚启动尚未注册，要么上次进程异常退出。
						status = task.StatusPending
					}
					en.Runtime = &task.RuntimeState{
						Status:          status,
						StartedAt:       e.StartedAt,
						EndedAt:         e.EndedAt,
						Error:           e.Error,
						TotalFiles:      e.Summary.TotalFiles,
						DownloadedFiles: e.Summary.DownloadedFiles,
						DeletedFiles:    e.Summary.DeletedFiles,
					}
				}
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
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少任务 ID"})
			return
		}
		// 先从运行时内存取，再从 SSE 缓冲，最后才从 DB 取
		runtimeLogs := deps.Runtime.GetLogs(tid)
		sseLogs := sse.GetServer().GetTaskLogs(tid)
		if len(runtimeLogs) > 0 || len(sseLogs) > 0 {
			merged := append([]string(nil), runtimeLogs...)
			for _, frame := range sseLogs {
				if line, ok := sseFrameLine(frame); ok {
					merged = append(merged, line)
				}
			}
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

// sseFrameLine 把 SSE 编码帧（"data: {json}\n\n"）还原成纯 JSON 行，
// 供 /api/taskLog 以纯文本格式返回，避免前端把 "data:" 前缀当成原始日志。
func sseFrameLine(frame string) (string, bool) {
	frame = strings.TrimSpace(frame)
	frame = strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
	if frame == "" {
		return "", false
	}
	return frame, true
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
// 布尔字段用 *bool：
//   - nil = 请求未显式传值（PUT 更新时保留 existing 里的原值，避免"编辑不改这字段"被归零）
//   - 非 nil = 显式 true/false，请求体 JSON bool / form value string("on"/"true"/"1"/…) 两种格式都能解
type UpsertTaskRequest struct {
	ID             string             `json:"id" form:"id"`
	Name           string             `json:"name" form:"name"`
	Account        string             `json:"account" form:"account"`
	AccountType    string             `json:"accountType" form:"accountType"`
	SourcePath     string             `json:"sourcePath" form:"sourcePath"`
	OriginPath     string             `json:"originPath" form:"originPath"` // alias
	TargetPath     string             `json:"targetPath" form:"targetPath"`
	Enabled        *bool              `json:"enabled" form:"-"` // form 字符串路径在 fillUpsertFromBody 单独写
	ScheduleMode   string             `json:"scheduleMode" form:"scheduleMode"`
	ScheduleValue  string             `json:"scheduleValue" form:"scheduleValue"`
	Schedule       *task.TaskSchedule `json:"schedule,omitempty"` // 嵌套 schedule 对象（前端 TaskScheduleDialog 发）
	StrmType       string             `json:"strmType" form:"strmType"`
	StrmPrefix     *string            `json:"strmPrefix" form:"-"`
	EnableEnc      *bool              `json:"enablePathEncoding" form:"-"`
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

// ptrBool 把 form 字符串形式的布尔值转成 *bool 指针：
//   - 未传值（空串）→ nil：表示请求未显式指定（PUT 更新时保留 existing 原值）
//   - 传值 → 对应 *bool，"on"/"off"/… 按 parseBoolAny 解析
func ptrBool(v string) *bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	b := parseBoolAny(v)
	return &b
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
	// form 解析：multipart 必须走 ParseMultipartForm，否则 PostForm 为空
	if strings.Contains(ct, "multipart/form-data") {
		_ = r.ParseMultipartForm(32 << 20)
	} else {
		_ = r.ParseForm()
	}
	req.ID = r.FormValue("id")
	req.Name = r.FormValue("name")
	req.Account = r.FormValue("account")
	req.AccountType = r.FormValue("accountType")
	req.SourcePath = r.FormValue("sourcePath")
	req.TargetPath = r.FormValue("targetPath")
	req.Enabled = ptrBool(r.FormValue("enabled"))
	req.ScheduleMode = r.FormValue("scheduleMode")
	req.ScheduleValue = r.FormValue("scheduleValue")
	// 表单路径需要区分“字段未提交”和“提交空值”，后者用于清空任务级 STRM 前缀
	if vals, ok := r.Form["strmPrefix"]; ok && len(vals) > 0 {
		v := vals[0]
		req.StrmPrefix = &v
	}
	req.EnableEnc = ptrBool(r.FormValue("enablePathEncoding"))
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
	if req.StrmPrefix != nil {
		t.StrmPrefix = strings.TrimSpace(*req.StrmPrefix)
	}
	// nil = 请求未传，保留 existing；非 nil = 显式覆盖
	if req.EnableEnc != nil {
		t.EnablePathEncoding = *req.EnableEnc
	}

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
			if req.Enabled != nil {
				sched.Enabled = *req.Enabled
			} else {
				sched.Enabled = false
			}
			t.Schedule = sched
		} else if req.ScheduleMode == "manual" {
			t.Schedule = nil
		}
	}
	return t
}

// validateTaskTarget 校验任务目标路径：绝对路径、目录、且默认不与已有任务共用。
// 返回 (0,"") 表示通过；否则返回 HTTP 状态码与错误文案。
func validateTaskTarget(t task.Task, existing []task.Task) (int, string) {
	target := strings.TrimSpace(t.TargetPath)
	if target == "" {
		return 0, ""
	}
	if !filepath.IsAbs(target) {
		return http.StatusBadRequest, "本地目标路径必须是绝对路径（如 /app/data/strm 或 D:\\strm）"
	}
	if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() {
		return http.StatusBadRequest, "本地目标路径必须是目录，不能是文件"
	}
	cleanTarget := filepath.Clean(target)
	for i := range existing {
		other := existing[i]
		if other.ID == t.ID || strings.TrimSpace(other.TargetPath) == "" {
			continue
		}
		otherTarget := filepath.Clean(other.TargetPath)
		same := otherTarget == cleanTarget
		if !same && filepath.Separator == '\\' {
			same = strings.EqualFold(otherTarget, cleanTarget)
		}
		if same {
			return http.StatusConflict, "目标路径已被任务「" + other.ID + "」使用；默认不允许共用 TargetPath，请为任务设置独立目录"
		}
	}
	return 0, ""
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
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "更新任务需要指定任务 ID"})
				return
			}
		}

		// 必填检查（新建）
		if !isUpdate {
			if req.Account == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少账号"})
				return
			}
			origin := req.OriginPath
			if origin == "" {
				origin = req.SourcePath
			}
			if origin == "" || req.TargetPath == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少远程路径或本地目标路径"})
				return
			}
		}

		var existing *task.Task
		// 新建也要读全量任务做 ID 查重；更新时顺便用于定位 existing
		list, err := deps.TasksStore.ReadTasks()
		if err != nil {
			if isUpdate {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			// 新建时读取失败不阻塞创建，ID 回退时间戳
			list = nil
		}
		if req.ID != "" {
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
		// 新建且未指定 ID：生成可读 ID「账户-目录名」（如 aliyun-电影）
		if !isUpdate && req.ID == "" {
			origin := req.OriginPath
			if origin == "" {
				origin = req.SourcePath
			}
			req.ID = GenerateTaskID(req.Account, origin, list)
		}

		t := req.toTask(existing)
		if code, msg := validateTaskTarget(t, list); code != 0 {
			httpx.WriteJson(w, code, map[string]string{"error": msg})
			return
		}
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
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少任务 ID"})
			return
		}
		// cleanStrm=true：删除任务前先取其输出目录，只清理当前任务托管的 STRM。
		// 默认移入 .faststrm-trash；hardDelete=true 才永久删除（需前端二次确认）。
		cleanStrm := r.URL.Query().Get("cleanStrm") == "true"
		hardDelete := cleanStrm && r.URL.Query().Get("hardDelete") == "true"
		strmRemoved := 0
		strmSkipped := 0
		var cleanupWarning string
		if cleanStrm {
			tasks, rerr := deps.TasksStore.ReadTasks()
			if rerr != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取任务失败: " + rerr.Error()})
				return
			}
			var target *task.Task
			for i := range tasks {
				if tasks[i].ID == id {
					target = &tasks[i]
					break
				}
			}
			if target == nil {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
				return
			}
			if deps.Runtime != nil {
				_ = deps.Runtime.Cancel(id)
				if !deps.Runtime.Wait(id, 30*time.Second) {
					httpx.WriteJson(w, http.StatusConflict, map[string]string{
						"error": "任务仍在停止中，请稍后重试删除",
					})
					return
				}
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			removed, skipped, cerr := cleanupTaskStrms(cleanupCtx, *target, deps.ExecutorDeps.ManagedStrm, hardDelete)
			cleanupCancel()
			if cerr != nil {
				httpx.WriteJson(w, http.StatusConflict, map[string]string{"error": "清理 STRM 失败，任务未删除: " + cerr.Error()})
				return
			}
			strmRemoved, strmSkipped = removed, skipped
			if deps.ExecutorDeps.ManagedStrm == nil {
				cleanupWarning = "未启用托管索引，无法确认文件归属，本次未删除任何 STRM" //nolint:gosec // 误报：说明文案不是凭据
			} else if skipped > 0 {
				cleanupWarning = fmt.Sprintf("有 %d 个 STRM 不属于当前任务或指纹已变化，已保留", skipped)
			}
			logger.S().Infof("[DeleteTask] 清理任务 %s 目录 %s: removed=%d skipped=%d hardDelete=%v",
				id, target.TargetPath, removed, skipped, hardDelete)
		}
		// 先尝试 cancel 避免残留
		if deps.Runtime != nil {
			_ = deps.Runtime.Cancel(id)
		}
		deleted, err := deps.TasksStore.DeleteTask(id)
		if err != nil {
			logger.S().Errorf("[DeleteTask] %s: %v", id, err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "删除任务失败: " + err.Error()})
			return
		}
		if !deleted {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
			return
		}
		go deps.Scheduler.RefreshAll(deps.TasksStore)
		w.Header().Set("HX-Trigger", "tasks-changed")
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"success": true, "message": "任务已删除", "strmRemoved": strmRemoved,
			"strmSkipped": strmSkipped, "hardDelete": hardDelete, "warning": cleanupWarning,
		})
	}
}

// cleanupTaskStrms 只清理属于指定任务的托管 STRM。
// 默认移入回收站；hardDelete=true 时永久删除（调用方必须已完成二次确认）。
func cleanupTaskStrms(ctx context.Context, t task.Task, managed strm.ManagedRecorder, hardDelete bool) (removed, skipped int, err error) {
	root := strings.TrimSpace(t.TargetPath)
	if root == "" {
		return 0, 0, fmt.Errorf("任务目标目录为空")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return 0, 0, fmt.Errorf("目标目录无效: %w", err)
	}
	if filepath.Dir(absRoot) == absRoot {
		return 0, 0, fmt.Errorf("拒绝清理文件系统根目录")
	}
	if _, statErr := os.Stat(absRoot); statErr != nil {
		if os.IsNotExist(statErr) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("读取目标目录失败: %w", statErr)
	}
	if managed == nil {
		return 0, 0, nil
	}

	trashDir := strmutil.TrashSessionDir(absRoot)
	walkErr := filepath.Walk(absRoot, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == strmutil.TrashDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".strm") {
			return nil
		}
		pc, perr := strmutil.ExtractPickcode(p)
		if perr != nil || pc == "" {
			skipped++
			return nil
		}
		ok, verr := managed.VerifyFile(ctx, p, pc)
		if verr != nil || !ok {
			skipped++
			return nil
		}
		owned, oerr := managed.OwnedBy(ctx, p, t.ID)
		if oerr != nil || !owned {
			skipped++
			return nil
		}
		if hardDelete {
			if rerr := os.Remove(p); rerr != nil {
				return fmt.Errorf("删除 %s 失败: %w", p, rerr)
			}
		} else {
			if _, terr := strmutil.MoveToTrash(p, absRoot, trashDir); terr != nil {
				return fmt.Errorf("移入回收站 %s 失败: %w", p, terr)
			}
		}
		if managed != nil {
			_ = managed.MarkDeleted(ctx, p)
		}
		removed++
		return nil
	})
	if walkErr != nil {
		return removed, skipped, walkErr
	}
	if removed > 0 {
		rmEmptyDirs(absRoot)
	}
	return removed, skipped, nil
}

// ==================== 任务 ID 生成 ====================

// maxTaskIDBytes 任务 ID 字节上限。任务 ID 会拼进 Telegram callback_data
// （如 "task_exec:<id>"、"sync_run:<id>"，前缀最多 10 字节），
// callback_data 总上限 64 字节，给 ID 留 48 字节余量充足。
const maxTaskIDBytes = 48

// GenerateTaskID 生成可读任务 ID：<账户>-<目录名>（如 "aliyun-电影"）。
// 只在新建任务且前端未指定 ID 时调用；旧任务 ID 不受影响。
//   - 清洗：/ \ : 空白 等特殊字符替换为 '-'，中文与字母数字保留
//   - 按字节截断且不切断 UTF-8 多字节字符，防 callback_data 超限
//   - 与现有任务 ID 冲突时追加 -2、-3… 直到唯一
//   - 账户与路径都为空时回退时间戳 ID（防御）
func GenerateTaskID(account, originPath string, existing []task.Task) string {
	name := path.Base(strings.TrimSpace(originPath))
	raw := sanitizeTaskIDPart(account) + "-" + sanitizeTaskIDPart(name)
	raw = collapseDashes(truncateUTF8Safe(raw, maxTaskIDBytes))
	if raw == "" {
		return fmt.Sprintf("task-%d", time.Now().UnixMilli())
	}
	taken := make(map[string]bool, len(existing))
	for _, t := range existing {
		taken[t.ID] = true
	}
	if !taken[raw] {
		return raw
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("-%d", i)
		id := truncateUTF8Safe(raw, maxTaskIDBytes-len(suffix)) + suffix
		if !taken[id] {
			return id
		}
	}
}

// sanitizeTaskIDPart 清洗 ID 片段：非法字符替换为 '-'，去掉首尾 '-'
// 保留：Unicode 字母（含汉字）、数字、- _ .
func sanitizeTaskIDPart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r), r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// truncateUTF8Safe 按字节截断且不切断 UTF-8 多字节字符
func truncateUTF8Safe(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimRight(cut, "-")
}

// collapseDashes 把连续 '-' 压缩为一个并去掉首尾 '-'（含 '.'，如 path.Base("") == "."）
func collapseDashes(s string) string {
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-.")
}
