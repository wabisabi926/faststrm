package handler

import (
	"net/http"
	"strconv"

	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
)

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

		account := strm.FindAccount115(opts.AccountStore, accountName)
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
		http.Redirect(w, r, meta.URL, http.StatusFound)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// WriteJSON 辅助：统一写 JSON（导出给其他 handler 用）
func writeJSON(w http.ResponseWriter, status int, v any) {
	httpx.WriteJson(w, status, v)
}
