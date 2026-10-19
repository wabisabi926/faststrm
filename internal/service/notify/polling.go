// Package notify 的 polling 子模块：长轮询管理器。
//
// 自持 getUpdates 循环（不再用 tgbotapi 的 GetUpdatesChan）：
//   - 能拿到错误信息，识别 409 冲突（同一 Token 被多个实例轮询）
//   - 失败指数退避，避免网络抖动时刷屏
//   - 停止时不再需要 StopReceivingUpdates（该调用会永久关闭底层 shutdownChannel，
//     导致同一 BotAPI 无法重新轮询）
package notify

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// UpdateHandler 处理单个 Telegram Update 的回调函数类型
type UpdateHandler func(ctx context.Context, update Update) error

// PollingManager 长轮询管理器
type PollingManager struct {
	bot      *TelegramBot
	stopCh   chan struct{}
	mu       sync.Mutex
	running  bool
	conflict atomic.Bool // 是否检测到 409（多实例抢同一 Token）
}

// NewPollingManager 创建轮询管理器
func NewPollingManager(bot *TelegramBot) *PollingManager {
	return &PollingManager{
		bot:    bot,
		stopCh: make(chan struct{}),
	}
}

// Start 启动长轮询 goroutine
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

	c, err := m.bot.Underlying()
	if err != nil {
		m.markStopped()
		return fmt.Errorf("get bot api for polling: %w", err)
	}

	m.conflict.Store(false)
	logger.S().Info("[Telegram] Polling started (getUpdates: timeout=30, limit=100)")
	go m.consume(ctx, handler, c)
	return nil
}

// consume 长轮询循环
func (m *PollingManager) consume(ctx context.Context, handler UpdateHandler, c *tgbotapi.BotAPI) {
	offset := 0
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			m.markStopped()
			return
		case <-m.stopCh:
			m.markStopped()
			return
		default:
		}

		cfg := tgbotapi.NewUpdate(offset)
		cfg.Timeout = 30
		cfg.Limit = 100
		updates, err := c.GetUpdates(cfg)
		if err != nil {
			// 409：同一 Bot Token 被另一个实例（或 webhook）占用
			if isPollingConflict(err) {
				if m.conflict.CompareAndSwap(false, true) {
					logger.S().Errorf("[Telegram] 检测到 409 冲突：同一 Bot Token 可能被另一个实例轮询，"+
						"或 Webhook 与轮询同时启用。请停掉重复实例 / 关闭其中一个接收方式。(%v)", err)
				}
			} else {
				logger.S().Warnf("[Telegram] getUpdates 失败: %v（%v 后重试）", err, backoff)
			}
			select {
			case <-ctx.Done():
				m.markStopped()
				return
			case <-m.stopCh:
				m.markStopped()
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		m.conflict.Store(false)
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if handler == nil {
				continue
			}
			if herr := handler(ctx, u); herr != nil {
				logger.S().Errorf("update handler failed: %v", herr)
			}
		}
	}
}

// isPollingConflict 判断是否为 409 冲突（多实例抢同一 Token / 与 Webhook 冲突）
func isPollingConflict(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "409") ||
		strings.Contains(s, "conflict") ||
		strings.Contains(s, "terminated by other getupdates")
}

// ConflictDetected 是否检测到 409 冲突（供状态页展示）
func (m *PollingManager) ConflictDetected() bool { return m.conflict.Load() }

// Stop 通知轮询 goroutine 退出
func (m *PollingManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	select {
	case <-m.stopCh:
		// 已关闭（防御性）
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
