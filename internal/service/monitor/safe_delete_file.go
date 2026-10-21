package monitor

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// managedFileDeleteResult 描述一次单文件安全删除的结果。
// SkipReason 非空表示因所有权/指纹/路径不可用而保留文件，调用方应视为“已处理但跳过”，
// 不能返回 error，否则失败事件会阻塞后续事件与游标推进。
type managedFileDeleteResult struct {
	Moved        bool
	TrashPath    string
	RelatedMoved int
	SkipReason   string
}

// trashManagedStrmFile 把已托管的单个 STRM 及其同名关联资源移入回收站。
// expectedCloudPath 非空时额外校验托管记录的云端路径，避免同名兜底命中另一个文件。
func (m *Monitor) trashManagedStrmFile(
	ctx context.Context,
	account, strmPath, expectedCloudPath string,
) (managedFileDeleteResult, error) {
	if m.managed == nil {
		return managedFileDeleteResult{SkipReason: "未启用托管索引"}, nil
	}
	pc, err := strmutil.ExtractPickcode(strmPath)
	if err != nil || pc == "" {
		return managedFileDeleteResult{SkipReason: "未托管或 pickcode 无法解析"}, nil
	}
	ref, err := m.managed.CloudRef(ctx, strmPath)
	if err != nil {
		return managedFileDeleteResult{SkipReason: "托管记录读取失败: " + err.Error()}, nil
	}
	if ref == nil {
		return managedFileDeleteResult{SkipReason: "未托管 STRM"}, nil
	}
	if expectedCloudPath != "" && ref.CloudPath != "" &&
		normalizeCloudPath(ref.CloudPath) != normalizeCloudPath(expectedCloudPath) {
		return managedFileDeleteResult{SkipReason: "云端路径不匹配"}, nil
	}
	ok, verr := m.managed.VerifyFile(ctx, strmPath, pc)
	if verr != nil {
		return managedFileDeleteResult{SkipReason: "托管指纹校验失败: " + verr.Error()}, nil
	}
	if !ok {
		return managedFileDeleteResult{SkipReason: "文件内容或归属已变化"}, nil
	}

	config := m.settingsFn()
	root := mappingRootForLocalPath(filepath.Dir(strmPath), config.PathMappings, account)
	if root == "" {
		return managedFileDeleteResult{SkipReason: "未找到匹配的映射根"}, nil
	}
	trashDir := strmutil.TrashSessionDir(root)
	dest, moveErr := strmutil.MoveToTrash(strmPath, root, trashDir)
	if moveErr != nil {
		return managedFileDeleteResult{}, fmt.Errorf("移入回收站失败: %w", moveErr)
	}
	if dest == "" {
		return managedFileDeleteResult{SkipReason: "文件不存在或已被处理"}, nil
	}
	related := strmutil.MoveRelatedFilesToTrash(strmPath, root, trashDir, strmutil.DefaultRelatedExts)
	if err := m.managed.MarkDeleted(ctx, strmPath); err != nil {
		logger.S().Warnf("[Monitor] 标记托管 STRM 已删除失败 path=%s: %v", strmPath, err)
	}
	return managedFileDeleteResult{Moved: true, TrashPath: dest, RelatedMoved: len(related)}, nil
}
