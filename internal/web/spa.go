package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed all:spa
var spaFS embed.FS

var spaSub fs.FS

func init() {
	spaSub, _ = fs.Sub(spaFS, "spa")
}

// SPAHandler 返回 Vite 构建的 React SPA。
// 直接从 embed.FS 查找文件，未命中则回退到 index.html。
func SPAHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		// 尝试从 embed.FS 读取文件
		if data, err := fs.ReadFile(spaSub, cleanPath); err == nil {
			w.Header().Set("Content-Type", contentType(cleanPath))
			// index.html 始终不缓存，确保 SPA 入口点始终获取最新版本
			if cleanPath == "index.html" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			// 必须显式写状态码：go-zero 的 notFoundHandler 会把本 handler 包进
			// HeaderOnceResponseWriter，并在其后强制调用 WriteHeader(404)。
			// 该包装器的 Write() 只是转发底层、不会置位 wroteHeader，
			// 若这里只调 Write，收尾的 WriteHeader(404) 会二次写头并打出
			// "superfluous response.WriteHeader call"。显式 WriteHeader(200) 可将其抑制。
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}

		// SPA fallback：所有不匹配的路径返回 index.html
		indexHTML, _ := fs.ReadFile(spaSub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexHTML)
	}
}

func contentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	default:
		return "application/octet-stream"
	}
}
