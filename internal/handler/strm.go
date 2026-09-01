package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/rate"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// ==================== STRM Proxy 连接池 ====================

// Proxy HTTP client 池：单独的 Transport，避免被 redirectCheckClient 共享
var (
	proxyHTTPClientOnce sync.Once
	proxyHTTPClient     *http.Client
)

// 进入 proxy 并发数限流的最大等待时间（Bottleneck.Enter 排队超时）
const bottleneckEnterTimeout = 500 * time.Millisecond

func getProxyHTTPClient() *http.Client {
	proxyHTTPClientOnce.Do(func() {
		proxyHTTPClient = &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:          128,
				MaxIdleConnsPerHost:   64,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 0, // 流式响应不设超时
				DisableCompression:    true,
				Proxy:                 http.ProxyFromEnvironment,
			},
			Timeout: 0, // 长流传输无超时
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return nil // follow redirects
			},
		}
	})
	return proxyHTTPClient
}

// 全局 proxy 并发计数（按账号名）
var (
	proxyCountMu sync.Mutex
	proxyCount   = make(map[string]int)
)

func proxyCountInc(name string) {
	proxyCountMu.Lock()
	proxyCount[name]++
	proxyCountMu.Unlock()
}
func proxyCountDec(name string) {
	proxyCountMu.Lock()
	if n, ok := proxyCount[name]; ok {
		n--
		if n <= 0 {
			delete(proxyCount, name)
		} else {
			proxyCount[name] = n
		}
	}
	proxyCountMu.Unlock()
}
func proxyCountNow(name string) int {
	proxyCountMu.Lock()
	defer proxyCountMu.Unlock()
	return proxyCount[name]
}

// ==================== 核心 Handler ====================

// StrmOptions Handler 所需依赖
type StrmOptions struct {
	Cfg          *config.AppConfig
	Client115    *client115.Client
	AccountStore *store.AccountStore
}

