package strm

import (
	"context"
	"errors"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
)

type fakeAccountReader struct {
	cookie string
}

func (f fakeAccountReader) Get(name string) *model.AccountInfo {
	if name != "alice" {
		return nil
	}
	return &model.AccountInfo{Name: "alice", Cookie: f.cookie}
}

type fakeDirLister struct {
	dirs      map[string]int64
	files     map[string][]client115.FsFileEntry
	listCalls int
	err       error
}

func (f *fakeDirLister) FsDirGetID(context.Context, string, string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	for p, id := range f.dirs {
		if p == "/电影" {
			return id, nil
		}
	}
	return 0, errors.New("dir not found")
}

func (f *fakeDirLister) FsFiles(_ context.Context, cid string, limit, offset int, _ string) (*client115.FsFilesResp, error) {
	f.listCalls++
	if f.err != nil {
		return nil, f.err
	}
	all := f.files[cid]
	if offset >= len(all) {
		return &client115.FsFilesResp{State: true, Count: len(all)}, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return &client115.FsFilesResp{State: true, Data: all[offset:end], Count: len(all)}, nil
}

func newFakeChecker(present bool) (*CloudChecker, *fakeDirLister) {
	lister := &fakeDirLister{
		dirs: map[string]int64{"/电影": 100},
	}
	if present {
		lister.files = map[string][]client115.FsFileEntry{
			"100": {{PickCode: "d18nbyd6w552vx2zs", Name: "movie.iso"}},
		}
	}
	return NewCloudChecker(lister, fakeAccountReader{cookie: "UID=x"}), lister
}

func TestCloudChecker_StillAbsent(t *testing.T) {
	checker, _ := newFakeChecker(false)
	absent, err := checker.StillAbsent(context.Background(), "alice", "/电影/movie.iso", "d18nbyd6w552vx2zs")
	if err != nil || !absent {
		t.Fatalf("absent=%v err=%v", absent, err)
	}
}

func TestCloudChecker_Present(t *testing.T) {
	checker, _ := newFakeChecker(true)
	absent, err := checker.StillAbsent(context.Background(), "alice", "/电影/movie.iso", "d18nbyd6w552vx2zs")
	if err != nil {
		t.Fatal(err)
	}
	if absent {
		t.Fatal("present pickcode must not be reported absent")
	}
}

func TestCloudChecker_SamePickcodeKeepsFile(t *testing.T) {
	checker, _ := newFakeChecker(true) // 同目录有 movie.iso（pickcode 相同）
	absent, err := checker.StillAbsent(context.Background(), "alice", "/电影/old-name.iso", "d18nbyd6w552vx2zs")
	if err != nil {
		t.Fatal(err)
	}
	if absent {
		t.Fatal("same pickcode still exists; local file must be preserved")
	}
}

func TestCloudChecker_CachesPerDirectory(t *testing.T) {
	checker, lister := newFakeChecker(false)
	_, _ = checker.StillAbsent(context.Background(), "alice", "/电影/a.iso", "d18nbyd6w552vx2zs")
	_, _ = checker.StillAbsent(context.Background(), "alice", "/电影/b.iso", "AAAAAAAAAAAAAAAAA")
	if lister.listCalls != 1 {
		t.Fatalf("listCalls=%d want 1", lister.listCalls)
	}
}

func TestCloudChecker_Unverified(t *testing.T) {
	checker, _ := newFakeChecker(false)
	if _, err := checker.StillAbsent(context.Background(), "alice", "", "d18nbyd6w552vx2zs"); err == nil {
		t.Fatal("empty cloud path should be unverified")
	}
	if _, err := checker.StillAbsent(context.Background(), "bob", "/电影/x.iso", "d18nbyd6w552vx2zs"); err == nil {
		t.Fatal("missing account should be unverified")
	}
}
