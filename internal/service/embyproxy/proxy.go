// Package embyproxy 实现精简版 Emby 反向代理
// 参考 MoviePilot embyreverseproxy 核心逻辑，仅保留：
//   - PlaybackInfo 拦截：识别 STRM 源 → 强制 DirectPlay
//   - 媒体流路由：STRM 源 → 跟随重定向链拿到最终 CDN 直链 → 302
//
// 用法：用户将 Emby 客户端（如 Emby for Kodi gen）连接到 FastStrm 的代理端口，
// 代理自动识别 STRM Item 并改写 PlaybackInfo 响应。
//
// 三层缓存架构（对齐 MoviePilot）：
//  1. playbackURLCache  — 已解析的最终 CDN URL，key=(itemID, sourceID, userID, headerHash)，TTL 90s
//  2. playbackUserCache — PlaybackInfo → Stream 关联，key=(clientIP, UA, itemID) → userID，TTL 300s
//  3. strmSourcesCache  — PlaybackInfo 阶段识别到的 STRM 源 Path，key=itemID，TTL 300s
package embyproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ============================================================
// 常量配置（对齐 MoviePilot embyreverseproxy）
// ============================================================

const (
	// MaxCacheSize 所有缓存共用的最大条目数
	MaxCacheSize = 500

	// playbackURLCacheTTL 已解析最终 CDN URL 缓存 TTL
	playbackURLCacheTTL = 90 * time.Second
	// playbackUserCacheTTL (ip, ua, itemID) → userID 关联缓存 TTL
	playbackUserCacheTTL = 300 * time.Second
	// strmSourcesCacheTTL PlaybackInfo 阶段识别到的 STRM 源缓存 TTL
	strmSourcesCacheTTL = 300 * time.Second
)

// redirectResolveTimeouts 重定向链解析渐进超时策略
// 对齐 MoviePilot REDIRECT_RESOLVE_TIMEOUTS = ((3,10),(3,15),(5,20))
var redirectResolveTimeouts = []struct {
	connect time.Duration
	read    time.Duration
}{
	{3 * time.Second, 10 * time.Second},
	{3 * time.Second, 15 * time.Second},
	{5 * time.Second, 20 * time.Second},
}

// cacheKeyHeaders 用于区分认证/设备的请求头白名单
// 对齐 MoviePilot CACHE_KEY_HEADERS
var cacheKeyHeaders = []string{
	"authorization",
	"cookie",
	"x-emby-token",
	"user-agent",
	"x-emby-device-id",
	"x-emby-device-name",
	"x-emby-client",
	"x-emby-client-version",
	"x-device-id",
	"x-device-name",
	"x-client",
	"x-client-version",
}

// hopByHopHeaders 代理时不应透传的 hop-by-hop 头
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailers":            true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// ============================================================
// 缓存 key / entry 类型
// ============================================================

// playbackCacheKey playbackURLCache 的复合 key
type playbackCacheKey struct {
	itemID        string
	mediaSourceID string
	userID        string
	headerHash    string
}

// playbackCacheEntry 已解析的最终 CDN URL
type playbackCacheEntry struct {
	finalURL string
	expiry   time.Time
}

// playbackUserKey playbackUserCache 的复合 key（ip, ua, itemID）
type playbackUserKey struct {
	ip     string
	ua     string
	itemID string
}

// playbackUserEntry (ip, ua, itemID) → userID 关联
type playbackUserEntry struct {
	userID string
	expiry time.Time
}

// strmCacheEntry PlaybackInfo 阶段识别到的 STRM 源
type strmCacheEntry struct {
	sources map[string]string // mediaSourceID → httpPath
	expiry  time.Time
}

// ============================================================
// Proxy 核心结构（三层缓存 + 两个 HTTP client）
// ============================================================

