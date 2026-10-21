package handler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// TestRunMaintenance_PurgesOnlyExpiredTrash 每日维护只删过期回收站批次，
// 保留期内的必须原样保留（用户还能恢复）。
func TestRunMaintenance_PurgesOnlyExpiredTrash(t *testing.T) {
	root := t.TempDir()
	trashBase := filepath.Join(root, ".faststrm-trash")

	// 过期批次：把目录 mtime 改到 8 天前
	oldBatch := filepath.Join(trashBase, "old-batch")
	if err := os.MkdirAll(oldBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldBatch, "a.strm"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldBatch, old, old); err != nil {
		t.Fatal(err)
	}

	// 新批次：保持当前时间
	newBatch := filepath.Join(trashBase, "new-batch")
	if err := os.MkdirAll(newBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newBatch, "b.strm"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	settingsStore := store.NewSettingsStore("maintenance_salt_32b___________", dir)
	if err := settingsStore.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	// 让 allowedCleanupRoots 能覆盖到临时目录
	cfg, _ := settingsStore.ReadSettings()
	cfg.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: "a", CloudPath: "/电影", LocalPath: root},
	}
	if err := settingsStore.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}

	runMaintenance(StrmCleanupDeps{SettingsStore: settingsStore})

	if _, err := os.Stat(oldBatch); err == nil {
		t.Fatal("过期批次应被清理")
	}
	if _, err := os.Stat(newBatch); err != nil {
		t.Fatal("保留期内的批次不应被清理")
	}
	if _, err := os.Stat(trashBase); err != nil {
		t.Fatal("回收站根目录不应被删除")
	}
}

// TestRunMaintenance_NilSettingsStore 依赖缺失时安全返回
func TestRunMaintenance_NilSettingsStore(t *testing.T) {
	runMaintenance(StrmCleanupDeps{}) // 不应 panic
	_ = strmutil.TrashDirName
}
