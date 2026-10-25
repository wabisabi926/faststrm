package strm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/db"
)

func TestParseManagedContent(t *testing.T) {
	account, pickcode := ParseManagedContent("http://nas:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs\n")
	if account != "alice" || pickcode != "d18nbyd6w552vx2zs" {
		t.Fatalf("got %q %q", account, pickcode)
	}
	account, pickcode = ParseManagedContent("not a url")
	if account != "" || pickcode != "" {
		t.Fatalf("expected empty, got %q %q", account, pickcode)
	}
}

func TestManagedRecorderVerifyAndBootstrap(t *testing.T) {
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.UpsertFilePathEntry(database, "alice", db.FilePathEntry{
		FileID: "100", Path: "/电影/movie.mkv", FileName: "movie.mkv",
		PickCode: "d18nbyd6w552vx2zs", UpdateTime: 1,
	}); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	managedPath := filepath.Join(root, "movie.strm")
	content := "http://nas:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs\n"
	if err := os.WriteFile(managedPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := NewManagedRecorder(database)
	if err := rec.Record(context.Background(), ManagedInput{
		LocalPath: managedPath, Account: "alice", CloudPath: "/电影/movie.mkv",
		Pickcode: "d18nbyd6w552vx2zs", Content: content, Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
	ok, err := rec.VerifyFile(context.Background(), managedPath, "d18nbyd6w552vx2zs")
	if err != nil || !ok {
		t.Fatalf("VerifyFile = %v, %v", ok, err)
	}
	if ok, _ := rec.VerifyFile(context.Background(), managedPath, "AAAAAAAAAAAAAAAAA"); ok {
		t.Fatal("wrong pickcode must fail verification")
	}
	if err := os.WriteFile(managedPath, []byte(content+"changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rec.VerifyFile(context.Background(), managedPath, "d18nbyd6w552vx2zs"); ok {
		t.Fatal("changed file must fail verification")
	}

	// 新目录测试历史纳管：合法且在云端索引中 → 纳管；手写/未知 pickcode → 跳过
	root2 := t.TempDir()
	known := filepath.Join(root2, "known.strm")
	if err := os.WriteFile(known, []byte("http://nas:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root2, "foreign.strm")
	if err := os.WriteFile(foreign, []byte("http://other/play"), 0o600); err != nil {
		t.Fatal(err)
	}
	thirdParty := filepath.Join(root2, "third-party.strm")
	if err := os.WriteFile(thirdParty, []byte("http://other/play?account=alice&pickcode=d18nbyd6w552vx2zs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := rec.Bootstrap(context.Background(), []string{root2}, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || skipped < 2 {
		t.Fatalf("bootstrap added=%d skipped=%d", added, skipped)
	}
	if m, _ := db.GetManagedStrm(database, known); m == nil {
		t.Fatal("known strm should be managed")
	} else if m.CloudPath != "电影/movie.mkv" {
		t.Fatalf("bootstrap cloud path = %q", m.CloudPath)
	}
	if m, _ := db.GetManagedStrm(database, foreign); m != nil {
		t.Fatal("foreign strm must not be managed")
	}
	if m, _ := db.GetManagedStrm(database, thirdParty); m != nil {
		t.Fatal("non-standard URL with pickcode must not be managed")
	}
}

func TestBootstrapCleansStaleStrmTmp(t *testing.T) {
	database, err := db.OpenNew(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	root := t.TempDir()
	stale := filepath.Join(root, "movie.strm.tmp-old")
	fresh := filepath.Join(root, "movie.strm.tmp-new")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("tmp"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-staleStrmTmpAge - time.Hour)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	rec := NewManagedRecorder(database)
	if _, _, err := rec.Bootstrap(context.Background(), []string{root}, []string{"alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("超过保留期的临时文件应被清理，stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("未到保留期的临时文件应保留: %v", err)
	}
}

func TestManagedRecorderNilDB(t *testing.T) {
	if rec := NewManagedRecorder(nil); rec != nil {
		t.Fatal("nil db should return nil recorder")
	}
}
