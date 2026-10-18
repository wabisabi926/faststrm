package monitor

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// TestResolvedStrmExtensions 覆盖「监控侧扩展名不再硬编码」这一修复：
// 用户自定义扩展名必须对生活事件生效，未配置时才回落默认值。
func TestResolvedStrmExtensions(t *testing.T) {
	t.Run("未配置时回落默认", func(t *testing.T) {
		got := resolvedStrmExtensions(model.LifeMonitorSettings{})
		if len(got) != len(model.DefaultStrmExtensions) {
			t.Fatalf("want %d default extensions, got %d (%v)",
				len(model.DefaultStrmExtensions), len(got), got)
		}
	})

	t.Run("自定义优先", func(t *testing.T) {
		custom := []string{"mkv", "iso"}
		got := resolvedStrmExtensions(model.LifeMonitorSettings{StrmExtensions: custom})
		if len(got) != 2 || got[0] != "mkv" || got[1] != "iso" {
			t.Fatalf("want custom extensions, got %v", got)
		}
	})

	t.Run("自定义扩展名参与匹配", func(t *testing.T) {
		cfg := model.LifeMonitorSettings{StrmExtensions: []string{"mkv"}}
		if !isMediaFile("movie.mkv", resolvedStrmExtensions(cfg)) {
			t.Fatal("movie.mkv should match custom extension list")
		}
		// 未列入自定义列表的默认扩展名不应再命中
		if isMediaFile("movie.mp4", resolvedStrmExtensions(cfg)) {
			t.Fatal("movie.mp4 should NOT match when custom list only contains mkv")
		}
	})
}
