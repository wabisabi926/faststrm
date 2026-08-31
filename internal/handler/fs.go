package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/rate"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

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

// ==================== /api/fs/get 文件下载（含智能路由） ====================

// HandleFsGet 处理文件下载请求
// 行为：小文件 302 重定向；ISO/BDMV 需 seek 的大文件走 proxy 支持 Range
func HandleFsGet(opts StrmOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		q := r.URL.Query()
		accountName := q.Get("account")
		pickcode := q.Get("pickcode")
		fileName := q.Get("file_name")

		if accountName == "" || pickcode == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"error": "account and pickcode are required",
			})
			return
		}
		if !strm.IsValidPickcode(pickcode) {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"error": "Bad pickcode: " + pickcode,
			})
			return
		}

		account := opts.AccountStore.Get(accountName)
		if account == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{
				"error": "Account not found: " + accountName,
			})
			return
		}

		userAgent := r.Header.Get("user-agent")
		if userAgent == "" {
			userAgent = opts.Cfg.Settings.UserAgent
		}

		// 解析直链
		meta, err := strm.ResolveDownloadUrl(r.Context(), opts.Client115, pickcode, accountName, account.Cookie, userAgent)
		if err != nil {
			logger.S().Errorf("[FS/GET] account=%s pickcode=%s resolve url: %v", accountName, pickcode, err)
			httpx.WriteJson(w, http.StatusBadGateway, map[string]string{
				"error": "Failed to get download URL: " + err.Error(),
			})
			return
		}

		finalName := fileName
		if finalName == "" {
			finalName = meta.FileName
		}
		finalName = strm.ResolveFileName(finalName)

		// 智能路由决策
		routeCfg := strm.StrmRouteConfig(opts.Cfg)
		dr := strm.DecideRoute(r, "", routeCfg.ForceProxyUaTokens, finalName)
		finalDecision := dr.Decision
		finalReason := dr.Reason

		// redirect check（仅 redirect 决策时）
		if finalDecision == strm.DecisionRedirect {
			check := strm.RedirectCheck(r.Context(), meta.URL, accountName, account.Cookie, userAgent, routeCfg.RedirectCheckTimeout)
			if !check.OK {
				finalDecision = strm.DecisionProxy
				finalReason = fmt.Sprintf("%s -> redirect_check_failed(%d) fallback_proxy", dr.Reason, check.Status)
			}
		}

		// Proxy 并发数限制
		if finalDecision == strm.DecisionProxy {
			current := proxyCountNow(accountName)
			if current >= routeCfg.AccountProxyConcurrencyLimit {
				finalDecision = strm.DecisionRedirect
				finalReason = fmt.Sprintf("%s -> proxy_concurrency_limit(%d/%d) fallback_redirect",
					finalReason, current, routeCfg.AccountProxyConcurrencyLimit)
			}
		}

		// 限流 Bottleneck
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

		elapsed := time.Since(t0).Milliseconds()
		logger.S().Infof("[FS/GET] account=%s pickcode=%s decision=%s reason=%s UA=%s elapsed=%dms",
			accountName, pickcode, finalDecision, finalReason, userAgent, elapsed)

		// 执行决策
		switch finalDecision {
		case strm.DecisionProxy:
			proxyCountInc(accountName)
			defer proxyCountDec(accountName)
			handleProxy(w, r, meta.URL, account.Cookie, userAgent, finalName)
		default:
			// 302 redirect
			if finalName != "" {
				w.Header().Set("Content-Disposition", strm.BuildContentDisposition(finalName))
			}
			if meta.FileSize > 0 {
				w.Header().Set("X-File-Size", strconv.FormatInt(meta.FileSize, 10))
			}
			http.Redirect(w, r, encodeRedirectURL(meta.URL), http.StatusFound)
		}
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// WriteJSON 辅助：统一写 JSON（导出给其他 handler 用）
func writeJSON(w http.ResponseWriter, status int, v any) {
	httpx.WriteJson(w, status, v)
}
