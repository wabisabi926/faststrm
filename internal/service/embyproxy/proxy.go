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
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/strm"
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
// crossOrigin 拦截（对齐 MoviePilot embyreverseproxy）
// 浏览器播放器对 302 直链（跨域 CDN）默认 crossorigin="anonymous"，
// 会触发 CORS 预检，而 115 CDN 无 CORS 响应头 → 播放失败。
// 以下脚本/正则将 HTMLMediaElement.crossOrigin 恒置为 null，避免预检。
// ============================================================

const crossOriginInterceptMarker = "[EmbyReverseProxy] crossOrigin"

// crossOriginInterceptScript 注入 Emby Web 的 HTML 拦截脚本
const crossOriginInterceptScript = `<script>
(function(){
  // [EmbyReverseProxy] crossOrigin 拦截器 — 302 直链播放避免 CORS
  try {
    Object.defineProperty(HTMLMediaElement.prototype,'crossOrigin',{
      get:function(){return null},
      set:function(){},
      configurable:true
    });
  } catch(e){}
  try {
    var ob=new MutationObserver(function(ms){
      ms.forEach(function(m){
        if(m.type==='attributes'&&m.attributeName==='crossorigin'){
          m.target.removeAttribute('crossorigin');
        }
        if(m.type==='childList'){
          m.addedNodes.forEach(function(n){
            if(n.nodeType===1&&(n.tagName==='VIDEO'||n.tagName==='AUDIO')){
              n.removeAttribute('crossorigin');
            }
          });
        }
      });
    });
    if(document.documentElement){
      ob.observe(document.documentElement,{attributes:true,attributeFilter:['crossorigin'],childList:true,subtree:true});
    } else {
      document.addEventListener('DOMContentLoaded',function(){
        ob.observe(document.documentElement,{attributes:true,attributeFilter:['crossorigin'],childList:true,subtree:true});
      });
    }
  } catch(e){}
})();
</script>`

// crossOriginValueRE 匹配 basehtmlplayer.js 中 getCrossOriginValue 的三元表达式：
//   IsRemote && "DirectPlay" === ... ? null : "anonymous"
// 命中后替换为 null（即恒不设置 crossorigin）
var crossOriginValueRE = regexp.MustCompile(`\w+\.IsRemote\s*&&\s*"DirectPlay"\s*===\s*\w+\s*\?\s*null\s*:\s*"anonymous"`)

// pluginCrossOriginRE 匹配 plugin.js 中 `&&(elem.crossOrigin=...)` 赋值，整体删除
var pluginCrossOriginRE = regexp.MustCompile(`&&\(\w+\.crossOrigin=\w+\)`)

// pluginCrossOriginPatternRE 匹配 plugin.js 中字幕流 crossOrigin 赋值，整体删除
var pluginCrossOriginPatternRE = regexp.MustCompile(`&&\s*\(elem\.crossOrigin\s*=\s*initialSubtitleStream\)`)

// ============================================================
// 流请求拦截面（对齐 MoviePilot MEDIA_ROUTES）
// ============================================================

// nonMediaNames 与媒体流无关的 name 段，命中则不做流拦截（避免误劫持 API）
var nonMediaNames = map[string]bool{
	"additionalparts": true,
	"subtitles":       true,
	"similar":         true,
	"thememedia":      true,
	"themevideos":     true,
	"themesongs":      true,
	"specialfeatures": true,
	"linkeditems":     true,
}

