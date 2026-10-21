package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

const trashListLimit = 500

// TrashEntry 回收站中的一个可恢复文件。
type TrashEntry struct {
	TrashPath    string `json:"trashPath"`
	OriginalPath string `json:"originalPath"`
	Root         string `json:"root"`
	Batch        string `json:"batch"`
	Size         int64  `json:"size"`
	ModTime      int64  `json:"modTime"`
	IsStrm       bool   `json:"isStrm"`
}

type TrashListResponse struct {
	Entries   []TrashEntry `json:"entries"`
	Total     int          `json:"total"`
	Roots     []string     `json:"roots"`
	Truncated bool         `json:"truncated,omitempty"`
}

type TrashRestoreRequest struct {
	LocalPath string `json:"localPath"`
	TrashPath string `json:"trashPath"`
	Overwrite bool   `json:"overwrite"`
}

type TrashRestoreResponse struct {
	RestoredPath string `json:"restoredPath"`
	Message      string `json:"message"`
}

// HandleStrmTrashListGET 列出回收站条目。
// GET /api/strmCleanup/trash?localPath=...
func HandleStrmTrashListGET(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		roots := allowedCleanupRoots(settings, deps)
		if q := strings.TrimSpace(r.URL.Query().Get("localPath")); q != "" {
			root, rerr := validateCleanupRoot(q, roots)
			if rerr != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": rerr.Error()})
				return
			}
			roots = []string{root}
		}

		seen := make(map[string]struct{})
		uniqueRoots := make([]string, 0, len(roots))
		entries := make([]TrashEntry, 0)
		truncated := false
		for _, root := range roots {
			root = filepath.Clean(root)
			if _, ok := seen[root]; ok {
				continue
			}
			seen[root] = struct{}{}
			uniqueRoots = append(uniqueRoots, root)
			found, trunc, lerr := listTrashEntries(root, trashListLimit-len(entries))
			if lerr != nil {
				logger.S().Warnf("[trash] list %s: %v", root, lerr)
				continue
			}
			entries = append(entries, found...)
			truncated = truncated || trunc
			if len(entries) >= trashListLimit {
				truncated = true
				break
			}
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].ModTime == entries[j].ModTime {
				return entries[i].OriginalPath < entries[j].OriginalPath
			}
			return entries[i].ModTime > entries[j].ModTime
		})
		httpx.WriteJson(w, http.StatusOK, TrashListResponse{
			Entries: entries, Total: len(entries), Roots: uniqueRoots, Truncated: truncated,
		})
	}
}

// HandleStrmTrashRestorePOST 把回收站文件恢复到原路径。
// POST /api/strmCleanup/trash/restore
func HandleStrmTrashRestorePOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeTrashRestoreRequest(r)
		if err != nil {
			writeTrashRestoreError(w, err)
			return
		}
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		roots := allowedCleanupRoots(settings, deps)
		if len(roots) == 0 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "没有可用的清理映射"})
			return
		}
		absTrash, root, err := resolveTrashRestoreTarget(req, roots)
		if err != nil {
			writeTrashRestoreError(w, err)
			return
		}
		original, err := restoreTrashEntry(absTrash, root, req.Overwrite)
		if err != nil {
			writeTrashRestoreError(w, err)
			return
		}
		registerRestoredManagedSTRM(r.Context(), deps, original)
		removeEmptyTrashDirs(absTrash)

		logger.S().Infof("[trash] restored %s -> %s", absTrash, original)
		httpx.WriteJson(w, http.StatusOK, TrashRestoreResponse{RestoredPath: original, Message: "已恢复"})
	}
}

type trashRestoreError struct {
	status int
	msg    string
}

func (e *trashRestoreError) Error() string { return e.msg }

func newTrashRestoreError(status int, msg string) error {
	return &trashRestoreError{status: status, msg: msg}
}

func writeTrashRestoreError(w http.ResponseWriter, err error) {
	var re *trashRestoreError
	if errors.As(err, &re) {
		httpx.WriteJson(w, re.status, map[string]string{"error": re.msg})
		return
	}
	httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func decodeTrashRestoreRequest(r *http.Request) (TrashRestoreRequest, error) {
	var req TrashRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, newTrashRestoreError(http.StatusBadRequest, "请求格式错误")
	}
	if strings.TrimSpace(req.TrashPath) == "" {
		return req, newTrashRestoreError(http.StatusBadRequest, "trashPath 不能为空")
	}
	return req, nil
}

func resolveTrashRestoreTarget(req TrashRestoreRequest, roots []string) (absTrash, root string, err error) {
	absTrash, err = filepath.Abs(req.TrashPath)
	if err != nil {
		return "", "", newTrashRestoreError(http.StatusBadRequest, "trashPath 无效")
	}
	if strings.TrimSpace(req.LocalPath) != "" {
		root, err = validateCleanupRoot(req.LocalPath, roots)
		if err != nil {
			return "", "", newTrashRestoreError(http.StatusBadRequest, err.Error())
		}
		if !pathWithinRoot(root, absTrash) {
			return "", "", newTrashRestoreError(http.StatusBadRequest, "trashPath 不在指定映射根内")
		}
		return absTrash, root, nil
	}
	for _, candidate := range roots {
		if pathWithinRoot(candidate, absTrash) {
			return absTrash, candidate, nil
		}
	}
	return "", "", newTrashRestoreError(http.StatusBadRequest, "trashPath 不在已配置的清理映射内")
}

