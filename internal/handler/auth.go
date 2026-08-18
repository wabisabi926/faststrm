package handler

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// ==================== Login ====================

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Message string `json:"message"`
	Token   string `json:"token"`
	User    struct {
		Username string `json:"username"`
	} `json:"user"`
}

// Login POST /api/auth/login 密码校验 + JWT 签发
// 支持两种请求格式：
//   1. application/json（前端 API 调用）
//   2. application/x-www-form-urlencoded（HTMX 表单提交）
func Login(issuer *auth.TokenIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req LoginRequest

		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
			// HTMX 表单提交
			if err := r.ParseForm(); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
			req.Username = r.FormValue("username")
			req.Password = r.FormValue("password")
		} else {
			// JSON 提交
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
		}

		cfg := config.Get()
		salt := cfg.Salt

		// 校验用户名 + 密码
		if req.Username != cfg.Admin.Username ||
			!pwdcrypto.VerifyPassword(salt, req.Password, cfg.Admin.Password) {
			httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{"error": "账号或密码错误"})
			return
		}

		// 签发 JWT
		token, err := issuer.Sign(req.Username)
		if err != nil {
			logger.S().Errorf("JWT sign failed: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "令牌生成失败"})
			return
		}

		logger.S().Infof("Login successful, user=%s", req.Username)

		// 设置 token Cookie（用于页面路由自动携带：/ → /dashboard）
		// SameSite=Lax 防止 CSRF，Path=/ 全站有效，Secure 仅 HTTPS 生效
		cookie := &http.Cookie{
			Name:     "token",
			Value:    token,
			Path:     "/",
			HttpOnly: false, // 允许前端 JS 从 cookie 读取（用于后续 API 调用或调试）
			SameSite: http.SameSiteLaxMode,
			MaxAge:   7 * 24 * 3600, // 7 天
		}
		http.SetCookie(w, cookie)

		httpx.OkJson(w, LoginResponse{
			Message: "登录成功",
			Token:   token,
			User: struct {
				Username string `json:"username"`
			}{Username: req.Username},
		})
	}
}

// ==================== Change Password ====================

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ChangePassword POST /api/auth/change-password
func ChangePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChangePasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		if req.CurrentPassword == "" || req.NewPassword == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请填写当前密码和新密码"})
			return
		}

		if len(req.NewPassword) < 6 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "新密码至少 6 位"})
			return
		}

		cfg := config.Get()

		// 验证当前密码
		if !pwdcrypto.VerifyPassword(cfg.Salt, req.CurrentPassword, cfg.Admin.Password) {
			httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{"error": "当前密码错误"})
			return
		}

		// 更新密码
		cfg.Admin.Password = pwdcrypto.HashPassword(cfg.Salt, req.NewPassword)
		if err := config.SaveAdmin(); err != nil {
			logger.S().Errorf("save admin config failed: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}

		httpx.OkJson(w, map[string]string{"message": "密码修改成功"})
	}
}

// ==================== Change Credentials ====================

// ChangeCredentialsRequest 修改凭据请求（用户名+密码）
type ChangeCredentialsRequest struct {
	CurrentPassword  string `json:"currentPassword"`
	NewUsername      string `json:"newUsername"`
	NewPassword      string `json:"newPassword"`
	ConfirmPassword  string `json:"confirmPassword"`
}

var (
	usernameMinLen    = 3
	usernameMaxLen    = 32
	usernameRegex     = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	pureDigitsRegex   = regexp.MustCompile(`^\d+$`)
)

// ChangeCredentials POST /api/auth/change-credentials
func ChangeCredentials() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChangeCredentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		if req.CurrentPassword == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请输入当前密码"})
			return
		}

		cfg := config.Get()

		// 验证当前密码
		if !pwdcrypto.VerifyPassword(cfg.Salt, req.CurrentPassword, cfg.Admin.Password) {
			httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{"error": "当前密码错误"})
			return
		}

		usernameInput := strings.TrimSpace(req.NewUsername)
		passwordInput := req.NewPassword
		confirmInput := req.ConfirmPassword

		var changes []string

		// 处理用户名修改
		if usernameInput != "" {
			if len(usernameInput) < usernameMinLen || len(usernameInput) > usernameMaxLen {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "用户名长度需在 3-32 位之间"})
				return
			}
			if !usernameRegex.MatchString(usernameInput) {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "用户名只能包含字母、数字和下划线，且以字母或下划线开头"})
				return
			}
			if pureDigitsRegex.MatchString(usernameInput) {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "用户名不能为纯数字"})
				return
			}
			if usernameInput == cfg.Admin.Username {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "新用户名不能与当前用户名相同"})
				return
			}
			cfg.Admin.Username = usernameInput
			changes = append(changes, "用户名")
		}

		// 处理密码修改
		if passwordInput != "" {
			if len(passwordInput) < 6 {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "密码长度不能少于 6 位"})
				return
			}
			if passwordInput != confirmInput {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "两次输入的新密码不一致"})
				return
			}
			cfg.Admin.Password = pwdcrypto.HashPassword(cfg.Salt, passwordInput)
			changes = append(changes, "密码")
		}

		if len(changes) == 0 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "未填写任何修改项"})
			return
		}

		if err := config.SaveAdmin(); err != nil {
			logger.S().Errorf("save admin config failed: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}

		httpx.OkJson(w, map[string]string{"message": strings.Join(changes, "、") + "修改成功"})
	}
}

// ==================== Logout ====================

// Logout POST /api/auth/logout
// JWT 无状态，客户端清除 token 即可
func Logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.OkJson(w, map[string]string{"message": "已退出登录"})
	}
}
