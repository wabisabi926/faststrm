// Package monitor 生活事件监控与 STRM 同步
// dedup.go 事件去重器：防止重复事件导致 STRM 重复创建
package monitor

import (
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// EventDeduplicator 事件去重器
// 基于 fileID + eventType 作为唯一键，TTL 时间窗口内视为重复
type EventDeduplicator struct {
	mu   sync.Mutex
	seen map[string]int64 // eventKey -> timestamp
	ttl  time.Duration   // 去重窗口
}

// NewEventDeduplicator 创建去重器
// ttl: 去重窗口，默认 24 小时
func NewEventDeduplicator(ttl time.Duration) *EventDeduplicator {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	d := &EventDeduplicator{
		seen: make(map[string]int64),
		ttl:  ttl,
	}
	go d.cleanupLoop()
	logger.S().Infof("[Dedup] 事件去重器已启动, TTL=%v", ttl)
	return d
}

// IsDuplicate 检查事件是否重复
// fileID: 115 文件 ID
// eventType: 事件类型名称 (create/delete/move/rename)
// 返回 true 表示重复事件，应跳过处理
func (d *EventDeduplicator) IsDuplicate(fileID, eventType string) bool {
	if d == nil || fileID == "" {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	key := fileID + ":" + eventType
	now := time.Now().UnixMilli()

	if ts, ok := d.seen[key]; ok {
		if now-ts < int64(d.ttl/time.Millisecond) {
			return true
		}
		d.seen[key] = now
		return false
	}

	d.seen[key] = now
	return false
}

// cleanupLoop 定期清理过期记录
func (d *EventDeduplicator) cleanupLoop() {
	ticker := time.NewTicker(d.ttl)
	defer ticker.Stop()

	for range ticker.C {
		d.mu.Lock()
		now := time.Now().UnixMilli()
		expiredCount := 0
		for key, ts := range d.seen {
			if now-ts >= int64(d.ttl/time.Millisecond)*2 {
				delete(d.seen, key)
				expiredCount++
			}
		}
		if expiredCount > 0 {
			logger.S().Debugf("[Dedup] 清理过期记录: %d 条, 剩余: %d 条", expiredCount, len(d.seen))
		}
		d.mu.Unlock()
	}
}

// Stats 返回去重器统计信息
func (d *EventDeduplicator) Stats() (totalKeys int) {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
