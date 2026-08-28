// Package server Swagger 文档端点
// swagger.json 从 routes.go 自动生成：node scripts/gen_swagger.js
package server

import (
	_ "embed"
	"net/http"

	"github.com/zeromicro/go-zero/rest"

	"github.com/wabisabi926/faststrm/internal/server/middleware"
)

//go:embed swagger.json
var swaggerJSON []byte

//go:embed swagger_ui.html
var swaggerUIHTML []byte

// SwaggerUIHandler 返回内嵌的 Swagger UI 页面
func SwaggerUIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(swaggerUIHTML)
}

// SwaggerJSONHandler 返回 swagger.json
func SwaggerJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(swaggerJSON)
}

// RegisterDocs 注册 API 文档路由（公开，无需 JWT）
func RegisterDocs(s *rest.Server) {
	s.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/docs", Handler: middleware.CORS(SwaggerJSONHandler)},
		{Method: http.MethodGet, Path: "/api/docs/ui", Handler: middleware.CORS(SwaggerUIHandler)},
	})
}
