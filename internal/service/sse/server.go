// Package sse Server-Sent Events 实时推送
// 严格对齐 TS 的帧格式：data: {JSON}\n\n  （无 event: 字段，overallPercent 必须是字符串）
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 事件类型（对齐 TS） ====================

// EventType SSE 事件类型标识（仅用于内部派发，帧中实际不带 event: 字段）
type EventType string

const (
	EventProgress EventType = "progress" // 下载进度
	EventLog      EventType = "log"      // 日志
	EventComplete EventType = "complete" // 任务完成
	EventError    EventType = "error"    // 任务出错
	EventCancel   EventType = "cancel"   // 任务取消
)

// ProgressPayload 进度事件
// ⚠️ overallPercent 必须为字符串（toFixed(2) 风格），严格对齐 TS 版本
type ProgressPayload struct {
	Event          string `json:"event"` // 固定为 "progress"
	TaskID         string `json:"taskId"`
	FilePath       string `json:"filePath,omitempty"`
	Percent        int    `json:"percent,omitempty"`        // 单文件 0-100
	OverallPercent string `json:"overallPercent,omitempty"` // 总体："0.00"–"100.00"，必须字符串
	Done           bool   `json:"done,omitempty"`
	Error          string `json:"error,omitempty"`
}

// LogPayload 日志事件
type LogPayload struct {
	Event     string `json:"event"` // 固定为 "log"
	TaskID    string `json:"taskId"`
	Level     string `json:"level,omitempty"` // info/warn/error
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// CompletePayload 任务完成事件
type CompletePayload struct {
	Event           string `json:"event"` // 固定为 "complete"
	TaskID          string `json:"taskId"`
	Status          string `json:"status"` // completed/failed/cancelled
	TotalFiles      int    `json:"totalFiles,omitempty"`
	DownloadedFiles int    `json:"downloadedFiles,omitempty"`
	DeletedFiles    int    `json:"deletedFiles,omitempty"`
	DurationMs      int64  `json:"durationMs,omitempty"`
	Error           string `json:"error,omitempty"`
}

// ==================== 帧编码 ====================

// encodeData 严格编码 SSE 帧：data: <json>\n\n
// 无 event: 前缀，避免前端订阅者多事件分支解析
func encodeData(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return "data: " + string(b) + "\n\n", nil
}

// ==================== 客户端连接 ====================

// Subscriber 单个客户端订阅者（只订阅特定 taskId，或 "*" 表示全局）
type Subscriber struct {
	ID     string
	TaskID string // "" 表示订阅全部（全局仪表盘）
	send   chan string
}

// Send 发送一帧给订阅者（非阻塞；超过缓冲就丢弃，避免背压拖垮服务器）
func (s *Subscriber) Send(frame string) {
	select {
	case s.send <- frame:
	default:
		logger.S().Warnf("[SSE] sub=%s task=%s channel full, dropping frame", s.ID, s.TaskID)
	}
}

// ==================== 服务端 ====================

// Server SSE 广播服务
type Server struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber // key = Subscriber.ID
	// 每个任务的环形日志缓冲（内存里保存最近 N 条，新客户端连接时重放）
	logMu       sync.Mutex
	taskLogs    map[string][]string // taskId -> frames（已编码）
	maxLogPer   int
}

var (
	inst     *Server
	instOnce sync.Once
)

const defaultMaxLogPerTask = 500

// GetServer 获取全局单例
func GetServer() *Server {
	instOnce.Do(func() {
		inst = &Server{
			subscribers: make(map[string]*Subscriber),
			taskLogs:    make(map[string][]string),
			maxLogPer:   defaultMaxLogPerTask,
		}
	})
	return inst
}

// Subscribe 新增订阅者
// taskID: 指定任务或空字符串订阅全部
func (s *Server) Subscribe(taskID string) *Subscriber {
	sub := &Subscriber{
		ID:     fmt.Sprintf("sub-%d-%s", time.Now().UnixNano(), randomHex(4)),
		TaskID: taskID,
		send:   make(chan string, 256),
	}
	s.mu.Lock()
	s.subscribers[sub.ID] = sub
	s.mu.Unlock()
	logger.S().Debugf("[SSE] subscribe id=%s task=%s (total=%d)", sub.ID, taskID, len(s.subscribers))

	// 重放历史日志
	s.logMu.Lock()
	if hist, ok := s.taskLogs[taskID]; ok && len(hist) > 0 {
		// 异步投递避免持锁
		go func(frames []string) {
			for _, f := range frames {
				sub.Send(f)
			}
		}(append([]string(nil), hist...))
	}
	s.logMu.Unlock()

	return sub
}

