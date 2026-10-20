package handler

import (
	"encoding/json"
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
		var req TrashRestoreRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		if strings.TrimSpace(req.TrashPath) == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "trashPath 不能为空"})
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

		absTrash, err := filepath.Abs(req.TrashPath)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "trashPath 无效"})
			return
		}
		var root string
		if strings.TrimSpace(req.LocalPath) != "" {
			root, err = validateCleanupRoot(req.LocalPath, roots)
			if err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if !pathWithinRoot(root, absTrash) {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "trashPath 不在指定映射根内"})
				return
			}
		} else {
			for _, candidate := range roots {
				if pathWithinRoot(candidate, absTrash) {
					root = candidate
					break
				}
			}
			if root == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "trashPath 不在已配置的清理映射内"})
				return
			}
		}

		info, lerr := os.Lstat(absTrash)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "回收站文件不存在"})
				return
			}
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": lerr.Error()})
			return
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "只能恢复普通文件"})
			return
		}
		if !trashEntryKindAllowed(absTrash) {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "只允许恢复 .strm 或关联媒体文件"})
			return
		}

		original, _, derr := deriveTrashOriginal(root, absTrash)
		if derr != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": derr.Error()})
			return
		}
		if _, statErr := os.Lstat(original); statErr == nil {
			if !req.Overwrite {
				httpx.WriteJson(w, http.StatusConflict, map[string]string{"error": "原路径已存在文件，拒绝覆盖"})
				return
			}
			// 覆盖前把现有文件也移入回收站，保持可逆
			if _, terr := strmutil.MoveToTrash(original, root, ""); terr != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "现有文件移入回收站失败: " + terr.Error()})
				return
			}
		}
		if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "创建原目录失败: " + err.Error()})
			return
		}
		if err := os.Rename(absTrash, original); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "恢复失败: " + err.Error()})
			return
		}

		// 恢复后的 STRM 重新登记托管，避免下一轮清理把它当未托管
		if deps.ManagedStrm != nil && strings.EqualFold(filepath.Ext(original), ".strm") {
			if content, rerr := os.ReadFile(original); rerr == nil {
				account, pickcode := strm.ParseManagedContent(string(content))
				if account != "" && pickcode != "" {
					if merr := deps.ManagedStrm.Record(r.Context(), strm.ManagedInput{
						LocalPath: original, Account: account, Pickcode: pickcode,
						Content: string(content), Source: "trash-restore",
					}); merr != nil {
						logger.S().Warnf("[trash] 恢复后托管登记失败 %s: %v", original, merr)
					}
				}
			}
		}

		// 顺手清理空的批次目录 / 回收站目录（非空时 os.Remove 会失败，忽略即可）
		batchDir := filepath.Dir(absTrash)
		_ = os.Remove(batchDir)
		trashDir := filepath.Dir(batchDir)
		if filepath.Base(trashDir) == strmutil.TrashDirName {
			_ = os.Remove(trashDir)
		}

		logger.S().Infof("[trash] restored %s -> %s", absTrash, original)
		httpx.WriteJson(w, http.StatusOK, TrashRestoreResponse{RestoredPath: original, Message: "已恢复"})
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
