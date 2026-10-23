package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// SetupStatusResponse 首次启动初始化状态。
type SetupStatusResponse struct {
	Required bool `json:"required"`
}

// SetupAdminRequest 创建首个管理员账号。
type SetupAdminRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// SetupAdminResponse 初始化成功后的登录态（与登录响应保持相同字段）。
type SetupAdminResponse struct {
	Message            string `json:"message"`
	Token              string `json:"token"`
	MustChangePassword bool   `json:"mustChangePassword"`
	User               struct {
		Username string `json:"username"`
	} `json:"user"`
}

// SetupStatus GET /api/setup/status
func SetupStatus(w http.ResponseWriter, r *http.Request) {
	httpx.OkJson(w, SetupStatusResponse{Required: config.NeedsSetup()})
}

// SetupAdmin POST /api/setup/admin
func SetupAdmin(issuer *auth.TokenIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !config.NeedsSetup() {
			httpx.WriteJson(w, http.StatusConflict, map[string]string{
				"code":  "SETUP_ALREADY_COMPLETED",
				"error": "管理员账号已初始化",
			})
			return
		}
		if !allowCredentialChange(w, r) {
			return
		}

		var req SetupAdminRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		username := strings.TrimSpace(req.Username)
		if username == "" {
			username = "admin"
		}
		if len([]rune(username)) > 64 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "用户名过长"})
			return
		}
		if len(req.Password) < 8 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "密码至少 8 位"})
			return
		}
		if strings.EqualFold(req.Password, "admin") {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "不能使用默认密码 admin"})
			return
		}

		salt := config.Get().Salt
		hashed := config.HashPassword(salt, req.Password)
		if err := config.InitializeAdmin(username, hashed); err != nil {
			if errors.Is(err, config.ErrAdminAlreadyInitialized) {
				httpx.WriteJson(w, http.StatusConflict, map[string]string{
					"code":  "SETUP_ALREADY_COMPLETED",
					"error": "管理员账号已初始化",
				})
				return
			}
			logger.S().Errorf("[Setup] 保存管理员配置失败: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存管理员配置失败"})
			return
		}
		// 清理旧版本遗留的明文初始密码文件
		_ = os.Remove(filepath.Join(config.Get().Paths.ConfigDir, "initial-admin-password.txt"))
		authLoginGuard.reset(loginClientKey(r))

		token, err := issuer.Sign(username)
		if err != nil {
			logger.S().Errorf("[Setup] JWT sign failed: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "初始化成功，请返回登录页登录"})
			return
		}
		resp := SetupAdminResponse{Message: "初始化成功", Token: token, MustChangePassword: false}
		resp.User.Username = username
		httpx.OkJson(w, resp)
	}
}
