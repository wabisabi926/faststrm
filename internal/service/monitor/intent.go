// Package monitor 生活事件监控与 STRM 同步
// intent.go 文件夹递归意图互斥缓存（对齐参考项目 p115strmhelper 的 creata_pan_transfer_list）
package monitor

import (
	"sync"
	"time"
)

// intentSuppressor 意图抑制器
// 记录"该 fileID 刚由文件夹递归为它生成过 STRM"，用于在同一批事件流中抑制
// 随后到达的、对同一文件独立的 create 事件，避免同一文件 STRM 被重复创建。
//
// 与 EventDeduplicator 正交：Deduplicator 是跨轮询时间去重（fileID+type+parent，TTL 24h）；
// 本抑制器是"一次文件夹递归 → 一次文件级事件"的消费式互斥（仅按 fileID，一次性消费）。
type intentSuppressor struct {
	mu  sync.Mutex
	m   map[string]int64 // fileID -> 标记时间戳(ms)
	ttl time.Duration
}

// newIntentSuppressor 创建意图抑制器
// ttl: 抑制标记的存活时长，默认 6 小时（足够覆盖同一批事件流，又不长期占用内存）
func newIntentSuppressor(ttl time.Duration) *intentSuppressor {
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	s := &intentSuppressor{
		m:   make(map[string]int64),
		ttl: ttl,
	}
	go s.cleanupLoop()
	return s
}

// Mark 记录 fileID 已由文件夹递归处理过
func (s *intentSuppressor) Mark(fileID string) {
	if s == nil || fileID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[fileID] = time.Now().UnixMilli()
}

// Consume 消费一次 fileID 的抑制标记：命中则删除标记并返回 true（调用方跳过该事件）
// 一次性消费：命中即移除，避免长期抑制后续真实发生的新 create 事件
func (s *intentSuppressor) Consume(fileID string) bool {
	if s == nil || fileID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[fileID]; !ok {
		return false
	}
	delete(s.m, fileID)
	return true
}

// cleanupLoop 定期清理过期标记（按 2 倍 TTL 兜底清理拖尾）
func (s *intentSuppressor) cleanupLoop() {
	ticker := time.NewTicker(s.ttl)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now().UnixMilli()
		limit := now - int64(s.ttl/time.Millisecond)*2
		for key, ts := range s.m {
			if ts < limit {
				delete(s.m, key)
			}
		}
		s.mu.Unlock()
	}
}

// Stats 返回当前抑制标记数量（统计信息）
func (s *intentSuppressor) Stats() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 顺带剔除已达 TTL 的过期项，保证统计与内存都贴近实际
	now := time.Now().UnixMilli()
	limit := now - int64(s.ttl/time.Millisecond)
	for key, ts := range s.m {
		if ts < limit {
			delete(s.m, key)
		}
	}
	return len(s.m)
}
