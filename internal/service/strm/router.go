// Package strm STRM 路由策略引擎
// 对齐 frontend/src/app/api/strm/route.ts decideRoute + redirectCheck + URL 缓存
package strm

import (
	"container/list"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

const (
	URLCacheTTL        = 5 * 60 * 1000        // 5 分钟（毫秒）
	ReachableCacheTTL  = 4 * 60 * 1000        // 4 分钟
	ReachableCacheMax  = 256                   // 简单 LRU 上限
	URLCacheMax        = 512
	ConnectTimeoutMs   = 30_000                // proxy 模式建连超时
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
	ttlMs   int
	order   *list.List                 // 链表头=最老，链表尾=最新
	items   map[string]*list.Element   // key -> *list.Element (value = *lruEntry)
}

func newSimpleLRU[V any](maxSize, ttlMs int) *simpleLRU[V] {
	return &simpleLRU[V]{
		maxSize: maxSize,
		ttlMs:   ttlMs,
		order:   list.New(),
		items:   make(map[string]*list.Element),
	}
}

func (c *simpleLRU[V]) Get(key string) (V, bool) {
	var zero V
	now := time.Now().UnixMilli()
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	entry := el.Value.(*lruEntry[V])
	if entry.expires <= now {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	// LRU touch
	c.order.MoveToBack(el)
	return entry.value, true
}

func (c *simpleLRU[V]) Set(key string, value V) {
	exp := time.Now().UnixMilli() + int64(c.ttlMs)
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
)

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

// DecideRoute 根据请求上下文决策 proxy vs redirect
// explicitMode: 仅私网生效。forceProxyUaTokens: UA 包含这些 token 时强制代理
func DecideRoute(
	r *http.Request,
	explicitMode string,
	forceProxyUaTokens []string,
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

	// 3) 默认 redirect
	return DecisionResult{Decision: DecisionRedirect, Reason: "default_redirect"}
}

// ==================== redirectCheck：HEAD 预检直链 ====================

// reusable head client（禁用 KeepAlive 避免长连接占坑）
var redirectCheckClient = &http.Client{
	Transport: &http.Transport{
		DisableKeepAlives:  true,
		ForceAttemptHTTP2:  false,
		MaxIdleConns:       64,
		IdleConnTimeout:    30 * time.Second,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: false},
		Proxy:              http.ProxyFromEnvironment,
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
	timeoutMs int,
) ReachableResult {
	cacheKey := accountName + "|" + cdnURL
	if cached, ok := reachableCache.Get(cacheKey); ok {
		return cached
	}

	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
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

// ==================== URL 解析（带缓存） ====================

// ResolveDownloadUrl 取下载直链（LRU 缓存 5 分钟）
func ResolveDownloadUrl(
	ctx context.Context,
	c115 *client115.Client,
	pickcode, accountName, cookie, userAgent string,
) (*client115.DownloadUrlMeta, error) {
	cacheKey := accountName + ":" + pickcode
	if cached, ok := urlCache.Get(cacheKey); ok {
		return cached, nil
	}
	meta, err := c115.GetDownloadUrlWebFull(ctx, pickcode, cookie)
	if err != nil {
		return nil, err
	}
	urlCache.Set(cacheKey, meta)
	return meta, nil
}

// ==================== 辅助：从 store 中找账号 ====================

// FindAccount115 从 accountStore 中按 name 查找 115 类型账户
func FindAccount115(as *store.AccountStore, name string) *model.AccountInfo {
	accounts, err := as.ReadAccounts()
	if err != nil {
		return nil
	}
	for i := range accounts {
		if accounts[i].Name == name && accounts[i].AccountType == "115" {
			return &accounts[i]
		}
	}
	return nil
}

// StrmRouteConfig 从 config 中取 STRM 路由策略（兜底默认）
func StrmRouteConfig(cfg *config.AppConfig) struct {
	ForceProxyUaTokens         []string
	AccountProxyConcurrencyLimit int
	RedirectCheckTimeoutMs     int
} {
	st := cfg.Settings.Strm
	tokens := st.ForceProxyUaTokens
	if len(tokens) == 0 {
		tokens = append([]string{}, model.DefaultSettings().Strm.ForceProxyUaTokens...)
	}
	proxyLimit := st.AccountProxyConcurrencyLimit
	if proxyLimit <= 0 {
		proxyLimit = model.DefaultSettings().Strm.AccountProxyConcurrencyLimit
	}
	checkTimeout := st.RedirectCheckTimeoutMs
	if checkTimeout <= 0 {
		checkTimeout = model.DefaultSettings().Strm.RedirectCheckTimeoutMs
	}
	return struct {
		ForceProxyUaTokens           []string
		AccountProxyConcurrencyLimit int
		RedirectCheckTimeoutMs       int
	}{
		ForceProxyUaTokens:           tokens,
		AccountProxyConcurrencyLimit: proxyLimit,
		RedirectCheckTimeoutMs:       checkTimeout,
	}
}

// ==================== 调试：转 JSON 字符串 ====================

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
