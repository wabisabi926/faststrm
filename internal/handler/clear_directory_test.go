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
)

func TestClearDirRejectsOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	allowed := t.TempDir()

	body, _ := json.Marshal(ClearDirRequest{TargetPath: target})
	req := httptest.NewRequest(http.MethodPost, "/api/directory/clear", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	HandleClearDir(ClearDeps{
		SettingsProvider: model.DefaultSettings,
		AllowedRoots:     func() []string { return []string{allowed} },
	})(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for outside root, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClearDirTargetAllowsTrailingSeparator(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveClearDirTarget(root + string(os.PathSeparator)); err != nil {
		t.Fatalf("trailing separator should be accepted: %v", err)
	}
}

func TestClearDirTargetRejectsDotDotSegment(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveClearDirTarget(filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal(".. segment must be rejected")
	}
}

func TestClearDirMovesJunkToTrashKeepsStrm(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), []byte("http://x/api/strm?pickcode=d18nbyd6w552vx2zs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "junk.txt"), []byte("delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(ClearDirRequest{TargetPath: root})
	req := httptest.NewRequest(http.MethodPost, "/api/directory/clear", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	HandleClearDir(ClearDeps{
		SettingsProvider: model.DefaultSettings,
		AllowedRoots:     func() []string { return []string{root} },
	})(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "movie.strm")); err != nil {
		t.Fatalf("strm must be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "junk.txt")); !os.IsNotExist(err) {
		t.Fatalf("junk should be moved to trash, stat err=%v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".faststrm-trash", "*", "junk.txt"))
	if len(matches) != 1 {
		t.Fatalf("junk should exist in trash, matches=%v", matches)
	}
}
