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

		// 三档优先级解析文件名（方案 B）：URL file_name > 115 API meta.FileName > CDN URL path 最后一段
		// 115 download API 的 fileName 字段在部分账号/文件类型下为空；CDN URL path 通常包含正确扩展名
		// （例如 /.../杜比视界测试：双层 FEL.iso），因此用它兜底解决 DecideRoute 扩展名识别漏判的致命问题
		finalName := pickOneFileName(fileName, meta.FileName, meta.URL)

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
		logger.S().Infof("[FS/GET] account=%s pickcode=%s decision=%s reason=%s UA=%s method=%s elapsed=%dms",
			accountName, pickcode, finalDecision, finalReason, userAgent, r.Method, elapsed)

		// ===== HEAD 短路径：Lavf ISO probe 第 1 步发 HEAD 探测是否支持 seek =====
		// 为什么不把 HEAD 真实转发给 115 CDN：
		//   115 signed download URL 的 HEAD 请求经常返回 403（签名对 GET 生成、对 HEAD 校验失败）
		//   或 Content-Length 缺失。优先用 download API 返回的 meta.FileSize；当 meta.FileSize == 0
		//   （部分账号/文件类型场景会发生，你现在这台 case 就是 meta.FileSize=0）时，fallback 发
		//   最轻量 Range: bytes=0-0 给 CDN，解析 Content-Range: bytes 0-0/<totalSize> 中的 totalSize
		//   做 Content-Length，全程只传 1 字节 + 响应头，延迟<30ms。
		if r.Method == http.MethodHead {
			h := w.Header()
			h.Set("Accept-Ranges", "bytes")
			size := meta.FileSize
			sizeFrom := "meta"
			if size <= 0 {
				fbT0 := time.Now()
				size = totalSizeFromCdn(meta.URL, account.Cookie, userAgent)
				fbMs := time.Since(fbT0).Milliseconds()
				sizeFrom = "cdn-range0-0"
				logger.S().Infof("[FS/HEAD][fallback] pickcode=%s meta.FileSize=%d -> fallback CDN Range 0-0 size=%d cost=%dms",
					pickcode, meta.FileSize, size, fbMs)
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
			logger.S().Infof("[FS/HEAD] pickcode=%s cl_source=%s size=%d ua=%s", pickcode, sizeFrom, size, userAgent)
			w.WriteHeader(http.StatusOK)
			return
		}

		// 执行决策（GET 路径）
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
