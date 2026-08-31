package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// HealthResponse 健康检查响应
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// appVersion 通过 ldflags 在构建时注入，默认值兜底。
// 默认 v1.2.2：Emby for Kodi Gen 端点 ISO .strm 「没有兼容的流」修复版本。
var appVersion = "v1.2.2"

// Health 健康检查
func Health(w http.ResponseWriter, r *http.Request) {
	httpx.OkJson(w, HealthResponse{
		Status:  "ok",
		Version: appVersion,
	})
}
