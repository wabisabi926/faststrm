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

// appVersion 通过 ldflags 在构建时注入，默认值兜底
var appVersion = "0.9.8"

// Health 健康检查
func Health(w http.ResponseWriter, r *http.Request) {
	httpx.OkJson(w, HealthResponse{
		Status:  "ok",
		Version: appVersion,
	})
}
