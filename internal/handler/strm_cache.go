package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// HandleStrmCacheClear POST /api/strm/cache/clear?ua=xxx
// 清理 CDN 直链缓存：ua 参数为空时清空全部，非空时只清理该 UA 对应的缓存
func HandleStrmCacheClear() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ua := r.URL.Query().Get("ua")
		var cleared int
		if ua != "" {
			cleared = strm.InvalidateUACache(ua)
			logger.S().Infof("[STRM/Cache] 清理 UA=%s 的缓存, 清除 %d 条负面缓存", ua, cleared)
		} else {
			cleared = strm.InvalidateAllCache()
			logger.S().Infof("[STRM/Cache] 清空全部缓存, 清除 %d 条直链缓存", cleared)
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"cleared": cleared,
			"ua":      ua,
		})
	}
}
