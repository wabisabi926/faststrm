package rate

import (
	"context"
	"sync"
)

// LimiterType 限流器类型（区分调用用途，它们的限流粒度不同）
//
// 为什么要按用途分桶：扫描列目录与播放取直链都会调用 115 API，
// 若共用一个桶，大库全量扫描会把播放取直链饿死（表现为「扫描时播放转圈」）。
// 分桶后二者互不影响，全局兜底桶再防多账号叠加触发 115 风控。
type LimiterType string

const (
	// TypeAPI115 扫描/目录/元数据等常规 115 API 调用：默认 60 次/分/账号
	TypeAPI115 LimiterType = "api115"
	// TypePlayback 播放取直链：默认 120 次/分/账号（播放是低频操作，给足配额保证秒开）
	TypePlayback LimiterType = "playback"
	// TypeLife 生活事件轮询：默认 60 次/分/账号
	TypeLife LimiterType = "life"
	// TypeDownload 115 文件下载（元数据等）：默认 30 次/分
	TypeDownload LimiterType = "download"
	// TypeProxy STRM proxy 并发：走 Bottleneck
	TypeProxy LimiterType = "proxy"
)

// Registry 按 {accountKey}:{type} 维度管理限流器实例
// 单例模式，全局共享
type Registry struct {
	mu          sync.RWMutex
	limiters    map[string]*Limiter
	bottlenecks map[string]*Bottleneck

	api115QPM          int // 常规 API 每分钟令牌数
	playbackQPM        int // 播放取直链每分钟令牌数
	lifeQPM            int // 生活事件每分钟令牌数
	globalCapQPM       int // 全进程兜底上限（<=0 表示不启用）
	downloadQPM        int // 下载每分钟令牌数
	downloadConcurrent int // 下载并发数

	globalCap *Limiter // 全进程兜底令牌桶（懒创建）
}

var (
	regInstance *Registry
	regOnce     sync.Once
)

// 默认限流参数
const (
	// RegistryDefaultAPI115QPM 默认 115 常规 API 限频：每分钟 60 次（每秒 1 次，保守）
	RegistryDefaultAPI115QPM = 60
	// RegistryDefaultPlaybackQPM 默认播放取直链限频：每分钟 120 次
	RegistryDefaultPlaybackQPM = 120
	// RegistryDefaultLifeQPM 默认生活事件限频：每分钟 60 次
	RegistryDefaultLifeQPM = 60
	// RegistryDefaultGlobalCapQPM 默认全进程兜底上限：每分钟 300 次（多账号防叠加风控）
	RegistryDefaultGlobalCapQPM = 300
	// RegistryDefaultDownloadQPM 默认下载 URL 解析：每分钟 30 次
	RegistryDefaultDownloadQPM = 30
	// RegistryDefaultDownloadConcurrent 默认下载并发：8
	RegistryDefaultDownloadConcurrent = 8
)

// Limits 限流参数集合（供启动时一次性配置）
type Limits struct {
	API115QPM          int
	PlaybackQPM        int
	LifeQPM            int
	GlobalCapQPM       int
	DownloadQPM        int
	DownloadConcurrent int
}

// GetRegistry 获取全局单例
func GetRegistry() *Registry {
	regOnce.Do(func() {
		regInstance = &Registry{
			limiters:           make(map[string]*Limiter),
			bottlenecks:        make(map[string]*Bottleneck),
			api115QPM:          RegistryDefaultAPI115QPM,
			playbackQPM:        RegistryDefaultPlaybackQPM,
			lifeQPM:            RegistryDefaultLifeQPM,
			globalCapQPM:       RegistryDefaultGlobalCapQPM,
			downloadQPM:        RegistryDefaultDownloadQPM,
			downloadConcurrent: RegistryDefaultDownloadConcurrent,
		}
	})
	return regInstance
}

// Configure 一次性配置全部限流参数（服务启动时调用）。
//
// 说明：参数变更会清空已创建的令牌桶，避免旧 QPM 被固化在已创建的实例上。
// 因此本方法只应在启动阶段调用；运行期热改需重启服务。
// GlobalCapQPM 传负数表示「不修改」，传 0 表示显式关闭兜底。
func (r *Registry) Configure(l Limits) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l.API115QPM > 0 {
		r.api115QPM = l.API115QPM
	}
	if l.PlaybackQPM > 0 {
		r.playbackQPM = l.PlaybackQPM
	}
	if l.LifeQPM > 0 {
		r.lifeQPM = l.LifeQPM
	}
	if l.GlobalCapQPM >= 0 {
		r.globalCapQPM = l.GlobalCapQPM
	}
	if l.DownloadQPM > 0 {
		r.downloadQPM = l.DownloadQPM
	}
	if l.DownloadConcurrent > 0 {
		r.downloadConcurrent = l.DownloadConcurrent
	}
	r.limiters = make(map[string]*Limiter)
	r.globalCap = nil
}

// SetDefaults 自定义默认限流参数（保留旧签名，兼容既有调用与测试）。
// 新代码请优先使用 Configure。
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
	r.limiters = make(map[string]*Limiter)
	r.globalCap = nil
}

func regKey(accountKey string, t LimiterType) string {
	if accountKey == "" {
		accountKey = GlobalKey
	}
	return accountKey + ":" + string(t)
}

// qpmForLocked 返回指定类型的每分钟额度（调用方需持有锁）
func (r *Registry) qpmForLocked(t LimiterType) int {
	switch t {
	case TypeAPI115:
		return r.api115QPM
	case TypePlayback:
		return r.playbackQPM
	case TypeLife:
		return r.lifeQPM
	case TypeDownload:
		return r.downloadQPM
	case TypeProxy:
		// proxy 走 Bottleneck，这里给一个宽松的令牌桶兜底
		return r.api115QPM * 2
	default:
		return r.api115QPM
	}
}

// GetLimiter 获取或创建令牌桶限流器
func (r *Registry) GetLimiter(accountKey string, t LimiterType) *Limiter {
	key := regKey(accountKey, t)
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
	qpm := r.qpmForLocked(t)
	l := NewLimiterBurst(qpm, qpm/4)
	r.limiters[key] = l
	return l
}

// globalCapLimiter 获取全进程兜底桶（未启用时返回 nil）
func (r *Registry) globalCapLimiter() *Limiter {
	r.mu.RLock()
	qpm, lim := r.globalCapQPM, r.globalCap
	r.mu.RUnlock()
	if qpm <= 0 {
		return nil
	}
	if lim != nil {
		return lim
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.globalCapQPM <= 0 {
		return nil
	}
	if r.globalCap == nil {
		// 兜底桶突发容量取 1/5，避免一个突发就吃掉整分钟配额而饿死其他调用方
		burst := r.globalCapQPM / 5
		if burst <= 0 {
			burst = 1
		}
		r.globalCap = NewLimiterBurst(r.globalCapQPM, burst)
	}
	return r.globalCap
}

// Acquire 按「账号 + 用途」获取一个 API 令牌。
//
// 顺序说明：先过全局兜底桶，再过分桶。
// 由于各分桶自身额度有限（扫描 60/分/账号），全局桶在正常负载下始终有令牌，
// 不会让扫描把播放挡在门口；它只在多账号叠加时起保护作用。
func (r *Registry) Acquire(ctx context.Context, accountKey string, t LimiterType) error {
	if lim := r.globalCapLimiter(); lim != nil {
		if err := lim.Acquire(ctx); err != nil {
			return err
		}
	}
	return r.GetLimiter(accountKey, t).Acquire(ctx)
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
	r.globalCap = nil
}
