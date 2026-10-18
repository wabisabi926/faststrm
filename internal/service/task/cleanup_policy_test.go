package task

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// defaultCleanupCfg 返回与 DefaultSettings 一致的清理配置
func defaultCleanupCfg() model.CleanupSettings {
	return model.DefaultSettings().Cleanup
}

func TestEvaluateCleanup(t *testing.T) {
	cases := []struct {
		name       string
		in         CleanupInput
		wantVerdic CleanupVerdict
	}{
		{
			name:       "小批量直接放行",
			in:         CleanupInput{Candidates: 3, TotalLocal: 1000, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupAllow,
		},
		{
			name:       "无待删",
			in:         CleanupInput{Candidates: 0, TotalLocal: 1000, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupAllow,
		},
		{
			name:       "比例超限 20% 触发保护",
			in:         CleanupInput{Candidates: 200, TotalLocal: 1000, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupProtected,
		},
		{
			name:       "绝对上限超限触发保护",
			in:         CleanupInput{Candidates: 1200, TotalLocal: 100000, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupProtected,
		},
		{
			name:       "目录数超限触发保护",
			in:         CleanupInput{Candidates: 50, TotalLocal: 10000, Dirs: 30, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupProtected,
		},
		{
			name:       "正好 10% 不超限，需二次确认",
			in:         CleanupInput{Candidates: 100, TotalLocal: 1000, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupConfirm,
		},
		{
			name:       "本地总数未知时跳过比例判定",
			in:         CleanupInput{Candidates: 50, TotalLocal: 0, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupConfirm,
		},
		{
			name:       "目录数未知时跳过目录判定",
			in:         CleanupInput{Candidates: 50, TotalLocal: 10000, Dirs: 0, Cfg: defaultCleanupCfg()},
			wantVerdic: CleanupConfirm,
		},
		{
			name: "自定义阈值生效",
			in: CleanupInput{Candidates: 100, TotalLocal: 1000, Cfg: model.CleanupSettings{
				MaxThreshold: 500, MaxRatio: 0.5, MaxDirs: 50, StableThreshold: 5,
			}},
			wantVerdic: CleanupConfirm,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := EvaluateCleanup(tc.in)
			if got != tc.wantVerdic {
				t.Fatalf("EvaluateCleanup() = %v (%s), want %v", got, reason, tc.wantVerdic)
			}
			// 非 Allow 必须有原因说明，否则用户无法判断为什么没删
			if got != CleanupAllow && reason == "" {
				t.Fatal("non-allow verdict must carry a reason")
			}
		})
	}
}

// TestEvaluateCleanup_LegacyThresholdStillWorks 老配置（只有 maxThreshold=10）不应导致永久保护
func TestEvaluateCleanup_LegacyThresholdStillWorks(t *testing.T) {
	// 未经过迁移的老配置：MaxRatio/MaxDirs 为 0，MaxThreshold=10
	cfg := model.CleanupSettings{MaxThreshold: 10, StableThreshold: 5}

	// MaxRatio=0 会被补成默认 0.10，因此 6/1000 仍走二次确认而非保护
	got, reason := EvaluateCleanup(CleanupInput{Candidates: 6, TotalLocal: 1000, Cfg: cfg})
	if got != CleanupConfirm {
		t.Fatalf("legacy cfg 6/1000 = %v (%s), want confirm", got, reason)
	}

	// 但绝对上限会退回默认 1000（配置值 10 仍生效，所以 11 个就保护）
	got, _ = EvaluateCleanup(CleanupInput{Candidates: 11, TotalLocal: 100000, Cfg: cfg})
	if got != CleanupProtected {
		t.Fatalf("legacy cfg 11/100000 = %v, want protected (maxThreshold=10)", got)
	}
}

func TestEffectiveCleanupLimits(t *testing.T) {
	maxAbs, maxRatio, maxDirs, stable := EffectiveCleanupLimits(model.CleanupSettings{})
	if maxAbs != DefaultCleanupMaxAbs || maxRatio != DefaultCleanupMaxRatio ||
		maxDirs != DefaultCleanupMaxDirs || stable != DefaultCleanupStable {
		t.Fatalf("zero cfg should yield defaults, got abs=%d ratio=%v dirs=%d stable=%d",
			maxAbs, maxRatio, maxDirs, stable)
	}

	maxAbs, maxRatio, maxDirs, stable = EffectiveCleanupLimits(model.CleanupSettings{
		MaxThreshold: 50, MaxRatio: 0.2, MaxDirs: 3, StableThreshold: 2,
	})
	if maxAbs != 50 || maxRatio != 0.2 || maxDirs != 3 || stable != 2 {
		t.Fatalf("custom cfg not honored: abs=%d ratio=%v dirs=%d stable=%d",
			maxAbs, maxRatio, maxDirs, stable)
	}
}

func TestCountUniqueDirs(t *testing.T) {
	if got := countUniqueDirs(nil); got != 0 {
		t.Fatalf("nil paths dirs = %d, want 0", got)
	}
	paths := []string{
		"/media/a/x.strm",
		"/media/a/y.strm",
		"/media/b/z.strm",
	}
	if got := countUniqueDirs(paths); got != 2 {
		t.Fatalf("dirs = %d, want 2", got)
	}
}
