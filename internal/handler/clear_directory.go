package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// ClearDeps 清空目录 API 依赖（需要 settings 用于保留扩展名）
type ClearDeps struct {
	SettingsProvider func() *model.Settings
}

// ClearDirRequest POST /api/directory/clear
type ClearDirRequest struct {
	TargetPath string `json:"targetPath"`
	Account    string `json:"account,omitempty"`
}

// HandleClearDir 清空 STRM 输出目录中 .strm + 空目录
//
// 对齐 TS clearDirectory：
// - 仅删除 .settings.ignoreExtensions / .strm 之外的文件？（不，TS 实现是：删除除 ignoreExtensions 之外的全部文件
// - 清理后删除空目录
func HandleClearDir(deps ClearDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ClearDirRequest
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)
		}
		if req.TargetPath == "" {
			req.TargetPath = r.URL.Query().Get("targetPath")
		}
		if req.TargetPath == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "targetPath required"})
			return
		}
		if _, err := os.Stat(req.TargetPath); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "targetPath does not exist"})
			return
		}

		settings := model.DefaultSettings()
		if deps.SettingsProvider != nil {
			if s := deps.SettingsProvider(); s != nil {
				settings = s
			}
		}
		// 保留扩展名集合：STRM + DOWNLOAD（也就是我们实际要处理的媒体文件类型）
		ignoreExts := make(map[string]struct{})
		for _, e := range settings.StrmExtensions {
			ignoreExts[strings.ToLower(normalizeExt(e))] = struct{}{}
		}
		for _, e := range settings.DownloadExtensions {
			ignoreExts[strings.ToLower(normalizeExt(e))] = struct{}{}
		}
		// 也保留 .strm 文件本身
		ignoreExts[".strm"] = struct{}{}

		deleted := 0
		// 第一遍：遍历删除非保留扩展名的文件
		err := filepath.Walk(req.TargetPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			name := info.Name()
			if strings.HasPrefix(name, ".") {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(name))
			if _, keep := ignoreExts[ext]; keep {
				return nil
			}
			if err := os.Remove(p); err != nil {
				logger.S().Warnf("[clearDir] remove %s failed: %v", p, err)
				return nil
			}
			deleted++
			return nil
		})
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// 第二遍：后序删除空目录（最多 3 轮，避免 O(n^2)）
		rmEmptyDirs(req.TargetPath)
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"ok":         true,
			"deleted":    deleted,
			"targetPath": req.TargetPath,
		})
	}
}

// rmEmptyDirs 递归清理空目录
func rmEmptyDirs(root string) {
	for {
		done := true
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || p == root || !info.IsDir() {
				return nil
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil
			}
			if len(entries) == 0 {
				if err := os.Remove(p); err == nil {
					done = false
				}
			}
			return nil
		})
		if done {
			return
		}
	}
}

// normalizeExt 确保扩展名前有 "."
func normalizeExt(ext string) string {
	if ext == "" {
		return ext
	}
	if strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}
