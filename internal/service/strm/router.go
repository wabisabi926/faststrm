// Package strm STRM 路由策略引擎
// 对齐 frontend/src/app/api/strm/route.ts decideRoute + redirectCheck + URL 缓存
package strm

import (
	"container/list"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

const (
	URLCacheTTL         = 5 * time.Minute
	ReachableCacheTTL   = 4 * time.Minute
	ReachableCacheMax   = 256
	URLCacheMax         = 512
	ConnectTimeout      = 30 * time.Second // proxy 模式建连超时
	URLNegativeCacheTTL = 10 * time.Second // P0-3 负面缓存 TTL：115 API 失败后短时间内不重试，避免雪崩
)

// RouteDecision 路由决策
type RouteDecision string

const (
	DecisionProxy    RouteDecision = "proxy"
	DecisionRedirect RouteDecision = "redirect"
)

// DecisionResult 决策结果
type DecisionResult struct {
	Decision RouteDecision
	Reason   string
}

// ReachableResult 直链可达性检查结果
type ReachableResult struct {
	OK     bool
	Status int
}

// hop-by-hop 头部，proxy 时不应透传
var HopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"content-encoding":    {},
}

var pickcodeRe = regexp.MustCompile(`^[a-zA-Z0-9]{17}$`)

// IsValidPickcode 校验 pickcode 格式
func IsValidPickcode(code string) bool {
	return pickcodeRe.MatchString(code)
}

// ==================== 简单 LRU 缓存 ====================

type lruEntry[V any] struct {
	key     string
	value   V
	expires int64 // unix ms
}

type simpleLRU[V any] struct {
	mu      sync.Mutex
	maxSize int
	ttl     time.Duration
	order   *list.List               // 链表头=最老，链表尾=最新
	items   map[string]*list.Element // key -> *list.Element (value = *lruEntry)
}

func newSimpleLRU[V any](maxSize int, ttl time.Duration) *simpleLRU[V] {
	return &simpleLRU[V]{
		maxSize: maxSize,
		ttl:     ttl,
		order:   list.New(),
		items:   make(map[string]*list.Element),
	}
}

func (c *simpleLRU[V]) Get(key string) (V, bool) {
	var zero V
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	entry := el.Value.(*lruEntry[V])
	if time.UnixMilli(entry.expires).Before(now) {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	// LRU touch
	c.order.MoveToBack(el)
	return entry.value, true
}

// DeleteBySuffix 删除所有 key 以指定后缀结尾的缓存条目，返回删除数量
func (c *simpleLRU[V]) DeleteBySuffix(suffix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for key, el := range c.items {
		if strings.HasSuffix(key, suffix) {
			c.order.Remove(el)
			delete(c.items, key)
			count++
		}
	}
	return count
}

// Clear 清空全部缓存，返回清除数量
func (c *simpleLRU[V]) Clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.order.Len()
	c.items = make(map[string]*list.Element)
	c.order = list.New()
	return n
}

func (c *simpleLRU[V]) Set(key string, value V) {
	c.SetWithExpires(key, value, time.Now().Add(c.ttl))
}

// SetWithExpires 支持调用方传入精确的过期时间（对齐 MoviePilot 解析 CDN URL t 参数）
func (c *simpleLRU[V]) SetWithExpires(key string, value V, expires time.Time) {
	exp := expires.UnixMilli()
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
	}
	for c.order.Len() >= c.maxSize {
		front := c.order.Front()
		if front == nil {
			break
		}
		oldEntry := front.Value.(*lruEntry[V])
		delete(c.items, oldEntry.key)
		c.order.Remove(front)
	}
	entry := &lruEntry[V]{key: key, value: value, expires: exp}
	el := c.order.PushBack(entry)
	c.items[key] = el
}

// ==================== 全局缓存实例 ====================

var (
	urlCache       = newSimpleLRU[*client115.DownloadUrlMeta](URLCacheMax, URLCacheTTL)
	reachableCache = newSimpleLRU[ReachableResult](ReachableCacheMax, ReachableCacheTTL)
	// P0-3 负面缓存：115 API 返回错误时短时间缓存，避免短时间内重复请求导致雪崩
	urlNegativeCache = newSimpleLRU[error](URLCacheMax, URLNegativeCacheTTL)
)

// InvalidateUACache 清理指定 UA 对应的所有 CDN 直链缓存（含负面缓存）
// 缓存 key 格式为 "account:pickcode|ua"，故按 "|ua" 后缀匹配
func InvalidateUACache(ua string) int {
	if ua == "" {
		return 0
	}
	suffix := "|" + ua
	urlCache.DeleteBySuffix(suffix)
	return urlNegativeCache.DeleteBySuffix(suffix)
}

