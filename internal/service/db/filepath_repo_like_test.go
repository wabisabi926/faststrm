package db

import (
	"database/sql"
	"testing"
)

// 相邻目录只差 _ / % / \ 时，前缀操作不得波及其它子树。
func seedLikeFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	files := []struct{ id, path string }{
		{"1", "movies/a_b/one.mkv"},
		{"2", "movies/axb/two.mkv"},
		{"3", "movies/a%b/x.mkv"},
		{"4", "movies/axxb/y.mkv"},
		{"5", `movies/a\b/z.mkv`},
		{"6", "movies/ab/w.mkv"},
	}
	for _, f := range files {
		if err := UpsertFilePathEntry(db, "acc", FilePathEntry{
			FileID: f.id, Path: f.path, FileName: pathBase(f.path),
			ParentID: "0", PickCode: "pc" + f.id, UpdateTime: 1700000000,
		}); err != nil {
			t.Fatalf("UpsertFilePathEntry %s: %v", f.path, err)
		}
	}
	folders := []struct{ id, path string }{
		{"10", "movies/a_b"},
		{"11", "movies/axb"},
		{"12", "movies/a%b"},
		{"13", "movies/axxb"},
		{"14", `movies/a\b`},
		{"15", "movies/ab"},
		{"16", "movies/ab/sub"},
	}
	for _, f := range folders {
		if err := UpsertFolderEntry(db, "acc", FilePathEntry{
			FileID: f.id, Path: f.path, FileName: pathBase(f.path),
			ParentID: "0", UpdateTime: 1700000000,
		}); err != nil {
			t.Fatalf("UpsertFolderEntry %s: %v", f.path, err)
		}
	}
}

func entryPath(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	e, err := GetFilePathEntry(db, "acc", id)
	if err != nil {
		t.Fatalf("GetFilePathEntry %s: %v", id, err)
	}
	if e == nil {
		return ""
	}
	return e.Path
}

func folderPath(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	e, err := GetFolderEntry(db, "acc", id)
	if err != nil {
		t.Fatalf("GetFolderEntry %s: %v", id, err)
	}
	if e == nil {
		return ""
	}
	return e.Path
}

func TestUpdatePathPrefixBatch_EscapesLikeMetacharacters(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		wantID   string
		wantPath string
		keepID   string
		keepPath string
	}{
		{"underscore", "movies/a_b", "movies/new", "1", "movies/new/one.mkv", "2", "movies/axb/two.mkv"},
		{"percent", "movies/a%b", "movies/pnew", "3", "movies/pnew/x.mkv", "4", "movies/axxb/y.mkv"},
		{"backslash", `movies/a\b`, "movies/bnew", "5", `movies/bnew/z.mkv`, "6", "movies/ab/w.mkv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := setupTmpDB(t)
			defer cleanup()
			seedLikeFixture(t, db)

			n, err := UpdatePathPrefixBatch(db, "acc", tc.old, tc.new)
			if err != nil {
				t.Fatalf("UpdatePathPrefixBatch: %v", err)
			}
			if n != 1 {
				t.Fatalf("更新行数 = %d, want 1（不得匹配相邻子树）", n)
			}
			if got := entryPath(t, db, tc.wantID); got != tc.wantPath {
				t.Errorf("目标子树 path = %q, want %q", got, tc.wantPath)
			}
			if got := entryPath(t, db, tc.keepID); got != tc.keepPath {
				t.Errorf("相邻子树 path = %q, want %q", got, tc.keepPath)
			}
		})
	}
}