// Proxy Emby 反向代理核心
type Proxy struct {
	embyHost string

	// httpClient 透传给 Emby 的客户端（不跟随重定向）
	httpClient *http.Client
	// followRedirectClient 用于解析重定向链拿最终 CDN URL（跟随所有重定向）
	followRedirectClient *http.Client

	// ===== 三层缓存 =====

	// 1. playbackURLCache 已解析的最终 CDN URL
	playbackURLCache   map[playbackCacheKey]playbackCacheEntry
	playbackCacheOrder []playbackCacheKey // LRU 淘汰顺序
	playbackCacheMu    sync.Mutex

	// 2. playbackUserCache (ip, ua, itemID) → userID 关联
	playbackUserCache   map[playbackUserKey]playbackUserEntry
	playbackUserCacheMu sync.Mutex

	// 3. strmSourcesCache PlaybackInfo 阶段识别到的 STRM 源
	strmSourcesCache map[string]strmCacheEntry // itemID → strmCacheEntry
	strmSourcesMu    sync.RWMutex
}

// New 创建 Emby 反向代理
func New(embyHost string) (*Proxy, error) {
	embyHost = strings.TrimRight(embyHost, "/")
	if _, err := url.Parse(embyHost); err != nil {
		return nil, fmt.Errorf("invalid emby host %q: %w", embyHost, err)
	}

	// Client A: 用于 Emby 反向代理透传，不跟随重定向
	proxyHTTPClient := &http.Client{
		Timeout: 120 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随重定向，让客户端自己 302
		},
	}

	// Client B: 用于解析重定向链拿最终 CDN URL
	// 对齐 MoviePilot httpx.AsyncClient(follow_redirects=True)
	followClient := &http.Client{
		Timeout: 30 * time.Second, // 单次超时由 resolveRedirectChain 自己控制
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects: %d", len(via))
			}
			return nil // 跟随所有重定向
		},
	}

	return &Proxy{
		embyHost:             embyHost,
		httpClient:           proxyHTTPClient,
		followRedirectClient: followClient,
		playbackURLCache:     make(map[playbackCacheKey]playbackCacheEntry),
		playbackCacheOrder:   make([]playbackCacheKey, 0, MaxCacheSize),
		playbackUserCache:    make(map[playbackUserKey]playbackUserEntry),
		strmSourcesCache:     make(map[string]strmCacheEntry),
	}, nil
}

// ============================================================
// Handler — 返回反代 HTTP handler
// ============================================================

