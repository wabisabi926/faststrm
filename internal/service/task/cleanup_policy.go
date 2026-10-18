package task

import (
	"fmt"
	"path/filepath"

	"github.com/wabisabi926/faststrm/internal/model"
)

// CleanupVerdict 清理放行判定结果
type CleanupVerdict int

const (
	// CleanupAllow 数量在稳定阈值内，可直接删除
	CleanupAllow CleanupVerdict = iota
	// CleanupConfirm 超过稳定阈值但未触发保护，需二次确认（ConfirmMode=none 时直接删）
	CleanupConfirm
	// CleanupProtected 触发保护：本次只生成不删除，需用户手动放行
	CleanupProtected
)

func (v CleanupVerdict) String() string {
	switch v {
	case CleanupAllow:
		return "allow"
	case CleanupConfirm:
		return "confirm"
	case CleanupProtected:
		return "protected"
	default:
		return "unknown"
	}
}

// 清理阈值的默认值（配置缺省或非法时使用）
const (
	DefaultCleanupMaxAbs   = 1000 // 单次删除绝对上限
	DefaultCleanupMaxRatio = 0.10 // 单次删除占本地总数比例上限
	DefaultCleanupMaxDirs  = 20   // 单次删除涉及目录数上限
	DefaultCleanupStable   = 5    // 稳定阈值：超过则需二次确认
)

// CleanupInput 清理判定输入
type CleanupInput struct {
	// Candidates 本次待删除数量
	Candidates int
	// TotalLocal 本地同类文件总数；0 表示未知（跳过比例判定）
	TotalLocal int
	// Dirs 本次待删涉及的目录数；0 表示未知（跳过目录数判定）
	Dirs int
	// Cfg 清理配置
	Cfg model.CleanupSettings
}

// EffectiveCleanupLimits 返回生效的清理阈值（补齐默认值后），供 UI 展示与日志使用。
// 顺序：maxAbs, maxRatio, maxDirs, stable
func EffectiveCleanupLimits(cfg model.CleanupSettings) (int, float64, int, int) {
	stable := cfg.StableThreshold
	if stable <= 0 {
		stable = DefaultCleanupStable
	}
	maxAbs := cfg.MaxThreshold
	if maxAbs <= 0 {
		maxAbs = DefaultCleanupMaxAbs
	}
	maxRatio := cfg.MaxRatio
	if maxRatio <= 0 {
		maxRatio = DefaultCleanupMaxRatio
	}
	maxDirs := cfg.MaxDirs
	if maxDirs <= 0 {
		maxDirs = DefaultCleanupMaxDirs
	}
	return maxAbs, maxRatio, maxDirs, stable
}

// EvaluateCleanup 统一的清理放行判定。
//
// 背景：原实现只用「删除个数」单阈值（默认 10），一个上万文件的库只要网盘移走
// 11 个文件就整批拒绝，自动清理在真实场景基本失效；而单纯调大又会失去防误删保护。
//
// 因此改为「比例 + 绝对 + 目录数」三重保护，任一超限即进入保护态：
//   - 比例：默认 10%（对齐 MoviePilot full_sync_remove_unless_max_threshold 的思路）
//   - 绝对：默认 1000（对齐 LitePan 的上限）
//   - 目录数：默认 20，防止「分散在很多目录的小批量删除」绕过前两者
//
// 保护态语义：**只停删除，不停生成**。调用方应把结果标记出来告知用户，
// 而不是让整个任务失败或静默什么都不做。
func EvaluateCleanup(in CleanupInput) (CleanupVerdict, string) {
	maxAbs, maxRatio, maxDirs, stable := EffectiveCleanupLimits(in.Cfg)

	if in.Candidates <= 0 {
		return CleanupAllow, ""
	}
	// 小批量直接放行：避免每次清理几个文件也要二次确认
	if in.Candidates <= stable {
		return CleanupAllow, ""
	}

	// 比例保护：仅在本地总数已知时生效
	if in.TotalLocal > 0 {
		ratio := float64(in.Candidates) / float64(in.TotalLocal)
		if ratio > maxRatio {
			return CleanupProtected, fmt.Sprintf(
				"待删 %d / 本地 %d = %.1f%%，超过比例上限 %.1f%%",
				in.Candidates, in.TotalLocal, ratio*100, maxRatio*100)
		}
	}

	// 绝对上限保护
	if in.Candidates > maxAbs {
		return CleanupProtected, fmt.Sprintf(
			"待删 %d 个超过绝对上限 %d 个", in.Candidates, maxAbs)
	}

	// 目录数保护：防止分散在大量目录的小批量删除绕过比例与绝对阈值
	if in.Dirs > 0 && in.Dirs > maxDirs {
		return CleanupProtected, fmt.Sprintf(
			"待删涉及 %d 个目录，超过目录数上限 %d", in.Dirs, maxDirs)
	}

	return CleanupConfirm, fmt.Sprintf(
		"待删 %d 个超过稳定阈值 %d，需二次确认", in.Candidates, stable)
}

// CleanupOutcome 清理执行结果
type CleanupOutcome struct {
	// Deleted 实际删除数量
	Deleted int
	// RequestID 非空表示已入队等待二次确认
	RequestID string
	// Verdict 判定结果
	Verdict CleanupVerdict
	// Reason 判定原因（保护态/确认态时非空）
	Reason string
}

// countUniqueDirs 统计待删路径涉及的唯一目录数
func countUniqueDirs(paths []string) int {
	if len(paths) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		seen[filepath.Dir(p)] = struct{}{}
	}
	return len(seen)
}
