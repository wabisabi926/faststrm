// Package embyproxy 实现精简版 Emby 反向代理
// 参考 MoviePilot embyreverseproxy 核心逻辑，仅保留：
//   - PlaybackInfo 拦截：识别 STRM 源 → 强制 DirectPlay
//   - 媒体流路由：STRM 源 → 302 到 CDN 直链
//
// 用法：用户将 Emby 客户端（如 Emby for Kodi gen）连接到 FastStrm 的代理端口，
// 代理自动识别 STRM Item 并改写 PlaybackInfo 响应。
package embyproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

const (
	// CacheTTL PlaybackInfo → MediaSource 映射缓存 TTL
	CacheTTL = 5 * time.Minute
	// MaxCacheSize 最大缓存条目数
	MaxCacheSize = 500
)

// Proxy Emby 反向代理核心
type Proxy struct {
	embyHost   string
	httpClient *http.Client

	// strmSources 缓存 PlaybackInfo 阶段识别到的 STRM 源
	// key: itemID, value: map[mediaSourceID]httpPath
	strmSources   map[string]map[string]string
	strmSourcesMu sync.RWMutex
}

// New 创建 Emby 反向代理
func New(embyHost string) *Proxy {
	embyHost = strings.TrimRight(embyHost, "/")
	return &Proxy{
		embyHost: embyHost,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // 不跟随重定向，让客户端自己 302
			},
		},
		strmSources: make(map[string]map[string]string),
	}
}

// Handler 返回反代 HTTP handler
func (p *Proxy) Handler() http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(mustParseURL(p.embyHost))
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

	return proxy
}

// modifyResponse 拦截关键路由的响应并改写
func (p *Proxy) modifyResponse(resp *http.Response) error {
	path := strings.ToLower(resp.Request.URL.Path)

	// 只拦截 200 OK 的响应
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	// 拦截 PlaybackInfo
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
		// 非 JSON，原样返回
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

	// 强制 DirectPlay
	p.forceDirectPlay(data, resp.Request)

	// 缓存 STRM 源映射
	if itemID := extractItemID(resp.Request.URL.Path); itemID != "" && len(strmMap) > 0 {
		p.cacheStrmSources(itemID, strmMap)
		logger.S().Infof("[EmbyProxy] STRM 缓存: item=%s sources=%d", itemID, len(strmMap))
	}

	newBody, _ := json.Marshal(data)
	resp.Body = io.NopCloser(strings.NewReader(string(newBody)))
	resp.ContentLength = int64(len(newBody))

	// 移除 Content-Encoding，因为我们解码了 JSON
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

		// 强制 DirectPlay 标志
		ms["SupportsDirectPlay"] = true
		ms["SupportsDirectStream"] = true
		ms["SupportsTranscoding"] = false

		// 移除转码相关字段
		for _, key := range []string{"TranscodingUrl", "TranscodingContainer", "TranscodingSubProtocol"} {
			delete(ms, key)
		}

		// 设置 DirectStreamUrl — 指向我们的代理流路由
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

// HandleMediaStream 处理媒体流请求：优先返回 STRM 缓存的 302，否则透传到 Emby
func (p *Proxy) HandleMediaStream(w http.ResponseWriter, r *http.Request) {
	// 尝试从 STRM 缓存获取 302
	itemID := extractItemID(r.URL.Path)
	sourceID := r.URL.Query().Get("MediaSourceId")

	if itemID != "" && sourceID != "" {
		if httpPath, ok := p.getCachedStrmSource(itemID, sourceID); ok {
			logger.S().Infof("[EmbyProxy] STRM 302: item=%s source=%s -> %s", itemID, sourceID, httpPath)
			http.Redirect(w, r, httpPath, http.StatusFound)
			return
		}
	}

	// 透传到 Emby 真实地址
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

	// 复制状态码和头
	for k, v := range resp.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// cacheStrmSources 缓存 STRM MediaSourceId → HTTP URL 映射
func (p *Proxy) cacheStrmSources(itemID string, sources map[string]string) {
	p.strmSourcesMu.Lock()
	defer p.strmSourcesMu.Unlock()

	// 清理过期条目（简单定期清理）
	if len(p.strmSources) >= MaxCacheSize {
		p.strmSources = make(map[string]map[string]string)
	}
	p.strmSources[itemID] = sources
}

// getCachedStrmSource 从缓存获取 STRM 源的 HTTP URL
func (p *Proxy) getCachedStrmSource(itemID, sourceID string) (string, bool) {
	p.strmSourcesMu.RLock()
	defer p.strmSourcesMu.RUnlock()
	sources, ok := p.strmSources[itemID]
	if !ok {
		return "", false
	}
	url, ok := sources[sourceID]
	return url, ok
}

// extractItemID 从 Emby API 路径中提取 Item ID
// 路径格式: /Items/{id}/PlaybackInfo, /Videos/{id}/stream, /Audio/{id}/stream
func extractItemID(path string) string {
	// /Items/{id}/PlaybackInfo -> {id}
	if idx := strings.Index(strings.ToLower(path), "/items/"); idx != -1 {
		rest := path[idx+len("/items/"):]
		if next := strings.Index(rest, "/"); next != -1 {
			return rest[:next]
		}
		return rest
	}
	// /Videos/{id}/stream -> {id}
	if idx := strings.Index(strings.ToLower(path), "/videos/"); idx != -1 {
		rest := path[idx+len("/videos/"):]
		if next := strings.Index(rest, "/"); next != -1 {
			return rest[:next]
		}
		return rest
	}
	// /Audio/{id}/stream -> {id}
	if idx := strings.Index(strings.ToLower(path), "/audio/"); idx != -1 {
		rest := path[idx+len("/audio/"):]
		if next := strings.Index(rest, "/"); next != -1 {
			return rest[:next]
		}
		return rest
	}
	return ""
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("invalid emby host %q: %v", s, err))
	}
	return u
}

// 确保 context 包已导入
var _ = context.TODO
