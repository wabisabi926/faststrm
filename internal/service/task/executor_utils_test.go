package task

import (
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func TestResolveStrmSettings_Default(t *testing.T) {
	task := &Task{
		Account:    "acc1",
		OriginPath: "/影视",
		TargetPath: "/out",
	}
	s := model.DefaultSettings()
	r := resolveStrmSettings(task, s, "http://localhost:8090", "")

	// Prefix fallback to baseURL + account
	if !strings.Contains(r.StrmPrefix, "account=acc1") {
		t.Fatalf("expect account in prefix, got: %s", r.StrmPrefix)
	}
	if !strings.HasPrefix(r.StrmPrefix, "http://") {
		t.Fatalf("expect http prefix, got: %s", r.StrmPrefix)
	}
	// Extensions
	if len(r.StrmExtensions) == 0 {
		t.Fatal("expected default strm extensions")
	}
	if _, ok := r.StrmExtensions[".mp4"]; !ok {
		t.Fatal("expected .mp4 in strm exts (with dot)")
	}
	_ = r.DownloadExtensions
}

func TestResolveStrmSettings_TaskOverride(t *testing.T) {
	task := &Task{
		Account:            "acc2",
		StrmPrefix:         "https://public.example.com",
		EnablePathEncoding: true,
		Enable302:          true,
	}
	s := &model.Settings{}
	r := resolveStrmSettings(task, s, "", "")

	if !strings.HasPrefix(r.StrmPrefix, "https://public.example.com") {
		t.Fatalf("expect public prefix, got: %s", r.StrmPrefix)
	}
	if !strings.Contains(r.StrmPrefix, "account=acc2") {
		t.Fatalf("expect account appended: %s", r.StrmPrefix)
	}
	if !r.Enable302 {
		t.Fatal("Enable302 should be overridden to true")
	}
	if !r.EnablePathEncoding {
		t.Fatal("EnablePathEncoding should be overridden to true")
	}
}

func TestResolveStrmSettings_PublicDefaultPrefix(t *testing.T) {
	task := &Task{Account: "acc"}
	s := model.DefaultSettings()
	s.StrmPrefix = "https://strm.example.com"
	r := resolveStrmSettings(task, s, "http://127.0.0.1:8090", "")
	if !strings.HasPrefix(r.StrmPrefix, "https://strm.example.com") {
		t.Fatalf("expected StrmPrefix from settings, got: %s", r.StrmPrefix)
	}
}

func TestPerFilePercent_Overall(t *testing.T) {
	p := newPerFilePercent(4)
	p.Mark("a", 100)
	p.Mark("b", 50)
	done, overall := p.Overall(4)
	if done {
		t.Fatal("should not be done yet")
	}
	// (100 + 50 + 0 + 0) / 4 = 37.5
	if overall != "37.50" {
		t.Fatalf("expect 37.50, got: %s", overall)
	}
	p.Mark("c", 100)
	p.Mark("d", 100)
	// Still b at 50
	p.Update("b", 100)
	done, overall = p.Overall(4)
	if !done {
		t.Fatal("should be done")
	}
	if overall != "100.00" {
		t.Fatalf("expect 100.00, got: %s", overall)
	}
}

func TestCountAndFilterKind(t *testing.T) {
	items := []*fileItem{
		{Kind: kindStrm},
		{Kind: kindStrm},
		{Kind: kindDownload},
		{Kind: kindSkip},
	}
	if countKind(items, kindStrm) != 2 {
		t.Fatal("strm count expect 2")
	}
	if countKind(items, kindDownload) != 1 {
		t.Fatal("download count expect 1")
	}
	if len(filterKind(items, kindSkip)) != 1 {
		t.Fatal("skip filter expect 1")
	}
}

func TestUrlPathEncode(t *testing.T) {
	out := urlPathEncode("电影 2024.mp4")
	// space -> %20, Chinese -> percent encoded as UTF-8 bytes
	if !strings.Contains(out, "%20") {
		t.Fatalf("space must be encoded, got: %s", out)
	}
	if strings.Contains(out, "/") == false && strings.Contains(out, ".mp4") == false {
		// this condition is always false but preserves variable use for lint
	}
}

func TestBuildStrmContent_302Mode(t *testing.T) {
	task := &Task{Account: "acc1"}
	f := &fileItem{Name: "movie.mkv", PickCode: "pc1"}
	r := resolvedStrm{
		StrmPrefix: "http://x:8090?a=1",
		Enable302:  true,
	}
	out, err := buildStrmContent(task, f, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/api/fs/get") {
		t.Fatalf("302 mode expects /api/fs/get, got: %s", out)
	}
	if !strings.Contains(out, "pickcode=pc1") {
		t.Fatalf("expect pickcode: %s", out)
	}
	if !strings.Contains(out, "account=acc1") {
		t.Fatalf("expect account=acc1: %s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("expect trailing newline")
	}
}

func TestBuildStrmContent_DefaultMode(t *testing.T) {
	task := &Task{Account: "acc1"}
	f := &fileItem{Name: "movie.mkv", PickCode: "pc1"}
	r := resolvedStrm{
		StrmPrefix: "http://x:8090",
		Enable302:  false,
	}
	out, err := buildStrmContent(task, f, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/api/strm") {
		t.Fatalf("default mode expects /api/strm, got: %s", out)
	}
}
