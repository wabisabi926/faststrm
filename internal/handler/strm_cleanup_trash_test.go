package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

func newTrashTestDeps(t *testing.T, root string) StrmCleanupDeps {
	t.Helper()
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	settings := model.DefaultSettings()
	settings.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: "alice", CloudPath: "/x", LocalPath: root},
	}
	storeDir := t.TempDir()
	settingsStore := store.NewSettingsStore("trash_test_salt_32b_padding______", storeDir)
	if err := settingsStore.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	return StrmCleanupDeps{
		SettingsStore: settingsStore,
		SQLiteDB:      database,
		ManagedStrm:   strm.NewManagedRecorder(database),
	}
}

func doTrashJSON(t *testing.T, h http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestTrashListAndRestore(t *testing.T) {
	root := t.TempDir()
	deps := newTrashTestDeps(t, root)
	content := "http://nas/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs\n"
	original := filepath.Join(root, "电影", "movie.strm")
	if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	trashDir := strmutil.TrashSessionDir(root)
	trashed, err := strmutil.MoveToTrash(original, root, trashDir)
	if err != nil {
		t.Fatal(err)
	}

	rec := doTrashJSON(t, HandleStrmTrashListGET(deps), http.MethodGet, "/api/strmCleanup/trash", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list TrashListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 {
		t.Fatalf("entries=%d body=%s", len(list.Entries), rec.Body.String())
	}
	if list.Entries[0].OriginalPath != original || !list.Entries[0].IsStrm || list.Entries[0].Root == "" {
		t.Fatalf("unexpected entry: %+v", list.Entries[0])
	}

	rec = doTrashJSON(t, HandleStrmTrashRestorePOST(deps), http.MethodPost, "/api/strmCleanup/trash/restore",
		TrashRestoreRequest{LocalPath: root, TrashPath: trashed})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", rec.Code, rec.Body.String())
	}
	if data, err := os.ReadFile(original); err != nil || string(data) != content {
		t.Fatalf("restored content: %v %q", err, data)
	}
	if _, err := os.Stat(trashed); !os.IsNotExist(err) {
		t.Fatal("trash file should be gone after restore")
	}
	if rec2, _ := db.GetManagedStrm(deps.SQLiteDB, original); rec2 == nil {
		t.Fatal("restored STRM should be re-registered as managed")
	}
}

func TestTrashRestore_NestedTrash(t *testing.T) {
	root := t.TempDir()
	deps := newTrashTestDeps(t, root)
	sub := filepath.Join(root, "剧集")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(sub, "episode.strm")
	if err := os.WriteFile(original, []byte("http://nas/api/strm?pickcode=d18nbyd6w552vx2zs"), 0o600); err != nil {
		t.Fatal(err)
	}
	trashed, err := strmutil.MoveToTrash(original, sub, strmutil.TrashSessionDir(sub))
	if err != nil {
		t.Fatal(err)
	}
	rec := doTrashJSON(t, HandleStrmTrashRestorePOST(deps), http.MethodPost, "/api/strmCleanup/trash/restore",
		TrashRestoreRequest{LocalPath: root, TrashPath: trashed})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("nested restore failed: %v", err)
	}
}

func TestTrashRestore_ConflictAndOverwrite(t *testing.T) {
	root := t.TempDir()
	deps := newTrashTestDeps(t, root)
	original := filepath.Join(root, "movie.strm")
	if err := os.WriteFile(original, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	trashed, err := strmutil.MoveToTrash(original, root, strmutil.TrashSessionDir(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := doTrashJSON(t, HandleStrmTrashRestorePOST(deps), http.MethodPost, "/api/strmCleanup/trash/restore",
		TrashRestoreRequest{LocalPath: root, TrashPath: trashed})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	rec = doTrashJSON(t, HandleStrmTrashRestorePOST(deps), http.MethodPost, "/api/strmCleanup/trash/restore",
		TrashRestoreRequest{LocalPath: root, TrashPath: trashed, Overwrite: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite restore status=%d body=%s", rec.Code, rec.Body.String())
	}
	if data, _ := os.ReadFile(original); string(data) != "old" {
		t.Fatalf("expected restored old content, got %q", data)
	}
}

func TestDeriveTrashOriginal_RejectsEscape(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, strmutil.TrashDirName, "batch", "..", "..", "outside.strm")
	if _, _, err := deriveTrashOriginal(root, bad); err == nil {
		t.Fatal("path escape should be rejected")
	}
}
