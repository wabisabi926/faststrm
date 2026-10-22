// Package journal 记录当前进程内的运行事件（启动、反代、设置、账号异常）。
//
// 这是“当前运行日记”，不是任务日志：任务执行明细仍看 taskHistory，
// 这里只保留状态变化和异常，避免高频播放请求把内存打满。
package journal

import (
	"sync"
	"time"
)

// Level 事件级别。
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event 一条运行事件。
type Event struct {
	ID       uint64         `json:"id"`
	Time     time.Time      `json:"time"`
	Level    Level          `json:"level"`
	Category string         `json:"category"`
	Message  string         `json:"message"`
	Detail   map[string]any `json:"detail,omitempty"`
}

// Journal 有界环形事件日志，线程安全。
type Journal struct {
	mu     sync.RWMutex
	events []Event
	max    int
	nextID uint64
}

// New 创建事件日志；max<=0 时使用 500。
func New(max int) *Journal {
	if max <= 0 {
		max = 500
	}
	return &Journal{max: max, events: make([]Event, 0, max)}
}

// Add 追加一条事件，超出上限时丢弃最旧的。
func (j *Journal) Add(level Level, category, message string, detail map[string]any) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.nextID++
	j.events = append(j.events, Event{
		ID:       j.nextID,
		Time:     time.Now(),
		Level:    level,
		Category: category,
		Message:  message,
		Detail:   detail,
	})
	if len(j.events) > j.max {
		drop := len(j.events) - j.max
		copy(j.events, j.events[drop:])
		j.events = j.events[:j.max]
	}
}

// List 返回最近的事件（新的在前），limit<=0 表示全部。
func (j *Journal) List(limit int) []Event {
	if j == nil {
		return nil
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	if limit <= 0 || limit > len(j.events) {
		limit = len(j.events)
	}
	out := make([]Event, 0, limit)
	for i := len(j.events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, j.events[i])
	}
	return out
}

// Len 当前事件数量。
func (j *Journal) Len() int {
	if j == nil {
		return 0
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.events)
}

var (
	defaultJournal = New(500)
	processStarted = time.Now()
)

// Default 返回全局事件日志。
func Default() *Journal { return defaultJournal }

// StartedAt 进程启动时间（包初始化时间，近似）。
func StartedAt() time.Time { return processStarted }

// Uptime 当前进程运行时长。
func Uptime() time.Duration { return time.Since(processStarted) }

// Info 记录信息事件。
func Info(category, message string, detail map[string]any) {
	defaultJournal.Add(LevelInfo, category, message, detail)
}

// Warn 记录警告事件。
func Warn(category, message string, detail map[string]any) {
	defaultJournal.Add(LevelWarn, category, message, detail)
}

// Error 记录错误事件。
func Error(category, message string, detail map[string]any) {
	defaultJournal.Add(LevelError, category, message, detail)
}