// Handler 返回反代 HTTP handler
// 媒体流请求 (/videos/{id}/stream, /audio/{id}/stream) 由 HandleMediaStream 拦截做 302，
// 其余请求透传给 Emby 反向代理，PlaybackInfo 响应被 modifyResponse 改写强制 DirectPlay。
func (p *Proxy) Handler() http.Handler {
	u, err := url.Parse(p.embyHost)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.S().Errorf("[EmbyProxy] invalid emby host %q: %v", p.embyHost, err)
			http.Error(w, fmt.Sprintf("Emby Proxy misconfigured: %v", err), http.StatusInternalServerError)
		})
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Del("Host")
		req.Host = ""
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		return p.modifyResponse(resp)
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.S().Warnf("[EmbyProxy] 代理失败 %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, fmt.Sprintf("Emby Proxy Error: %v", err), http.StatusBadGateway)
	}

	// 媒体流路径走 HandleMediaStream（查缓存/解析重定向链 → 302），其余透传反代
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lowerPath := strings.ToLower(r.URL.Path)
		if strings.HasPrefix(lowerPath, "/videos/") || strings.HasPrefix(lowerPath, "/audio/") {
			p.HandleMediaStream(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// ============================================================
// modifyResponse — 拦截 PlaybackInfo 改写
// ============================================================

// modifyResponse 拦截关键路由的响应并改写
func (p *Proxy) modifyResponse(resp *http.Response) error {
	path := strings.ToLower(resp.Request.URL.Path)

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	if strings.Contains(path, "/playbackinfo") {
		return p.modifyPlaybackInfo(resp)
	}

	return nil
}

// modifyPlaybackInfo 拦截 PlaybackInfo 响应，识别 STRM 源并强制 DirectPlay
func (p *Proxy) modifyPlaybackInfo(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		resp.ContentLength = int64(len(body))
		return nil
	}

	// 识别 STRM 源
	sources, _ := data["MediaSources"].([]interface{})
	isStrm := false
	strmMap := make(map[string]string)

	for _, s := range sources {
		ms, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		isRemote, _ := ms["IsRemote"].(bool)
		protocol, _ := ms["Protocol"].(string)
		if isRemote && strings.EqualFold(protocol, "Http") {
			isStrm = true
			sid, _ := ms["Id"].(string)
			path, _ := ms["Path"].(string)
			if sid != "" && strings.HasPrefix(path, "http") {
				strmMap[sid] = path
			}
		}
	}

	// 也检查 MediaSource.Path 是否为 HTTP URL（STRM 被 Emby 解析后的形态）
	if !isStrm {
		for _, s := range sources {
			ms, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := ms["Path"].(string)
			if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
				isStrm = true
				sid, _ := ms["Id"].(string)
				if sid != "" {
					strmMap[sid] = path
				}
			}
		}
	}

	if !isStrm {
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		resp.ContentLength = int64(len(body))
		return nil
	}

	itemID := extractItemID(resp.Request.URL.Path)

	// 强制 DirectPlay
	p.forceDirectPlay(data, resp.Request)

	// 缓存 STRM 源映射（带 TTL 过期）
	if itemID != "" && len(strmMap) > 0 {
		p.cacheStrmSources(itemID, strmMap)
		logger.S().Infof("[EmbyProxy] STRM 缓存: item=%s sources=%d", itemID, len(strmMap))
	}

	// 同时缓存 (ip, ua, itemID) → userID 关联，供 HandleMediaStream 构建 playbackCacheKey
	if itemID != "" {
		if uid, _ := data["UserId"].(string); uid != "" {
			p.cachePlaybackUser(resp.Request, itemID, uid)
		}
	}

	newBody, _ := json.Marshal(data)
	resp.Body = io.NopCloser(strings.NewReader(string(newBody)))
	resp.ContentLength = int64(len(newBody))
	resp.Header.Del("Content-Encoding")

	logger.S().Infof("[EmbyProxy] PlaybackInfo 强制 DirectPlay: path=%s, sources=%d", resp.Request.URL.Path, len(strmMap))
	return nil
}

// forceDirectPlay 对 PlaybackInfo 响应中的 MediaSources 强制 DirectPlay
func (p *Proxy) forceDirectPlay(data map[string]interface{}, req *http.Request) {
	sources, ok := data["MediaSources"].([]interface{})
	if !ok {
		return
	}
	itemID := extractItemID(req.URL.Path)

	for _, s := range sources {
		ms, ok := s.(map[string]interface{})
		if !ok {
			continue
		}

		ms["SupportsDirectPlay"] = true
		ms["SupportsDirectStream"] = true
		ms["SupportsTranscoding"] = false

		for _, key := range []string{"TranscodingUrl", "TranscodingContainer", "TranscodingSubProtocol"} {
			delete(ms, key)
		}

		sid, _ := ms["Id"].(string)
		if sid == "" {
			continue
		}

		sidEncoded := url.QueryEscape(sid)
		sidEncoded = strings.ReplaceAll(sidEncoded, "+", "%2B")
		var streamURL string
		if mediaType, _ := ms["Type"].(string); strings.EqualFold(mediaType, "Audio") {
			streamURL = fmt.Sprintf("/audio/%s/stream?Static=true&MediaSourceId=%s", itemID, sidEncoded)
		} else {
			streamURL = fmt.Sprintf("/videos/%s/stream?Static=true&MediaSourceId=%s", itemID, sidEncoded)
		}
		ms["DirectStreamUrl"] = streamURL
	}
}

// ============================================================
// HandleMediaStream — 核心媒体流路由
// ============================================================

