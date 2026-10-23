package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func TestInitializeAdminConcurrentOnlyOneWins(t *testing.T) {
	dir := t.TempDir()
	SetForTest(&AppConfig{
		Admin: &model.AppConfig{Username: "admin"},
		Salt:  "setup_test_salt_32b_padding______",
		Paths: AppConfigPaths{ConfigPath: filepath.Join(dir, "config.json")},
	})

	const workers = 8
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- InitializeAdmin("owner", "hash-"+string(rune('a'+i)))
		}(i)
	}
	wg.Wait()
	close(results)

	success, already := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrAdminAlreadyInitialized):
			already++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || already != workers-1 {
		t.Fatalf("success=%d already=%d, want 1/%d", success, already, workers-1)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored model.AppConfig
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Username != "owner" || stored.Password == "" {
		t.Fatalf("persisted admin invalid: %+v", stored)
	}
}

func TestInitializeAdminPersistenceFailureLeavesSetupPending(t *testing.T) {
	dir := t.TempDir()
	SetForTest(&AppConfig{
		Admin: &model.AppConfig{Username: "admin"},
		Salt:  "setup_test_salt_32b_padding______",
		// 父目录不存在，写入必然失败。
		Paths: AppConfigPaths{ConfigPath: filepath.Join(dir, "missing", "config.json")},
	})

	if err := InitializeAdmin("owner", "hash"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if !NeedsSetup() {
		t.Fatal("admin must remain uninitialized when disk write fails")
	}
}
