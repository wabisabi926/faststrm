// Package monitor 生活事件监控与 STRM 同步
// monitor.go 监控编排器（对齐 frontend/src/lib/eventMonitor.ts + eventMonitorState.ts）
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 接口定义 ====================

// Notifier 通知接口（避免直接依赖 notify 包）
// 对齐 TS telegram.sendTelegramNotification
type Notifier interface {
	// Notify 发送文本通知
	Notify(ctx context.Context, message string) error
	// NotifyWithPhoto 发送带图片的通知
	NotifyWithPhoto(ctx context.Context, caption, photoURL string) error
}

// AccountReader 账号读取接口（由 store.AccountStore 实现）
type AccountReader interface {
	ReadAccounts() ([]model.AccountInfo, error)
}

// ==================== 类型定义 ====================

// AccountMonitor 单账号监控状态
type AccountMonitor struct {
	Account      string
	Timer        *time.Timer
	Running      bool
	LastPollTime int64
	EventCount   int
	// 非导出字段
	cursor  int64          // PullEvents 游标（用于分页）
	cancel  context.CancelFunc // 停止 pollLoop
	lastErr string         // 最近一次错误
}

// AccountMonitorStatus 账号监控状态（对外暴露）
type AccountMonitorStatus struct {
	Account      string `json:"account"`
	Running      bool   `json:"running"`
	LastPollTime int64  `json:"lastPollTime"`
	EventCount   int    `json:"eventCount"`
	Error        string `json:"error,omitempty"`
}

// Monitor 生活事件监控编排器
// 对齐 TS eventMonitor + eventMonitorState
type Monitor struct {
	config          model.LifeMonitorSettings
	accounts        map[string]*AccountMonitor
	settingsFn      func() model.LifeMonitorSettings
	lifeEventRepo   *db.LifeEventRepo
	lifeEventLogRepo *db.LifeEventLogRepo
	runtime         *runtime.StateManager
	notifier        Notifier
	accountReader   AccountReader
	mu              sync.RWMutex
}

// ==================== 构造函数 ====================

// NewMonitor 创建监控器
// settingsFn: 热重载配置函数（每次调用返回最新配置）
// accountReader: 账号读取器（用于获取 cookie）
func NewMonitor(
	settingsFn func() model.LifeMonitorSettings,
	lifeEventRepo *db.LifeEventRepo,
	lifeEventLogRepo *db.LifeEventLogRepo,
	stateMgr *runtime.StateManager,
	notifier Notifier,
	accountReader AccountReader,
) *Monitor {
	config := model.LifeMonitorSettings{}
	if settingsFn != nil {
		config = settingsFn()
	}
	return &Monitor{
		config:           config,
		accounts:         make(map[string]*AccountMonitor),
		settingsFn:       settingsFn,
		lifeEventRepo:    lifeEventRepo,
		lifeEventLogRepo: lifeEventLogRepo,
		runtime:          stateMgr,
		notifier:         notifier,
		accountReader:    accountReader,
	}
}

// ==================== 生命周期管理 ====================

