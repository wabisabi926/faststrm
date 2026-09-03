// Package store 账号持久化存储
// 对齐 frontend/src/lib/passwordCrypto.ts encryptAccounts/decryptAccounts
// 和 frontend/src/lib/serverUtils.ts readAccounts
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// 敏感字段列表（对齐 TS ACCOUNT_ENCRYPTED_FIELDS）
var accountEncryptedFields = []string{"cookie", "password"}

// 刷盘间隔
const bgFlushInterval = 30 * time.Second

// 错误定义
var (
	ErrAccountNotFound = errors.New("account not found")
)

// AccountStore 账号存储（内存优先 + 异步刷盘）
// 启动时一次性加载解密，后续所有读操作走内存（O(1)）
// 写操作修改内存 + 标记 dirty，由后台 goroutine 异步刷盘
type AccountStore struct {
	mu        sync.RWMutex
	path      string
	salt      string
	accounts  map[string]*model.AccountInfo
	dirty     bool
	stopCh    chan struct{}
	doneCh    chan struct{} // bgFlushLoop 退出后关闭，Close 用来等待
	closeOnce sync.Once
	closeErr  error
}

// NewAccountStore 创建账号存储并立即加载磁盘数据到内存
func NewAccountStore(salt, configDir string) (*AccountStore, error) {
	s := &AccountStore{
		path:     filepath.Join(configDir, "account.json"),
		salt:     salt,
		accounts: make(map[string]*model.AccountInfo),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	go s.bgFlushLoop()
	return s, nil
}

// load 从磁盘读取并解密所有账号到内存
func (s *AccountStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// 空文件或 {} → 空存储
	if len(data) == 0 || string(data) == "{}" {
		return nil
	}

	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// 解密敏感字段
	for _, acc := range raw {
		for _, field := range accountEncryptedFields {
			if val, ok := acc[field].(string); ok && pwdcrypto.IsEncrypted(val) {
				acc[field] = pwdcrypto.DecryptCredential(s.salt, val)
			}
		}
	}

	// 反序列化为 AccountInfo 并存入 map
	for _, m := range raw {
		out, err := json.Marshal(m)
		if err != nil {
			return err
		}
		var acc model.AccountInfo
		if err := json.Unmarshal(out, &acc); err != nil {
			return err
		}
		if acc.Name != "" {
			s.accounts[acc.Name] = &acc
		}
	}
	return nil
}

// bgFlushLoop 后台定时刷盘
func (s *AccountStore) bgFlushLoop() {
	ticker := time.NewTicker(bgFlushInterval)
	defer ticker.Stop()
	defer close(s.doneCh)
	for {
		select {
		case <-s.stopCh:
			s.Flush()
			return
		case <-ticker.C:
			if err := s.Flush(); err != nil {
				logger.S().Warnf("AccountStore bg flush failed: %v", err)
			}
		}
	}
}

// ==================== Cookie 校验 ====================

// ValidateCookie 校验账号 cookie 格式并更新元数据
// 返回校验结果（不修改 Cookie 本身，仅更新校验时间和状态）
func (s *AccountStore) ValidateCookie(name string) (bool, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[name]
	if !ok {
		return false, nil, ErrAccountNotFound
	}
	if acc.AccountType != "115" || acc.Cookie == "" {
		// 非 115 账号或无 cookie，直接标记为有效（格式上没问题）
		now := time.Now().UnixMilli()
		acc.LastCookieCheck = now
		valid := true
		acc.CookieValid = &valid
		s.dirty = true
		return true, nil, nil
	}
	result := client115.ValidateCookie(acc.Cookie)
	now := time.Now().UnixMilli()
	acc.LastCookieCheck = now
	valid := result.Valid
	acc.CookieValid = &valid
	s.dirty = true
	return result.Valid, result.Missing, nil
}

// ValidateAllCookies 批量校验所有 115 账号的 cookie
func (s *AccountStore) ValidateAllCookies() (validCount, invalidCount int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	for _, acc := range s.accounts {
		if acc.AccountType == "115" && acc.Cookie != "" {
			result := client115.ValidateCookie(acc.Cookie)
			acc.LastCookieCheck = now
			valid := result.Valid
			acc.CookieValid = &valid
			if result.Valid {
				validCount++
			} else {
				invalidCount++
			}
		}
	}
	s.dirty = true
	return validCount, invalidCount, nil
}

// ==================== Cookie 状态管理 ====================

// MarkCookieStatus 标记账号 cookie 有效/失效（由 monitor 错误检测调用）
func (s *AccountStore) MarkCookieStatus(name string, valid bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[name]
	if !ok {
		return ErrAccountNotFound
	}
	acc.CookieValid = &valid
	acc.LastCookieCheck = time.Now().UnixMilli()
	s.dirty = true
	return nil
}

// ==================== 热路径 API（内存操作） ====================

// Get 按名称获取单个账号（O(1)），返回只读指针
func (s *AccountStore) Get(name string) *model.AccountInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accounts[name]
}

