package handler

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func TestGetDefaultScanRequestsFromSettings_DisabledMonitorKeepsMappings(t *testing.T) {
	s := model.DefaultSettings()
	s.LifeMonitor.Enabled = false
	s.LifeMonitor.PathMappings = []model.MonitorPathMapping{
		{CloudPath: "/电影", LocalPath: "/media/movies"},
		{Account: "alice", CloudPath: "/tv", LocalPath: "/media/tv"},
	}

	got := getDefaultScanRequestsFromSettings(s)
	if len(got) != 2 {
		t.Fatalf("mappings = %d, want 2 even when monitor disabled", len(got))
	}
	if got[0].Account != "" || got[1].Account != "alice" {
		t.Fatalf("unexpected accounts: %+v", got)
	}
}

func TestExpandCleanupMappingsForAccounts(t *testing.T) {
	accounts := []*model.AccountInfo{
		{Name: "alice", AccountType: "115", Cookie: "UID=alice"},
		{Name: "bob", AccountType: "115", Cookie: "UID=bob"},
		{Name: "no-cookie", AccountType: "115"},
		{Name: "openlist", AccountType: "openlist", Cookie: "token"},
	}
	mappings := []MappingScanRequest{
		{CloudPath: "/电影", LocalPath: "/media/movies"},
		{Account: "alice", CloudPath: "/tv", LocalPath: "/media/tv"},
		{Account: "alice", CloudPath: "/电影", LocalPath: "/media/movies"},
	}

	got := ExpandCleanupMappingsForAccounts(mappings, accounts)
	if len(got) != 3 {
		t.Fatalf("expanded mappings = %d, want 3: %+v", len(got), got)
	}

	type mapKey struct {
		account string
		cloud   string
		local   string
	}
	seen := make(map[mapKey]bool)
	for _, m := range got {
		seen[mapKey{m.Account, m.CloudPath, m.LocalPath}] = true
	}
	for _, want := range []mapKey{
		{"alice", "/电影", "/media/movies"},
		{"bob", "/电影", "/media/movies"},
		{"alice", "/tv", "/media/tv"},
	} {
		if !seen[want] {
			t.Fatalf("missing expanded mapping %+v in %+v", want, got)
		}
	}
}