// Start 启动单个账号的监控轮询
func (m *Monitor) Start(ctx context.Context, account string) error {
	config := m.settingsFn()
	if !config.Enabled {
		return fmt.Errorf("请先启用监控并保存配置")
	}

	// 检查账号是否在监控列表中
	found := false
	for _, acc := range config.Accounts {
		if acc == account {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("账号 %s 不在监控列表中", account)
	}

	m.mu.Lock()
	if accMon, ok := m.accounts[account]; ok && accMon.Running {
		m.mu.Unlock()
		return fmt.Errorf("账号 %s 的监控已在运行中", account)
	}

	// 创建子 context 用于停止 goroutine
	accCtx, cancel := context.WithCancel(ctx)
	accMon := &AccountMonitor{
		Account: account,
		Running: true,
		cancel:  cancel,
	}
	m.accounts[account] = accMon
	m.mu.Unlock()

	logger.S().Infof("[Monitor] 启动监控: account=%s, 轮询间隔=%ds", account, config.PollInterval)

	go m.pollLoop(accCtx, account)

	return nil
}

// Stop 停止单个账号的监控
func (m *Monitor) Stop(account string) {
	m.mu.Lock()
	accMon, ok := m.accounts[account]
	if !ok {
		m.mu.Unlock()
		return
	}

	accMon.Running = false
	if accMon.cancel != nil {
		accMon.cancel()
		accMon.cancel = nil
	}
	if accMon.Timer != nil {
		accMon.Timer.Stop()
	}
	m.mu.Unlock()

	logger.S().Infof("[Monitor] 停止监控: account=%s", account)
}

// StartAll 启动所有配置账号的监控
func (m *Monitor) StartAll(ctx context.Context) error {
	config := m.settingsFn()
	if !config.Enabled {
		return fmt.Errorf("监控未启用")
	}

	for _, account := range config.Accounts {
		if err := m.Start(ctx, account); err != nil {
			logger.S().Warnf("[Monitor] 启动账号 %s 失败: %v", account, err)
		}
	}
	return nil
}

// StopAll 停止所有监控
func (m *Monitor) StopAll() {
	m.mu.RLock()
	var toStop []string
	for account, accMon := range m.accounts {
		if accMon.Running {
			toStop = append(toStop, account)
		}
	}
	m.mu.RUnlock()

	for _, account := range toStop {
		m.Stop(account)
	}
}

// Refresh 热重载配置，按需启停监控
func (m *Monitor) Refresh() {
	config := m.settingsFn()

	m.mu.Lock()
	m.config = config
	m.mu.Unlock()

	if !config.Enabled {
		m.StopAll()
		return
	}

	// 构建配置中的账号集合
	configuredSet := make(map[string]bool, len(config.Accounts))
	for _, acc := range config.Accounts {
		configuredSet[acc] = true
	}

	// 停止不在配置中的账号
	m.mu.RLock()
	var toStop []string
	for account, accMon := range m.accounts {
		if !configuredSet[account] && accMon.Running {
			toStop = append(toStop, account)
		}
	}
	m.mu.RUnlock()
	for _, acc := range toStop {
		m.Stop(acc)
	}

	// 启动新加入的账号
	ctx := context.Background()
	for _, account := range config.Accounts {
		m.mu.RLock()
		accMon, ok := m.accounts[account]
		running := ok && accMon.Running
		m.mu.RUnlock()
		if !running {
			if err := m.Start(ctx, account); err != nil {
				logger.S().Warnf("[Monitor] Refresh 启动账号 %s 失败: %v", account, err)
			}
		}
	}
}

// Status 返回所有配置账号的监控状态
func (m *Monitor) Status() map[string]AccountMonitorStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config := m.settingsFn()
	result := make(map[string]AccountMonitorStatus, len(config.Accounts))

	for _, account := range config.Accounts {
		if accMon, ok := m.accounts[account]; ok {
			result[account] = AccountMonitorStatus{
				Account:      account,
				Running:      accMon.Running,
				LastPollTime: accMon.LastPollTime,
				EventCount:   accMon.EventCount,
				Error:        accMon.lastErr,
			}
		} else {
			result[account] = AccountMonitorStatus{
				Account: account,
				Running: false,
			}
		}
	}

	return result
}

// VerifyAccount 验证账号的 115 API 连通性
func (m *Monitor) VerifyAccount(ctx context.Context, account string) error {
	cookie, err := m.getCookie(account)
	if err != nil {
		return err
	}

	lifeClient := client115.NewLifeClient(cookie)

	// 测试生活事件开关
	if err := lifeClient.LifeShow(ctx); err != nil {
		return fmt.Errorf("生活事件未开启或 cookie 失效: %w", err)
	}

	// 测试拉取事件
	events, _, err := lifeClient.PullEvents(ctx, account, 0)
	if err != nil {
		return fmt.Errorf("拉取事件失败: %w", err)
	}

	logger.S().Infof("[Monitor] 账号 %s 验证成功，最近事件数: %d", account, len(events))
	return nil
}

// ==================== 轮询循环 ====================

// pollLoop 单账号轮询循环
// 使用 time.Timer 而非 time.Ticker，以支持动态调整轮询间隔
func (m *Monitor) pollLoop(ctx context.Context, account string) {
	config := m.settingsFn()
	interval := time.Duration(pollIntervalSeconds(config.PollInterval)) * time.Second

	// 首次立即触发
	timer := time.NewTimer(0)

	// 存储 timer 到 AccountMonitor（允许 Stop 时提前停止）
	m.mu.Lock()
	if accMon, ok := m.accounts[account]; ok {
		accMon.Timer = timer
	} else {
		m.mu.Unlock()
		timer.Stop()
		return
	}
	m.mu.Unlock()

	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// 检查运行状态
			m.mu.RLock()
			accMon, ok := m.accounts[account]
			running := ok && accMon.Running
			m.mu.RUnlock()
			if !running {
				return
			}

			// 执行轮询
			if err := m.oncePoll(ctx, account); err != nil {
				logger.S().Errorf("[Monitor] 轮询失败 account=%s: %v", account, err)
			}

			// 重置 timer（读取最新间隔，支持热重载）
			config = m.settingsFn()
			interval = time.Duration(pollIntervalSeconds(config.PollInterval)) * time.Second
			timer.Reset(interval)
		}
	}
}

