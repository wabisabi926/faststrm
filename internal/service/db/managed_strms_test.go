package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedStrmCRUD(t *testing.T) {
	database, err := OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	localPath := filepath.Join(root, "movie.strm")
	if err := os.WriteFile(localPath, []byte("url"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertFilePathEntry(database, "alice", FilePathEntry{
		FileID: "100", Path: "/电影/movie.mkv", FileName: "movie.mkv",
		PickCode: "d18nbyd6w552vx2zs", UpdateTime: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertManagedStrm(database, ManagedStrm{
		LocalPath: localPath, Account: "alice", CloudPath: "/电影/movie.mkv",
		Pickcode: "d18nbyd6w552vx2zs", FileID: "100", ContentSHA256: "abc",
		Size: 3, State: "active",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := GetManagedStrm(database, localPath)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Pickcode != "d18nbyd6w552vx2zs" || got.State != "active" {
		t.Fatalf("unexpected managed record: %+v", got)
	}

	list, err := ListManagedStrmsByRoot(database, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list[NormalizeManagedLocalPath(localPath)]; !ok {
		t.Fatalf("managed path not found in root list: %+v", list)
	}

	ok, err := PickcodeExists(database, "alice", "d18nbyd6w552vx2zs")
	if err != nil || !ok {
		t.Fatalf("PickcodeExists = %v, %v", ok, err)
	}
	ok, _ = PickcodeExists(database, "bob", "d18nbyd6w552vx2zs")
	if ok {
		t.Fatal("PickcodeExists should be account-scoped")
	}

	if err := MarkManagedStrmDeleted(database, localPath); err != nil {
		t.Fatal(err)
	}
	got, _ = GetManagedStrm(database, localPath)
	if got == nil || got.State != "deleted" {
		t.Fatalf("expected deleted state, got %+v", got)
	}
	list, _ = ListManagedStrmsByRoot(database, root)
	if len(list) != 0 {
		t.Fatalf("deleted records must not be listed, got %d", len(list))
	}
}
