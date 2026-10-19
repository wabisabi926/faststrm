package model

import (
	"net/url"
	"strings"
	"testing"
)

// TestRenderStrmUrlTemplate_NewVars 新增变量（对齐 p115strmhelper）：
// {file_path} {file_id} {file_name} {file_stem} {file_suffix} {base_url}
func TestRenderStrmUrlTemplate_NewVars(t *testing.T) {
	got := RenderStrmUrlTemplate(StrmURLTemplateVars{
		Template:  "{base_url}/play?pc={pickcode}&fid={file_id}&n={file_name}&p={file_path}&s={file_stem}&x={file_suffix}",
		Prefix:    "http://nas:8090/",
		Account:   "acc1",
		PickCode:  "abcdefghij1234567",
		FileName:  "流浪地球2.2023.mkv",
		Ext:       ".mkv",
		Stem:      "流浪地球2.2023",
		CloudPath: "电影/科幻/流浪地球2.2023.mkv",
		FileID:    "3490000000000000001",
	})
	want := "http://nas:8090/play?pc=abcdefghij1234567&fid=3490000000000000001" +
		"&n=" + url.QueryEscape("流浪地球2.2023.mkv") +
		"&p=" + url.QueryEscape("电影/科幻/流浪地球2.2023.mkv") +
		"&s=流浪地球2.2023&x=.mkv\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// TestRenderStrmUrlTemplate_LegacyVars faststrm 既有变量必须继续可用（向后兼容）
func TestRenderStrmUrlTemplate_LegacyVars(t *testing.T) {
	got := RenderStrmUrlTemplate(StrmURLTemplateVars{
		Template:  "{prefix}/api/strm?account={account}&pickcode={pickcode}&file_name={filename}&ext={ext}&stem={stem}",
		Prefix:    "http://nas:8090",
		Account:   "acc1",
		PickCode:  "abcdefghij1234567",
		FileName:  "movie.mkv",
		Ext:       ".mkv",
		Stem:      "movie",
		CloudPath: "电影/movie.mkv",
		FileID:    "100",
	})
	want := "http://nas:8090/api/strm?account=acc1&pickcode=abcdefghij1234567" +
		"&file_name=movie.mkv&ext=.mkv&stem=movie\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderStrmUrlTemplate_EmptyAndMissing 空模板返回空串；未知变量原样保留
func TestRenderStrmUrlTemplate_EmptyAndMissing(t *testing.T) {
	if got := RenderStrmUrlTemplate(StrmURLTemplateVars{}); got != "" {
		t.Fatalf("empty template should return empty, got %q", got)
	}
	got := RenderStrmUrlTemplate(StrmURLTemplateVars{
		Template: "{prefix}/x?unknown={nope}",
		Prefix:   "http://h",
	})
	if !strings.Contains(got, "{nope}") {
		t.Fatalf("unknown var should stay as-is, got %q", got)
	}
}

// TestRenderStrmUrlTemplate_FilePathRaw {file_path_raw} 不编码，可拼进 URL 路径段
func TestRenderStrmUrlTemplate_FilePathRaw(t *testing.T) {
	got := RenderStrmUrlTemplate(StrmURLTemplateVars{
		Template:  "{prefix}/play/{file_path_raw}?p={file_path}",
		Prefix:    "http://nas:8090",
		CloudPath: "电影/科幻/流浪地球2.2023.mkv",
	})
	want := "http://nas:8090/play/电影/科幻/流浪地球2.2023.mkv?p=" +
		url.QueryEscape("电影/科幻/流浪地球2.2023.mkv") + "\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// TestRenderStrmUrlTemplate_FileIDEmpty file_id 未知时渲染为空串（不残留占位符）
func TestRenderStrmUrlTemplate_FileIDEmpty(t *testing.T) {
	got := RenderStrmUrlTemplate(StrmURLTemplateVars{
		Template: "{prefix}/x?fid={file_id}",
		Prefix:   "http://h",
		FileID:   "",
	})
	if strings.Contains(got, "{file_id}") {
		t.Fatalf("placeholder should be replaced, got %q", got)
	}
	if got != "http://h/x?fid=\n" {
		t.Fatalf("got %q", got)
	}
}
