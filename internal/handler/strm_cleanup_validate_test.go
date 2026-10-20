package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func TestValidateCleanupRequest_PathSafety(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	if err := os.MkdirAll(filepath.Join(media, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, "ok.strm"), []byte("url"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.strm")
	if err := os.WriteFile(outside, []byte("url"), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := model.DefaultSettings()
	settings.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: "a", CloudPath: "/x", LocalPath: media},
	}
	deps := StrmCleanupDeps{}

	cases := []struct {
		name string
		body ExecuteRequest
		ok   bool
	}{
		{
			name: "valid",
			body: ExecuteRequest{Entries: []ExecuteEntry{{LocalPath: media, StaleRelPaths: []string{"ok.strm"}}}},
			ok:   true,
		},
		{
			name: "relative traversal",
			body: ExecuteRequest{Entries: []ExecuteEntry{{LocalPath: media, StaleRelPaths: []string{"../outside.strm"}}}},
			ok:   false,
		},
		{
			name: "absolute rel path",
			body: ExecuteRequest{Entries: []ExecuteEntry{{LocalPath: media, StaleRelPaths: []string{outside}}}},
			ok:   false,
		},
		{
			name: "local path outside mapped roots",
			body: ExecuteRequest{Entries: []ExecuteEntry{{LocalPath: root, StaleRelPaths: []string{"outside.strm"}}}},
			ok:   false,
		},
		{
			name: "non strm extension",
			body: ExecuteRequest{Entries: []ExecuteEntry{{LocalPath: media, StaleRelPaths: []string{"sub/movie.mkv"}}}},
			ok:   false,
		},
		{
			name: "scan summary traversal",
			body: ExecuteRequest{ScanSummary: &ScanResponse{Mappings: []MappingScanResult{{
				LocalPath:  media,
				StaleStrms: []StaleStrm{{RelPath: "../../etc/passwd.strm"}},
			}}}},
			ok: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCleanupRequest(c.body, settings, deps)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateCleanupRequest_RejectsSymlink(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.strm")
	if err := os.WriteFile(target, []byte("url"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(media, "link.strm")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	settings := model.DefaultSettings()
	settings.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{Account: "a", CloudPath: "/x", LocalPath: media},
	}
	err := validateCleanupRequest(ExecuteRequest{
		Entries: []ExecuteEntry{{LocalPath: media, StaleRelPaths: []string{"link.strm"}}},
	}, settings, StrmCleanupDeps{})
	if err == nil {
		t.Fatal("symlink should be rejected")
	}
}
