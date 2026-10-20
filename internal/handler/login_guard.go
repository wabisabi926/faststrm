package handler

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxFailures = 5
	loginFailWindow  = 15 * time.Minute
	loginBlockFor    = 15 * time.Minute
)

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

// loginGuard 按来源 IP 限制登录失败次数，防止默认口令被在线爆破。
// 单进程内存实现：重启后清零，不引入额外存储依赖。
type loginGuard struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
	now      func() time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		attempts: make(map[string]*loginAttempt),
		now:      time.Now,
	}
}

var authLoginGuard = newLoginGuard()

// loginClientKey 以 TCP 对端地址作为限速键，不信任 X-Forwarded-For，
// 避免直连场景下通过伪造请求头绕过登录失败限制。
func loginClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	host = strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return "unknown"
	}
	return host
}

// allow 判断当前 IP 是否允许尝试登录；被阻止时返回剩余等待时间。
func (g *loginGuard) allow(key string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	a := g.attempts[key]
	if a == nil {
		return true, 0
	}
	if now.Before(a.blockedUntil) {
		return false, a.blockedUntil.Sub(now)
	}
	// 阻止窗口已过：允许一次新尝试，并重置失败计数
	if !a.blockedUntil.IsZero() {
		delete(g.attempts, key)
		return true, 0
	}
	// 普通失败窗口过期：重置
	if now.Sub(a.windowStart) > loginFailWindow {
		delete(g.attempts, key)
	}
	return true, 0
}

// fail 记录一次失败；达到阈值后进入阻止窗口。
func (g *loginGuard) fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	a := g.attempts[key]
	if a == nil || now.Sub(a.windowStart) > loginFailWindow {
		a = &loginAttempt{failures: 1, windowStart: now}
		g.attempts[key] = a
	} else {
		a.failures++
	}
	if a.failures >= loginMaxFailures {
		a.blockedUntil = now.Add(loginBlockFor)
		a.failures = 0
	}
	// 顺手清理过期条目，避免 map 无限增长
	for k, v := range g.attempts {
		if !v.blockedUntil.IsZero() {
			if now.After(v.blockedUntil) {
				delete(g.attempts, k)
			}
			continue
		}
		if now.Sub(v.windowStart) > loginFailWindow {
			delete(g.attempts, k)
		}
	}
}

// reset 登录成功后清除该 IP 的失败记录。
func (g *loginGuard) reset(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.attempts, key)
}

// resetAll 仅供测试使用。
func (g *loginGuard) resetAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.attempts = make(map[string]*loginAttempt)
}
