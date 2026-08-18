// Package runtime 账号运行时状态管理
// 对齐 frontend/src/lib/accountRuntimeState.ts
package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// AccountRuntimeState 账号运行时状态
type AccountRuntimeState struct {
	ActiveTaskId          string `json:"activeTaskId,omitempty"`
	ActiveTaskStartAt     int64  `json:"activeTaskStartAt,omitempty"`
	MonitorSuspendedUntil int64  `json:"monitorSuspendedUntil,omitempty"`
	MonitorSuspendedBy    string `json:"monitorSuspendedBy,omitempty"`
}

// EnterFullScanResult 尝试进入全量扫描的结果
type EnterFullScanResult struct {
	Ok     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

const (
	fullScanTimeoutMs     = 10 * 60 * 1000 // 10 分钟
	monitorResumeGraceMs  = 5 * 60 * 1000  // 5 分钟
)

// StateManager 全局状态管理器（单例）
type StateManager struct {
	mu       sync.RWMutex
	states   map[string]*AccountRuntimeState
	filePath string
}

var (
	instance *StateManager
	once     sync.Once
)

// Init 初始化状态管理器（读取持久化文件）
func Init(configDir string) *StateManager {
	once.Do(func() {
		instance = &StateManager{
			states:   make(map[string]*AccountRuntimeState),
			filePath: filepath.Join(configDir, "runtime.json"),
		}
		instance.loadFromDisk()
	})
	return instance
}

// Get 获取全局实例（需先 Init）
func Get() *StateManager {
	if instance == nil {
		panic("runtime state not initialized, call Init() first")
	}
	return instance
}

// ==================== 全量扫描锁 ====================

// TryEnterFullScan 尝试获取全量扫描锁
// 对齐 TS tryEnterFullScan
func (m *StateManager) TryEnterFullScan(account, taskId string) EnterFullScanResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.states[account]
	if existing != nil && existing.ActiveTaskId != "" {
		elapsed := time.Now().UnixMilli() - existing.ActiveTaskStartAt
		if elapsed < fullScanTimeoutMs {
			return EnterFullScanResult{Ok: false, Reason: "task_running"}
		}
	}

	state := &AccountRuntimeState{
		ActiveTaskId:      taskId,
		ActiveTaskStartAt: time.Now().UnixMilli(),
	}

	// 保留现有的监控挂起状态
	if existing != nil && existing.MonitorSuspendedUntil > 0 && time.Now().UnixMilli() < existing.MonitorSuspendedUntil {
		state.MonitorSuspendedUntil = existing.MonitorSuspendedUntil
		state.MonitorSuspendedBy = existing.MonitorSuspendedBy
	}

	m.states[account] = state
	m.saveToDisk()

	logger.S().Infof("[AccountRuntime] fullscan enter: account=%s taskId=%s", account, taskId)
	return EnterFullScanResult{Ok: true}
}

// ExitFullScan 释放全量扫描锁
// 对齐 TS exitFullScan
func (m *StateManager) ExitFullScan(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.states[account]
	if current == nil || current.ActiveTaskId == "" {
		return
	}

	state := &AccountRuntimeState{}

	// 如果监控被挂起，设置恢复宽限期
	if current.MonitorSuspendedUntil > 0 && time.Now().UnixMilli() < current.MonitorSuspendedUntil {
		resumeGrace := time.Now().UnixMilli() + monitorResumeGraceMs
		state.MonitorSuspendedUntil = min(current.MonitorSuspendedUntil, resumeGrace)
		state.MonitorSuspendedBy = current.MonitorSuspendedBy
	}

	if state.ActiveTaskId == "" && state.MonitorSuspendedUntil == 0 {
		delete(m.states, account)
	} else {
		m.states[account] = state
	}
	m.saveToDisk()

	logger.S().Infof("[AccountRuntime] fullscan exit: account=%s", account)
}

// TouchFullScanHeartbeat 更新全量扫描心跳（防超时）
// 对齐 TS touchFullScanHeartbeat
func (m *StateManager) TouchFullScanHeartbeat(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.states[account]
	if state != nil && state.ActiveTaskId != "" {
		state.ActiveTaskStartAt = time.Now().UnixMilli()
		m.saveToDisk()
	}
}

// IsAccountInFullScan 账号是否正在全量扫描
func (m *StateManager) IsAccountInFullScan(account string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s := m.states[account]
	if s == nil || s.ActiveTaskId == "" {
		return false
	}
	return time.Now().UnixMilli()-s.ActiveTaskStartAt < fullScanTimeoutMs
}

