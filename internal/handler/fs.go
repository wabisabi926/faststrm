package handler

import (
	"net/http"
	"strconv"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// encodeRedirectURL 对 302 重定向 URL 进行编码：
// 只编码非 ASCII 字符和空格，保留 URL 保留字符和已有的百分号转义。
// 对齐参考项目 utils/url.py UrlUtils.encode_url_fully:
//
//	Python: quote(url, safe=":/?#@!$&'()*+,;=%")
func encodeRedirectURL(rawURL string) string {
	var sb []byte
	for i := 0; i < len(rawURL); i++ {
		b := rawURL[i]
		switch {
		case b == ' ':
			sb = append(sb, '%', '2', '0')
		case b < 0x80:
			// ASCII 字符（含 % : / ? # @ ! $ & ' ( ) * + , ; =）全部保留
			sb = append(sb, b)
		default:
			// 非 ASCII 字节：百分号编码（UTF-8 多字节逐字节编码）
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

// ==================== /api/fs/get 小文件下载 ====================

// FsGetRequest GET /api/fs/get?account=&pickcode=
type FsGetRequest struct {
	Account  string `json:"account"`
	Pickcode string `json:"pickcode"`
}

// HandleFsGet 处理小文件下载请求（非 STRM，如字幕、nfo、图片）
// 行为：取直链后 302 重定向（不走 proxy，节省带宽）
func HandleFsGet(opts StrmOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		// 直接 302 跳转（fs 文件较小，播放器兼容性好）
		if finalName != "" {
			w.Header().Set("Content-Disposition", strm.BuildContentDisposition(finalName))
		}
		if meta.FileSize > 0 {
			w.Header().Set("X-File-Size", itoa(meta.FileSize))
		}
		encodedURL := encodeRedirectURL(meta.URL)
		logger.S().Infof("[FS/GET] redirect: account=%s pickcode=%s origURL[:200]=%q encodedURL[:200]=%q",
			accountName, pickcode, meta.URL, encodedURL)
		http.Redirect(w, r, encodedURL, http.StatusFound)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// WriteJSON 辅助：统一写 JSON（导出给其他 handler 用）
func writeJSON(w http.ResponseWriter, status int, v any) {
	httpx.WriteJson(w, status, v)
}
