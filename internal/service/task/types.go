// Package task 任务执行引擎
package task

import (
	"context"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
)

// ==================== 依赖抽象接口（避免循环 import store） ====================

// AccountReader 读取账号列表（由 store.AccountStore 实现）
type AccountReader interface {
	ReadAccounts() ([]model.AccountInfo, error)
}

// TasksReaderWriter 任务读写（由 store.TasksStore 实现）
type TasksReaderWriter interface {
	ReadTasks() ([]Task, error)
	SaveTasks(tasks []Task) error
}

// Status 任务状态
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// TaskSchedule 定时调度配置（对齐 TS TaskSchedule）
type TaskSchedule struct {
	Enabled        bool     `json:"enabled"`
	Mode           string   `json:"mode,omitempty"`            // "interval" | "daily" | "weekly"
	IntervalMinutes int     `json:"intervalMinutes,omitempty"` // interval 模式：分钟
	Time           string   `json:"time,omitempty"`            // daily/weekly 模式："HH:MM"
	Weekdays       []int    `json:"weekdays,omitempty"`        // weekly：0-6 (周日=0)
	LastRunAt      int64    `json:"lastRunAt,omitempty"`       // 上次运行 unix ms
}

// Task 任务定义（持久化在 .settings.json tasks 中）
type Task struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name,omitempty"`
	Account             string        `json:"account"`
	AccountType         string        `json:"accountType,omitempty"`
	OriginPath          string        `json:"originPath"`
	TargetPath          string        `json:"targetPath"`
	StrmPrefix          string        `json:"strmPrefix,omitempty"`
	EnablePathEncoding  bool          `json:"enablePathEncoding,omitempty"`
	Enable302           bool          `json:"enable302,omitempty"`
	RemoveExtraFiles    bool          `json:"removeExtraFiles,omitempty"`
	Schedule            *TaskSchedule `json:"schedule,omitempty"`
	CreatedAt           int64         `json:"createdAt,omitempty"`
	UpdatedAt           int64         `json:"updatedAt,omitempty"`
}

// RuntimeState 运行时状态（内存态，不持久化）
type RuntimeState struct {
	Status    Status `json:"status"`
	StartedAt int64  `json:"startedAt,omitempty"`
	EndedAt   int64  `json:"endedAt,omitempty"`
	Error     string `json:"error,omitempty"`

	TotalFiles      int `json:"totalFiles,omitempty"`
	DownloadedFiles int `json:"downloadedFiles,omitempty"`
	DeletedFiles    int `json:"deletedFiles,omitempty"`
}

// ExecuteResult 启动任务同步返回结果（对齐 TS executeTask 返回）
type ExecuteResult struct {
	Success bool   `json:"success"`
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	TaskID  string `json:"taskId,omitempty"`
}

// CancelResult 取消任务返回
type CancelResult struct {
	Success bool   `json:"success"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ==================== 运行时注册表 ====================

// Runtime 全局运行时：跟踪每个 taskId 的运行态 + 取消函数
type Runtime struct {
	mu          sync.Mutex
	tasks       map[string]*runningTask
	accountLock map[string]string // accountName -> running taskId（每账号单并发）
}

type runningTask struct {
	task    *Task
	state   *RuntimeState
	cancel  context.CancelFunc
	ctx     context.Context
	logLock sync.Mutex
	logs    []string
}

var (
	rtInstance *Runtime
	rtOnce     sync.Once
)

// GetRuntime 获取全局运行时单例
func GetRuntime() *Runtime {
	rtOnce.Do(func() {
		rtInstance = &Runtime{
			tasks:       make(map[string]*runningTask),
			accountLock: make(map[string]string),
		}
	})
	return rtInstance
}

// TryEnter 尝试获得账号级独占锁
func (r *Runtime) TryEnter(accountName, taskID string) (ok bool, conflictTaskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if exist, active := r.accountLock[accountName]; active {
		return false, exist
	}
	r.accountLock[accountName] = taskID
	return true, ""
}

// Exit 释放账号级独占锁
func (r *Runtime) Exit(accountName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.accountLock, accountName)
}

// Register 注册任务为运行态
func (r *Runtime) Register(task *Task) (context.Context, context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	rt := &runningTask{
		task:   task,
		state:  &RuntimeState{Status: StatusRunning, StartedAt: time.Now().UnixMilli()},
		cancel: cancel,
		ctx:    ctx,
	}
	r.tasks[task.ID] = rt
	return ctx, cancel
}

// Unregister 移除运行态
func (r *Runtime) Unregister(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt, ok := r.tasks[taskID]; ok {
		rt.cancel()
		delete(r.tasks, taskID)
	}
}

// IsRunning 是否正在运行
func (r *Runtime) IsRunning(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.tasks[taskID]
	return ok && (rt.state.Status == StatusRunning || rt.state.Status == StatusPending)
}

// GetState 获取运行时状态（快照）
func (r *Runtime) GetState(taskID string) *RuntimeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.tasks[taskID]
	if !ok {
		return nil
	}
	cp := *rt.state
	return &cp
}

// SetState 设置状态
func (r *Runtime) SetState(taskID string, fn func(s *RuntimeState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt, ok := r.tasks[taskID]; ok {
		fn(rt.state)
	}
}

// Cancel 取消任务（同步）
func (r *Runtime) Cancel(taskID string) (found bool) {
	r.mu.Lock()
	rt, ok := r.tasks[taskID]
	if ok {
		rt.cancel()
		rt.state.Status = StatusCancelled
		rt.state.EndedAt = time.Now().UnixMilli()
	}
	r.mu.Unlock()
	return ok
}

// RunningTasks 当前全部运行中的任务
func (r *Runtime) RunningTasks() map[string]*RuntimeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*RuntimeState, len(r.tasks))
	for id, rt := range r.tasks {
		cp := *rt.state
		out[id] = &cp
	}
	return out
}

// AppendLog 追加一条内存日志（供 /api/taskLog/:taskId 读取）
func (r *Runtime) AppendLog(taskID, line string) {
	r.mu.Lock()
	rt, ok := r.tasks[taskID]
	r.mu.Unlock()
	if !ok {
		return
	}
	rt.logLock.Lock()
	defer rt.logLock.Unlock()
	rt.logs = append(rt.logs, line)
	if len(rt.logs) > 20000 {
		rt.logs = rt.logs[len(rt.logs)-20000:]
	}
}

// GetLogs 读取日志
func (r *Runtime) GetLogs(taskID string) []string {
	r.mu.Lock()
	rt, ok := r.tasks[taskID]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	rt.logLock.Lock()
	defer rt.logLock.Unlock()
	out := make([]string, len(rt.logs))
	copy(out, rt.logs)
	return out
}

// ==================== 任务配置持久化接口 ====================

// SettingsStore 读取/保存任务定义（由 store.SettingsAdapter 实现）
type SettingsStore interface {
	ReadSettings() (*model.Settings, error)
	SaveSettings(s *model.Settings) error
}