// oncePoll 单次轮询周期
// 1. 检查全量扫描挂起
// 2. 拉取事件
// 3. 逐个处理事件
// 4. 更新账号状态
// 5. 错误时记录日志
func (m *Monitor) oncePoll(ctx context.Context, account string) error {
	// 1. 检查全量扫描挂起
	if m.runtime != nil {
		ok, suspendedUntil := m.runtime.TryPollMonitor(account)
		if !ok {
			logger.S().Infof("[Monitor] account=%s 被挂起（全量扫描中），跳过本次轮询, 恢复时间: %s",
				account, time.UnixMilli(suspendedUntil).Format(time.RFC3339))
			return nil
		}
	}

	// 2. 获取账号 cookie
	cookie, err := m.getCookie(account)
	if err != nil {
		m.markAccountError(account, err.Error())
		m.appendLog(ctx, account, "poll", false, "", "", fmt.Sprintf("获取 cookie 失败: %v", err))
		return err
	}

	// 3. 拉取事件
	lifeClient := client115.NewLifeClient(cookie)

	m.mu.RLock()
	accMon, ok := m.accounts[account]
	cursor := int64(0)
	if ok {
		cursor = accMon.cursor
	}
	m.mu.RUnlock()

	events, nextCursor, err := lifeClient.PullEvents(ctx, account, cursor)
	if err != nil {
		m.markAccountError(account, err.Error())
		m.appendLog(ctx, account, "poll", false, "", "", fmt.Sprintf("拉取事件失败: %v", err))
		return err
	}

	// 4. 逐个处理事件
	processedCount := 0
	errorCount := 0
	for _, event := range events {
		if ctx.Err() != nil {
			break
		}
		if err := m.processEvent(ctx, account, event); err != nil {
			errorCount++
			logger.S().Warnf("[Monitor] 处理事件失败 account=%s type=%d file=%s: %v",
				account, event.Type, event.FileName, err)
		}
		processedCount++
	}

	// 5. 更新账号状态
	m.mu.Lock()
	if accMon, ok := m.accounts[account]; ok {
		// 仅当不是全部失败时才推进游标（允许重试）
		if processedCount == 0 || errorCount < processedCount {
			accMon.cursor = nextCursor
		}
		accMon.LastPollTime = time.Now().UnixMilli()
		accMon.EventCount += processedCount
		if errorCount > 0 {
			accMon.lastErr = fmt.Sprintf("%d/%d 事件处理失败", errorCount, processedCount)
		} else {
			accMon.lastErr = ""
		}
	}
	m.mu.Unlock()

	if len(events) > 0 {
		logger.S().Infof("[Monitor] account=%s 拉取 %d 事件, 处理 %d, 失败 %d, next_cursor=%d",
			account, len(events), processedCount, errorCount, nextCursor)
	}

	return nil
}

// ==================== 辅助方法 ====================

// getCookie 获取账号 cookie
func (m *Monitor) getCookie(account string) (string, error) {
	if m.accountReader == nil {
		return "", fmt.Errorf("accountReader 未设置")
	}
	accounts, err := m.accountReader.ReadAccounts()
	if err != nil {
		return "", fmt.Errorf("读取账号列表失败: %w", err)
	}
	for _, acc := range accounts {
		if acc.Name == account {
			if acc.Cookie == "" {
				return "", fmt.Errorf("账号 %s 无 cookie", account)
			}
			return acc.Cookie, nil
		}
	}
	return "", fmt.Errorf("账号 %s 不存在", account)
}

// markAccountError 标记账号最近错误
func (m *Monitor) markAccountError(account, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accMon, ok := m.accounts[account]; ok {
		accMon.lastErr = errMsg
	}
}

// pollIntervalSeconds 计算轮询间隔（秒），最小 5
func pollIntervalSeconds(configured int) int {
	if configured < 5 {
		return 30
	}
	return configured
}
