// Package web 提供 Templ 渲染的 Web UI 页面与静态资源服务
// 静态资源通过 embed.FS 嵌入二进制，运行时无外部文件依赖
//
// 路由严格对齐原始 Next.js 前端（frontend/src/app/下的目录名）：
//   /login  /account  /task  /settings
//   /account-alerts  /tg-notify  /emby-notify
//   /history  /life-events
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/auth"
)

//go:embed all:static
var staticFS embed.FS

// StaticFS 返回静态资源子树（去除 "static/" 前缀）
func StaticFS() fs.FS {
	sub, _ := fs.Sub(staticFS, "static")
	return sub
}

// StaticHandler 暴露 /static/* 静态资源
func StaticHandler() http.HandlerFunc {
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(StaticFS())))
	return func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	}
}

// WebDeps Web 页面 handler 依赖
type WebDeps struct {
	TokenIssuer *auth.TokenIssuer
}

// ---------------- 公开页面 ----------------

// HandleLoginPage GET /login
func HandleLoginPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		LoginPage().Render(r.Context(), w)
	}
}

// HandleIndex GET / → 登录后默认跳到账户页（与侧边栏第一个菜单项一致）
func HandleIndex(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromRequest(r)
		if token != "" && deps.TokenIssuer != nil {
			if _, err := deps.TokenIssuer.Parse(token); err == nil {
				http.Redirect(w, r, "/account", http.StatusSeeOther)
				return
			}
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// ---------------- 主菜单（activePath 用于侧边栏高亮）----------------

// HandleAccountsPage GET /account
func HandleAccountsPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		AccountsPage().Render(r.Context(), w)
	}
}

// HandleTasksPage GET /task
func HandleTasksPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		TasksPage().Render(r.Context(), w)
	}
}

// HandleSettingsPage GET /settings
func HandleSettingsPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		SettingsPage().Render(r.Context(), w)
	}
}

// ---------------- 通知（侧边栏第 2 组）----------------

// HandleAccountAlertsPage GET /account-alerts
func HandleAccountAlertsPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		AccountAlertsPage().Render(r.Context(), w)
	}
}

// HandleTgNotifyPage GET /tg-notify
func HandleTgNotifyPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		TgNotifyPage().Render(r.Context(), w)
	}
}

// HandleEmbyNotifyPage GET /emby-notify
func HandleEmbyNotifyPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		EmbyNotifyPage().Render(r.Context(), w)
	}
}

// ---------------- 日志（侧边栏第 3 组）----------------

// HandleTaskHistoryPage GET /history
func HandleTaskHistoryPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		TaskHistoryPage().Render(r.Context(), w)
	}
}

// HandleLifeEventsPage GET /life-events
func HandleLifeEventsPage(deps WebDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		LifeEventsPage().Render(r.Context(), w)
	}
}

// ---------------- 辅助 ----------------

// tokenFromRequest 从 Cookie 或 Authorization 头提取 token
func tokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie("token"); err == nil && c.Value != "" {
		return c.Value
	}
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	if strings.HasPrefix(authHeader, "Bearer ") {
		return authHeader[7:]
	}
	return authHeader
}

// AuthRequiredMiddleware 对页面进行 JWT 校验
func AuthRequiredMiddleware(issuer *auth.TokenIssuer) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := tokenFromRequest(r)
			if token == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			claims, err := issuer.Parse(token)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			r.Header.Set("X-User", claims.Username)
			next(w, r)
		}
	}
}
