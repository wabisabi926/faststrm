package handler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wabisabi926/faststrm/internal/service/db"
)

const (
	cleanupScanTTL = 30 * time.Minute
	cleanupScanMax = 8
)

// cleanupScanSnapshot 一次扫描的服务端快照。
// 执行删除/补建时只认快照，客户端提交的路径只能作为“选择”，不能决定删除范围。
type cleanupScanSnapshot struct {
	ID        string
	User      string
	CreatedAt time.Time
	ExpiresAt time.Time
	Result    ScanResponse
	// Stale 规范化绝对路径 → 映射根。用于校验客户端选择的条目确实来自本次扫描。
	Stale map[string]string
}

type cleanupScanStore struct {
	mu      sync.Mutex
	entries map[string]*cleanupScanSnapshot
	max     int
	ttl     time.Duration
	now     func() time.Time
}

func newCleanupScanStore(max int, ttl time.Duration) *cleanupScanStore {
	return &cleanupScanStore{
		entries: make(map[string]*cleanupScanSnapshot),
		max:     max,
		ttl:     ttl,
		now:     time.Now,
	}
}

var cleanupScanCache = newCleanupScanStore(cleanupScanMax, cleanupScanTTL)

// SeedCleanupScanForTest 供跨包测试注入扫描快照；生产代码不要调用。
func SeedCleanupScanForTest(result ScanResponse) (string, int64) {
	return cleanupScanCache.Put("", result)
}

// Put 保存扫描快照并返回 scanId/过期时间（UnixMilli）。
func (s *cleanupScanStore) Put(user string, result ScanResponse) (string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.gcLocked(now)
	id := uuid.NewString()
	stale := make(map[string]string)
	for _, m := range result.Mappings {
		for _, st := range m.StaleStrms {
			full := db.NormalizeManagedLocalPath(filepath.Join(m.LocalPath, st.RelPath))
			stale[full] = m.LocalPath
		}
	}
	snapshot := &cleanupScanSnapshot{
		ID:        id,
		User:      user,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
		Result:    cloneScanResponse(result),
		Stale:     stale,
	}
	s.entries[id] = snapshot
	if s.max > 0 && len(s.entries) > s.max {
		// 淘汰最旧的一条（正常情况下只是用户连续扫描留下的旧快照）
		type pair struct {
			id string
			at time.Time
		}
		all := make([]pair, 0, len(s.entries))
		for k, v := range s.entries {
			if k != id {
				all = append(all, pair{id: k, at: v.CreatedAt})
			}
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		for len(s.entries) > s.max && len(all) > 0 {
			delete(s.entries, all[0].id)
			all = all[1:]
		}
	}
	return id, snapshot.ExpiresAt.UnixMilli()
}

// Get 读取快照并校验过期/用户。返回的 Result 是深拷贝，调用方可安全修改。
func (s *cleanupScanStore) Get(id, user string) (*cleanupScanSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.gcLocked(now)
	snapshot, ok := s.entries[id]
	if !ok {
		return nil, false
	}
	if snapshot.User != "" && user != "" && snapshot.User != user {
		return nil, false
	}
	out := *snapshot
	out.Result = cloneScanResponse(snapshot.Result)
	return &out, true
}

func (s *cleanupScanStore) gcLocked(now time.Time) {
	for id, snapshot := range s.entries {
		if !now.Before(snapshot.ExpiresAt) {
			delete(s.entries, id)
		}
	}
}

func (s *cleanupScanStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]*cleanupScanSnapshot)
}

func cloneScanResponse(in ScanResponse) ScanResponse {
	data, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out ScanResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return in
	}
	return out
}

// bindExecuteToScan 用服务端快照重建执行范围：
//   - delete / delete_and_regenerate：客户端条目必须存在于快照 Stale 集合，路径/根以快照为准
//   - delete_all / regenerate：完全忽略客户端 ScanSummary/Entries，使用快照
func bindExecuteToScan(body *ExecuteRequest, snapshot *cleanupScanSnapshot) error {
	if body == nil || snapshot == nil {
		return fmt.Errorf("扫描快照不存在")
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	if action == "" {
		action = "delete"
	}
	body.Action = action
	serverResult := cloneScanResponse(snapshot.Result)

	switch action {
	case "delete", "delete_and_regenerate":
		selected := make(map[string]struct{})
		for _, entry := range body.Entries {
			for _, rel := range entry.StaleRelPaths {
				full := db.NormalizeManagedLocalPath(filepath.Join(entry.LocalPath, rel))
				if _, ok := snapshot.Stale[full]; !ok {
					return fmt.Errorf("扫描结果不包含该文件，请重新扫描: %s", rel)
				}
				selected[full] = struct{}{}
			}
		}
		byRoot := make(map[string][]string)
		for _, m := range snapshot.Result.Mappings {
			for _, st := range m.StaleStrms {
				full := db.NormalizeManagedLocalPath(filepath.Join(m.LocalPath, st.RelPath))
				if _, ok := selected[full]; ok {
					byRoot[m.LocalPath] = append(byRoot[m.LocalPath], st.RelPath)
				}
			}
		}
		entries := make([]ExecuteEntry, 0, len(byRoot))
		for root, rels := range byRoot {
			if len(rels) > 0 {
				entries = append(entries, ExecuteEntry{LocalPath: root, StaleRelPaths: rels})
			}
		}
		body.Entries = entries
		body.ScanSummary = &serverResult
	case "delete_all", "regenerate":
		body.Entries = nil
		body.ScanSummary = &serverResult
	default:
		body.ScanSummary = &serverResult
	}
	return nil
}
