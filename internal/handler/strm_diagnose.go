package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/strm"
)

// HandleStrmDiagnose GET /api/strm/diagnose
// 受 JWT 保护：复现 STRM 路由决策并做一次 CDN Range 探测，只返回诊断信息，
// 不返回 CDN URL / Cookie，也不代理大文件。
func HandleStrmDiagnose(opts StrmOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		accountName := strings.TrimSpace(q.Get("account"))
		pickcode := strings.TrimSpace(q.Get("pickcode"))
		fileName := strings.TrimSpace(q.Get("file_name"))
		if accountName == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "缺少 account"})
			return
		}
		if !strm.IsValidPickcode(pickcode) {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "pickcode 无效（需 17 位字母数字）"})
			return
		}
		account := opts.AccountStore.Get(accountName)
		if account == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账号不存在: " + accountName})
			return
		}
		if account.AccountType != "115" || account.Cookie == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "仅支持带 Cookie 的 115 账号"})
			return
		}

		ua := strings.TrimSpace(q.Get("ua"))
		if ua == "" {
			ua = opts.Cfg.Settings.UserAgent
		}
		if ua == "" {
			ua = "Mozilla/5.0"
		}

		meta, err := strm.ResolveDownloadUrl(r.Context(), opts.Client115, pickcode, accountName, account.Cookie, ua)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadGateway, map[string]string{
				"error":   "获取下载直链失败",
				"details": err.Error(),
			})
			return
		}

		routeCfg := strm.StrmRouteConfig(opts.Cfg)
		privateNetwork := strm.IsPrivateNetworkIp(strm.GetClientIp(r))
		explicitMode := ""
		if privateNetwork {
			explicitMode = strings.ToLower(strings.TrimSpace(q.Get("mode")))
		}
		finalName := pickOneFileName(fileName, meta.FileName, meta.URL)
		probeReq := r.Clone(r.Context())
		probeReq.Header.Set("User-Agent", ua)
		decision := strm.DecideRoute(probeReq, explicitMode, routeCfg.ForceProxyUaTokens, finalName, routeCfg.ForceProxySeekFormats)

		finalDecision := decision.Decision
		finalReason := decision.Reason
		redirectCheck := map[string]any{"attempted": false}
		if finalDecision == strm.DecisionRedirect {
			check := strm.RedirectCheck(r.Context(), meta.URL, accountName, account.Cookie, ua, routeCfg.RedirectCheckTimeout)
			redirectCheck = map[string]any{
				"attempted": true,
				"ok":        check.OK,
				"status":    check.Status,
			}
			if !check.OK {
				finalDecision = strm.DecisionProxy
				finalReason += " -> redirect_check_failed fallback_proxy"
			}
		}

		cdn := probeCdnHeaders(r.Context(), meta.URL, account.Cookie, ua)

		// 原盘/seek 格式与分片参数：排查「为什么走了 302 / 为什么没走分片」的关键
		seekFormat := strm.IsSeekFormatName(finalName, routeCfg.ForceProxySeekFormats)
		rangeProxy := "off"
		if !opts.Cfg.Settings.Strm.DisableRangeProxy {
			rangeProxy = fmt.Sprintf("%dMBx%d", opts.Cfg.Settings.Strm.ProxyChunkSizeMB, opts.Cfg.Settings.Strm.ProxyConcurrency)
		}

		httpx.OkJson(w, map[string]any{
			"account":        accountName,
			"pickcode":       strings.ToLower(pickcode),
			"fileName":       finalName,
			"fileSize":       meta.FileSize,
			"userAgent":      ua,
			"clientIp":       strm.GetClientIp(r),
			"privateNetwork": privateNetwork,
			"explicitMode":   explicitMode,
			"decision":       string(finalDecision),
			"reason":         finalReason,
			"redirectCheck":  redirectCheck,
			"cdn":            cdn,
			// 新增：格式与限流上下文
			"seekFormat": seekFormat,
			"rangeProxy": rangeProxy,
			"proxyInUse": proxyCountNow(accountName),
			"proxyLimit": routeCfg.AccountProxyConcurrencyLimit,
			"forceProxy": routeCfg.ForceProxyUaTokens,
		})
	}
}

// probeCdnHeaders 用 Range: bytes=0-0 探测 CDN 响应头；只读最多 2 字节，避免拖流。
func probeCdnHeaders(ctx context.Context, cdnURL, cookie, userAgent string) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnURL, nil)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	req.Header.Set("Accept-Encoding", "identity")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"error": probeErrorMessage(err), "elapsedMs": elapsed}
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 2)
	return map[string]any{
		"status":        resp.StatusCode,
		"contentType":   resp.Header.Get("Content-Type"),
		"contentLength": resp.Header.Get("Content-Length"),
		"contentRange":  resp.Header.Get("Content-Range"),
		"acceptRanges":  resp.Header.Get("Accept-Ranges"),
		"elapsedMs":     elapsed,
	}
}

// probeErrorMessage 去掉 url.Error 中携带的完整 CDN 直链（含签名），只保留底层原因。
func probeErrorMessage(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() || errors.Is(urlErr.Err, context.DeadlineExceeded) {
			return "CDN 探测超时"
		}
		return "CDN 探测失败: " + urlErr.Err.Error()
	}
	return "CDN 探测失败"
}
