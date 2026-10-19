package handler

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/client115"
)

// cleanupWalkRT 假 115：按 cid 返回固定列目录响应（/files 接口）
type cleanupWalkRT struct {
	bodies map[string]string // cid → JSON body
}

func (r *cleanupWalkRT) RoundTrip(req *http.Request) (*http.Response, error) {
	body := r.bodies[req.URL.Query().Get("cid")]
	if body == "" {
		body = `{"state":true,"data":[],"count":0}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// walkCloudTree 必须与生成侧同规则转义名字里的 '/'（115 允许），
// 否则含 '/' 的文件其本地 STRM 会与云端名单对不上而被清理误删；
// Name 字段保留原始名供 pickcode 对账。
func TestWalkCloudTree_EscapesSlashInNames(t *testing.T) {
	rt := &cleanupWalkRT{bodies: map[string]string{
		"100": `{"state":true,"count":2,"data":[
			{"fid":1,"cid":100,"n":"国语次时代/台/粤音轨.mkv","fc":0},
			{"cid":300,"n":"纪录片 4K/60fps","fc":1}
		]}`,
		"300": `{"state":true,"count":1,"data":[
			{"fid":2,"cid":300,"n":"正片/花絮.mkv","fc":0}
		]}`,
	}}
	c := client115.NewClient("ua")
	c.HTTP = &http.Client{Transport: rt}

	var out []cloudWalkItem
	if err := walkCloudTree(context.Background(), c, "cookie", 100, "", &out); err != nil {
		t.Fatalf("walkCloudTree: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("应收集 3 个条目，实际 %d: %+v", len(out), out)
	}
	want := []struct{ name, rel string }{
		{"国语次时代/台/粤音轨.mkv", "国语次时代_台_粤音轨.mkv"},
		{"纪录片 4K/60fps", "纪录片 4K_60fps"},
		{"正片/花絮.mkv", "纪录片 4K_60fps/正片_花絮.mkv"},
	}
	for i, w := range want {
		if out[i].Name != w.name {
			t.Errorf("item %d Name=%q, want 原始名 %q（供 pickcode 对账）", i, out[i].Name, w.name)
		}
		if out[i].RelPath != w.rel {
			t.Errorf("item %d RelPath=%q, want 转义后 %q", i, out[i].RelPath, w.rel)
		}
	}
}

// cloudStrmRelKey 必须与生成侧 replaceRelPathExtToStrm 逐字节一致：
// 换扩展名之后还要走 SanitizeRelPath（':' → '：'），否则含冒号的片名会被误判失效。
func TestCloudStrmRelKey_MatchesGenerationRule(t *testing.T) {
	cases := map[string]string{
		"电影:1.mkv":      filepath.Join("电影：1.strm"),
		"纪录片 4K/正片.mkv": filepath.Join("纪录片 4K", "正片.strm"),
		"a/b.iso":       filepath.Join("a", "b.iso.strm"),
		"国语次时代_台/片.mkv": filepath.Join("国语次时代_台", "片.strm"),
	}
	for in, want := range cases {
		if got := cloudStrmRelKey(in); got != want {
			t.Errorf("cloudStrmRelKey(%q) = %q, want %q", in, got, want)
		}
	}
}
