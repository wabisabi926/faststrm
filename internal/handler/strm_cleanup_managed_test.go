package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/strm"
)

func TestSplitManagedStale(t *testing.T) {
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	managedFile := filepath.Join(root, "managed.strm")
	foreignFile := filepath.Join(root, "foreign.strm")
	for _, p := range []string{managedFile, foreignFile} {
		if err := os.WriteFile(p, []byte("url"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rec := strm.NewManagedRecorder(database)
	if err := rec.Record(context.Background(), strm.ManagedInput{
		LocalPath: managedFile, Account: "alice", Pickcode: "d18nbyd6w552vx2zs", Content: "url",
	}); err != nil {
		t.Fatal(err)
	}
	result := MappingScanResult{
		Account: "alice", CloudPath: "/x", LocalPath: root,
		StaleStrms: []StaleStrm{{RelPath: "managed.strm"}, {RelPath: "foreign.strm"}},
	}
	splitManagedStale(&result, StrmCleanupDeps{SQLiteDB: database, ManagedStrm: rec})
	if len(result.StaleStrms) != 1 || result.StaleStrms[0].RelPath != "managed.strm" {
		t.Fatalf("stale = %+v", result.StaleStrms)
	}
	if result.UnmanagedCount != 1 || len(result.UnmanagedStrms) != 1 {
		t.Fatalf("unmanaged = %+v", result.UnmanagedStrms)
	}
}

func TestFilterManagedStale(t *testing.T) {
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	managedFile := filepath.Join(root, "managed.strm")
	foreignFile := filepath.Join(root, "foreign.strm")
	for _, p := range []string{managedFile, foreignFile} {
		if err := os.WriteFile(p, []byte("http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rec := strm.NewManagedRecorder(database)
	if err := rec.Record(context.Background(), strm.ManagedInput{
		LocalPath: managedFile, Account: "alice", Pickcode: "d18nbyd6w552vx2zs",
		Content: "http://nas/api/strm?pickcode=d18nbyd6w552vx2zs",
	}); err != nil {
		t.Fatal(err)
	}
	body := ExecuteRequest{Entries: []ExecuteEntry{{
		LocalPath: root, StaleRelPaths: []string{"managed.strm", "foreign.strm"},
	}}}
	skipped := filterManagedStale(context.Background(), &body, rec)
	if skipped != 1 {
		t.Fatalf("skipped = %d", skipped)
	}
	if len(body.Entries[0].StaleRelPaths) != 1 || body.Entries[0].StaleRelPaths[0] != "managed.strm" {
		t.Fatalf("kept = %+v", body.Entries[0].StaleRelPaths)
	}
}

func TestDeleteAllStaleFromScan_CloudRestoredAndUnverified(t *testing.T) {
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	root := t.TempDir()
	file := filepath.Join(root, "managed.strm")
	content := "http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := strm.NewManagedRecorder(database)
	if err := rec.Record(context.Background(), strm.ManagedInput{
		LocalPath: file, Account: "alice", CloudPath: "/电影/managed.strm",
		Pickcode: "d18nbyd6w552vx2zs", Content: content,
	}); err != nil {
		t.Fatal(err)
	}
	body := ExecuteRequest{ScanSummary: &ScanResponse{Mappings: []MappingScanResult{{
		Account: "alice", CloudPath: "/x", LocalPath: root,
		StaleStrms: []StaleStrm{{RelPath: "managed.strm"}},
	}}}}
	cleanup := model.CleanupSettings{RemoveStrm: true}

	resp := ExecuteResponse{}
	deleted, _ := deleteAllStaleFromScan(context.Background(), body, &resp, &cleanup, rec, fakeCloudAbsence{absent: false})
	if deleted != 0 || resp.SkippedCloudRestored != 1 {
		t.Fatalf("restored: deleted=%d resp=%+v", deleted, resp)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("cloud-restored file must be preserved")
	}

	resp = ExecuteResponse{}
	deleted, _ = deleteAllStaleFromScan(context.Background(), body, &resp, &cleanup, rec, nil)
	if deleted != 0 || resp.SkippedCloudUnverified != 1 {
		t.Fatalf("unverified: deleted=%d resp=%+v", deleted, resp)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("unverified file must be preserved")
	}
}

func TestExecuteCleanup_DeleteAllRatioProtection(t *testing.T) {
	root := t.TempDir()
	files := []string{filepath.Join(root, "a.strm"), filepath.Join(root, "b.strm")}
	for _, p := range files {
		if err := os.WriteFile(p, []byte("url"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings := model.DefaultSettings()
	settings.Cleanup.RemoveStrm = true
	// StableThreshold=1 让 2 个候选越过“小批量直接放行”，从而触发 2/2=100% 的比例保护
	settings.Cleanup.StableThreshold = 1
	body := ExecuteRequest{
		Action: "delete_all",
		ScanSummary: &ScanResponse{Mappings: []MappingScanResult{{
			Account: "alice", CloudPath: "/x", LocalPath: root,
			StaleStrms: []StaleStrm{{RelPath: "a.strm"}, {RelPath: "b.strm"}},
		}}},
	}
	resp := executeCleanup(context.Background(), body, settings, StrmCleanupDeps{}, nil)
	if resp.ThresholdError == "" {
		t.Fatalf("expected ratio protection, got %+v", resp)
	}
	for _, p := range files {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("protected delete must keep files: %v", err)
		}
	}
}

type fakeCloudAbsence struct {
	absent bool
	err    error
}

func (f fakeCloudAbsence) StillAbsent(context.Context, string, string, string) (bool, error) {
	return f.absent, f.err
}

func TestDeleteAllStaleFromScan_OwnershipGuardAndTrash(t *testing.T) {
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	managedFile := filepath.Join(root, "managed.strm")
	foreignFile := filepath.Join(root, "foreign.strm")
	content := "http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"
	for _, p := range []string{managedFile, foreignFile} {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rec := strm.NewManagedRecorder(database)
	if err := rec.Record(context.Background(), strm.ManagedInput{
		LocalPath: managedFile, Account: "alice", CloudPath: "/电影/managed.strm",
		Pickcode: "d18nbyd6w552vx2zs", Content: content,
	}); err != nil {
		t.Fatal(err)
	}

	body := ExecuteRequest{ScanSummary: &ScanResponse{Mappings: []MappingScanResult{{
		Account: "alice", CloudPath: "/x", LocalPath: root,
		StaleStrms: []StaleStrm{{RelPath: "managed.strm"}, {RelPath: "foreign.strm"}},
	}}}}
	resp := ExecuteResponse{}
	cleanup := model.CleanupSettings{RemoveStrm: true}
	deleted, failed := deleteAllStaleFromScan(context.Background(), body, &resp, &cleanup, rec, fakeCloudAbsence{absent: true})
	if deleted != 1 || failed != 0 {
		t.Fatalf("deleted=%d failed=%d resp=%+v", deleted, failed, resp)
	}
	if resp.SkippedUnmanaged != 1 {
		t.Fatalf("skipped=%d", resp.SkippedUnmanaged)
	}
	if _, err := os.Stat(foreignFile); err != nil {
		t.Fatal("foreign file must be preserved")
	}
	if _, err := os.Stat(managedFile); !os.IsNotExist(err) {
		t.Fatal("managed file should be moved to trash")
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".faststrm-trash", "*", "managed.strm"))
	if len(matches) != 1 {
		t.Fatalf("trash matches = %v", matches)
	}
}
