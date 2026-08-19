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
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Write(data)
			return
		}

		// SPA fallback：所有不匹配的路径返回 index.html
		indexHTML, _ := fs.ReadFile(spaSub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write(indexHTML)
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
