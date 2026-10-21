package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

func TestCleanupScanStore_TTLUserAndCopy(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s := newCleanupScanStore(2, time.Minute)
	s.now = func() time.Time { return now }

	root := t.TempDir()
	result := ScanResponse{Mappings: []MappingScanResult{{
		LocalPath: root, StaleStrms: []StaleStrm{{RelPath: "a.strm"}},
	}}}
	id, expires := s.Put("alice", result)
	if id == "" || expires == 0 {
		t.Fatalf("id=%q expires=%d", id, expires)
	}
	snap, ok := s.Get(id, "alice")
	if !ok {
		t.Fatal("snapshot should exist")
	}
	if _, ok := s.Get(id, "bob"); ok {
		t.Fatal("snapshot should be bound to user")
	}
	snap.Result.Mappings[0].StaleStrms[0].RelPath = "mutated.strm"
	snap2, _ := s.Get(id, "alice")
	if snap2.Result.Mappings[0].StaleStrms[0].RelPath != "a.strm" {
		t.Fatal("Get must return a deep copy")
	}

	now = now.Add(2 * time.Minute)
	if _, ok := s.Get(id, "alice"); ok {
		t.Fatal("expired snapshot should be removed")
	}
}

func TestCleanupScanStore_MarkUsedOnce(t *testing.T) {
	s := newCleanupScanStore(2, time.Minute)
	id, _ := s.Put("alice", ScanResponse{})
	if !s.MarkUsed(id, "alice") {
		t.Fatal("first MarkUsed should succeed")
	}
	if s.MarkUsed(id, "alice") {
		t.Fatal("second MarkUsed must fail")
	}
	if s.MarkUsed(id, "bob") {
		t.Fatal("other user must not consume snapshot")
	}
}

func TestCleanupScanStore_EvictsOldest(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s := newCleanupScanStore(2, time.Hour)
	s.now = func() time.Time { return now }
	id1, _ := s.Put("", ScanResponse{})
	now = now.Add(time.Second)
	id2, _ := s.Put("", ScanResponse{})
	now = now.Add(time.Second)
	id3, _ := s.Put("", ScanResponse{})
	if _, ok := s.Get(id1, ""); ok {
		t.Fatal("oldest snapshot should be evicted")
	}
	if _, ok := s.Get(id2, ""); !ok {
		t.Fatal("second snapshot should remain")
	}
	if _, ok := s.Get(id3, ""); !ok {
		t.Fatal("newest snapshot should remain")
	}
}

func TestBindExecuteToScan_RejectsForgedPath(t *testing.T) {
	root := t.TempDir()
	empty, _ := cleanupScanCache.Put("", ScanResponse{
		Mappings: []MappingScanResult{{LocalPath: root, StaleStrms: []StaleStrm{{RelPath: "stale.strm"}}}},
	})
	snap, _ := cleanupScanCache.Get(empty, "")
	body := ExecuteRequest{
		Action:  "delete",
		Entries: []ExecuteEntry{{LocalPath: root, StaleRelPaths: []string{"forged.strm"}}},
	}
	if err := bindExecuteToScan(&body, snap); err == nil {
		t.Fatal("forged path should be rejected")
	}
}

func TestBindExecuteToScan_UsesServerSnapshot(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotResult := ScanResponse{
		Mappings: []MappingScanResult{
			{LocalPath: root, StaleStrms: []StaleStrm{{RelPath: "a.strm"}}},
			{LocalPath: other, StaleStrms: []StaleStrm{{RelPath: "b.strm"}}},
		},
	}
	id, _ := cleanupScanCache.Put("", snapshotResult)
	snap, _ := cleanupScanCache.Get(id, "")
	body := ExecuteRequest{
		Action:  "delete_all",
		Entries: []ExecuteEntry{{LocalPath: root, StaleRelPaths: []string{"forged.strm"}}},
		ScanSummary: &ScanResponse{Mappings: []MappingScanResult{{
			LocalPath: root, StaleStrms: []StaleStrm{{RelPath: "forged.strm"}},
		}}},
	}
	if err := bindExecuteToScan(&body, snap); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 0 || body.ScanSummary == nil {
		t.Fatalf("delete_all should use server snapshot, body=%+v", body)
	}
	if len(body.ScanSummary.Mappings) != 2 {
		t.Fatalf("server snapshot mappings=%d", len(body.ScanSummary.Mappings))
	}
}