// HandleStrm GET/HEAD /api/strm?account=&pickcode=&file_name=&mode=
// 播放器无 JWT，公开路由
func HandleStrm(opts StrmOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		q := r.URL.Query()
		accountName := q.Get("account")
		pickcode := q.Get("pickcode")
		fileName := q.Get("file_name")
		rawMode := strings.ToLower(q.Get("mode"))

		if accountName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing account"})
			return
		}
		if pickcode == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing pickcode"})
			return
		}
		if !strm.IsValidPickcode(pickcode) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Bad pickcode: " + pickcode})
			return
		}

		// T9: token 签名校验（EnableTokenSigning=true 且 secret 非空才生效，向后兼容）
		if opts.Cfg.Settings.Strm.EnableTokenSigning && opts.Cfg.Settings.Strm.TokenSecret != "" {
			token := q.Get("token")
			ok, reason := strm.VerifyStrmToken(
				opts.Cfg.Settings.Strm.TokenSecret, token, accountName, pickcode)
			if !ok {
				logger.S().Warnf("[STRM] token verify failed: account=%s reason=%s", accountName, reason)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized: " + reason})
				return
			}
		}

		account := opts.AccountStore.Get(accountName)
		if account == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Account not found: " + accountName})
			return
		}

		userAgent := r.Header.Get("user-agent")
		if userAgent == "" {
			userAgent = opts.Cfg.Settings.UserAgent
		}
		logger.S().Infof("[STRM] account=%s pickcode=%s UA=%s mode=%s", accountName, pickcode, userAgent, rawMode)

		routeCfg := strm.StrmRouteConfig(opts.Cfg)

		// 解析直链
		meta, err := strm.ResolveDownloadUrl(r.Context(), opts.Client115, pickcode, accountName, account.Cookie, userAgent)
		if err != nil {
			logger.S().Errorf("[STRM] account=%s failed to get download URL: %v", accountName, err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Failed to get download URL: " + err.Error()})
			return
		}
		cdnURL := meta.URL

		// explicit mode 仅私网生效
		isPrivate := strm.IsPrivateNetworkIp(strm.GetClientIp(r))
		var explicitMode string
		if isPrivate {
			explicitMode = rawMode
		}

		// 规则引擎：三档 finalName — URL file_name(前端最权威) > 115 API 返回的 fileName > CDN URL path
		finalName := pickOneFileName(fileName, meta.FileName, cdnURL)
		dr := strm.DecideRoute(r, explicitMode, routeCfg.ForceProxyUaTokens, finalName)
		finalDecision := dr.Decision
		finalReason := dr.Reason
		var redirectCheckStatus *int

		if finalDecision == strm.DecisionRedirect {
			check := strm.RedirectCheck(r.Context(), cdnURL, accountName, account.Cookie, userAgent, routeCfg.RedirectCheckTimeout)
			s := check.Status
			redirectCheckStatus = &s
			if !check.OK {
				finalDecision = strm.DecisionProxy
				finalReason = fmt.Sprintf("%s -> redirect_check_failed(%d) fallback_proxy", dr.Reason, check.Status)
			}
		}

		// Proxy 并发数限制 + Bottleneck 拿槽位（集中在子函数里处理，降低 HandleStrm 圈复杂度）
		if finalDecision == strm.DecisionProxy {
			applyProxyConcurrency, leaveBn, reason, decision := applyProxyConcurrencyLimit(
				r.Context(), accountName, finalDecision, finalReason,
				routeCfg.AccountProxyConcurrencyLimit)
			if !applyProxyConcurrency {
				finalDecision = decision
				finalReason = reason
			} else if leaveBn != nil {
				defer leaveBn()
			}
		}

		// 日志
		shortPc := pickcode
		if len(pickcode) > 7 {
			shortPc = pickcode[:4] + "…" + pickcode[len(pickcode)-3:]
		}
		sizeLog := ""
		if meta.FileSize > 0 {
			sizeLog = fmt.Sprintf(" size=%d", meta.FileSize)
		}
		rcs := "skipped"
		if redirectCheckStatus != nil {
			rcs = strconv.Itoa(*redirectCheckStatus)
		}
		elapsed := time.Since(t0).Milliseconds()
		logger.S().Infof("[STRM] account=%s pickcode=%s decision=%s reason=%s redirect_check=%s%s method=%s elapsed=%dms",
			accountName, shortPc, finalDecision, finalReason, rcs, sizeLog, r.Method, elapsed)

		// ===== HEAD 短路径：用 meta.FileSize 直接吐 Accept-Ranges + Content-Length 头，不打 CDN =====
		// 115 signed URL HEAD 经常 403 / Content-Length 缺失，download API 的 meta.FileSize 100% 正确。
		// 这条给 Lavf probe 判定 seek 能力，后续 GET Range 206 链路独立工作。
		if r.Method == http.MethodHead {
			writeStrmHeadResponse(w, r, meta.FileSize, cdnURL, account.Cookie, userAgent, finalName, shortPc)
			return
		}

		// 执行决策（GET 路径）
		switch finalDecision {
		case strm.DecisionProxy:
			proxyCountInc(accountName)
			defer proxyCountDec(accountName)
			handleProxy(w, r, cdnURL, account.Cookie, userAgent, finalName)
		default:
			doRedirect(w, finalName, cdnURL)
		}
	}
}