func TestDeleteByPathPrefix_EscapesLikeMetacharacters(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		gone   []string
		kept   []string
	}{
		{"underscore", "movies/a_b", []string{"1", "10"}, []string{"2", "3"}},
		{"percent", "movies/a%b", []string{"3", "12"}, []string{"1", "4"}},
		{"backslash", `movies/a\b`, []string{"5", "14"}, []string{"6"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := setupTmpDB(t)
			defer cleanup()
			seedLikeFixture(t, db)

			if _, err := DeleteByPathPrefix(db, "acc", tc.prefix); err != nil {
				t.Fatalf("DeleteByPathPrefix: %v", err)
			}
			for _, id := range tc.gone {
				if got := entryPath(t, db, id); got != "" {
					t.Errorf("目标 files id=%s 应删除，got %q", id, got)
				}
				if got := folderPath(t, db, id); got != "" {
					t.Errorf("目标 files id=%s 或 folders 同 id 残留 %q", id, got)
				}
			}
			for _, id := range tc.kept {
				if got := entryPath(t, db, id); got == "" {
					t.Errorf("相邻 files id=%s 不应删除", id)
				}
			}
			for _, id := range []string{"10", "11", "12", "13", "14", "15", "16"} {
				if got := folderPath(t, db, id); got == "" && !containsID(tc.gone, id) {
					t.Errorf("相邻 folders id=%s 不应删除", id)
				}
			}
		})
	}
}

func TestFolderPrefixBatch_EscapesLikeMetacharacters(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()
	seedLikeFixture(t, db)

	n, err := UpdateFolderPathPrefixBatch(db, "acc", "movies/a_b", "movies/new")
	if err != nil {
		t.Fatalf("UpdateFolderPathPrefixBatch: %v", err)
	}
	if n != 1 {
		t.Fatalf("更新行数 = %d, want 1", n)
	}
	if got := folderPath(t, db, "10"); got != "movies/new" {
		t.Errorf("目标 folder path = %q, want movies/new", got)
	}
	if got := folderPath(t, db, "11"); got != "movies/axb" {
		t.Errorf("相邻 folder path = %q, want movies/axb", got)
	}

	if _, err := DeleteFolderByPathPrefix(db, "acc", `movies/a\b`); err != nil {
		t.Fatalf("DeleteFolderByPathPrefix: %v", err)
	}
	if got := folderPath(t, db, "14"); got != "" {
		t.Errorf("目标 folder 应删除，got %q", got)
	}
	if got := folderPath(t, db, "15"); got != "movies/ab" {
		t.Errorf("相邻 folder 不应删除，got %q", got)
	}
	if got := folderPath(t, db, "16"); got != "movies/ab/sub" {
		t.Errorf("相邻子树 folder 不应删除，got %q", got)
	}
}

func TestPrefixQueries_EscapesLikeMetacharacters(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()
	seedLikeFixture(t, db)

	counts := []struct {
		prefix string
		want   int64
	}{
		{"movies/a_b", 1},
		{"/movies/a_b", 1},
		{"movies/a%b", 1},
		{`movies/a\b`, 1},
	}
	for _, tc := range counts {
		got, err := CountFilesByPrefix(db, "acc", tc.prefix)
		if err != nil {
			t.Fatalf("CountFilesByPrefix %q: %v", tc.prefix, err)
		}
		if got != tc.want {
			t.Errorf("CountFilesByPrefix(%q) = %d, want %d", tc.prefix, got, tc.want)
		}
	}

	snaps := []struct {
		prefix string
		want   string
	}{
		{"movies/a_b", "movies/a_b/one.mkv"},
		{"/movies/a_b", "movies/a_b/one.mkv"},
		{"movies/a%b", "movies/a%b/x.mkv"},
		{`movies/a\b`, `movies/a\b/z.mkv`},
	}
	for _, tc := range snaps {
		snap, err := ListSnapshotByAccount(db, "acc", tc.prefix)
		if err != nil {
			t.Fatalf("ListSnapshotByAccount %q: %v", tc.prefix, err)
		}
		if len(snap) != 1 {
			t.Fatalf("ListSnapshotByAccount(%q) 条数 = %d, want 1: %v", tc.prefix, len(snap), snap)
		}
		if _, ok := snap[tc.want]; !ok {
			t.Errorf("ListSnapshotByAccount(%q) 缺少 %q: %v", tc.prefix, tc.want, snap)
		}
	}
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
