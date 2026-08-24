// Package notify 的 polling 子模块：长轮询管理器。
// 使用 tgbotapi/v5 的 GetUpdatesChan 替代手写轮询循环，
// 一次性缓冲 100 条 update，命令响应从 30s 级降到 1s 级。
package notify

import (
	"context"
	"fmt"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// UpdateHandler 处理单个 Telegram Update 的回调函数类型
type UpdateHandler func(ctx context.Context, update Update) error

// PollingManager 长轮询管理器，使用 GetUpdatesChan + goroutine 控制
type PollingManager struct {
	bot     *TelegramBot
	stopCh  chan struct{}
	mu      sync.Mutex
	running bool
	updates tgbotapi.UpdatesChannel // GetUpdatesChan 返回的 channel
}

// NewPollingManager 创建轮询管理器
func NewPollingManager(bot *TelegramBot) *PollingManager {
	return &PollingManager{
		bot:    bot,
		stopCh: make(chan struct{}),
	}
}

// Start 启动长轮询 goroutine，使用 GetUpdatesChan（timeout=60, limit=100）
// 已在运行时返回错误
func (m *PollingManager) Start(ctx context.Context, handler UpdateHandler) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("polling already running")
	}
	// 重新创建 stopCh（之前可能已 close）
	m.stopCh = make(chan struct{})
	m.running = true
	m.mu.Unlock()

	// 获取底层 BotAPI 实例
	c, err := m.bot.Underlying()
	if err != nil {
		m.markStopped()
		return fmt.Errorf("get bot api for polling: %w", err)
	}

	// 使用 GetUpdatesChan：timeout=60（长轮询），limit=100（批量拉取）
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.Limit = 100

	m.updates = c.GetUpdatesChan(u)

	logger.S().Info("[Telegram] Polling started (GetUpdatesChan: timeout=60, limit=100)")

	go m.consume(ctx, handler)
	return nil
}

// consume 从 UpdatesChannel 消费 update，分发给 handler
func (m *PollingManager) consume(ctx context.Context, handler UpdateHandler) {
	for {
		select {
		case <-ctx.Done():
			m.markStopped()
			m.drainChannel()
			return
		case <-m.stopCh:
			m.markStopped()
			m.drainChannel()
			return
		case update, ok := <-m.updates:
			if !ok {
				// channel 被关闭（可能是 Stop 调用 c.StopReceiving）
				m.markStopped()
				return
			}
			if handler != nil {
				if herr := handler(ctx, update); herr != nil {
					logger.S().Errorf("update handler failed: %v", herr)
				}
			}
		}
	}
}

// drainChannel 排空 channel（防止 goroutine 泄漏）
func (m *PollingManager) drainChannel() {
	if m.updates == nil {
		return
	}
	// tgbotapi v5 的 UpdatesChannel 只有 Clear() 方法，没有 StopReceiving
	// 清空残留的 update，防止底层 goroutine 阻塞
	m.updates.Clear()
	m.updates = nil
}

// Stop 信号通知轮询 goroutine 退出，并标记 running=false
// 多次调用安全（不会重复关闭 channel）
func (m *PollingManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	select {
	case <-m.stopCh:
		// 已关闭（防御性，正常不会走到）
	default:
		close(m.stopCh)
	}
	m.running = false
	logger.S().Info("Telegram polling stopped")
}

// IsRunning 返回当前是否在运行
func (m *PollingManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// markStopped 标记为已停止（goroutine 退出路径）
func (m *PollingManager) markStopped() {
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}