// applyProxyConcurrencyLimit 把 HandleStrm 中 proxy 并发数检查 + Bottleneck 拿槽位两个分支合在一起，
// 降低 HandleStrm 的圈复杂度（cyclop 指标 ≥ 1 的 if/else 会被循环+嵌套快速叠加）。
// 返回：
//   - applyProxyConcurrency: true=继续 proxy；false=已降级 redirect，上层按 decision/reason 覆盖
//   - leaveBn: 非空时上层需 defer leaveBn() 释放 Bottleneck 槽位
func applyProxyConcurrencyLimit(
	rCtx context.Context,
	accountName string,
	decision strm.RouteDecision,
	reason string,
	concurrencyLimit int,
) (applyProxyConcurrency bool, leaveBn func(), outReason string, outDecision strm.RouteDecision) {
	current := proxyCountNow(accountName)
	if current >= concurrencyLimit {
		reason = fmt.Sprintf("%s -> proxy_concurrency_limit(%d/%d) fallback_redirect",
			reason, current, concurrencyLimit)
		logger.S().Warnf("[STRM] account=%s proxy concurrency hit limit (%d/%d), fallback to redirect",
			accountName, current, concurrencyLimit)
		return false, nil, reason, strm.DecisionRedirect
	}
	bn := rate.GetRegistry().GetBottleneck(accountName, rate.TypeProxy, concurrencyLimit)
	enterCtx, cancel := context.WithTimeout(rCtx, bottleneckEnterTimeout)
	enterErr := bn.Enter(enterCtx)
	cancel()
	if enterErr != nil {
		return false, nil, fmt.Sprintf("%s -> proxy_bottleneck_timeout fallback_redirect", reason), strm.DecisionRedirect
	}
	return true, bn.Leave, "", ""
}

// writeStrmHeadResponse 是 HandleStrm 的 HEAD 短路径实现（独立函数以降低 HandleStrm 圈复杂度）。
// meta.FileSize 优先（100% 正确且零延迟），否则 fallback 走 CDN Range bytes=0-0 解析总大小。
// 对客户端只写 200 + Accept-Ranges/Content-Length/CORS/C-Disposition 头，不写 body。
func writeStrmHeadResponse(
	w http.ResponseWriter,
	r *http.Request,
	metaFileSize int64,
	cdnURL, cookie, userAgent, finalName, shortPc string,
) {
	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	size := metaFileSize
	sizeFrom := "meta"
	if size <= 0 {
		// meta.FileSize 为空（部分账号/API 场景会 0），fallback 发最轻量的 Range bytes=0-0，
		// 从 115 CDN 返回的 Content-Range: bytes 0-0/<totalSize> 的 "/<totalSize>" 段解析真正大小。
		// 只传 1 字节 + 响应头，纯内存操作，不写 body 到客户端，延迟<30ms。
		fbT0 := time.Now()
		size = totalSizeFromCdn(cdnURL, cookie, userAgent)
		fbMs := time.Since(fbT0).Milliseconds()
		sizeFrom = "cdn-range0-0"
		logger.S().Infof("[STRM/HEAD][fallback] pc=%s meta.FileSize=%d -> fallback CDN Range 0-0 size=%d cost=%dms",
			shortPc, metaFileSize, size, fbMs)
	}
	if size > 0 {
		h.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if finalName != "" {
		h.Set("Content-Disposition", strm.BuildContentDisposition(finalName))
	}
	origin := r.Header.Get("origin")
	if origin == "" {
		origin = "*"
	}
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Expose-Headers",
		"Content-Disposition, Content-Length, Content-Type, Accept-Ranges, Content-Range")
	logger.S().Infof("[STRM/HEAD] pc=%s cl_source=%s size=%d ua=%s", shortPc, sizeFrom, size, userAgent)
	w.WriteHeader(http.StatusOK)
}

// pickOneFileName 选择文件名，三档优先级：
//  1. a: URL 参数中的 file_name（来自前端/STRM URL 最权威）
//  2. b: 115 download API 返回的 fileName 字段（部分情况下为空）
//  3. cdnURL: 115 CDN URL path 的最后一段（通常包含正确扩展名，如 /.../杜比视界...iso）
//     - 会自动做 URL 解码 + 丢弃 query/fragment
//     - 仅当解析出的文件名带扩展名（存在 "."）时才兜底，避免 "0ea5f97860..." 这种 hash 式无意义字符串误判
func pickOneFileName(a, b, cdnURL string) string {
	if a != "" {
		return strm.ResolveFileName(a)
	}
	if b != "" {
		return strm.ResolveFileName(b)
	}
	if cdnURL == "" {
		return ""
	}
	u, err := neturl.Parse(cdnURL)
	if err != nil || u.Path == "" {
		return ""
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		return ""
	}
	decoded, derr := neturl.PathUnescape(name)
	if derr == nil && decoded != "" {
		name = decoded
	}
	// 仅当扩展名存在时返回（"xxx.iso" 而非 "xxhash" 式纯名称）
	if idx := strings.LastIndexByte(name, '.'); idx > 0 && idx < len(name)-1 {
		return strm.ResolveFileName(name)
	}
	return ""
}

