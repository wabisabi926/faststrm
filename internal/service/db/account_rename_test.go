package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameAccountDataMigratesManagedStrms(t *testing.T) {
	database, err := OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	localPath := filepath.Join(t.TempDir(), "movie.strm")
	if err := UpsertManagedStrm(database, ManagedStrm{
		LocalPath: localPath,
		Account:   "alice",
		CloudPath: "/电影/movie.mkv",
		Pickcode:  "d18nbyd6w552vx2zs",
		State:     "active",
	}); err != nil {
		t.Fatal(err)
	}

	counts, err := RenameAccountData(database, "alice", "bob")
	if err != nil && !strings.Contains(err.Error(), "no such table: life_event_cursor") {
		t.Fatal(err)
	}
	if counts["managed_strms"] != 1 {
		t.Fatalf("managed_strms migrated=%d, want 1", counts["managed_strms"])
	}
	got, err := GetManagedStrm(database, localPath)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Account != "bob" {
		t.Fatalf("managed account should be bob, got %+v", got)
	}
}
