package middleware

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/config"
)

// RequireSetupComplete 首次启动尚未创建管理员时拦截业务接口。
// 只放行健康检查、静态资源、登录/初始化状态与 /api/setup/*。
func RequireSetupComplete() func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if config.NeedsSetup() {
				httpx.WriteJson(w, http.StatusConflict, map[string]string{
					"code":  "SETUP_REQUIRED",
					"error": "首次启动尚未创建管理员账号，请先完成初始化",
				})
				return
			}
			next(w, r)
		}
	}
}

// RequirePasswordChanged 在仍使用出厂默认口令时拦截所有受保护接口。
// 登录、改密、登出等公开接口不受影响，前端据此展示不可关闭的改密门禁。
func RequirePasswordChanged() func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if config.IsDefaultAdminPassword() {
				httpx.WriteJson(w, http.StatusForbidden, map[string]string{
					"code":  "PASSWORD_CHANGE_REQUIRED",
					"error": "请先修改默认管理员密码",
				})
				return
			}
			next(w, r)
		}
	}
}
