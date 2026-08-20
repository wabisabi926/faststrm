// Package monitor 生活事件监控与 STRM 同步
// monitor.go 监控编排器（对齐 frontend/src/lib/eventMonitor.ts + eventMonitorState.ts）
package monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
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
	Get(name string) *model.AccountInfo
	MarkCookieStatus(name string, valid bool) error
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
	cursor              int64            // PullEvents 游标（用于分页）
	cancel              context.CancelFunc // 停止 pollLoop
	lastErr             string           // 最近一次错误
	consecutiveFailures int              // 连续失败次数
	cookieMarkedInvalid bool             // 是否已标记 cookie 失效
	rateLimiter         *client115.APIRateLimiter // API 冷却限流器
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
	config           model.LifeMonitorSettings
	accounts         map[string]*AccountMonitor
	settingsFn       func() model.LifeMonitorSettings
	lifeEventRepo    *db.LifeEventRepo
	lifeEventLogRepo *db.LifeEventLogRepo
	runtime          *runtime.StateManager
	notifier         Notifier
	accountReader    AccountReader
	dedup            *EventDeduplicator  // 事件去重器
	embyRefresh      *emby.MediaServerRefresh // Emby 媒体库刷库服务
	mu               sync.RWMutex
}

// ==================== 构造函数 ====================

