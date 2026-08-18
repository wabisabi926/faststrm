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

const appVersion = "0.1.0-go-m1"

// Health 健康检查
func Health(w http.ResponseWriter, r *http.Request) {
	httpx.OkJson(w, HealthResponse{
		Status:  "ok",
		Version: appVersion,
	})
}