func restoreTrashEntry(absTrash, root string, overwrite bool) (string, error) {
	info, err := os.Lstat(absTrash)
	if err != nil {
		if os.IsNotExist(err) {
			return "", newTrashRestoreError(http.StatusNotFound, "回收站文件不存在")
		}
		return "", newTrashRestoreError(http.StatusInternalServerError, err.Error())
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", newTrashRestoreError(http.StatusBadRequest, "只能恢复普通文件")
	}
	if !trashEntryKindAllowed(absTrash) {
		return "", newTrashRestoreError(http.StatusBadRequest, "只允许恢复 .strm 或关联媒体文件")
	}
	original, _, err := deriveTrashOriginal(root, absTrash)
	if err != nil {
		return "", newTrashRestoreError(http.StatusBadRequest, err.Error())
	}
	if err := strmutil.EnsureNoSymlinkComponents(root, absTrash); err != nil {
		return "", newTrashRestoreError(http.StatusBadRequest, err.Error())
	}
	if err := strmutil.EnsureNoSymlinkComponents(root, original); err != nil {
		return "", newTrashRestoreError(http.StatusBadRequest, err.Error())
	}
	if _, statErr := os.Lstat(original); statErr == nil {
		if !overwrite {
			return "", newTrashRestoreError(http.StatusConflict, "原路径已存在文件，拒绝覆盖")
		}
		// 覆盖前把现有文件也移入回收站，保持可逆
		if _, terr := strmutil.MoveToTrash(original, root, ""); terr != nil {
			return "", newTrashRestoreError(http.StatusInternalServerError, "现有文件移入回收站失败: "+terr.Error())
		}
	}
	if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
		return "", newTrashRestoreError(http.StatusInternalServerError, "创建原目录失败: "+err.Error())
	}
	if err := os.Rename(absTrash, original); err != nil {
		return "", newTrashRestoreError(http.StatusInternalServerError, "恢复失败: "+err.Error())
	}
	return original, nil
}

func registerRestoredManagedSTRM(ctx context.Context, deps StrmCleanupDeps, original string) {
	if deps.ManagedStrm == nil || !strings.EqualFold(filepath.Ext(original), ".strm") {
		return
	}
	content, err := os.ReadFile(original)
	if err != nil {
		return
	}
	account, pickcode := strm.ParseManagedContent(string(content))
	if account == "" || pickcode == "" {
		return
	}
	if err := deps.ManagedStrm.Record(ctx, strm.ManagedInput{
		LocalPath: original, Account: account, Pickcode: pickcode,
		Content: string(content), Source: "trash-restore",
	}); err != nil {
		logger.S().Warnf("[trash] 恢复后托管登记失败 %s: %v", original, err)
	}
}

func removeEmptyTrashDirs(absTrash string) {
	batchDir := filepath.Dir(absTrash)
	_ = os.Remove(batchDir)
	trashDir := filepath.Dir(batchDir)
	if filepath.Base(trashDir) == strmutil.TrashDirName {
		_ = os.Remove(trashDir)
	}
}

func trashEntryKindAllowed(p string) bool {
	if strings.EqualFold(filepath.Ext(p), ".strm") {
		return true
	}
	return strmutil.IsRelatedAssetPath(p)
}

// deriveTrashOriginal 从回收站路径反推原路径。
// 布局：<base>/.faststrm-trash/<batch>/<relativeOriginalPath>
func deriveTrashOriginal(root, trashPath string) (original, batch string, err error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	absTrash, err := filepath.Abs(trashPath)
	if err != nil {
		return "", "", err
	}
	if !pathWithinRoot(absRoot, absTrash) {
		return "", "", fmt.Errorf("回收站路径不在映射根内")
	}
	marker := string(os.PathSeparator) + strmutil.TrashDirName + string(os.PathSeparator)
	idx := strings.LastIndex(absTrash, marker)
	if idx < 0 {
		return "", "", fmt.Errorf("不是回收站路径")
	}
	base := absTrash[:idx]
	rest := absTrash[idx+len(marker):]
	parts := strings.Split(rest, string(os.PathSeparator))
	if len(parts) < 2 {
		return "", "", fmt.Errorf("回收站路径缺少批次或文件")
	}
	batch = parts[0]
	rel := filepath.Join(parts[1:]...)
	if rel == "" || rel == "." {
		return "", "", fmt.Errorf("回收站路径缺少原文件名")
	}
	original = filepath.Join(base, rel)
	if !pathWithinRoot(absRoot, original) {
		return "", "", fmt.Errorf("还原路径越出映射根")
	}
	return original, batch, nil
}

func listTrashEntries(root string, limit int) ([]TrashEntry, bool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = trashListLimit
	}
	entries := make([]TrashEntry, 0)
	truncated := false
	walkErr := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() || d.Name() != strmutil.TrashDirName {
			return nil
		}
		batches, rerr := os.ReadDir(p)
		if rerr != nil {
			return filepath.SkipDir
		}
		for _, batch := range batches {
			if !batch.IsDir() {
				continue
			}
			batchDir := filepath.Join(p, batch.Name())
			_ = filepath.WalkDir(batchDir, func(fp string, fd fs.DirEntry, ferr error) error {
				if ferr != nil || fd.IsDir() || fd.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if !trashEntryKindAllowed(fp) {
					return nil
				}
				original, batchName, derr := deriveTrashOriginal(absRoot, fp)
				if derr != nil {
					return nil
				}
				info, ierr := fd.Info()
				if ierr != nil {
					return nil
				}
				entries = append(entries, TrashEntry{
					TrashPath:    fp,
					OriginalPath: original,
					Root:         absRoot,
					Batch:        batchName,
					Size:         info.Size(),
					ModTime:      info.ModTime().UnixMilli(),
					IsStrm:       strings.EqualFold(filepath.Ext(fp), ".strm"),
				})
				if len(entries) >= limit {
					truncated = true
					return fs.SkipAll
				}
				return nil
			})
			if truncated {
				return fs.SkipAll
			}
		}
		return filepath.SkipDir
	})
	return entries, truncated, walkErr
}