// List 返回所有账号的指针切片（按名称排序，稳定顺序）
func (s *AccountStore) List() []*model.AccountInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*model.AccountInfo, 0, len(s.accounts))
	for _, acc := range s.accounts {
		result = append(result, acc)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Update 按名称函数式更新账号（改内存 + 标记 dirty，异步刷盘）
func (s *AccountStore) Update(name string, mutateFn func(*model.AccountInfo)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[name]
	if !ok {
		return ErrAccountNotFound
	}
	mutateFn(acc)
	s.dirty = true
	return nil
}

// Upsert 插入或更新账号（改内存 + 标记 dirty，异步刷盘）
func (s *AccountStore) Upsert(acc *model.AccountInfo) error {
	if acc == nil || acc.Name == "" {
		return errors.New("account name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[acc.Name] = acc
	s.dirty = true
	return nil
}

// Delete 按名称删除账号（改内存 + 标记 dirty，异步刷盘）
func (s *AccountStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[name]; !ok {
		return ErrAccountNotFound
	}
	delete(s.accounts, name)
	s.dirty = true
	return nil
}

// ==================== 向后兼容 API ====================

// ReadAccounts 读取账号列表（自动解密敏感字段）
// 从内存返回副本，保持与旧接口一致
func (s *AccountStore) ReadAccounts() ([]model.AccountInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.AccountInfo, 0, len(s.accounts))
	for _, acc := range s.accounts {
		cp := *acc
		result = append(result, cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// WriteAccounts 全量替换账号列表
// 加密敏感字段并同步刷盘（保证持久化）
func (s *AccountStore) WriteAccounts(accounts []model.AccountInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	newMap := make(map[string]*model.AccountInfo, len(accounts))
	for i := range accounts {
		acc := accounts[i]
		if acc.Name == "" {
			continue
		}
		accCopy := acc
		newMap[acc.Name] = &accCopy
	}
	s.accounts = newMap
	s.dirty = true
	return s.flushLocked()
}

// ==================== 持久化 ====================

// Flush 将内存中的账号数据同步刷盘
func (s *AccountStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.flushLocked()
}

// flushLocked 内部刷盘（调用方必须持有写锁）
func (s *AccountStore) flushLocked() error {
	// 按 Name 排序，保证 JSON 输出顺序稳定（避免 map 无序导致 findField 匹配错位）
	names := make([]string, 0, len(s.accounts))
	for name := range s.accounts {
		names = append(names, name)
	}
	sort.Strings(names)

	data := make([]map[string]any, 0, len(names))
	for _, name := range names {
		acc := s.accounts[name]
		m := map[string]any{}
		if acc.Name != "" {
			m["name"] = acc.Name
		}
		if acc.AccountType != "" {
			m["accountType"] = acc.AccountType
		}
		if acc.Cookie != "" {
			m["cookie"] = acc.Cookie
		}
		if acc.Account != "" {
			m["account"] = acc.Account
		}
		if acc.Password != "" {
			m["password"] = acc.Password
		}
		if acc.URL != "" {
			m["url"] = acc.URL
		}
		if acc.Token != "" {
			m["token"] = acc.Token
		}
		if acc.ExpiresAt != 0 {
			m["expiresAt"] = acc.ExpiresAt
		}

		// 加密敏感字段
		for _, field := range accountEncryptedFields {
			if val, ok := m[field].(string); ok && val != "" && !pwdcrypto.IsEncrypted(val) {
				encrypted, err := pwdcrypto.EncryptCredential(s.salt, val)
				if err != nil {
					logger.S().Warnf("encrypt account field %s failed: %v", field, err)
					continue
				}
				m[field] = encrypted
			}
		}
		data = append(data, m)
	}

	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, out, 0600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Close 优雅关闭：停止后台刷盘并同步持久化
func (s *AccountStore) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopCh)
	})
	<-s.doneCh // 等 bgFlushLoop 退出，避免 t.TempDir RemoveAll 与 Flush 写文件竞争
	return s.closeErr
}

// ==================== 辅助 ====================

// Count 返回账号数量
func (s *AccountStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.accounts)
}

// Has 检查账号是否存在
func (s *AccountStore) Has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.accounts[name]
	return ok
}
