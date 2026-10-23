package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateManagedPathPrefixMigratesTree(t *testing.T) {
	database, err := OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	newDir := filepath.Join(root, "new")
	otherDir := filepath.Join(root, "other")
	for _, dir := range []string{oldDir, otherDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	records := []ManagedStrm{
		{LocalPath: filepath.Join(oldDir, "a.strm"), Account: "alice", CloudPath: "/电影/old/a.mkv", Pickcode: "pc-a", State: "active"},
		{LocalPath: filepath.Join(oldDir, "sub", "b.strm"), Account: "alice", CloudPath: "/电影/old/sub/b.mkv", Pickcode: "pc-b", State: "active"},
		{LocalPath: filepath.Join(otherDir, "c.strm"), Account: "alice", CloudPath: "/电影/other/c.mkv", Pickcode: "pc-c", State: "active"},
	}
	for _, r := range records {
		if err := UpsertManagedStrm(database, r); err != nil {
			t.Fatal(err)
		}
	}

	n, err := UpdateManagedPathPrefix(database, oldDir, newDir, "/电影/old", "/电影/new")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 migrated rows, got %d", n)
	}

	cases := []struct {
		newPath   string
		wantCloud string
	}{
		{filepath.Join(newDir, "a.strm"), "/电影/new/a.mkv"},
		{filepath.Join(newDir, "sub", "b.strm"), "/电影/new/sub/b.mkv"},
	}
	for _, c := range cases {
		got, err := GetManagedStrm(database, c.newPath)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatalf("managed record missing at new path %s", c.newPath)
		}
		if got.CloudPath != c.wantCloud {
			t.Fatalf("cloud path = %q, want %q", got.CloudPath, c.wantCloud)
		}
		if got.State != "active" {
			t.Fatalf("state = %q, want active", got.State)
		}
	}
	for _, oldPath := range []string{filepath.Join(oldDir, "a.strm"), filepath.Join(oldDir, "sub", "b.strm")} {
		got, err := GetManagedStrm(database, oldPath)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("old managed record should be gone: %s", oldPath)
		}
	}
	other, err := GetManagedStrm(database, filepath.Join(otherDir, "c.strm"))
	if err != nil || other == nil || other.CloudPath != "/电影/other/c.mkv" {
		t.Fatalf("unrelated record changed: %+v, err=%v", other, err)
	}
}

func TestUpdateManagedPathPrefixRejectsConflict(t *testing.T) {
	database, err := OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	newDir := filepath.Join(root, "new")
	oldPath := filepath.Join(oldDir, "a.strm")
	newPath := filepath.Join(newDir, "a.strm")

	if err := UpsertManagedStrm(database, ManagedStrm{
		LocalPath: oldPath, Account: "alice", CloudPath: "/电影/old/a.mkv", Pickcode: "pc-a", State: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertManagedStrm(database, ManagedStrm{
		LocalPath: newPath, Account: "alice", CloudPath: "/电影/new/a.mkv", Pickcode: "pc-new", State: "active",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := UpdateManagedPathPrefix(database, oldDir, newDir, "/电影/old", "/电影/new"); err == nil {
		t.Fatal("expected conflict error")
	}

	got, err := GetManagedStrm(database, oldPath)
	if err != nil || got == nil || got.Pickcode != "pc-a" {
		t.Fatalf("old record must survive failed migration: %+v, err=%v", got, err)
	}
	got, err = GetManagedStrm(database, newPath)
	if err != nil || got == nil || got.Pickcode != "pc-new" {
		t.Fatalf("new record must not be overwritten: %+v, err=%v", got, err)
	}
}

func TestUpdateManagedPathPrefixRejectsNestedMove(t *testing.T) {
	database, err := OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	nested := filepath.Join(oldDir, "sub")
	if err := UpsertManagedStrm(database, ManagedStrm{
		LocalPath: filepath.Join(oldDir, "a.strm"), Account: "alice", CloudPath: "/电影/old/a.mkv", Pickcode: "pc-a", State: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateManagedPathPrefix(database, oldDir, nested, "/电影/old", "/电影/old/sub"); err == nil {
		t.Fatal("expected nested move rejection")
	}
}
