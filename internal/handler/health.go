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

// appVersion 通过 ldflags 在构建时注入；未注入时返回 dev，避免源码兜底版本漂移。
// 启动时 main 会调用 SetVersion 覆盖；release 构建仍可通过 ldflags 直接覆盖 appVersion。
var appVersion = "dev"

// SetVersion 由 main 在启动时注入当前构建版本，保证本地构建的 /api/health
// 与启动日志一致；release 构建仍可通过 ldflags 直接覆盖 appVersion。
func SetVersion(v string) {
	if v != "" {
		appVersion = v
	}
}

// Health 健康检查
func Health(w http.ResponseWriter, r *http.Request) {
	httpx.OkJson(w, HealthResponse{
		Status:  "ok",
		Version: appVersion,
	})
}