// mediaRoutePatterns MEDIA_ROUTES 的通用正则，命中返回 itemID（组 1）
// 覆盖 /videos/{id}/{name}、/audio/{id}/{name}（含 /emby/ 前缀）、
// /items/{id}/download|file、/sync/jobitems/{id}/file
var mediaRoutePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^/(?:emby/)?(?:audio|videos)/([^/]+)/[^/]+$`),
	regexp.MustCompile(`^/(?:emby/)?items/([^/]+)/(?:download|file)$`),
	regexp.MustCompile(`^/(?:emby/)?sync/jobitems/([^/]+)/file$`),
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

// strmSourceMeta PlaybackInfo 阶段识别到的单个 STRM 源元数据
type strmSourceMeta struct {
	path      string // STRM URL（http://.../api/strm?...）
	container string // Emby 推断的容器类型（iso/mkv/m2ts/...，可能为空）
	name      string // 文件名（含扩展名，可能为空）
}

// strmCacheEntry PlaybackInfo 阶段识别到的 STRM 源
type strmCacheEntry struct {
	sources map[string]strmSourceMeta // mediaSourceID → meta
	expiry  time.Time
}

// ============================================================
// Proxy 核心结构（三层缓存 + 两个 HTTP client）
// ============================================================

// Proxy Emby 反向代理核心
type Proxy struct {
	embyHost           string
	forceProxyUaTokens []string

	// httpClient 透传给 Emby 的客户端（不跟随重定向）
	httpClient *http.Client
	// followRedirectClient 用于解析重定向链拿最终 CDN URL（跟随所有重定向）
	followRedirectClient *http.Client
	// streamClient 用于代理流（透传 Range 到 STRM 端点，跟随重定向、无超时）
	streamClient *http.Client

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
// forceProxyUaTokens 可选变长参数：仅传一个 []string。未传时视为空白名单（所有客户端强制 DirectPlay）。
func New(embyHost string, forceProxyUaTokens ...[]string) (*Proxy, error) {
	var uaTokens []string
	if len(forceProxyUaTokens) > 0 {
		uaTokens = forceProxyUaTokens[0]
	}
	embyHost = strings.TrimRight(embyHost, "/")
	parsed, err := url.Parse(embyHost)
	if err != nil {
		return nil, fmt.Errorf("invalid emby host %q: %w", embyHost, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid emby host %q: missing http/https scheme", embyHost)
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

	// Client C: 代理流（透传 Range 到 FastStrm 自身 STRM 端点，跟随重定向、无总超时）
	// 用于 ISO/BDMV 等需要 byte-range seek 的格式，避免 302 到 CDN 后 Range 被拒。
	streamClient := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			MaxIdleConns:          128,
			MaxIdleConnsPerHost:   64,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 0,
			DisableCompression:    true,
			Proxy:                 http.ProxyFromEnvironment,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects: %d", len(via))
			}
			return nil
		},
	}

	return &Proxy{
		embyHost:             embyHost,
		forceProxyUaTokens:   uaTokens,
		httpClient:           proxyHTTPClient,
		followRedirectClient: followClient,
		streamClient:         streamClient,
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
	// 分发顺序：JS 修补（crossOrigin）→ 媒体流拦截 → HTML 注入（crossOrigin）→ 透传
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// 1. htmlvideoplayer JS 修补（crossOrigin 拦截，/emby/web/ 与 /web/ 两种挂载）
		if isPatchedJSPath(path) {
			p.servePatchedJS(w, r)
			return
		}

		// 2. 媒体流拦截：
		//    a) 反代自己生成的直链流请求（/videos/{id}/stream 或 /audio/{id}/stream 且 Static=true）
		//    b) MEDIA_ROUTES 通用格式（/videos/{id}/{name}、/items/{id}/download|file 等）
		if isStaticDirectStream(path, r) {
			p.HandleMediaStream(w, r)
			return
		}
		if _, ok := matchMediaRoute(path); ok {
			p.HandleMediaStream(w, r)
			return
		}

		// 3. 可能返回 Emby Web HTML 壳的 GET 请求：整包拉取并注入 crossOrigin 拦截脚本
		if r.Method == http.MethodGet && mayReturnEmbyHTMLShell(path) {
			p.serveHTMLInjected(w, r)
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
	strmMap := make(map[string]strmSourceMeta)

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
				container, _ := ms["Container"].(string)
				// 文件名优先从 STRM URL 解析（对齐 STRM 层 pickOneFileName 兜底逻辑），
				// 避免 Emby Name 字段不带扩展名导致 ISO seek 判断失效
				name := resolveFileNameFromStrmURL(path)
				if name == "" {
					name, _ = ms["Name"].(string)
				}
				strmMap[sid] = strmSourceMeta{path: path, container: container, name: name}
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
				if sid == "" {
					continue
				}
				container, _ := ms["Container"].(string)
				name := resolveFileNameFromStrmURL(path)
				if name == "" {
					name, _ = ms["Name"].(string)
				}
				strmMap[sid] = strmSourceMeta{path: path, container: container, name: name}
			}
		}
	}

	if !isStrm {
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		resp.ContentLength = int64(len(body))
		return nil
	}

	itemID := extractItemID(resp.Request.URL.Path)

	// 对齐参考项目 embyreverseproxy：STRM 源一律强制 DirectPlay（含浏览器）。
	// 原因：STRM 源是带签名/防盗链的 115 CDN 直链，走转码时 Emby ffmpeg 拉流会因
	// UA 不匹配而失败，导致浏览器反复重试 PlaybackInfo 却始终无法开始播放；
	// DirectPlay 由客户端自己拿 302 直链（带正确 UA）拉流，才是可靠路径。
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

	logger.S().Infof("[EmbyProxy] PlaybackInfo 强制 DirectPlay: path=%s, sources=%d", resp.Request.URL.Path, len(strmMap))

	newBody, _ := json.Marshal(data)
	resp.Body = io.NopCloser(strings.NewReader(string(newBody)))
	resp.ContentLength = int64(len(newBody))
	resp.Header.Del("Content-Encoding")
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
		// 对齐参考项目：显式关闭转码能力并删除转码相关字段。
		// STRM 源走转码会让 Emby ffmpeg 直接拉 115 直链（UA 不匹配→失败），
		// 因此必须让所有客户端（含浏览器）都走 DirectPlay 302 直链。
		ms["SupportsTranscoding"] = false
		delete(ms, "TranscodingUrl")
		delete(ms, "TranscodingContainer")
		delete(ms, "TranscodingSubProtocol")

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
// 客户端浏览器识别 + ISO/原盘 seek 格式识别
// ============================================================

// isBrowserClient 判断请求是否来自浏览器/Web 客户端。
// 注意：v1.2.8 起该函数已不再参与 PlaybackInfo 的 DirectPlay 决策——STRM 源一律
// 强制 DirectPlay（含浏览器）。此函数仅保留用于诊断/日志或未来的差异化策略。
func (p *Proxy) isBrowserClient(req *http.Request) bool {
	client := strings.ToLower(strings.TrimSpace(req.Header.Get("X-Emby-Client")))
	ua := strings.ToLower(strings.TrimSpace(req.Header.Get("User-Agent")))

	// 1) 明确的 Web 客户端
	if strings.Contains(client, "web") || strings.Contains(client, "browser") {
		return true
	}

	// 1.5) 没有任何客户端信息时，无法判定为浏览器，按强播放器处理（默认直连）
	if client == "" && ua == "" {
		return false
	}

	// 2) 明确的非 Web 客户端（Infuse/VidHub/SenPlayer/Kodi/Emby 等）
	nonWebClients := []string{"infuse", "vidhub", "senplayer", "senplayerhd", "emby", "kodi", "fileball", "vlc", "mxplayer", "nplayer", "ddplay", "potplayer", "omniplayer", "figplayer", "mpv"}
	for _, c := range nonWebClients {
		if strings.Contains(client, c) {
			return false
		}
	}

	// 3) 兜底：常见浏览器 UA 且没有播放器标识
	if strings.Contains(ua, "mozilla/5.0") {
		return true
	}

	return false
}

// seekRequiredContainers Emby Container 字段中需要 byte-range seek 的容器（原盘/直播流）
var seekRequiredContainers = map[string]bool{
	"iso":    true,
	"bdmv":   true,
	"bdav":   true,
	"dvd":    true,
	"bluray": true,
	"m2ts":   true,
	"ts":     true,
	"vob":    true,
	"ifo":    true,
	"bup":    true,
}

// seekRequiredExts 文件名扩展名中需要 byte-range seek 的格式（含点号，小写）
var seekRequiredExts = map[string]bool{
	".iso":  true,
	".bdmv": true,
	".bdav": true,
	".ts":   true,
	".m2ts": true,
	".vob":  true,
	".ifo":  true,
	".bup":  true,
}

// resolveFileNameFromStrmURL 从 STRM URL 解析文件名，对齐 STRM 层 pickOneFileName 兜底逻辑：
//  1. URL query 参数 file_name（最权威，115 保存时的原始文件名）
//  2. URL path 末段（需含 "." 扩展名才返回，避免 hash 式无意义字符串）
//
// 用途：Emby PlaybackInfo 的 Name 字段经常不带扩展名（如 "movie"），
// 而 ISO/原盘 seek 判断依赖扩展名，故优先从这里解析。
func resolveFileNameFromStrmURL(strmURL string) string {
	if strmURL == "" {
		return ""
	}
	u, err := url.Parse(strmURL)
	if err != nil {
		return ""
	}
	if q := u.Query().Get("file_name"); q != "" {
		return strm.ResolveFileName(q)
	}
	name := pathpkg.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		return ""
	}
	if decoded, derr := url.PathUnescape(name); derr == nil && decoded != "" {
		name = decoded
	}
	// 末段需含 "." 扩展名，避免 115 CDN 那种 hash 型 path 段被误当文件名
	if idx := strings.LastIndexByte(name, '.'); idx > 0 && idx < len(name)-1 {
		return strm.ResolveFileName(name)
	}
	return ""
}

// ============================================================
// crossOrigin 拦截：HTML 注入 + htmlvideoplayer JS 修补
// 对齐 MoviePilot embyreverseproxy _may_return_emby_html_shell /
// _inject_scripts_into_html / _patch_basehtmlplayer_js / _patch_plugin_js
// ============================================================

// mayReturnEmbyHTMLShell 判断路径是否可能返回 Emby Web 的 HTML 壳（用于是否整包拉取并注入脚本）
func mayReturnEmbyHTMLShell(path string) bool {
	pl := strings.ToLower(path)
	if strings.Contains(pl, "playbackinfo") {
		return false
	}
	if path == "/" || path == "" {
		return true
	}
	if strings.HasPrefix(path, "/web/") || path == "/web" {
		return true
	}
	if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".htm") {
		return true
	}
	if strings.HasPrefix(path, "/emby/") || strings.HasPrefix(path, "/items/") ||
		strings.HasPrefix(path, "/videos/") || strings.HasPrefix(path, "/audio/") ||
		strings.HasPrefix(path, "/sync/") {
		return false
	}
	last := path[strings.LastIndex(path, "/")+1:]
	if !strings.Contains(last, ".") {
		return true
	}
	return false
}

// injectScriptsIntoHTML 在 HTML 的 head 中注入 crossOrigin 拦截脚本；
// 已注入（含 marker）则跳过；找不到 </head> 或 <head> 时返回原样。
func injectScriptsIntoHTML(html string) string {
	if strings.Contains(html, crossOriginInterceptMarker) {
		return html
	}
	script := crossOriginInterceptScript
	// 优先 </head>（不区分大小写）
	lower := strings.ToLower(html)
	if idx := strings.Index(lower, "</head>"); idx != -1 {
		return html[:idx] + script + html[idx:]
	}
	// 退而找 <head...>，插入到标签结束符之后
	if idx := strings.Index(lower, "<head"); idx != -1 {
		if gt := strings.Index(html[idx:], ">"); gt != -1 {
			end := idx + gt + 1
			return html[:end] + script + html[end:]
		}
	}
	return html
}

// patchBasehtmlplayerJS 修补 getCrossOriginValue 相关逻辑，使其恒返回 null（不设置 crossorigin）。
// 精确正则未命中但含 getCrossOriginValue 时，退而将 anonymous 字符串替换为 null。
func patchBasehtmlplayerJS(content string) string {
	original := content
	patched := crossOriginValueRE.ReplaceAllString(content, "null")
	if patched != original {
		return patched
	}
	if strings.Contains(original, "getCrossOriginValue") {
		patched = strings.ReplaceAll(original, `"anonymous"`, "null")
		patched = strings.ReplaceAll(patched, `'anonymous'`, "null")
	}
	return patched
}

// patchPluginJS 去除 plugin.js 中的 crossOrigin 赋值，避免 302 重定向时触发 CORS 预检
func patchPluginJS(content string) string {
	patched := pluginCrossOriginRE.ReplaceAllString(content, "")
	patched = pluginCrossOriginPatternRE.ReplaceAllString(patched, "")
	return patched
}

// isPatchedJSPath 判断是否为需要修补的 htmlvideoplayer JS 路径
// （覆盖 /emby/web/ 与 /web/ 两种挂载的 basehtmlplayer.js / plugin.js）
func isPatchedJSPath(path string) bool {
	lower := strings.ToLower(path)
	if !strings.HasSuffix(lower, "basehtmlplayer.js") && !strings.HasSuffix(lower, "plugin.js") {
		return false
	}
	return strings.Contains(lower, "/htmlvideoplayer/")
}

// serveHTMLInjected 对可能返回 Emby Web HTML 壳的 GET 请求整包拉取，
// 注入 crossOrigin 拦截脚本后再返回；body 变化时剥离校验头并禁用缓存。
func (p *Proxy) serveHTMLInjected(w http.ResponseWriter, r *http.Request) {
	target := p.embyHost + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")
	// 去掉 Accept-Encoding，确保上游返回未压缩内容以便修改 body
	req.Header.Del("Accept-Encoding")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] HTML 注入: 上游请求失败: %v", err)
		http.Error(w, fmt.Sprintf("Emby Error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	out := body
	injected := false
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode == http.StatusOK && strings.Contains(ct, "text/html") {
		html := string(body)
		if newHTML := injectScriptsIntoHTML(html); newHTML != html {
			out = []byte(newHTML)
			injected = true
			logger.S().Infof("[EmbyProxy] 已在 HTML 注入 crossOrigin 脚本: path=%s", r.URL.Path)
		}
	}

	for k, vv := range resp.Header {
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] || lk == "content-encoding" || lk == "content-length" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	if injected {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Del("ETag")
		w.Header().Del("Last-Modified")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

// servePatchedJS 拦截 htmlvideoplayer 的 basehtmlplayer.js / plugin.js 并修补后返回
func (p *Proxy) servePatchedJS(w http.ResponseWriter, r *http.Request) {
	target := p.embyHost + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")
	req.Header.Del("Accept-Encoding")

	resp, err := p.followRedirectClient.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] JS 修补: 上游请求失败: %v", err)
		http.Error(w, fmt.Sprintf("Emby Error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// 非 200 透传原样
	if resp.StatusCode != http.StatusOK {
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return
	}

	original := string(body)
	var patched string
	if strings.HasSuffix(r.URL.Path, "basehtmlplayer.js") {
		patched = patchBasehtmlplayerJS(original)
	} else {
		patched = patchPluginJS(original)
	}

	for k, vv := range resp.Header {
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] || lk == "content-encoding" || lk == "content-length" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	if patched != original {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		logger.S().Infof("[EmbyProxy] 已修补 JS: path=%s", r.URL.Path)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(patched))
}

// isSeekRequiredFormat 判断 STRM 源是否需要代理流（而非 302）。
// 依据：Emby 的 Container 字段，或文件名扩展名。
func isSeekRequiredFormat(container, name string) bool {
	if c := strings.ToLower(strings.TrimSpace(container)); c != "" {
		if seekRequiredContainers[c] {
			return true
		}
	}
	if name != "" {
		ext := strings.ToLower(filepath.Ext(name))
		if seekRequiredExts[ext] {
			return true
		}
	}
	return false
}

// proxyStreamToStrm 将媒体流请求透传给 FastStrm 自身 STRM 端点（/api/strm?...），
// 由 STRM handler 层走 proxy 模式转发 Range 到 115 CDN，保证 ISO/原盘 seek 正常。
func (p *Proxy) proxyStreamToStrm(w http.ResponseWriter, r *http.Request, strmURL string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, strmURL, nil)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] proxyStreamToStrm: 构造请求失败: %v", err)
		http.Error(w, "Upstream init failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// 透传客户端 Range/If-Range/UA，让 STRM handler 正确转发 seek 请求
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	if ifr := r.Header.Get("If-Range"); ifr != "" {
		req.Header.Set("If-Range", ifr)
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	upstream, err := p.streamClient.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] proxyStreamToStrm: 上游请求失败: %v", err)
		http.Error(w, "Upstream fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Body.Close()

	// 回写响应头（过滤 hop-by-hop + set-cookie，避免泄漏 115 cookie）
	for k, vv := range upstream.Header {
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] {
			continue
		}
		if lk == "set-cookie" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(upstream.StatusCode)

	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, upstream.Body)
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

	// 1b. strmSourcesCache — PlaybackInfo 阶段缓存的 STRM 源元数据
	meta := strmSourceMeta{}
	if itemID != "" {
		if strmEntry, ok := p.getCachedStrmSources(itemID); ok {
			if sourceID != "" {
				if m, ok2 := strmEntry.sources[sourceID]; ok2 {
					meta = m
					logger.S().Debugf("[EmbyProxy] strmSourcesCache 命中: item=%s source=%s", itemID, sourceID)
				}
			} else if len(strmEntry.sources) > 0 {
				// sourceID 为空（如 /videos/{id}/{name} 直链请求）→ 任取一个 STRM 源
				for _, m := range strmEntry.sources {
					meta = m
					break
				}
				logger.S().Debugf("[EmbyProxy] strmSourcesCache 命中(任选源): item=%s", itemID)
			}
		}
	}

	// 没拿到 STRM URL → 实时 POST PlaybackInfo 查询兜底（对齐参考项目实时查询逻辑）
	if meta.path == "" {
		meta = p.fetchPlaybackInfoFallback(r.Context(), r, itemID, sourceID)
	}

	// 仍拿不到 → 透传到 Emby
	if meta.path == "" {
		p.passthroughToEmby(w, r)
		return
	}

	// ISO/BDMV/M2TS/TS 等需要 byte-range seek 的格式：不走 302，改走代理流。
	// 302 后客户端 Range 直连 CDN，可能因 CDN UA 绑定/签名过期而被拒；
	// 代理流由 FastStrm 服务器转发 Range 到 STRM 端点（STRM handler 层已支持 proxy 转发），保证 seek 正常。
	if isSeekRequiredFormat(meta.container, meta.name) {
		logger.S().Infof("[EmbyProxy] media proxy(seek): item=%s source=%s container=%q name=%q", itemID, sourceID, meta.container, meta.name)
		p.proxyStreamToStrm(w, r, meta.path)
		return
	}

	// ===== 步骤 2: 解析重定向链拿最终 CDN URL =====
	finalURL := p.resolveRedirectChain(r.Context(), meta.path, r, userID)
	if finalURL != meta.path {
		logger.S().Infof("[EmbyProxy] resolveRedirectChain: item=%s %s -> %s", itemID, meta.path, finalURL)
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
func (p *Proxy) cacheStrmSources(itemID string, sources map[string]strmSourceMeta) {
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

// matchMediaRoute 匹配 MEDIA_ROUTES 通用格式，命中返回 (itemID, true)。
// - /stream 后缀由 isStaticDirectStream 分支管辖（需 Static=true），这里不重复拦截，
//   避免非 Static 的转码 URL（浏览器转码等）被误劫持
// - name 段命中 nonMediaNames（如 subtitles/similar）则视为 API 而非媒体流，不拦截
func matchMediaRoute(path string) (string, bool) {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, "/stream") {
		return "", false
	}
	for _, re := range mediaRoutePatterns {
		m := re.FindStringSubmatch(lower)
		if m == nil {
			continue
		}
		segs := strings.Split(strings.TrimRight(lower, "/"), "/")
		last := segs[len(segs)-1]
		if nonMediaNames[last] {
			return "", false
		}
		return m[1], true
	}
	return "", false
}

// isHTTPPath 判断是否为 http/https 直链（STRM 源被 Emby 解析后的形态）
func isHTTPPath(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// extractAPIKey 从请求头提取 Emby API Key：
// 优先 X-Emby-Token，其次 Authorization: MediaBrowser Token="xxx" / Token="xxx"
func extractAPIKey(r *http.Request) string {
	if tk := strings.TrimSpace(r.Header.Get("X-Emby-Token")); tk != "" {
		return tk
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	if idx := strings.Index(auth, "Token="); idx != -1 {
		tk := auth[idx+len("Token="):]
		return strings.Trim(tk, `"' `)
	}
	return ""
}

