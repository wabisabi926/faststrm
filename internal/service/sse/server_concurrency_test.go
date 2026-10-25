package sse

import (
	"sync"
	"testing"
	"time"
)

// 并发订阅/退订回归：订阅表长度只在锁内读取，-race 下应无报告。
func TestSubscribeUnsubscribeConcurrent(t *testing.T) {
	s := &Server{
		subscribers: make(map[string]*Subscriber),
		taskLogs:    make(map[string][]string),
		taskLogAt:   make(map[string]time.Time),
		maxLogPer:   10,
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sub := s.Subscribe("")
				s.Unsubscribe(sub)
			}
		}()
	}
	wg.Wait()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if got := len(s.subscribers); got != 0 {
		t.Fatalf("subscribers = %d, want 0", got)
	}
}