// HandleMediaStream 处理媒体流请求：
//  1. 查 playbackURLCache（已解析的最终 CDN URL）→ 302 返回
//  2. 否则拿 STRM URL → resolveRedirectChain 跟随重定向链 → 缓存 + 302
//  3. 拿不到 STRM URL → 透传到 Emby
//
// 对齐 MoviePilot _try_media_response 核心逻辑
func (p *Proxy) HandleMediaStream(w http.ResponseWriter, r *http.Request) {
	itemID := extractItemID(r.URL.Path)
	sourceID := r.URL.Query().Get("MediaSourceId")

	// ===== 步骤 1: 查三层缓存 =====
	userID := p.getUserForPlayback(r, itemID)
	headerHash := computeHeaderHash(r)
	cacheKey := playbackCacheKey{
		itemID:        itemID,
		mediaSourceID: sourceID,
		userID:        userID,
		headerHash:    headerHash,
	}

	// 1a. playbackURLCache — 已解析的最终 CDN URL（命中率最高）
	if finalURL, ok := p.getCachedPlaybackURL(cacheKey); ok {
		logger.S().Debugf("[EmbyProxy] playbackURLCache 命中: item=%s source=%s -> %s", itemID, sourceID, finalURL)
		w.Header().Set("Location", finalURL)
		w.WriteHeader(http.StatusFound)
		return
	}

	// 1b. strmSourcesCache — PlaybackInfo 阶段缓存的 STRM Path
	httpPath := ""
	if itemID != "" && sourceID != "" {
		if strmEntry, ok := p.getCachedStrmSources(itemID); ok {
			if path, ok2 := strmEntry.sources[sourceID]; ok2 {
				httpPath = path
				logger.S().Debugf("[EmbyProxy] strmSourcesCache 命中: item=%s source=%s", itemID, sourceID)
			}
		}
	}

	// 没拿到 STRM URL → 透传到 Emby
	if httpPath == "" {
		p.passthroughToEmby(w, r)
		return
	}

	// ===== 步骤 2: 解析重定向链拿最终 CDN URL =====
	finalURL := p.resolveRedirectChain(r.Context(), httpPath, r, userID)
	if finalURL != httpPath {
		logger.S().Infof("[EmbyProxy] resolveRedirectChain: item=%s %s -> %s", itemID, httpPath, finalURL)
	}

	// ===== 步骤 3: 缓存最终 URL 并 302 =====
	p.cachePlaybackURL(cacheKey, finalURL)

	logger.S().Infof("[EmbyProxy] media 302: item=%s source=%s -> %s", itemID, sourceID, finalURL)
	w.Header().Set("Location", finalURL)
	w.WriteHeader(http.StatusFound)
}

// passthroughToEmby 透传媒体流请求到 Emby 真实地址
func (p *Proxy) passthroughToEmby(w http.ResponseWriter, r *http.Request) {
	target := p.embyHost + r.URL.Path + "?" + r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] 媒体流请求失败: %v", err)
		http.Error(w, fmt.Sprintf("Emby Error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ============================================================
// resolveRedirectChain — 跟随重定向链拿最终 CDN URL
// 对齐 MoviePilot _resolve_redirect
// ============================================================

// resolveRedirectChain 对起始 URL 发 HEAD 请求，跟随所有重定向拿到最终 URL
// 使用渐进超时策略（3 次重试，每次更长）
// 失败时返回原始 url（让调用方 fallback 到其他策略）
func (p *Proxy) resolveRedirectChain(ctx context.Context, startURL string, r *http.Request, userID string) string {
	fwdHeaders := buildForwardHeaders(r)
	if userID != "" {
		fwdHeaders["X-Emby-UserId"] = userID
	}

	for attempt, to := range redirectResolveTimeouts {
		reqCtx, cancel := context.WithTimeout(ctx, to.read)

		req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, startURL, nil)
		if err != nil {
			cancel()
			logger.S().Warnf("[EmbyProxy] resolveRedirectChain: 构造 HEAD 请求失败: %v", err)
			return startURL
		}
		for k, v := range fwdHeaders {
			req.Header.Set(k, v)
		}

		client := &http.Client{
			Timeout: to.connect + to.read,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects: %d", len(via))
				}
				return nil
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			// 超时 → 重试（最后一次超时则放弃）
			if attempt < len(redirectResolveTimeouts)-1 {
				logger.S().Infof("[EmbyProxy] resolveRedirectChain 超时，重试 %d/%d: url=%s err=%v",
					attempt+1, len(redirectResolveTimeouts), startURL, err)
				continue
			}
			logger.S().Warnf("[EmbyProxy] resolveRedirectChain 最终失败: url=%s err=%v", startURL, err)
			return startURL
		}

		// resp.Request.URL 是跟随所有重定向后的最终 URL
		finalURL := resp.Request.URL.String()
		resp.Body.Close()
		cancel()

		logger.S().Debugf("[EmbyProxy] resolveRedirectChain: %s -> %s (attempt %d)", startURL, finalURL, attempt+1)
		return finalURL
	}

	return startURL
}

