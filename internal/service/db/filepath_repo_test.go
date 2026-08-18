package db

import (
	"database/sql"
	"os"
	"strconv"
	"testing"
)

func setupTmpDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := OpenNew(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	}
	return db, cleanup
}

func TestDBCreateAndSchema(t *testing.T) {
	_, cleanup := setupTmpDB(t)
	defer cleanup()
}

func TestUpsertAndGet(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	e := FilePathEntry{
		FileID:     "123456789",
		Path:       "/电影/测试片.mkv",
		FileName:   "测试片.mkv",
		ParentID:   "100",
		PickCode:   "abc123",
		UpdateTime: 1700000000,
	}
	if err := UpsertFilePathEntry(db, "acc1", e); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := GetFilePathEntry(db, "acc1", "123456789")
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if got.Path != "电影/测试片.mkv" {
		t.Fatalf("path not normalized: %q", got.Path)
	}
	if got.PickCode != "abc123" {
		t.Fatalf("pickcode mismatch")
	}
	byPath, err := GetFilePathEntryByPath(db, "acc1", "电影/测试片.mkv")
	if err != nil || byPath == nil {
		t.Fatalf("GetByPath: err=%v", err)
	}
	if byPath.FileID != e.FileID {
		t.Fatalf("byPath fileId wrong: %s", byPath.FileID)
	}
	// Update
	e2 := e
	e2.PickCode = "new987"
	if err := UpsertFilePathEntry(db, "acc1", e2); err != nil {
		t.Fatal(err)
	}
	got2, _ := GetFilePathEntry(db, "acc1", "123456789")
	if got2.PickCode != "new987" {
		t.Fatalf("update not applied: %s", got2.PickCode)
	}
}

func TestBatchUpsert(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	var entries []FilePathEntry
	for i := 0; i < 5; i++ {
		s := strconv.Itoa(i)
		entries = append(entries, FilePathEntry{
			FileID:     strconv.Itoa(1000 + i),
			Path:       "/电影/片" + s + ".mp4",
			FileName:   "片" + s + ".mp4",
			ParentID:   "500",
			PickCode:   "p" + s,
			UpdateTime: 1700000000,
		})
	}
	if err := UpsertFilePathEntryBatch(db, "acc2", entries); err != nil {
		t.Fatal(err)
	}
	n, _ := GetEntryCount(db, "acc2")
	if n != 5 {
		t.Fatalf("count want 5 got %d", n)
	}
}

func TestUpdatePathPrefixBatch(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	entries := []FilePathEntry{
		{FileID: "1", Path: "/老文件夹/a.mkv", FileName: "a.mkv"},
		{FileID: "2", Path: "/老文件夹/sub/b.mkv", FileName: "b.mkv"},
		{FileID: "3", Path: "/其他/x.mkv", FileName: "x.mkv"},
	}
	if err := UpsertFilePathEntryBatch(db, "acc", entries); err != nil {
		t.Fatalf("batch upsert: %v", err)
	}
	// 先确认插入成功
	pre, _ := GetFilePathEntry(db, "acc", "1")
	if pre == nil {
		t.Fatalf("precheck: file 1 not inserted")
	}
	n1, _ := GetEntryCount(db, "acc")
	if n1 != 3 {
		t.Fatalf("precheck: count want 3 got %d", n1)
	}
	changed, err := UpdatePathPrefixBatch(db, "acc", "/老文件夹", "/新文件夹")
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("changed want 2 got %d", changed)
	}
	e1, err := GetFilePathEntry(db, "acc", "1")
	if err != nil {
		t.Fatalf("get 1 err: %v", err)
	}
	if e1 == nil {
		t.Fatalf("file 1 missing after update")
	}
	if e1.Path != "新文件夹/a.mkv" {
		t.Fatalf("prefix1 wrong: %q", e1.Path)
	}
	e2, _ := GetFilePathEntry(db, "acc", "2")
	if e2.Path != "新文件夹/sub/b.mkv" {
		t.Fatalf("prefix2 wrong: %q", e2.Path)
	}
	e3, _ := GetFilePathEntry(db, "acc", "3")
	if e3.Path != "其他/x.mkv" {
		t.Fatalf("other wrongly modified: %q", e3.Path)
	}
}

func TestDeleteBatchAndCount(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	es := []FilePathEntry{
		{FileID: "1", Path: "/a/1.mkv", FileName: "1.mkv"},
		{FileID: "2", Path: "/a/2.mkv", FileName: "2.mkv"},
		{FileID: "3", Path: "/a/sub/3.mkv", FileName: "3.mkv"},
		{FileID: "4", Path: "/b/4.mkv", FileName: "4.mkv"},
	}
	_ = UpsertFilePathEntryBatch(db, "x", es)

	n, err := DeleteByPath(db, "x", "/a/1.mkv")
	if err != nil || n != 1 {
		t.Fatalf("DeleteByPath: n=%d err=%v", n, err)
	}
	n2, err := DeleteByPathPrefix(db, "x", "/a")
	if err != nil || n2 != 2 {
		t.Fatalf("DeleteByPathPrefix: n=%d err=%v", n2, err)
	}
	cnt, _ := GetEntryCount(db, "x")
	if cnt != 1 {
		t.Fatalf("remaining want 1 got %d", cnt)
	}
	delN, _ := RemoveFilePathEntryBatch(db, "x", []string{"4"})
	if delN != 1 {
		t.Fatalf("RemoveBatch fail: %d", delN)
	}
}

func TestNormalizeDbPathPrefixRootProtect(t *testing.T) {
	n, err := DeleteByPathPrefix(nil, "acc", "/")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("root prefix should not delete, got %d", n)
	}
	n2, _ := UpdatePathPrefixBatch(nil, "acc", "/", "/x")
	if n2 != 0 {
		t.Fatalf("root prefix update should not update, got %d", n2)
	}
}