// InvalidateAllCache 清空全部 CDN 直链缓存（含负面缓存），返回清除的正缓存数量
func InvalidateAllCache() int {
	n := urlCache.Clear()
	urlNegativeCache.Clear()
	return n
}

// P0-3 singleflight：合并并发相同 pickcode 的下载链接查询，
// 避免多个 goroutine 同时调用 115 download API 造成配额浪费与触发限流。
// 对齐参考项目 r302 的下载链接缓存并发锁
var urlCallGroup = &callGroup{calls: make(map[string]*urlCall)}

type urlCall struct {
	wg  sync.WaitGroup
	val *client115.DownloadUrlMeta
	err error
}

type callGroup struct {
	mu    sync.Mutex
	calls map[string]*urlCall
}

// Do 合并并发相同 key 的调用：首个调用者执行 fn，后续调用者等待结果
func (g *callGroup) Do(key string, fn func() (*client115.DownloadUrlMeta, error)) (*client115.DownloadUrlMeta, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &urlCall{}
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()

	return c.val, c.err
}

// ==================== 工具函数 ====================

// IsPrivateNetworkIp 判断是否为私网 IP
func IsPrivateNetworkIp(ip string) bool {
	if ip == "" {
		return false
	}
	ip = strings.TrimSpace(ip)
	if strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "10.") ||
		ip == "127.0.0.1" || ip == "::1" || ip == "localhost" ||
		strings.HasPrefix(ip, "[") {
		return true
	}
	if re172 := regexp.MustCompile(`^172\.(1[6-9]|2\d|3[0-1])\.`); re172.MatchString(ip) {
		return true
	}
	return false
}

// GetClientIp 从请求头中取客户端 IP（x-forwarded-for -> x-real-ip）
func GetClientIp(r *http.Request) string {
	if xf := r.Header.Get("x-forwarded-for"); xf != "" {
		first := strings.Split(xf, ",")[0]
		first = strings.TrimSpace(first)
		if first != "" {
			return first
		}
	}
	if xr := r.Header.Get("x-real-ip"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return ""
}

// BuildContentDisposition 构造 Content-Disposition 头部（兼容非 ASCII 文件名）
func BuildContentDisposition(fileName string) string {
	asciiOnly := true
	for _, r := range fileName {
		if r > 0x7F {
			asciiOnly = false
			break
		}
	}
	if asciiOnly {
		return `attachment; filename="` + fileName + `"`
	}
	return `attachment; filename*=UTF-8''` + percentEncodePath(fileName)
}

// percentEncodePath 做 RFC 5987 风格的百分号编码
func percentEncodePath(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		if isUnreserved(b) || b == '/' {
			sb.WriteByte(b)
		} else {
			sb.WriteString(upperHex(b))
		}
	}
	return sb.String()
}

func isUnreserved(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '-' || b == '_' || b == '.' || b == '~'
}

func upperHex(b byte) string {
	const hex = "0123456789ABCDEF"
	return "%" + string(hex[b>>4]) + string(hex[b&0x0F])
}

// ResolveFileName 解码 Content-Disposition filename
func ResolveFileName(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "%") {
		if decoded, err := urlQueryUnescape(raw); err == nil {
			return decoded
		}
	}
	return raw
}

func urlQueryUnescape(s string) (string, error) {
	// 简化版 url.QueryUnescape，将 + 保持为 +
	var sb strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '%':
			if i+2 >= len(s) {
				return "", ErrInvalidPercentEncoding
			}
			b, ok := parseHexByte(s[i+1], s[i+2])
			if !ok {
				return "", ErrInvalidPercentEncoding
			}
			sb.WriteByte(b)
			i += 3
		default:
			sb.WriteByte(s[i])
			i++
		}
	}
	return sb.String(), nil
}

var ErrInvalidPercentEncoding = &invalidEncodingErr{}

type invalidEncodingErr struct{}

func (*invalidEncodingErr) Error() string { return "invalid percent-encoding" }

func parseHexByte(a, b byte) (byte, bool) {
	hi, ok := hexDigit(a)
	if !ok {
		return 0, false
	}
	lo, ok := hexDigit(b)
	if !ok {
		return 0, false
	}
	return byte(hi<<4 | lo), true
}

