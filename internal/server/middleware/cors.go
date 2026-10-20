package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// CORS 跨域中间件。
//
// 管理接口只对同源请求回写 CORS 头：管理接口带 Bearer token，不应把
// Access-Control-Allow-Origin 反射给任意站点。播放端点（/api/strm、/api/fs/get）
// 仍需允许任意来源，避免浏览器播放器因跨源被拦。
func CORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowAnyOrigin := isPlaybackPath(r.URL.Path)
		if origin != "" && (allowAnyOrigin || isSameOrigin(origin, r.Host)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Requested-With,X-Internal-Token")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length,Content-Disposition")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func isPlaybackPath(path string) bool {
	return path == "/api/strm" ||
		strings.HasPrefix(path, "/api/strm/") ||
		path == "/api/fs/get" ||
		strings.HasPrefix(path, "/api/fs/get/")
}

func isSameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
