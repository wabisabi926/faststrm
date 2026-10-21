package sse

import (
	"fmt"
	"testing"
	"time"
)

func TestTaskLogBuffersAreBounded(t *testing.T) {
	s := &Server{
		subscribers: make(map[string]*Subscriber),
		taskLogs:    make(map[string][]string),
		taskLogAt:   make(map[string]time.Time),
		maxLogPer:   10,
	}
	for i := 0; i < maxTaskLogBuffers+5; i++ {
		s.EmitLog(fmt.Sprintf("task-%d", i), "info", "log")
	}
	s.logMu.Lock()
	n := len(s.taskLogs)
	s.logMu.Unlock()
	if n > maxTaskLogBuffers {
		t.Fatalf("task log buffers=%d, want <= %d", n, maxTaskLogBuffers)
	}
}