func hexDigit(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// ==================== 路由策略：decideRoute ====================

// SeekRequiredExtensions 需要 HTTP Range/Seek 的容器格式
// 这些格式通过 302 redirect 后，客户端 Range 请求可能因 CDN UA 绑定/签名过期而失败
// 必须走 proxy 模式保证 Range 透传
var SeekRequiredExtensions = map[string]bool{
	".iso":  true, // DVD/BD 原盘，Libdvd 大量 seek
	".bdmv": true, // 蓝光原盘目录（STRM 指向 BDMV 入口）
	".bdav": true, // 蓝光原盘（alternative）
	".ts":   true, // MPEG-TS 直播流，Range 不稳定
	".m2ts": true, // Blu-ray M2TS，需要 seek
	".vob":  true, // DVD VOB
	".ifo":  true, // DVD IFO 导航
	".bup":  true, // DVD BUP 备份
}

// DecideRoute 根据请求上下文决策 proxy vs redirect
// explicitMode: 仅私网生效。forceProxyUaTokens: UA 包含这些 token 时强制代理
// fileName: 文件名，用于识别需要 seek 的格式强制 proxy
func DecideRoute(
	r *http.Request,
	explicitMode string,
	forceProxyUaTokens []string,
	fileName string,
) DecisionResult {
	// 1) 手动指定优先级最高
	switch explicitMode {
	case "redirect":
		return DecisionResult{Decision: DecisionRedirect, Reason: "explicit_mode_redirect"}
	case "proxy":
		return DecisionResult{Decision: DecisionProxy, Reason: "explicit_mode_proxy"}
	}

	ua := r.Header.Get("user-agent")

	// 2) seek 坑客户端 → 强制代理（Infuse/VidHub/SenPlayer…）
	for _, tok := range forceProxyUaTokens {
		if tok != "" && strings.Contains(ua, tok) {
			return DecisionResult{Decision: DecisionProxy, Reason: "force_proxy_ua:" + tok}
		}
	}

	// 3) 文件格式需要 Range/Seek → 强制 proxy
	// ISO 原盘 + BDMV + M2TS + TS 等格式，Libdvd/播放器需要精确 seek
	// 302 redirect 后客户端 Range 请求可能因 CDN UA 绑定/签名过期而被拒
	if fileName != "" {
		ext := strings.ToLower(filepath.Ext(fileName))
		if SeekRequiredExtensions[ext] {
			return DecisionResult{Decision: DecisionProxy, Reason: "seek_required:" + ext}
		}
	}

	// 4) 默认 redirect
	return DecisionResult{Decision: DecisionRedirect, Reason: "default_redirect"}
}

// ==================== redirectCheck：HEAD 预检直链 ====================

// reusable head client（禁用 KeepAlive 避免长连接占坑）
var redirectCheckClient = &http.Client{
	Transport: &http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
		MaxIdleConns:      64,
		IdleConnTimeout:   30 * time.Second,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: false},
		Proxy:             http.ProxyFromEnvironment,
	},
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return nil // follow redirects
	},
}

func build115SafeHeaders(cookie, userAgent string, extras map[string]string) map[string]string {
	if userAgent == "" {
		userAgent = client115.DefaultUA
	}
	h := map[string]string{
		"User-Agent": userAgent,
		"Referer":    "https://115.com/",
		"Origin":     "https://115.com",
	}
	if cookie != "" {
		h["Cookie"] = cookie
	}
	for k, v := range extras {
		h[k] = v
	}
	return h
}

// RedirectCheck 检查直链是否可达（带 LRU 缓存）
func RedirectCheck(
	ctx context.Context,
	cdnURL string,
	accountName, cookie, userAgent string,
	timeout time.Duration,
) ReachableResult {
	// reachableCache key 也需要 UA 维度：
	// 不同 UA 可能拿到不同的 CDN URL（UA 绑定），且同一 CDN URL 对不同 UA 响应不同
	ua := userAgent
	if ua == "" {
		ua = "NoUA"
	}
	cacheKey := accountName + "|" + cdnURL + "|" + ua
	if cached, ok := reachableCache.Get(cacheKey); ok {
		return cached
	}

	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, cdnURL, nil)
	if err != nil {
		result := ReachableResult{OK: false, Status: 0}
		return result
	}
	for k, v := range build115SafeHeaders(cookie, userAgent, nil) {
		req.Header.Set(k, v)
	}
	resp, err := redirectCheckClient.Do(req)
	status := 0
	if err == nil {
		status = resp.StatusCode
		resp.Body.Close()
	} else {
		logger.S().Warnf("[STRM][redirectCheck] HEAD failed: %v", err)
	}
	ok := status >= 200 && status < 400
	result := ReachableResult{OK: ok, Status: status}
	if ok {
		reachableCache.Set(cacheKey, result)
	}
	return result
}

// ==================== URL 解析（带缓存 + singleflight + 负面缓存） ====================

