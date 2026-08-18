// Package notify 的 polling 子模块：长轮询管理器。
// 用 goroutine + context 替代 frontend/src/lib/telegramPolling.ts 中的 globalThis + setInterval 模式。
package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// UpdateHandler 处理单个 Telegram Update 的回调函数类型
type UpdateHandler func(ctx context.Context, update Update) error

// PollingManager 长轮询管理器，使用 goroutine + context 控制
type PollingManager struct {
	bot     *TelegramBot
	stopCh  chan struct{}
	mu      sync.Mutex
	running bool
}

// NewPollingManager 创建轮询管理器
func NewPollingManager(bot *TelegramBot) *PollingManager {
	return &PollingManager{
		bot:    bot,
		stopCh: make(chan struct{}),
	}
}

// Start 启动长轮询 goroutine，每个 update 分发给 handler
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

	logger.S().Info("[Telegram] Polling started")
	go m.loop(ctx, handler)
	return nil
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

// loop 长轮询主循环：offset 起始为 0，每次 GetUpdates(offset, 1, 30)，
// 取到 update 后将 offset 置为 lastUpdateID+1；出错睡眠 5s 后重试
func (m *PollingManager) loop(ctx context.Context, handler UpdateHandler) {
	var offset int64
	for {
		// 退出信号检查
		select {
		case <-ctx.Done():
			m.markStopped()
			return
		case <-m.stopCh:
			m.markStopped()
			return
		default:
		}

		updates, err := m.bot.GetUpdates(ctx, offset, 1, 30)
		if err != nil {
			logger.S().Warnf("Polling error: %v", err)
			// 睡眠 5s 后重试，期间响应 ctx/stop
			select {
			case <-ctx.Done():
				m.markStopped()
				return
			case <-m.stopCh:
				m.markStopped()
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			if handler != nil {
				if herr := handler(ctx, u); herr != nil {
					logger.S().Errorf("update handler failed: %v", herr)
				}
			}
		}
	}
}

// markStopped 标记为已停止（goroutine 退出路径）
func (m *PollingManager) markStopped() {
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}
