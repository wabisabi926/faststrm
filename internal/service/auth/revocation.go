package auth

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// RevocationList 保存已登出的 JWT jti（含过期时间），支持持久化到配置文件目录。
//
// 登出只吊销当前会话，不再递增全局 TokenVersion；改密仍走全局版本使全部会话失效。
type RevocationList struct {
	mu      sync.Mutex
	path    string
	entries map[string]int64 // jti -> exp unix
}

// NewRevocationList 创建吊销表；path 为空时仅内存保存。
func NewRevocationList(path string) *RevocationList {
	r := &RevocationList{path: path, entries: make(map[string]int64)}
	if path == "" {
		return r
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.S().Warnf("[auth] 读取 JWT 吊销表失败: %v", err)
		}
		return r
	}
	if err := json.Unmarshal(data, &r.entries); err != nil {
		logger.S().Warnf("[auth] 解析 JWT 吊销表失败，按空表处理: %v", err)
		r.entries = make(map[string]int64)
	}
	r.pruneLocked(time.Now().Unix())
	return r
}

// Revoke 吊销一个 jti，过期时间到达后自动清理。
func (r *RevocationList) Revoke(jti string, expiresAt time.Time) {
	if r == nil || jti == "" {
		return
	}
	now := time.Now().Unix()
	exp := expiresAt.Unix()
	if exp <= now {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	r.entries[jti] = exp
	r.persistLocked()
}

// Revoked 判断 jti 是否已吊销。
func (r *RevocationList) Revoked(jti string) bool {
	if r == nil || jti == "" {
		return false
	}
	now := time.Now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	_, ok := r.entries[jti]
	return ok
}

func (r *RevocationList) pruneLocked(now int64) {
	for jti, exp := range r.entries {
		if exp <= now {
			delete(r.entries, jti)
		}
	}
}

func (r *RevocationList) persistLocked() {
	if r.path == "" {
		return
	}
	data, err := json.Marshal(r.entries)
	if err != nil {
		logger.S().Warnf("[auth] 序列化 JWT 吊销表失败: %v", err)
		return
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		logger.S().Warnf("[auth] 写入 JWT 吊销表失败: %v", err)
		return
	}
	if err := os.Rename(tmp, r.path); err != nil {
		logger.S().Warnf("[auth] 替换 JWT 吊销表失败: %v", err)
	}
}
