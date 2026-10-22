package strmimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeResolver struct {
	pickcode string
	err      error
	calls    int
}

func (r *fakeResolver) ResolvePickCode(context.Context, string, string) (string, error) {
	r.calls++
	return r.pickcode, r.err
}

func writeTestStrm(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunDryRunAndWrite(t *testing.T) {
	root := t.TempDir()
	legacy := writeTestStrm(t, root, filepath.Join("电影", "legacy.strm"),
		"http://old:8090/api/fs/get?account=old&pickcode=d18nbyd6w552vx2zs&file_name=Movie.iso\n")
	p115 := writeTestStrm(t, root, filepath.Join("电影", "p115.strm"),
		"http://moviepilot/api/v1/plugin/P115StrmHelper/redirect_url?pickcode=d18nbyd6w552vx2zs\n")
	lite := writeTestStrm(t, root, filepath.Join("电影", "lite.strm"),
		"https://pan.example.com/api/strm/play/2/MTIzNDU/t/token/n/Movie.iso\n")
	unknown := writeTestStrm(t, root, "cdn.strm", "https://cdn.115.com/foo?t=1\n")

	opts := Options{
		Root:               root,
		TargetAccount:      "alice",
		Prefix:             "http://nas:8090",
		EnablePathEncoding: true,
		Resolver:           &fakeResolver{pickcode: "abcdefghij1234567"},
	}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.Changed != 3 || report.Written != 0 || report.Skipped != 1 {
		t.Fatalf("dry run report = %+v", report)
	}
	if got, _ := os.ReadFile(legacy); !strings.Contains(string(got), "/api/fs/get") {
		t.Fatalf("dry run must not rewrite: %q", got)
	}

	opts.Write = true
	opts.BackupDir = filepath.Join(root, ".backup")
	report, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if report.Changed != 3 || report.Written != 3 || report.Skipped != 1 {
		t.Fatalf("write report = %+v", report)
	}
	got, _ := os.ReadFile(legacy)
	if want := "http://nas:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs&file_name=Movie.iso\n"; string(got) != want {
		t.Fatalf("legacy content = %q, want %q", got, want)
	}
	got, _ = os.ReadFile(p115)
	if !strings.Contains(string(got), "/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs") {
		t.Fatalf("p115 content = %q", got)
	}
	got, _ = os.ReadFile(lite)
	if !strings.Contains(string(got), "pickcode=abcdefghij1234567") {
		t.Fatalf("litepan content = %q", got)
	}
	got, _ = os.ReadFile(unknown)
	if strings.Contains(string(got), "/api/strm") {
		t.Fatalf("unknown must stay untouched: %q", got)
	}
	backup, err := os.ReadFile(filepath.Join(root, ".backup", "电影", "legacy.strm"))
	if err != nil || !strings.Contains(string(backup), "/api/fs/get") {
		t.Fatalf("backup = %q, err=%v", backup, err)
	}

	// 二次执行应为 unchanged。
	report, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Unchanged != 3 || report.Changed != 0 {
		t.Fatalf("second run report = %+v", report)
	}
}

func TestRunAccountMapAndRegister(t *testing.T) {
	root := t.TempDir()
	writeTestStrm(t, root, "电影/lite.strm",
		"https://pan.example.com/api/strm/play/2/MTIzNDU/t/token/n/Movie.iso\n")
	var registered RegisterInput
	report, err := Run(context.Background(), Options{
		Root:               root,
		AccountMap:         map[string]string{"2": "alice"},
		Prefix:             "http://nas:8090",
		Write:              true,
		CloudRoot:          "/媒体",
		EnablePathEncoding: true,
		Resolver:           &fakeResolver{pickcode: "abcdefghij1234567"},
		Register: func(_ context.Context, in RegisterInput) error {
			registered = in
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Written != 1 {
		t.Fatalf("report = %+v", report)
	}
	if registered.Account != "alice" || registered.CloudPath != "/媒体/电影/Movie.iso" {
		t.Fatalf("registered = %+v", registered)
	}
}

func TestRunFromFilter(t *testing.T) {
	root := t.TempDir()
	writeTestStrm(t, root, "a.strm", "http://old/api/fs/get?account=a&pickcode=d18nbyd6w552vx2zs\n")
	report, err := Run(context.Background(), Options{
		Root:   root,
		From:   SourceP115,
		Prefix: "http://nas:8090",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Skipped != 1 {
		t.Fatalf("report = %+v", report)
	}
}
