package middleware

import (
	"net/http"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// JWTMiddleware JWT 鉴权中间件
// 对齐 frontend/src/middleware.ts 的路由分组逻辑：
//   - 公开路由：/api/auth, /api/health, /api/strm, /api/emby/webhook, /api/notify/webhook
//   - 受保护路由：其他 /api/* 路由需要 JWT
//
// 使用方式：
//
//	server.AddRoutes(routes, rest.WithMiddleware(middleware.JWTMiddleware(issuer)))
func JWTMiddleware(issuer *auth.TokenIssuer) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// 从 Authorization 头提取 token
			authHeader := r.Header.Get("Authorization")
			token := extractToken(authHeader)
			if token == "" {
				httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{
					"error": "未授权：缺少 token",
				})
				return
			}

			// 校验 token
			claims, err := issuer.Parse(token)
			if err != nil {
				httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{
					"error": "token 无效或已过期",
				})
				return
			}

			// 将用户名注入请求头，供后续 handler 使用
			r.Header.Set("X-User", claims.Username)
			next(w, r)
		}
	}
}

// extractToken 从 Authorization 头提取 token
// 支持 "Bearer <token>" 和纯 token 两种格式
func extractToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	// Bearer <token>
	if strings.HasPrefix(authHeader, "Bearer ") {
		return authHeader[7:]
	}
	return authHeader
}
