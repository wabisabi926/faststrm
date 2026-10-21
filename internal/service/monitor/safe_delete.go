package monitor

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// deleteManagedStrmTree 删除目录树内已托管的 STRM 及关联文件，移入回收站。
// 未托管的 STRM、真实媒体文件和其他用户文件一律保留；不再使用 os.RemoveAll。
func (m *Monitor) deleteManagedStrmTree(ctx context.Context, account, dir string) (int, error) {
	if strings.TrimSpace(dir) == "" {
		return 0, errors.New("safe delete: empty dir")
	}
	if m.managed == nil {
		return 0, errors.New("safe delete: managed recorder unavailable")
	}
	config := m.settingsFn()
	root := mappingRootForLocalPath(dir, config.PathMappings, account)
	if root == "" {
		return 0, errors.New("safe delete: no matching mapping root")
	}
	trashDir := strmutil.TrashSessionDir(root)

	var strmPaths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == strmutil.TrashDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".strm") {
			strmPaths = append(strmPaths, p)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	moved := 0
	for _, strmPath := range strmPaths {
		ref, refErr := m.managed.CloudRef(ctx, strmPath)
		if refErr != nil || ref == nil {
			logger.S().Infof("[Monitor] 安全删除跳过未托管 STRM: %s", strmPath)
			continue
		}
		pc, _ := strmutil.ExtractPickcode(strmPath)
		if pc == "" {
			pc = ref.Pickcode
		}
		if pc == "" {
			logger.S().Infof("[Monitor] 安全删除跳过无法解析 pickcode 的 STRM: %s", strmPath)
			continue
		}
		ok, verifyErr := m.managed.VerifyFile(ctx, strmPath, pc)
		if verifyErr != nil || !ok {
			logger.S().Infof("[Monitor] 安全删除跳过指纹已变化的 STRM: %s", strmPath)
			continue
		}
		dest, moveErr := strmutil.MoveToTrash(strmPath, root, trashDir)
		if moveErr != nil {
			logger.S().Warnf("[Monitor] 移入回收站失败: %s err=%v", strmPath, moveErr)
			continue
		}
		if dest != "" {
			moved++
			strmutil.MoveRelatedFilesToTrash(strmPath, root, trashDir, strmutil.DefaultRelatedExts)
		}
	}
	if moved > 0 {
		removeEmptyParents(dir, config.PathMappings)
	}
	return moved, nil
}

// mappingRootForLocalPath 找到包含 target 的最长映射根目录。
func mappingRootForLocalPath(target string, mappings []model.MonitorPathMapping, account string) string {
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return ""
	}
	best := ""
	for _, mp := range mappings {
		if mp.LocalPath == "" {
			continue
		}
		if mp.Account != "" && account != "" && mp.Account != account {
			continue
		}
		absRoot, aerr := filepath.Abs(filepath.Clean(mp.LocalPath))
		if aerr != nil {
			continue
		}
		rel, rerr := filepath.Rel(absRoot, absTarget)
		if rerr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if len(absRoot) > len(best) {
				best = absRoot
			}
		}
	}
	return best
}