// ==================== doRedirect 302 重定向 ====================

func doRedirect(w http.ResponseWriter, fileName, cdnURL string) {
	if fileName != "" {
		w.Header().Set("Content-Disposition", strm.BuildContentDisposition(fileName))
	}
	http.Redirect(w, &http.Request{Method: http.MethodGet}, encodeRedirectURL(cdnURL), http.StatusFound)
}

// ==================== handleProxy 流式代理 ====================

func handleProxy(
	w http.ResponseWriter,
	r *http.Request,
	cdnURL string,
	cookie string,
	userAgent string,
	fileName string,
) {
	// 构造转发头
	reqHeaders := map[string]string{
		"User-Agent": userAgent,
		"Referer":    "https://115.com/",
		"Origin":     "https://115.com",
	}
	if cookie != "" {
		reqHeaders["Cookie"] = cookie
	}
	if rng := r.Header.Get("range"); rng != "" {
		reqHeaders["Range"] = rng
	}
	if ifr := r.Header.Get("if-range"); ifr != "" {
		reqHeaders["If-Range"] = ifr
	}
	// 禁用上游压缩，否则没法 passthrough Content-Length
	reqHeaders["Accept-Encoding"] = "identity"

	// 建连超时 + 客户端断连传播
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	connTimer := time.AfterFunc(strm.ConnectTimeout, func() {
		cancel(fmt.Errorf("proxy connect timeout"))
	})

	// 客户端断连（使用 Request.Context() 替代已弃用的 http.CloseNotifier）
	go func() {
		<-r.Context().Done()
		cancel(fmt.Errorf("client disconnected: %w", r.Context().Err()))
	}()

	reqCtx, reqCancel := context.WithCancelCause(ctx)
	defer reqCancel(nil)
	req, err := http.NewRequestWithContext(reqCtx, r.Method, cdnURL, nil)
	if err != nil {
		logger.S().Warnf("[STRM][proxy] new request failed: %v", err)
		http.Error(w, "Upstream init failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	for k, v := range reqHeaders {
		req.Header.Set(k, v)
	}

	upstream, err := getProxyHTTPClient().Do(req)
	connTimer.Stop()
	if err != nil {
		logger.S().Warnf("[STRM][proxy] upstream fetch failed: %v", err)
		http.Error(w, "Upstream fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Body.Close()

	// Range 206 / 200 关键头诊断：Lavf ISO probe 对 Content-Range/Content-Length 零容忍，
	// 用 Info 级别一条日志把四个关键字段全部打出来，排错时直接 grep 这一行即可。
	crange := upstream.Header.Get("Content-Range")
	clen := upstream.Header.Get("Content-Length")
	rreq := r.Header.Get("Range")
	if rreq != "" || clen != "" || crange != "" {
		logger.S().Infof("[STRM][proxy-range] status=%d reqRange=%q respCL=%q respCR=%q",
			upstream.StatusCode, rreq, clen, crange)
	}

	// 写回响应头：过滤 hop-by-hop + 安全头部
	respHeader := w.Header()
	for k, vv := range upstream.Header {
		lk := strings.ToLower(k)
		if _, skip := strm.HopByHopHeaders[lk]; skip {
			continue
		}
		if lk == "set-cookie" {
			continue // 不泄漏 115 cookie
		}
		for _, v := range vv {
			respHeader.Add(k, v)
		}
	}
	// 强制覆盖安全/展示头部
	respHeader.Set("Accept-Ranges", "bytes")
	if fileName != "" {
		respHeader.Set("Content-Disposition", strm.BuildContentDisposition(fileName))
	}
	origin := r.Header.Get("origin")
	if origin == "" {
		origin = "*"
	}
	respHeader.Set("Access-Control-Allow-Origin", origin)
	respHeader.Set("Access-Control-Expose-Headers",
		"Content-Disposition, Content-Length, Content-Type, Accept-Ranges, Content-Range")

	w.WriteHeader(upstream.StatusCode)

	// 流式复制：零拷贝
	if r.Method != http.MethodHead {
		written, copyErr := io.Copy(w, upstream.Body)
		if copyErr != nil {
			// 客户端断连是正常现象，降级 debug
			if ne, ok := copyErr.(net.Error); ok && ne.Timeout() {
				logger.S().Debugf("[STRM][proxy] stream copy timeout after %d bytes", written)
			} else if strings.Contains(copyErr.Error(), "client disconnected") ||
				strings.Contains(copyErr.Error(), "broken pipe") {
				logger.S().Debugf("[STRM][proxy] client gone after %d bytes", written)
			} else {
				logger.S().Warnf("[STRM][proxy] stream copy error after %d bytes: %v", written, copyErr)
			}
		}
	}
}

// totalSizeFromCdn 是 HEAD 响应 Content-Length 的兜底 helper：
// 当 115 download API 的 meta.FileSize == 0（部分账号/场景会发生），
// 向 CDN 发极轻量 `Range: bytes=0-0` 请求，解析其 Content-Range: bytes 0-0/<totalSize>
// 中的 "/<totalSize>" 段拿到真实文件总大小。只传输 1 字节响应体 + 头部，<30ms。
// 失败或解析不到时返回 0（上层会跳过 Content-Length 设置，Accept-Ranges 仍然保留）。
func totalSizeFromCdn(cdnURL, cookie, userAgent string) int64 {
	if cdnURL == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnURL, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := getProxyHTTPClient().Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	// 立即丢弃 1 字节 body，避免 keep-alive 连接复用失败
	_, _ = io.CopyN(io.Discard, resp.Body, 2)
	// 解析 Content-Range: bytes 0-0/178323456 → 提取 '/' 之后的 totalSize
	cr := resp.Header.Get("Content-Range")
	if cr == "" {
		return 0
	}
	idx := strings.LastIndexByte(cr, '/')
	if idx < 0 || idx >= len(cr)-1 {
		return 0
	}
	numPart := cr[idx+1:]
	if numPart == "*" {
		return 0
	}
	numPart = strings.TrimSpace(numPart)
	n, perr := strconv.ParseInt(numPart, 10, 64)
	if perr != nil || n <= 0 {
		return 0
	}
	return n
}

// ===== 原 fs.go 的辅助函数（两个端点合并后统一放 strm.go） =====

// encodeRedirectURL 对 302 重定向 URL 进行编码：
// 只编码非 ASCII 字符和空格，保留 URL 保留字符和已有的百分号转义。
func encodeRedirectURL(rawURL string) string {
	var sb []byte
	for i := 0; i < len(rawURL); i++ {
		b := rawURL[i]
		switch {
		case b == ' ':
			sb = append(sb, '%', '2', '0')
		case b < 0x80:
			sb = append(sb, b)
		default:
			sb = append(sb, '%')
			sb = append(sb, hexUpper(b>>4))
			sb = append(sb, hexUpper(b&0x0f))
		}
	}
	return string(sb)
}

// hexUpper 将 0-15 转为大写十六进制字符
func hexUpper(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'A' + n - 10
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// writeJSON 辅助：统一写 JSON（导出给其他 handler 用）
func writeJSON(w http.ResponseWriter, status int, v any) {
	httpx.WriteJson(w, status, v)
}
