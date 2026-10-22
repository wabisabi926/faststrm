package handler

import (
	"encoding/json"
	"errors"
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
// clearDirError 清空目录时带 HTTP 状态码的错误。
type clearDirError struct {
	status int
	msg    string
}

func (e *clearDirError) Error() string { return e.msg }

func newClearDirError(status int, msg string) error { return &clearDirError{status: status, msg: msg} }

func writeClearDirError(w http.ResponseWriter, err error) {
	var ce *clearDirError
	if errors.As(err, &ce) {
		httpx.WriteJson(w, ce.status, map[string]string{"error": ce.msg})
		return
	}
	httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func parseClearDirRequest(r *http.Request) (ClearDirRequest, error) {
	var req ClearDirRequest
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return req, newClearDirError(http.StatusBadRequest, "read body failed: "+err.Error())
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return req, newClearDirError(http.StatusBadRequest, "invalid json: "+err.Error())
		}
	}
	if req.TargetPath == "" {
		req.TargetPath = r.URL.Query().Get("targetPath")
	}
	if req.TargetPath == "" {
		return req, newClearDirError(http.StatusBadRequest, "targetPath required")
	}
	return req, nil
}

func resolveClearDirTarget(path string) (string, error) {
	// 只拒绝真正的 ".." 路径段；允许结尾分隔符、"D:\\Media\\." 等等价写法。
	if hasDotDotSegment(path) {
		return "", newClearDirError(http.StatusBadRequest, "invalid path")
	}
	cleaned := filepath.Clean(path)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", newClearDirError(http.StatusBadRequest, "invalid path")
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", newClearDirError(http.StatusBadRequest, "targetPath does not exist")
	}
	if filepath.Dir(absPath) == absPath {
		return "", newClearDirError(http.StatusBadRequest, "refuse to clear filesystem root")
	}
	return absPath, nil
}

func hasDotDotSegment(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// resolveClearDirAllowedRoot 只允许任务输出目录或监控映射根目录（及其子目录）。
func resolveClearDirAllowedRoot(target string, allowedRoots func() []string) (string, error) {
	allowedRoot := ""
	if allowedRoots != nil {
		for _, root := range allowedRoots() {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				continue
			}
			if pathWithinRoot(absRoot, target) && len(absRoot) > len(allowedRoot) {
				allowedRoot = absRoot
			}
		}
	}
	if allowedRoot == "" {
		return "", newClearDirError(
			http.StatusForbidden,
			"targetPath 不在任务输出目录或生活监控映射范围内，已拒绝清理",
		)
	}
	return allowedRoot, nil
}

func clearDirSettings(provider func() *model.Settings) *model.Settings {
	if provider != nil {
		if settings := provider(); settings != nil {
			return settings
		}
	}
	return model.DefaultSettings()
}

func clearDirKeepExtensions(settings *model.Settings) map[string]struct{} {
	keep := make(map[string]struct{})
	for _, ext := range settings.EffectiveStrmExtensions() {
		keep[strings.ToLower(normalizeExt(ext))] = struct{}{}
	}
	for _, ext := range settings.DownloadExtensions {
		keep[strings.ToLower(normalizeExt(ext))] = struct{}{}
	}
	keep[".strm"] = struct{}{}
	return keep
}

// trashClearDirFiles 把非保留扩展名的文件移入回收站，并返回数量。
func trashClearDirFiles(target, allowedRoot, trashDir string, settings *model.Settings) (int, error) {
	ignoreExts := clearDirKeepExtensions(settings)
	deleted := 0
	err := filepath.Walk(target, func(p string, info os.FileInfo, err error) error {
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
	return deleted, err
}

func HandleClearDir(deps ClearDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := parseClearDirRequest(r)
		if err != nil {
			writeClearDirError(w, err)
			return
		}
		targetPath, err := resolveClearDirTarget(req.TargetPath)
		if err != nil {
			writeClearDirError(w, err)
			return
		}
		allowedRoot, err := resolveClearDirAllowedRoot(targetPath, deps.AllowedRoots)
		if err != nil {
			writeClearDirError(w, err)
			return
		}
		if err := strmutil.EnsureNoSymlinkComponents(allowedRoot, targetPath); err != nil {
			writeClearDirError(w, newClearDirError(http.StatusForbidden, err.Error()))
			return
		}

		trashDir := strmutil.TrashSessionDir(allowedRoot)
		deleted, err := trashClearDirFiles(targetPath, allowedRoot, trashDir, clearDirSettings(deps.SettingsProvider))
		if err != nil {
			writeClearDirError(w, err)
			return
		}
		// 后序删除空目录（最多 3 轮，避免 O(n^2)）
		rmEmptyDirs(targetPath)
		httpx.OkJson(w, map[string]any{
			"ok":         true,
			"deleted":    deleted,
			"targetPath": targetPath,
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