func newScanTokenDeps(t *testing.T, root string) StrmCleanupDeps {
	t.Helper()
	salt := "scan_token_salt_32b_padding_______"
	cfgDir := t.TempDir()
	settings := model.DefaultSettings()
	settings.LifeMonitor.Enabled = true
	settings.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: "alice", CloudPath: "/x", LocalPath: root},
	}
	settingsStore := store.NewSettingsStore(salt, cfgDir)
	if err := settingsStore.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	accountStore, err := store.NewAccountStore(salt, cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	return StrmCleanupDeps{SettingsStore: settingsStore, AccountStore: accountStore}
}

func TestStrmCleanupScanReturnsScanId(t *testing.T) {
	root := t.TempDir()
	deps := newScanTokenDeps(t, root)
	rec := doTrashJSON(t, HandleStrmCleanupScanPOST(deps), http.MethodPost,
		"/api/strmCleanup/scan", map[string]any{"useSettingsDefaults": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ScanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ScanId == "" || resp.ScanExpiresAt == 0 {
		t.Fatalf("scanId=%q expires=%d", resp.ScanId, resp.ScanExpiresAt)
	}
}

func TestStrmCleanupExecuteRequiresValidScanToken(t *testing.T) {
	root := t.TempDir()
	deps := newScanTokenDeps(t, root)

	rec := doTrashJSON(t, HandleStrmCleanupExecutePOST(deps), http.MethodPost,
		"/api/strmCleanup/execute", ExecuteRequest{Action: "delete"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing token status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = doTrashJSON(t, HandleStrmCleanupExecutePOST(deps), http.MethodPost,
		"/api/strmCleanup/execute", ExecuteRequest{Action: "delete", ScanId: "not-exist"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid token status=%d body=%s", rec.Code, rec.Body.String())
	}

	id, _ := SeedCleanupScanForTest(ScanResponse{Mappings: []MappingScanResult{{
		Account: "alice", CloudPath: "/x", LocalPath: root,
		StaleStrms: []StaleStrm{{RelPath: "stale.strm"}},
	}}})
	rec = doTrashJSON(t, HandleStrmCleanupExecutePOST(deps), http.MethodPost,
		"/api/strmCleanup/execute", ExecuteRequest{
			Action: "delete", ScanId: id,
			Entries: []ExecuteEntry{{LocalPath: root, StaleRelPaths: []string{"forged.strm"}}},
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged path status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStrmCleanupExecute_DeleteAllUsesServerSnapshot(t *testing.T) {
	root := t.TempDir()
	deps := newScanTokenDeps(t, root)
	stalePath := filepath.Join(root, "a.strm")
	otherPath := filepath.Join(root, "b.strm")
	for _, p := range []string{stalePath, otherPath} {
		if err := os.WriteFile(p, []byte("url"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id, _ := SeedCleanupScanForTest(ScanResponse{Mappings: []MappingScanResult{{
		Account: "alice", CloudPath: "/x", LocalPath: root,
		StaleStrms: []StaleStrm{{RelPath: "a.strm"}},
	}}})
	rec := doTrashJSON(t, HandleStrmCleanupExecutePOST(deps), http.MethodPost,
		"/api/strmCleanup/execute", ExecuteRequest{
			Action: "delete_all", ScanId: id,
			ScanSummary: &ScanResponse{Mappings: []MappingScanResult{{
				LocalPath: root, StaleStrms: []StaleStrm{{RelPath: "b.strm"}},
			}}},
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("execute status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ExecuteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.DeletedCount != 1 {
		t.Fatalf("deleted=%d body=%s", resp.DeletedCount, rec.Body.String())
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatal("snapshot stale file should be trashed")
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatal("forged client path must be preserved")
	}

	// 同一 scanId 第二次真实执行必须被拒绝
	rec = doTrashJSON(t, HandleStrmCleanupExecutePOST(deps), http.MethodPost,
		"/api/strmCleanup/execute", ExecuteRequest{Action: "delete_all", ScanId: id})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "SCAN_TOKEN_USED") {
		t.Fatalf("reused scanId should be rejected, status=%d body=%s", rec.Code, rec.Body.String())
	}
}
