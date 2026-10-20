package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// ClearDeps 清空目录 API 依赖（需要 settings 用于保留扩展名）
type ClearDeps struct {
	SettingsProvider func() *model.Settings
	// AllowedRoots 返回允许清理的本地根目录（任务 TargetPath + 监控映射）。
	// 为空时拒绝清理，避免登录用户对任意目录执行破坏性操作。
	AllowedRoots func() []string
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
			body, err := io.ReadAll(r.Body)
			if err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "read body failed: " + err.Error()})
				return
			}
			if err := json.Unmarshal(body, &req); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
				return
			}
		}
		if req.TargetPath == "" {
			req.TargetPath = r.URL.Query().Get("targetPath")
		}
		if req.TargetPath == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "targetPath required"})
			return
		}

		// Path traversal protection: reject paths with ".." segments
		cleaned := filepath.Clean(req.TargetPath)
		if cleaned != req.TargetPath || strings.Contains(req.TargetPath, "..") {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
			return
		}

		// Resolve to absolute path to ensure it's safe
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
			return
		}
		req.TargetPath = absPath

		if _, err := os.Stat(req.TargetPath); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "targetPath does not exist"})
			return
		}
		if filepath.Dir(req.TargetPath) == req.TargetPath {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "refuse to clear filesystem root"})
			return
		}

		// 只允许任务输出目录或监控映射根目录（及其子目录）。
		allowedRoot := ""
		if deps.AllowedRoots != nil {
			for _, root := range deps.AllowedRoots() {
				root = strings.TrimSpace(root)
				if root == "" {
					continue
				}
				absRoot, aerr := filepath.Abs(root)
				if aerr != nil {
					continue
				}
				if pathWithinRoot(absRoot, req.TargetPath) && len(absRoot) > len(allowedRoot) {
					allowedRoot = absRoot
				}
			}
		}
		if allowedRoot == "" {
			httpx.WriteJson(w, http.StatusForbidden, map[string]string{
				"error": "targetPath 不在任务输出目录或生活监控映射范围内，已拒绝清理",
			})
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
		trashDir := strmutil.TrashSessionDir(allowedRoot)
		// 第一遍：把非保留扩展名的文件移入回收站，而不是永久删除
		err = filepath.Walk(req.TargetPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				logger.S().Warnf("[clearDir] walk error at %s: %v", p, err)
				return nil
			}
			if info.IsDir() {
				if info.Name() == strmutil.TrashDirName {
					return filepath.SkipDir
				}
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
			if _, terr := strmutil.MoveToTrash(p, allowedRoot, trashDir); terr != nil {
				logger.S().Warnf("[clearDir] trash %s failed: %v", p, terr)
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
			"trashDir":   trashDir,
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
				logger.S().Warnf("[clearDir] read dir %s failed: %v", p, err)
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

// normalizeExt 确保扩展名前有 "."（实现在 pkg/strmutil，task 侧共用同一份）
func normalizeExt(ext string) string { return strmutil.NormalizeExt(ext) }
