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
	"path"
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
	"github.com/wabisabi926/faststrm/pkg/strmutil"
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

// Delete 删除指定 key 的缓存条目，返回是否命中
func (c *simpleLRU[V]) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return false
	}
	c.order.Remove(el)
	delete(c.items, key)
	return true
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
//
// 文件名可能来自 115 的 Name 字段，历史上出现过含 '/' 的情况（脏数据/多行标题）。
// filename* 按 RFC 5987 编码时必须把 '/' 编成 %2F，不能原样保留，否则部分播放器
// 会把它当成路径分隔符；同时提供 ASCII filename 作为旧客户端兜底。
func BuildContentDisposition(fileName string) string {
	if strings.TrimSpace(fileName) == "" {
		return `attachment; filename=""`
	}
	name := sanitizeDispositionName(fileName)
	if name == "" {
		return `attachment; filename=""`
	}
	fallback := asciiDispositionFallback(name)
	if isASCII(name) {
		return `attachment; filename="` + escapeQuotedFileName(name) + `"`
	}
	return `attachment; filename="` + escapeQuotedFileName(fallback) + `"; filename*=UTF-8''` + percentEncodePath(name)
}

// sanitizeDispositionName 去掉头部非法字符，并把路径分隔符降级为下划线。
func sanitizeDispositionName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			// 控制字符（含 CR/LF/NUL）不能进入响应头
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "media"
	}
	return out
}

// asciiDispositionFallback 为 filename= 生成纯 ASCII 兜底名，保留扩展名。
func asciiDispositionFallback(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	ext := path.Ext(out)
	stem := strings.TrimSuffix(out, ext)
	if strings.Trim(stem, "._- ") == "" {
		if ext == "" {
			return "media"
		}
		return "media" + ext
	}
	return out
}

func escapeQuotedFileName(name string) string {
	return strings.ReplaceAll(name, `"`, "'")
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return false
		}
	}
	return true
}

// percentEncodePath 做 RFC 5987 风格的百分号编码（'/' 也必须编码为 %2F）
func percentEncodePath(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) * 3)
	for _, b := range []byte(s) {
		if isUnreserved(b) {
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
// URL 百分解码后追加 115 字符引用解码（"\x02<码点>" → 真实字符，emoji 等特殊字符
// 在 115 下载接口 / CDN 文件名中以此编码返回）；无编码时为 O(1) 快速跳过。
func ResolveFileName(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "%") {
		if decoded, err := urlQueryUnescape(raw); err == nil {
			raw = decoded
		}
	}
	return strmutil.Unescape115CharRef(raw)
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

// DecideRoute 根据请求上下文决策 proxy vs redirect
// explicitMode: 仅私网生效。forceProxyUaTokens: UA 包含这些 token 时强制代理
// fileName: 请求的文件名（用于识别原盘格式）；forceProxySeekFormats: 强制代理的原盘扩展名
func DecideRoute(
	r *http.Request,
	explicitMode string,
	forceProxyUaTokens []string,
	fileName string,
	forceProxySeekFormats []string,
) DecisionResult {
	// 1) 手动指定优先级最高
	switch explicitMode {
	case "redirect":
		return DecisionResult{Decision: DecisionRedirect, Reason: "explicit_mode_redirect"}
	case "proxy":
		return DecisionResult{Decision: DecisionProxy, Reason: "explicit_mode_proxy"}
	}

	ua := r.Header.Get("user-agent")

	// 2) 坑客户端（Infuse/VidHub/SenPlayer…）→ 强制代理
	for _, tok := range forceProxyUaTokens {
		if tok != "" && strings.Contains(ua, tok) {
			return DecisionResult{Decision: DecisionProxy, Reason: "force_proxy_ua:" + tok}
		}
	}

	// 3) 原盘 / seek 格式 → 强制代理。
	// 302 让客户端直连 CDN，ISO 等镜像的 Range/UA 绑定极易被拒（表现为卡在加载或无法播放），
	// 服务端代理是唯一可靠的交付方式；与反代层的 isSeekRequiredFormat 保持一致。
	if IsSeekFormatName(fileName, forceProxySeekFormats) {
		return DecisionResult{Decision: DecisionProxy, Reason: "seek_format_force_proxy"}
	}

	// 4) 默认 redirect：普通视频由客户端直连 115 CDN，不占 NAS 带宽
	return DecisionResult{Decision: DecisionRedirect, Reason: "default_redirect"}
}

// IsSeekFormatName 判断文件名扩展名是否属于「需要服务端代理」的原盘格式。
// 与设置项 strm.forceProxySeekFormats / forceProxyFormats 同源，大小写不敏感、点号可省略。
func IsSeekFormatName(fileName string, formats []string) bool {
	if fileName == "" || len(formats) == 0 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return false
	}
	ext = strings.TrimPrefix(ext, ".")
	for _, f := range formats {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(f), "."), ext) {
			return true
		}
	}
	return false
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
	cacheExpires := computeCdnURLCacheExpires(meta.URL, URLCacheTTL)
	// 剩余寿命不足安全余量时不缓存：之前 fallback 到默认 TTL 会把
	// “只剩 4 分钟”的链接缓存 5 分钟，302 客户端最后 1 分钟必然拿到死链。
	if cacheExpires.After(time.Now()) {
		urlCache.SetWithExpires(cacheKey, meta, cacheExpires)
	}
	return meta, nil
}

