package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// StrmCacheEntry 单个缓存条目
type StrmCacheEntry struct {
	UUID       string   `json:"uuid"`
	TaskID     string   `json:"taskId"`
	Target     string   `json:"target"`
	Account    string   `json:"account"`
	RelPaths   []string `json:"relPaths"`
	LocalPaths []string `json:"localPaths"`
	CreatedAt  int64    `json:"createdAt"`
}

type strmCacheFile struct {
	Entries map[string]*StrmCacheEntry `json:"entries"`
}

// StrmCacheStore STRM 生成缓存
type StrmCacheStore struct {
	mu   sync.RWMutex
	path string
	data strmCacheFile
}

// NewStrmCacheStore 基于配置目录创建
func NewStrmCacheStore(configDir string) *StrmCacheStore {
	s := &StrmCacheStore{
		path: filepath.Join(configDir, "strm_cache.json"),
		data: strmCacheFile{Entries: map[string]*StrmCacheEntry{}},
	}
	s.load()
	return s
}

func (s *StrmCacheStore) load() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.S().Warnf("[StrmCache] load %s failed: %v", s.path, err)
		}
		return
	}
	if len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		logger.S().Warnf("[StrmCache] unmarshal failed: %v", err)
		return
	}
	if s.data.Entries == nil {
		s.data.Entries = map[string]*StrmCacheEntry{}
	}
}

func (s *StrmCacheStore) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Save 保存新缓存条目
func (s *StrmCacheStore) Save(entry *StrmCacheEntry) error {
	if entry == nil || entry.UUID == "" {
		return nil
	}
	entry.CreatedAt = time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Entries[entry.UUID] = entry
	return s.saveLocked()
}

// Get 按 UUID 读取
func (s *StrmCacheStore) Get(uuid string) *StrmCacheEntry {
	if uuid == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.data.Entries[uuid]
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}

// LatestByTaskID 返回某任务最新的缓存条目
func (s *StrmCacheStore) LatestByTaskID(taskID string) *StrmCacheEntry {
	if taskID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *StrmCacheEntry
	var latestAt int64
	for _, e := range s.data.Entries {
		if e == nil || e.TaskID != taskID {
			continue
		}
		if latest == nil || e.CreatedAt > latestAt {
			latest = e
			latestAt = e.CreatedAt
		}
	}
	if latest == nil {
		return nil
	}
	cp := *latest
	return &cp
}

// Delete 按 UUID 删除
func (s *StrmCacheStore) Delete(uuid string) error {
	if uuid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Entries[uuid]; !ok {
		return nil
	}
	delete(s.data.Entries, uuid)
	return s.saveLocked()
}

// CleanupExpired 清理过期条目（超过 maxAgeMs 毫秒）
func (s *StrmCacheStore) CleanupExpired(maxAgeMs int64) (int, error) {
	if maxAgeMs <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UnixMilli() - maxAgeMs
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed []string
	for k, v := range s.data.Entries {
		if v == nil || v.CreatedAt < cutoff {
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	for _, k := range removed {
		delete(s.data.Entries, k)
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return len(removed), nil
}

// ListTaskRecent 返回某任务最近 limit 条缓存
func (s *StrmCacheStore) ListTaskRecent(taskID string, limit int) []*StrmCacheEntry {
	if taskID == "" || limit <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*StrmCacheEntry
	for _, e := range s.data.Entries {
		if e != nil && e.TaskID == taskID {
			cp := *e
			all = append(all, &cp)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt > all[j].CreatedAt })
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// FullPathSet 基于缓存条目构建绝对路径集合
func FullPathSet(entry *StrmCacheEntry) map[string]struct{} {
	out := make(map[string]struct{}, len(entry.LocalPaths))
	for _, p := range entry.LocalPaths {
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}