// ==================== 监控挂起/恢复 ====================

// SuspendMonitorForFullScan 挂起监控（全量扫描期间）
// 对齐 TS suspendMonitorForFullScan
func (m *StateManager) SuspendMonitorForFullScan(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.states[account]
	if state == nil {
		state = &AccountRuntimeState{}
	}
	state.MonitorSuspendedUntil = time.Now().UnixMilli() + fullScanTimeoutMs
	state.MonitorSuspendedBy = "fullscan"
	m.states[account] = state
	m.saveToDisk()

	logger.S().Infof("[AccountRuntime] monitor suspended: account=%s", account)
}

// TryPollMonitor 尝试轮询监控（被挂起则返回 false）
// 对齐 TS tryPollMonitor
func (m *StateManager) TryPollMonitor(account string) (ok bool, suspendedUntil int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state := m.states[account]
	if state != nil && state.MonitorSuspendedUntil > 0 && time.Now().UnixMilli() < state.MonitorSuspendedUntil {
		return false, state.MonitorSuspendedUntil
	}
	return true, 0
}

// IsMonitorSuspended 监控是否被挂起
func (m *StateManager) IsMonitorSuspended(account string) bool {
	ok, _ := m.TryPollMonitor(account)
	return !ok
}

// ClearMonitorSuspend 清除监控挂起
func (m *StateManager) ClearMonitorSuspend(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.states[account]
	if current == nil || current.MonitorSuspendedUntil == 0 {
		return
	}
	current.MonitorSuspendedUntil = 0
	current.MonitorSuspendedBy = ""

	if current.ActiveTaskId == "" {
		delete(m.states, account)
	} else {
		m.states[account] = current
	}
	m.saveToDisk()
}

// ==================== 查询 ====================

// GetState 获取账号运行时状态
func (m *StateManager) GetState(account string) AccountRuntimeState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if s, ok := m.states[account]; ok {
		return *s
	}
	return AccountRuntimeState{}
}

// GetAllStates 获取所有账号运行时状态
func (m *StateManager) GetAllStates() map[string]AccountRuntimeState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]AccountRuntimeState, len(m.states))
	for k, v := range m.states {
		result[k] = *v
	}
	return result
}

// ==================== 持久化 ====================

func (m *StateManager) loadFromDisk() {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.S().Warnf("[AccountRuntime] read runtime.json failed: %v", err)
		}
		return
	}

	var persisted map[string]AccountRuntimeState
	if err := json.Unmarshal(data, &persisted); err != nil {
		logger.S().Warnf("[AccountRuntime] parse runtime.json failed: %v", err)
		return
	}

	now := time.Now().UnixMilli()
	needUpdate := false

	for account, state := range persisted {
		cleaned := &AccountRuntimeState{}

		// 清理过期的全量扫描锁
		if state.ActiveTaskId != "" && state.ActiveTaskStartAt > 0 {
			if now-state.ActiveTaskStartAt < fullScanTimeoutMs {
				cleaned.ActiveTaskId = state.ActiveTaskId
				cleaned.ActiveTaskStartAt = state.ActiveTaskStartAt
			} else {
				logger.S().Infof("[AccountRuntime] cleanup stale fullscan lock for %s", account)
				needUpdate = true
			}
		}

		// 保留未过期的监控挂起
		if state.MonitorSuspendedUntil > 0 && now < state.MonitorSuspendedUntil {
			cleaned.MonitorSuspendedUntil = state.MonitorSuspendedUntil
			cleaned.MonitorSuspendedBy = state.MonitorSuspendedBy
		}

		if cleaned.ActiveTaskId != "" || cleaned.MonitorSuspendedUntil > 0 {
			m.states[account] = cleaned
		}
	}

	if needUpdate || len(persisted) != len(m.states) {
		m.saveToDisk()
	}

	logger.S().Infof("[AccountRuntime] initialized, %d account(s) with runtime state", len(m.states))
}

func (m *StateManager) saveToDisk() {
	obj := make(map[string]AccountRuntimeState, len(m.states))
	for k, v := range m.states {
		obj[k] = *v
	}

	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		logger.S().Errorf("[AccountRuntime] marshal failed: %v", err)
		return
	}

	dir := filepath.Dir(m.filePath)
	_ = os.MkdirAll(dir, 0755)

	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		logger.S().Errorf("[AccountRuntime] write failed: %v", err)
	}
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