// ResolveDownloadUrl 取下载直链
//   - 正面 LRU 缓存 5 分钟
//   - P0-3 singleflight 合并并发相同 pickcode 的调用，避免重复请求 115 API
//   - P0-3 负面缓存：115 API 失败后 10 秒内不重试，避免短时间内雪崩请求
//
// 对齐参考项目 r302 的下载链接缓存 + 并发锁
func ResolveDownloadUrl(
	ctx context.Context,
	c115 *client115.Client,
	pickcode, accountName, cookie, userAgent string,
) (*client115.DownloadUrlMeta, error) {
	// 对齐参考项目 api.py#L829: 115 API 要求 pickcode 小写
	// 统一小写化保证缓存 key 一致，避免大小写不同导致缓存未命中
	pickcode = strings.ToLower(pickcode)
	ua := userAgent
	if ua == "" {
		ua = "NoUA"
	}
	// cacheKey 必须包含 UA：115 CDN URL 与请求时的 UA 严格绑定
	// 对齐参考项目 r302/__init__.py L67: key = (pickcode, cache_ua)
	cacheKey := accountName + ":" + pickcode + "|" + ua
	// 1) 正面缓存命中
	if cached, ok := urlCache.Get(cacheKey); ok {
		return cached, nil
	}
	// 2) 负面缓存命中（短时间内已失败过，不重试）
	if negErr, ok := urlNegativeCache.Get(cacheKey); ok {
		return nil, fmt.Errorf("cached 115 download error (retry in %ds): %w",
			int(URLNegativeCacheTTL.Seconds()), negErr)
	}
	// 3) singleflight 合并并发相同 key 的调用
	meta, err := urlCallGroup.Do(cacheKey, func() (*client115.DownloadUrlMeta, error) {
		return c115.GetDownloadUrlWebFull(ctx, pickcode, cookie, userAgent)
	})
	if err != nil {
		// 负面缓存错误，避免短时间内重复请求
		urlNegativeCache.Set(cacheKey, err)
		return nil, err
	}
	// 对齐 MoviePilot r302cacher：解析 CDN URL 的 t 参数计算精确过期时间
	// t = 115 CDN 返回的 unix 秒级时间戳（URL 真正过期时间）
	// 提前 5min 过期（t - 300s），避免客户端拿到已过期的 URL
	cacheExpires := computeCdnURLCacheExpires(meta.URL, URLCacheTTL)
	urlCache.SetWithExpires(cacheKey, meta, cacheExpires)
	return meta, nil
}

// computeCdnURLCacheExpires 从 CDN URL 解析 t 参数，计算缓存过期时间
// 115 CDN URL 格式: http://cdn-xxx.115.com/...?t=1757769600&...
// t 是 unix 秒级时间戳（URL 真实过期时间），提前 5min 返回
// 解析失败或 t 已过期时 fallback defaultTTL
func computeCdnURLCacheExpires(cdnURL string, defaultTTL time.Duration) time.Time {
	parsed, err := url.Parse(cdnURL)
	if err != nil {
		return time.Now().Add(defaultTTL)
	}
	tStr := parsed.Query().Get("t")
	if tStr == "" {
		return time.Now().Add(defaultTTL)
	}
	t, err := strconv.ParseInt(tStr, 10, 64)
	if err != nil {
		return time.Now().Add(defaultTTL)
	}
	// 提前 5min 过期，避免 URL 在缓存内到期
	expires := time.Unix(t, 0).Add(-5 * time.Minute)
	// 如果算出来已经过期了（t 本身太小），fallback 默认 TTL
	if time.Now().After(expires) {
		return time.Now().Add(defaultTTL)
	}
	return expires
}

// StrmRouteConfig 从 config 中取 STRM 路由策略（兜底默认）
func StrmRouteConfig(cfg *config.AppConfig) struct {
	ForceProxyUaTokens           []string
	AccountProxyConcurrencyLimit int
	RedirectCheckTimeout         time.Duration
} {
	st := cfg.Settings.Strm
	tokens := st.ForceProxyUaTokens
	// 尊重用户配置：即使为空也不回填默认值，确保默认全部直连。
	proxyLimit := st.AccountProxyConcurrencyLimit
	if proxyLimit <= 0 {
		proxyLimit = model.DefaultSettings().Strm.AccountProxyConcurrencyLimit
	}
	checkTimeout := st.RedirectCheckTimeout()
	if checkTimeout <= 0 {
		checkTimeout = model.DefaultSettings().Strm.RedirectCheckTimeout()
	}
	return struct {
		ForceProxyUaTokens           []string
		AccountProxyConcurrencyLimit int
		RedirectCheckTimeout         time.Duration
	}{
		ForceProxyUaTokens:           tokens,
		AccountProxyConcurrencyLimit: proxyLimit,
		RedirectCheckTimeout:         checkTimeout,
	}
}
