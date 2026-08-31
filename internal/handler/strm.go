package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
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

		// 规则引擎
		dr := strm.DecideRoute(r, explicitMode, routeCfg.ForceProxyUaTokens)
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

		// Proxy 并发数限制（按账号）
		if finalDecision == strm.DecisionProxy {
			current := proxyCountNow(accountName)
			if current >= routeCfg.AccountProxyConcurrencyLimit {
				// 尝试用 Bottleneck 兜底（仍拿不到槽位才降级）
				finalDecision = strm.DecisionRedirect
				finalReason = fmt.Sprintf("%s -> proxy_concurrency_limit(%d/%d) fallback_redirect",
					finalReason, current, routeCfg.AccountProxyConcurrencyLimit)
				logger.S().Warnf("[STRM] account=%s proxy concurrency hit limit (%d/%d), fallback to redirect",
					accountName, current, routeCfg.AccountProxyConcurrencyLimit)
			}
		}

		// 限流：proxy 单独的 Bottleneck，确保不会超并发
		if finalDecision == strm.DecisionProxy {
			bn := rate.GetRegistry().GetBottleneck(accountName, rate.TypeProxy, routeCfg.AccountProxyConcurrencyLimit)
			enterCtx, cancel := context.WithTimeout(r.Context(), bottleneckEnterTimeout)
			enterErr := bn.Enter(enterCtx)
			cancel()
			if enterErr != nil {
				finalDecision = strm.DecisionRedirect
				finalReason = fmt.Sprintf("%s -> proxy_bottleneck_timeout fallback_redirect", finalReason)
			} else {
				defer bn.Leave()
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
		logger.S().Infof("[STRM] account=%s pickcode=%s decision=%s reason=%s redirect_check=%s%s elapsed=%dms",
			accountName, shortPc, finalDecision, finalReason, rcs, sizeLog, elapsed)

		// 执行决策
		switch finalDecision {
		case strm.DecisionProxy:
			proxyCountInc(accountName)
			defer proxyCountDec(accountName)
			handleProxy(w, r, cdnURL, account.Cookie, userAgent, pickOneFileName(fileName, meta.FileName))
		default:
			doRedirect(w, pickOneFileName(fileName, meta.FileName), cdnURL)
		}
	}
}

// pickOneFileName 优先用 URL 参数中的 file_name（来自前端），再用 115 解析的文件名
func pickOneFileName(a, b string) string {
	if a != "" {
		return strm.ResolveFileName(a)
	}
	return strm.ResolveFileName(b)
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