// NewMonitor 创建监控器
// settingsFn: 热重载配置函数（每次调用返回最新配置）
// accountReader: 账号读取器（用于获取 cookie）
// embyRefresh: Emby 媒体库刷库服务（可为 nil）
func NewMonitor(
	settingsFn func() model.LifeMonitorSettings,
	lifeEventRepo *db.LifeEventRepo,
	lifeEventLogRepo *db.LifeEventLogRepo,
	stateMgr *runtime.StateManager,
	notifier Notifier,
	accountReader AccountReader,
	embyRefresh *emby.MediaServerRefresh,
) *Monitor {
	config := model.LifeMonitorSettings{}
	if settingsFn != nil {
		config = settingsFn()
	}

	// 初始化去重器
	var dedup *EventDeduplicator
	if config.EnableDedup {
		dedupWindow := time.Duration(config.DedupWindowHours) * time.Hour
		if dedupWindow <= 0 {
			dedupWindow = 24 * time.Hour
		}
		dedup = NewEventDeduplicator(dedupWindow)
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
		dedup:            dedup,
		embyRefresh:      embyRefresh,
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

	// 初始化 API 限流器
	var rateLimiter *client115.APIRateLimiter
	if config.EnableRateLimit {
		rateLimitInterval := time.Duration(config.RateLimitMs) * time.Millisecond
		if rateLimitInterval <= 0 {
			rateLimitInterval = 1 * time.Second
		}
		rateLimiter = client115.NewAPIRateLimiter(rateLimitInterval)
	}

	accMon := &AccountMonitor{
		Account:            account,
		Running:             true,
		cancel:              cancel,
		consecutiveFailures: 0,
		rateLimiter:         rateLimiter,
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

	// 更新去重器配置
	if config.EnableDedup && m.dedup == nil {
		dedupWindow := time.Duration(config.DedupWindowHours) * time.Hour
		if dedupWindow <= 0 {
			dedupWindow = 24 * time.Hour
		}
		m.dedup = NewEventDeduplicator(dedupWindow)
	} else if !config.EnableDedup {
		m.dedup = nil
	}

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
		// 标记 cookie 可能失效
		m.markCookiePotentiallyInvalid(account, err)
		return fmt.Errorf("生活事件未开启或 cookie 失效: %w", err)
	}

	// 测试拉取事件
	events, _, err := lifeClient.PullEvents(ctx, account, 0)
	if err != nil {
		m.markCookiePotentiallyInvalid(account, err)
		return fmt.Errorf("拉取事件失败: %w", err)
	}

	// 验证成功，重置连续失败计数
	m.resetConsecutiveFailures(account)

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
// 2. 拉取事件（含重试）
// 3. 逐个处理事件（含去重）
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
		m.handlePollError(account, err)
		m.appendLog(ctx, account, "poll", false, "", "", fmt.Sprintf("获取 cookie 失败: %v", err))
		return err
	}

	// 3. 拉取事件（含重试和 API 冷却）
	config := m.settingsFn()
	events, nextCursor, err := m.pullEventsWithRetry(ctx, account, cookie, config)
	if err != nil {
		m.handlePollError(account, err)
		m.appendLog(ctx, account, "poll", false, "", "", fmt.Sprintf("拉取事件失败: %v", err))
		return err
	}

	// 4. 逐个处理事件（含去重）
	processedCount := 0
	errorCount := 0
	duplicateCount := 0
	for _, event := range events {
		if ctx.Err() != nil {
			break
		}

		// 事件去重检查
		if m.dedup != nil {
			eventTypeName := client115.TypeNumberToString(event.Type)
			if m.dedup.IsDuplicate(event.FileID, eventTypeName) {
				duplicateCount++
				continue
			}
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
		accMon.consecutiveFailures = 0
		accMon.cookieMarkedInvalid = false
		if errorCount > 0 {
			accMon.lastErr = fmt.Sprintf("%d/%d 事件处理失败", errorCount, processedCount)
		} else {
			accMon.lastErr = ""
		}
	}
	m.mu.Unlock()

	if len(events) > 0 {
		logger.S().Infof("[Monitor] account=%s 拉取 %d 事件, 处理 %d, 重复 %d, 失败 %d, next_cursor=%d",
			account, len(events), processedCount, duplicateCount, errorCount, nextCursor)
	}

	return nil
}

// pullEventsWithRetry 带重试的事件拉取
func (m *Monitor) pullEventsWithRetry(
	ctx context.Context,
	account string,
	cookie string,
	config model.LifeMonitorSettings,
) ([]client115.LifeEventItem, int64, error) {
	lifeClient := client115.NewLifeClient(cookie)

	m.mu.RLock()
	accMon, ok := m.accounts[account]
	cursor := int64(0)
	if ok {
		cursor = accMon.cursor
	}
	m.mu.RUnlock()

	maxRetries := config.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	retryDelayMs := config.RetryDelayMs
	if retryDelayMs <= 0 {
		retryDelayMs = 1000
	}

	var lastErr error
	for attempt := maxRetries; attempt >= 0; attempt-- {
		// API 冷却等待
		m.mu.RLock()
		if accMon, ok := m.accounts[account]; ok && accMon.rateLimiter != nil {
			accMon.rateLimiter.Wait()
		}
		m.mu.RUnlock()

		events, nextCursor, err := lifeClient.PullEvents(ctx, account, cursor)
		if err == nil {
			return events, nextCursor, nil
		}

		lastErr = err
		if attempt == 0 {
			break
		}

		// 指数退避
		delay := time.Duration(maxRetries-attempt+1) * time.Duration(retryDelayMs) * time.Millisecond
		logger.S().Warnf("[Monitor] 拉取事件失败 account=%s, 剩余重试=%d, 等待=%v: %v",
			account, attempt, delay, err)

		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, 0, fmt.Errorf("拉取事件失败（重试%d次）: %w", maxRetries, lastErr)
}

// ==================== Cookie 有效性自动检测 ====================

// pollErrorPatterns 可能表示 cookie 失效的错误关键词
var pollErrorPatterns = []string{
	"未登录",
	"cookie",
	"登录过期",
	"401",
	"403",
	"unauthorized",
	"invalid",
	"expired",
	"auth",
	"login expired",
}

// isAuthError 判断错误是否为认证/授权错误
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	for _, pattern := range pollErrorPatterns {
		if strings.Contains(errStr, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// handlePollError 处理轮询错误，检测 cookie 失效
func (m *Monitor) handlePollError(account string, err error) {
	isAuth := isAuthError(err)

	m.mu.Lock()
	accMon, ok := m.accounts[account]
	if ok {
		accMon.lastErr = err.Error()
		if isAuth {
			accMon.consecutiveFailures++
			// 连续 3 次认证错误则标记 cookie 失效
			if accMon.consecutiveFailures >= 3 && !accMon.cookieMarkedInvalid {
				accMon.cookieMarkedInvalid = true
				m.mu.Unlock()
				m.markCookiePotentiallyInvalid(account, err)
				return
			}
		} else {
			accMon.consecutiveFailures = 0
		}
	}
	m.mu.Unlock()
}

// markCookiePotentiallyInvalid 标记账号 cookie 可能失效
func (m *Monitor) markCookiePotentiallyInvalid(account string, err error) {
	if m.accountReader == nil {
		return
	}
	if err := m.accountReader.MarkCookieStatus(account, false); err != nil {
		logger.S().Warnf("[Monitor] 标记 cookie 失效失败 account=%s: %v", account, err)
	}
	logger.S().Warnf("[Monitor] 账号 %s cookie 可能已失效: %v", account, err)
}

// resetConsecutiveFailures 重置连续失败计数
func (m *Monitor) resetConsecutiveFailures(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accMon, ok := m.accounts[account]; ok {
		accMon.consecutiveFailures = 0
		accMon.cookieMarkedInvalid = false
	}
}

// ==================== 辅助方法 ====================

// getCookie 获取账号 cookie
func (m *Monitor) getCookie(account string) (string, error) {
	if m.accountReader == nil {
		return "", fmt.Errorf("accountReader 未设置")
	}
	acc := m.accountReader.Get(account)
	if acc == nil {
		return "", fmt.Errorf("账号 %s 不存在", account)
	}
	if acc.Cookie == "" {
		return "", fmt.Errorf("账号 %s 无 cookie", account)
	}
	return acc.Cookie, nil
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

// GetDedupStats 获取去重器统计信息
func (m *Monitor) GetDedupStats() int {
	if m.dedup == nil {
		return 0
	}
	return m.dedup.Stats()
}
