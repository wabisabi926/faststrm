package notify

import (
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// sendGuard Telegram 发送失败节流与退避。
//
// 背景：api.telegram.org 不可达（i/o timeout）时，每次任务通知都会失败；
// 底层重试与调用方日志叠加会把日志刷满。这里做两层保护：
//   - 日志节流：同类失败在 sendGuardLogInterval 内只记一次 Warn
//   - 连续失败退避：达到 sendGuardMaxFailures 后暂停发送 sendGuardBackoffPeriod，
//     期间静默跳过（不再发起网络请求），恢复后只提示一次
//
// 只作用于 Telegram 通道，Webhook 不受影响。
type sendGuard struct {
	mu           sync.Mutex
	consecutive  int
	lastLogAt    time.Time
	backoffUntil time.Time
}

// 变量而非常量：便于单测缩短时长
var (
	sendGuardLogInterval   = 5 * time.Minute
	sendGuardMaxFailures   = 5
	sendGuardBackoffPeriod = 10 * time.Minute
)

// allow 是否允许本次发送（退避期内返回 false）。
func (g *sendGuard) allow() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !time.Now().Before(g.backoffUntil)
}

// onFailure 记录一次失败：日志节流；达到阈值时进入退避并只提示一次。
func (g *sendGuard) onFailure(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.consecutive++
	now := time.Now()
	if g.consecutive == sendGuardMaxFailures {
		g.backoffUntil = now.Add(sendGuardBackoffPeriod)
		g.lastLogAt = now
		logger.S().Warnf("[Telegram] 连续 %d 次发送失败，暂停通知 %s（网络恢复后自动重试）: %v",
			g.consecutive, sendGuardBackoffPeriod, err)
		return
	}
	if now.Sub(g.lastLogAt) >= sendGuardLogInterval {
		g.lastLogAt = now
		logger.S().Warnf("[Telegram] 通知发送失败（连续 %d 次，同类错误 %s 内只提示一次）: %v",
			g.consecutive, sendGuardLogInterval, err)
	}
}

// onSuccess 发送成功后复位；若刚从退避恢复，提示一次。
func (g *sendGuard) onSuccess() {
	g.mu.Lock()
	defer g.mu.Unlock()
	wasBackoff := !g.backoffUntil.IsZero()
	g.consecutive = 0
	g.backoffUntil = time.Time{}
	if wasBackoff {
		logger.S().Infof("[Telegram] 通知发送已恢复")
	}
}