// fetchPlaybackInfoFallback 缓存 miss 时实时 POST PlaybackInfo 查询兜底。
// 对齐参考项目：POST {emby_host}/Items/{itemID}/PlaybackInfo?X-Emby-Token={api_key}（timeout 10s），
// 遍历 MediaSources 优先 sid 精确匹配；sourceID 为空或未匹配时取第一个 HTTP 源。
func (p *Proxy) fetchPlaybackInfoFallback(ctx context.Context, r *http.Request, itemID, sourceID string) strmSourceMeta {
	if itemID == "" {
		return strmSourceMeta{}
	}
	apiKey := extractAPIKey(r)
	apiURL := fmt.Sprintf("%s/Items/%s/PlaybackInfo?X-Emby-Token=%s",
		p.embyHost, url.PathEscape(itemID), url.QueryEscape(apiKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		return strmSourceMeta{}
	}
	if apiKey != "" {
		req.Header.Set("X-Emby-Token", apiKey)
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] 实时 PlaybackInfo 查询失败: item=%s err=%v", itemID, err)
		return strmSourceMeta{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return strmSourceMeta{}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return strmSourceMeta{}
	}
	var data struct {
		MediaSources []struct {
			Id        string `json:"Id"`
			Path      string `json:"Path"`
			Container string `json:"Container"`
			Name      string `json:"Name"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return strmSourceMeta{}
	}

	buildMeta := func(ms struct {
		Id        string `json:"Id"`
		Path      string `json:"Path"`
		Container string `json:"Container"`
		Name      string `json:"Name"`
	}) strmSourceMeta {
		meta := strmSourceMeta{path: ms.Path, container: ms.Container}
		meta.name = resolveFileNameFromStrmURL(ms.Path)
		if meta.name == "" {
			meta.name = ms.Name
		}
		return meta
	}

	// 优先 sid 精确匹配
	for _, ms := range data.MediaSources {
		if !isHTTPPath(ms.Path) {
			continue
		}
		if sourceID != "" && ms.Id != "" && ms.Id == sourceID {
			logger.S().Infof("[EmbyProxy] 实时 PlaybackInfo 命中: item=%s source=%s", itemID, sourceID)
			return buildMeta(ms)
		}
	}
	// sourceID 为空或未精确匹配 → 取第一个 HTTP 源
	for _, ms := range data.MediaSources {
		if isHTTPPath(ms.Path) {
			logger.S().Infof("[EmbyProxy] 实时 PlaybackInfo 命中(任选源): item=%s", itemID)
			return buildMeta(ms)
		}
	}
	return strmSourceMeta{}
}

// isStaticDirectStream 判断是否为反代自己生成的直链流请求（/videos/ 或 /audio/ 且 Static=true）
func isStaticDirectStream(path string, r *http.Request) bool {
	lower := strings.ToLower(path)
	if !strings.HasPrefix(lower, "/videos/") && !strings.HasPrefix(lower, "/audio/") {
		return false
	}
	return strings.EqualFold(r.URL.Query().Get("Static"), "true")
}

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