// InvalidateDownloadURL 主动失效 (账号, pickcode, UA) 的直链缓存与负缓存。
//
// 使用场景：代理流发现上游返回 401/403/404 等「链接可能已过期」的状态码时，
// 先失效缓存再重新取链，否则会从缓存里拿回同一个已失效的 URL。
func InvalidateDownloadURL(accountName, pickcode, userAgent string) {
	pickcode = strings.ToLower(pickcode)
	ua := userAgent
	if ua == "" {
		ua = "NoUA"
	}
	cacheKey := accountName + ":" + pickcode + "|" + ua
	urlCache.Delete(cacheKey)
	urlNegativeCache.Delete(cacheKey)
}

// cdnURLCacheSafety 缓存相对 CDN 签名的安全余量。
// 太大会导致“明明还能播几分钟却不缓存”，太小可能让客户端拿到临界链接；
// 1 分钟足够覆盖 302 响应和客户端起播。
const cdnURLCacheSafety = time.Minute

// computeCdnURLCacheExpires 从 CDN URL 解析 t 参数，计算缓存过期时间。
//
// 115 CDN URL 格式: http://cdn-xxx.115.com/...?t=1757769600&...
// t 是 unix 秒级时间戳（URL 真实过期时间）。返回值早于 now 表示不应缓存；
// 只有解析不到 t / t 非法时才回退 defaultTTL（无法判断寿命，按默认缓存）。
func computeCdnURLCacheExpires(cdnURL string, defaultTTL time.Duration) time.Time {
	now := time.Now()
	parsed, err := url.Parse(cdnURL)
	if err != nil {
		return now.Add(defaultTTL)
	}
	tStr := parsed.Query().Get("t")
	if tStr == "" {
		return now.Add(defaultTTL)
	}
	t, err := strconv.ParseInt(tStr, 10, 64)
	if err != nil {
		return now.Add(defaultTTL)
	}
	expiry := time.Unix(t, 0)
	if !expiry.After(now) {
		return now.Add(-time.Second)
	}
	remain := expiry.Sub(now) - cdnURLCacheSafety
	if remain <= 0 {
		return now.Add(-time.Second)
	}
	if remain > defaultTTL {
		remain = defaultTTL
	}
	return now.Add(remain)
}

// StrmRouteConfig 从 config 中取 STRM 路由策略（兜底默认）
func StrmRouteConfig(cfg *config.AppConfig) struct {
	ForceProxyUaTokens           []string
	AccountProxyConcurrencyLimit int
	RedirectCheckTimeout         time.Duration
	ForceProxyFormats            []string
	ForceProxySeekFormats        []string
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
	formats := st.ForceProxyFormats
	if len(formats) == 0 {
		formats = model.DefaultForceProxyFormats
	}
	// 按格式强制代理：两层（EmbyProxy / STRM 端点）共用 ForceProxyFormats，
	// DisableSeekFormatProxy 为统一开关——关闭后两层都不再按格式代理。
	seekFormats := formats
	if st.DisableSeekFormatProxy {
		seekFormats = nil // 显式关闭：原盘按普通格式处理
	}
	return struct {
		ForceProxyUaTokens           []string
		AccountProxyConcurrencyLimit int
		RedirectCheckTimeout         time.Duration
		ForceProxyFormats            []string
		ForceProxySeekFormats        []string
	}{
		ForceProxyUaTokens:           tokens,
		AccountProxyConcurrencyLimit: proxyLimit,
		RedirectCheckTimeout:         checkTimeout,
		ForceProxyFormats:            formats,
		ForceProxySeekFormats:        seekFormats,
	}
}