// ============================================================
// 三层缓存读写
// ============================================================

// getCachedPlaybackURL 查 playbackURLCache（带 TTL 过期检查 + LRU 更新）
func (p *Proxy) getCachedPlaybackURL(key playbackCacheKey) (string, bool) {
	p.playbackCacheMu.Lock()
	defer p.playbackCacheMu.Unlock()

	entry, ok := p.playbackURLCache[key]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiry) {
		delete(p.playbackURLCache, key)
		p.removeOrderKeyLocked(key)
		return "", false
	}
	// LRU: 移到末尾
	p.moveOrderToEndLocked(key)
	return entry.finalURL, true
}

// cachePlaybackURL 写入 playbackURLCache（带 TTL + LRU 淘汰）
func (p *Proxy) cachePlaybackURL(key playbackCacheKey, finalURL string) {
	p.playbackCacheMu.Lock()
	defer p.playbackCacheMu.Unlock()

	// 清理过期
	now := time.Now()
	for k, v := range p.playbackURLCache {
		if now.After(v.expiry) {
			delete(p.playbackURLCache, k)
			p.removeOrderKeyLocked(k)
		}
	}

	// LRU 淘汰
	for len(p.playbackURLCache) >= MaxCacheSize && len(p.playbackCacheOrder) > 0 {
		oldest := p.playbackCacheOrder[0]
		p.playbackCacheOrder = p.playbackCacheOrder[1:]
		delete(p.playbackURLCache, oldest)
	}

	p.playbackURLCache[key] = playbackCacheEntry{
		finalURL: finalURL,
		expiry:   now.Add(playbackURLCacheTTL),
	}
	p.playbackCacheOrder = append(p.playbackCacheOrder, key)
}

// moveOrderToEndLocked 把 key 移到 order 末尾（LRU 命中更新）
func (p *Proxy) moveOrderToEndLocked(key playbackCacheKey) {
	for i, k := range p.playbackCacheOrder {
		if k == key {
			p.playbackCacheOrder = append(p.playbackCacheOrder[:i], p.playbackCacheOrder[i+1:]...)
			p.playbackCacheOrder = append(p.playbackCacheOrder, key)
			return
		}
	}
	p.playbackCacheOrder = append(p.playbackCacheOrder, key)
}

// removeOrderKeyLocked 从 order 中移除 key
func (p *Proxy) removeOrderKeyLocked(key playbackCacheKey) {
	for i, k := range p.playbackCacheOrder {
		if k == key {
			p.playbackCacheOrder = append(p.playbackCacheOrder[:i], p.playbackCacheOrder[i+1:]...)
			return
		}
	}
}

// cacheStrmSources 缓存 PlaybackInfo 阶段识别到的 STRM 源（带 TTL 过期）
func (p *Proxy) cacheStrmSources(itemID string, sources map[string]string) {
	p.strmSourcesMu.Lock()
	defer p.strmSourcesMu.Unlock()

	// 容量保护
	if len(p.strmSourcesCache) >= MaxCacheSize {
		// 清理过期
		now := time.Now()
		for k, v := range p.strmSourcesCache {
			if now.After(v.expiry) {
				delete(p.strmSourcesCache, k)
			}
		}
	}

	p.strmSourcesCache[itemID] = strmCacheEntry{
		sources: sources,
		expiry:  time.Now().Add(strmSourcesCacheTTL),
	}
}

