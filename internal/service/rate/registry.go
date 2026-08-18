package rate

import (
	"sync"
)

// LimiterType 限流器类型（区分 API 调用 vs 文件下载，它们的限流粒度不同）
type LimiterType string

const (
	TypeAPI115   LimiterType = "api115"   // 115 API 调用：默认每分钟 60 次
	TypeDownload LimiterType = "download" // 115 文件下载：默认并发 8，QPS 较低
	TypeProxy    LimiterType = "proxy"    // STRM proxy 并发：按账号 accountProxyConcurrencyLimit
)

// Registry 按 {accountId}:{type} 维度管理限流器实例
// 单例模式，全局共享
type Registry struct {
	mu          sync.RWMutex
	limiters    map[string]*Limiter
	bottlenecks map[string]*Bottleneck

	api115QPM         int // 默认 115 API 每分钟令牌数
	downloadQPM       int // 默认下载每分钟令牌数
	downloadConcurrent int // 默认下载并发数
}

var (
	regInstance *Registry
	regOnce     sync.Once
)

// RegistryDefaultAPI115QPM 默认 115 API 限频：每分钟 60 次（每秒 1 次，保守）
const RegistryDefaultAPI115QPM = 60

// RegistryDefaultDownloadQPM 默认下载 URL 解析：每分钟 30 次
const RegistryDefaultDownloadQPM = 30

// RegistryDefaultDownloadConcurrent 默认下载并发：8
const RegistryDefaultDownloadConcurrent = 8

// GetRegistry 获取全局单例
func GetRegistry() *Registry {
	regOnce.Do(func() {
		regInstance = &Registry{
			limiters:          make(map[string]*Limiter),
			bottlenecks:       make(map[string]*Bottleneck),
			api115QPM:         RegistryDefaultAPI115QPM,
			downloadQPM:       RegistryDefaultDownloadQPM,
			downloadConcurrent: RegistryDefaultDownloadConcurrent,
		}
	})
	return regInstance
}

// SetDefaults 自定义默认限流参数（可选，服务启动时调用一次）
func (r *Registry) SetDefaults(api115QPM, downloadQPM, downloadConcurrent int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if api115QPM > 0 {
		r.api115QPM = api115QPM
	}
	if downloadQPM > 0 {
		r.downloadQPM = downloadQPM
	}
	if downloadConcurrent > 0 {
		r.downloadConcurrent = downloadConcurrent
	}
}

func regKey(accountID string, t LimiterType) string {
	return accountID + ":" + string(t)
}

// GetLimiter 获取或创建令牌桶限流器
func (r *Registry) GetLimiter(accountID string, t LimiterType) *Limiter {
	key := regKey(accountID, t)
	r.mu.RLock()
	if l, ok := r.limiters[key]; ok {
		r.mu.RUnlock()
		return l
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.limiters[key]; ok {
		return l
	}
	var qpm int
	switch t {
	case TypeAPI115:
		qpm = r.api115QPM
	case TypeDownload:
		qpm = r.downloadQPM
	case TypeProxy:
		// proxy 走 Bottleneck，这里给一个宽松的令牌桶兜底
		qpm = r.api115QPM * 2
	default:
		qpm = r.api115QPM
	}
	l := NewLimiter(qpm)
	r.limiters[key] = l
	return l
}

// GetBottleneck 获取或创建并发数限制器
func (r *Registry) GetBottleneck(accountID string, t LimiterType, overrideLimit int) *Bottleneck {
	key := regKey(accountID, t)
	r.mu.RLock()
	if b, ok := r.bottlenecks[key]; ok {
		r.mu.RUnlock()
		return b
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.bottlenecks[key]; ok {
		return b
	}
	limit := overrideLimit
	if limit <= 0 {
		switch t {
		case TypeDownload:
			limit = r.downloadConcurrent
		case TypeProxy:
			limit = 8 // 默认 STRM proxy 并发 8，后续可从 settings 读
		default:
			limit = r.downloadConcurrent
		}
	}
	b := NewBottleneck(limit)
	r.bottlenecks[key] = b
	return b
}

// Clear 清空所有限流器（测试用）
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limiters = make(map[string]*Limiter)
	r.bottlenecks = make(map[string]*Bottleneck)
}