// Unsubscribe 移除订阅者
func (s *Server) Unsubscribe(sub *Subscriber) {
	if sub == nil {
		return
	}
	s.mu.Lock()
	if _, ok := s.subscribers[sub.ID]; ok {
		delete(s.subscribers, sub.ID)
		close(sub.send)
	}
	s.mu.Unlock()
	logger.S().Debugf("[SSE] unsubscribe id=%s (total=%d)", sub.ID, len(s.subscribers))
}

// Broadcast 广播事件给所有匹配的订阅者，同时写入 taskLogs 环形缓冲
func (s *Server) Broadcast(taskID string, payload any) {
	frame, err := encodeData(payload)
	if err != nil {
		logger.S().Warnf("[SSE] encode failed: %v", err)
		return
	}

	// 写日志缓冲
	if taskID != "" {
		s.logMu.Lock()
		buf := s.taskLogs[taskID]
		buf = append(buf, frame)
		if len(buf) > s.maxLogPer {
			buf = buf[len(buf)-s.maxLogPer:]
		}
		s.taskLogs[taskID] = buf
		s.logMu.Unlock()
	}

	// 广播
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sub := range s.subscribers {
		if sub.TaskID == "" || sub.TaskID == taskID || sub.TaskID == "*" {
			sub.Send(frame)
		}
	}
}

// EmitProgress 便捷：发进度事件
func (s *Server) EmitProgress(p ProgressPayload) {
	p.Event = string(EventProgress)
	s.Broadcast(p.TaskID, p)
}

// EmitLog 便捷：发日志事件
func (s *Server) EmitLog(taskID, level, message string) {
	payload := LogPayload{
		Event:     string(EventLog),
		TaskID:    taskID,
		Level:     level,
		Message:   message,
		Timestamp: time.Now().UnixMilli(),
	}
	s.Broadcast(taskID, payload)
}

// EmitComplete 便捷：发完成事件
func (s *Server) EmitComplete(c CompletePayload) {
	c.Event = string(EventComplete)
	s.Broadcast(c.TaskID, c)
}

// GetTaskLogs 读取某个任务的内存缓冲日志（历史条数返回，已编码）
func (s *Server) GetTaskLogs(taskID string) []string {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]string, len(s.taskLogs[taskID]))
	copy(out, s.taskLogs[taskID])
	return out
}

// ClearTaskLogs 清理任务日志（任务完成后可清理，留内存）
func (s *Server) ClearTaskLogs(taskID string) {
	s.logMu.Lock()
	delete(s.taskLogs, taskID)
	s.logMu.Unlock()
}

// ==================== HTTP Handler ====================

// Handler 默认 SSE HTTP handler（GET /api/events/stream?taskId=xxx）
// Content-Type: text/event-stream，禁用缓存，支持心跳
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.URL.Query().Get("taskId")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// SSE 标准头部
		h := w.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no") // nginx 禁用缓冲
		h.Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		srv := GetServer()
		sub := srv.Subscribe(taskID)
		defer srv.Unsubscribe(sub)

		// 心跳：30s 发送一次 comment line，保活中间层连接
		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()

		// 客户端断连感知
		ctx := r.Context()

		// 先写 welcome comment，让客户端尽快建立连接
		_, _ = fmt.Fprintf(w, ": connected sse stream task=%s\n\n", taskID)
		flusher.Flush()

		for {
			select {
			case <-ctx.Done():
				logger.S().Debugf("[SSE] client disconnected task=%s", taskID)
				return
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case frame, ok := <-sub.send:
				if !ok {
					return
				}
				if _, err := fmt.Fprint(w, frame); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

// ==================== 辅助 ====================

func randomHex(n int) string {
	const h = "0123456789abcdef"
	now := time.Now().UnixNano()
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = h[(int(now>>uint(i*4))+i)&0x0F]
	}
	return string(out)
}