// getCachedStrmSources 从缓存获取 STRM 源（带 TTL 过期检查）
func (p *Proxy) getCachedStrmSources(itemID string) (strmCacheEntry, bool) {
	p.strmSourcesMu.RLock()
	defer p.strmSourcesMu.RUnlock()

	entry, ok := p.strmSourcesCache[itemID]
	if !ok {
		return strmCacheEntry{}, false
	}
	if time.Now().After(entry.expiry) {
		return strmCacheEntry{}, false
	}
	return entry, true
}

// cachePlaybackUser 缓存 (ip, ua, itemID) → userID 关联
func (p *Proxy) cachePlaybackUser(r *http.Request, itemID, userID string) {
	ip := getClientIP(r)
	ua := r.Header.Get("User-Agent")
	key := playbackUserKey{ip: ip, ua: ua, itemID: itemID}

	p.playbackUserCacheMu.Lock()
	defer p.playbackUserCacheMu.Unlock()

	// 容量保护
	if len(p.playbackUserCache) >= MaxCacheSize {
		now := time.Now()
		for k, v := range p.playbackUserCache {
			if now.After(v.expiry) {
				delete(p.playbackUserCache, k)
			}
		}
	}

	p.playbackUserCache[key] = playbackUserEntry{
		userID: userID,
		expiry: time.Now().Add(playbackUserCacheTTL),
	}
}

// getUserForPlayback 从 playbackUserCache 获取 userID
func (p *Proxy) getUserForPlayback(r *http.Request, itemID string) string {
	ip := getClientIP(r)
	ua := r.Header.Get("User-Agent")
	key := playbackUserKey{ip: ip, ua: ua, itemID: itemID}

	p.playbackUserCacheMu.Lock()
	defer p.playbackUserCacheMu.Unlock()

	entry, ok := p.playbackUserCache[key]
	if !ok {
		return ""
	}
	if time.Now().After(entry.expiry) {
		delete(p.playbackUserCache, key)
		return ""
	}
	return entry.userID
}

// ============================================================
// 辅助函数
// ============================================================

// extractItemID 从 Emby API 路径中提取 Item ID
func extractItemID(path string) string {
	lower := strings.ToLower(path)
	patterns := []string{"/items/", "/videos/", "/audio/"}
	for _, prefix := range patterns {
		if idx := strings.Index(lower, prefix); idx != -1 {
			rest := path[idx+len(prefix):]
			if next := strings.Index(rest, "/"); next != -1 {
				return rest[:next]
			}
			return rest
		}
	}
	return ""
}

// computeHeaderHash 对 cacheKeyHeaders 白名单内的请求头做稳定序列化并 sha256
// 对齐 MoviePilot _header_hash
func computeHeaderHash(r *http.Request) string {
	parts := make([]string, 0, len(cacheKeyHeaders))
	for _, name := range cacheKeyHeaders {
		value := r.Header.Get(name)
		if value != "" {
			parts = append(parts, name+":"+value)
		}
	}
	sort.Strings(parts)
	joined := strings.Join(parts, "\n")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}

// getClientIP 从请求头解析真实客户端 IP
// 优先 X-Forwarded-For → X-Real-IP → r.RemoteAddr
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// r.RemoteAddr 格式: "host:port"
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

// buildForwardHeaders 构建转发请求头，排除 hop-by-hop 和 host
// 对齐 MoviePilot _build_forward_headers
func buildForwardHeaders(r *http.Request) map[string]string {
	result := make(map[string]string, len(r.Header))
	for k, vv := range r.Header {
		kLower := strings.ToLower(k)
		if hopByHopHeaders[kLower] || kLower == "host" {
			continue
		}
		if len(vv) > 0 {
			result[k] = vv[0]
		}
	}
	return result
}

// 确保 context 包已导入
var _ = context.TODO
