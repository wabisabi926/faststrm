// Manager 管理 Emby 反代 HTTP server 的生命周期（支持热重启）。
// 用途：用户改 ProxyPort 或 Emby URL 时，POST /api/emby/settings 保存后调用
// Manager.Restart() 即可优雅停旧端口、拉起新端口，无需重启主程序。

package embyproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// Status 当前反代运行状态
type Status struct {
	Running bool   `json:"running"`
	Addr    string `json:"addr,omitempty"`    // 正在监听的 host:port
	EmbyURL string `json:"embyURL,omitempty"` // 当前代理的 Emby 源 URL
}

// Manager 管理反代 server 生命周期
type Manager struct {
	mu                 sync.Mutex
	server             *http.Server
	proxy              *Proxy
	addr               string // 当前监听地址 "host:port"
	embyURL            string // 当前代理的 Emby 源 URL
	forceProxyUaTokens []string
}

// NewManager 创建一个空的 Manager（未启动）
func NewManager() *Manager {
	return &Manager{}
}

// Start 在 host:port 启动反代，embyURL 为上游 Emby 源地址。
// 幂等：如果已经在运行且地址 + embyURL 完全一致，直接返回 nil。
// 否则先 Stop 旧 server 再启动新的。
func (m *Manager) Start(host string, port int, embyURL string, forceProxyUaTokens ...[]string) error {
	var uaTokens []string
	if len(forceProxyUaTokens) > 0 {
		uaTokens = forceProxyUaTokens[0]
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wantAddr := fmt.Sprintf("%s:%d", host, port)

	// 幂等：同地址 + 同 embyURL 直接返回
	if m.server != nil && m.addr == wantAddr && m.embyURL == embyURL {
		logger.S().Infof("[EmbyProxy] 已在运行 %s → %s，跳过启动", wantAddr, embyURL)
		return nil
	}

	// 如果旧 server 还在跑，先优雅停掉
	if m.server != nil {
		logger.S().Infof("[EmbyProxy] 旧实例 %s → %s 正在停服...", m.addr, m.embyURL)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = m.server.Shutdown(ctx)
		cancel()
		m.server = nil
		m.proxy = nil
		m.addr = ""
		m.embyURL = ""
		m.forceProxyUaTokens = nil
	}

	// 创建新 Proxy
	proxy, err := New(embyURL, uaTokens)
	if err != nil {
		return fmt.Errorf("embyproxy.New(%q): %w", embyURL, err)
	}

	// 同步预检测端口是否可用（避免异步 ListenAndServe 失败但 Start 已返回 nil）
	ln, err := net.Listen("tcp", wantAddr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", wantAddr, err)
	}
	_ = ln.Close() // 仅检测，立即释放；ListenAndServe 会重新绑定

	srv := &http.Server{
		Addr:         wantAddr,
		Handler:      proxy.Handler(),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	m.server = srv
	m.proxy = proxy
	m.addr = wantAddr
	m.embyURL = embyURL
	m.forceProxyUaTokens = uaTokens

	go func() {
		logger.S().Infof("[EmbyProxy] 启动中: %s → %s", wantAddr, embyURL)
		logger.S().Infof("[EmbyProxy] 反代策略：STRM 源一律强制 DirectPlay（含浏览器，禁止转码；STRM 端点代理 UA: %v）", uaTokens)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.S().Warnf("[EmbyProxy] 监听 %s 出错: %v", wantAddr, err)
		}
		logger.S().Infof("[EmbyProxy] 已停止: %s", wantAddr)
	}()

	return nil
}

// Stop 优雅停服。ctx 控制超时（建议 ≤ 5s）。未运行时返回 nil。
// 如果 ctx 已过期/取消，直接返回 error 而不调用 Shutdown（避免 listener 被关掉但内部状态未清理）。
func (m *Manager) Stop(ctx context.Context) error {
	// ctx 已过期或取消 → 直接返回，不做任何事
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Stop aborted: context %w", err)
	}

	m.mu.Lock()
	srv := m.server
	m.mu.Unlock()

	if srv == nil {
		return nil
	}

	logger.S().Infof("[EmbyProxy] 正在优雅停服 %s...", m.addr)
	shutdownErr := srv.Shutdown(ctx)

	// 无论 Shutdown 是否报错，都要把内部状态清干净
	// （Shutdown 即使超时也已经关了 listener，只是等待空闲连接超时）
	m.mu.Lock()
	m.server = nil
	m.proxy = nil
	m.addr = ""
	m.embyURL = ""
	m.forceProxyUaTokens = nil
	m.mu.Unlock()

	if shutdownErr != nil {
		return fmt.Errorf("embyproxy.Shutdown: %w", shutdownErr)
	}
	return nil
}

// Restart 等价于 Start。在旧实例上换地址/embyURL 时会自动先 Stop 再 Start。
func (m *Manager) Restart(host string, port int, embyURL string, forceProxyUaTokens ...[]string) error {
	return m.Start(host, port, embyURL, forceProxyUaTokens...)
}

// StopAll 方便全局关闭时调用（context.Background 超时 5s）
func (m *Manager) StopAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.Stop(ctx)
}

// Status 返回当前运行状态（线程安全）
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		Running: m.server != nil,
		Addr:    m.addr,
		EmbyURL: m.embyURL,
	}
}
