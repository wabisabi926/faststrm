package handler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// TestBuildStrmForMissing_MatchesUnified 清理补建链路必须与统一生成器逐字节一致。
// 历史问题：cleanup 自己拼 URL，缺结尾换行、缺 file_name、前缀空时硬编码 127.0.0.1，
// 与全量任务生成的内容不同 → 全量重扫判定「内容变化」后重写，触发 Emby 假重扫。
func TestBuildStrmForMissing_MatchesUnified(t *testing.T) {
	cases := []struct {
		name     string
		cloud    string
		relPath  string
		account  string
		pickcode string
		prefix   string
		encode   bool
		template string
	}{
		{
			name: "标准格式", cloud: "电影", relPath: "movie.mkv", account: "user",
			pickcode: "pc1234567890abcde", prefix: "http://nas:8090", encode: true,
		},
		{
			name: "编码关闭", cloud: "电影", relPath: "电影 2024.mkv", account: "副卡",
			pickcode: "pc1234567890abcde", prefix: "http://nas:8090", encode: false,
		},
		{
			name: "多级目录", cloud: "电影/华语", relPath: "东邪西毒 (2008)/x.mkv", account: "acc",
			pickcode: "pc1234567890abcde", prefix: "http://nas:8090", encode: true,
		},
		{
			name: "模板优先", cloud: "电影", relPath: "movie.mkv", account: "user",
			pickcode: "pc1234567890abcde", prefix: "http://nas:8090", encode: true,
			template: "{prefix}/api/strm?account={account}&pickcode={pickcode}&path={file_path}",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := MappingScanResult{Account: c.account, LocalPath: "/data/media", CloudPath: c.cloud}
			miss := MissingStrm{RelPath: c.relPath, PickCode: c.pickcode}
			settings := &model.Settings{
				StrmPrefix:         c.prefix,
				EnablePathEncoding: c.encode,
				Strm:               model.StrmSettings{StrmUrlTemplate: c.template},
			}

			_, got, err := buildStrmForMissing(m, miss, settings)
			if err != nil {
				t.Fatalf("buildStrmForMissing: %v", err)
			}

			cloudPath := strings.TrimRight(c.cloud, "/") + "/" + strmutil.LocalRelToCloudRel(c.relPath)
			want, err := strm.BuildContent(strm.ContentInput{
				Template:      c.template,
				Prefix:        c.prefix,
				Account:       c.account,
				PickCode:      c.pickcode,
				FileName:      filepath.Base(c.relPath),
				CloudPath:     cloudPath,
				DisableEncode: !c.encode,
			})
			if err != nil {
				t.Fatalf("统一生成器返回错误: %v", err)
			}
			if got != want {
				t.Fatalf("cleanup 与统一生成器输出不一致:\ncleanup=%q\nunified=%q", got, want)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Fatalf("内容应以换行结尾: %q", got)
			}
		})
	}
}

// TestBuildStrmForMissing_UnusablePrefixFallback 前缀为空/回环时不再硬编码 127.0.0.1：
// 能探测到内网 IP 就用内网 IP，探测不到就报错（宁可失败也不写死链）。
func TestBuildStrmForMissing_UnusablePrefixFallback(t *testing.T) {
	m := MappingScanResult{Account: "user", LocalPath: "/data/media", CloudPath: "电影"}
	miss := MissingStrm{RelPath: "movie.mkv", PickCode: "pc1234567890abcde"}

	for _, bad := range []string{"", "http://127.0.0.1:8090", "http://0.0.0.0:8090", "http://localhost:8090"} {
		settings := &model.Settings{StrmPrefix: bad, EnablePathEncoding: true}
		_, content, err := buildStrmForMissing(m, miss, settings)
		if err != nil {
			// 无可用内网地址时允许报错，但不允许生成死链
			if !strings.Contains(err.Error(), "前缀不可用") && !strings.Contains(err.Error(), "STRM 前缀") {
				t.Fatalf("prefix %q 报错信息应说明前缀不可用: %v", bad, err)
			}
			continue
		}
		if strings.Contains(content, "127.0.0.1") || strings.Contains(content, "0.0.0.0") || strings.Contains(content, "localhost") {
			t.Fatalf("prefix %q 不应生成回环地址: %q", bad, content)
		}
	}
}

// TestBuildStrmForMissing_InvalidPickcode 非法 pickcode 必须报错（不再生成打不开的 STRM）
func TestBuildStrmForMissing_InvalidPickcode(t *testing.T) {
	m := MappingScanResult{Account: "user", LocalPath: "/data/media", CloudPath: "电影"}
	settings := &model.Settings{StrmPrefix: "http://nas:8090", EnablePathEncoding: true}

	for _, pc := range []string{"", "abc", "pickcode1234567890"} {
		_, _, err := buildStrmForMissing(m, MissingStrm{RelPath: "movie.mkv", PickCode: pc}, settings)
		if err == nil {
			t.Fatalf("pickcode %q 应报错", pc)
		}
	}
}

// TestBuildStrmForMissing_UsesSourceName 补建必须用源文件名（.mkv/.iso），而不是 .strm 路径。
// 回归：历史实现把 MissingStrm.RelPath（xxx.strm）当源文件名，补建出的 STRM 里
// file_name=xxx.strm，播放器拿不到真实容器扩展名（ISO 尤其明显）。
func TestBuildStrmForMissing_UsesSourceName(t *testing.T) {
	m := MappingScanResult{Account: "acc", LocalPath: "/data/media", CloudPath: "电影"}
	settings := &model.Settings{StrmPrefix: "http://nas:8090", EnablePathEncoding: true}

	cases := []struct {
		name     string
		miss     MissingStrm
		wantURL  string
		wantName string
	}{
		{
			name: "普通视频",
			miss: MissingStrm{
				RelPath: "movie.strm", CloudRelPath: "movie.mkv", PickCode: "pc1234567890abcde",
			},
			wantURL:  "http://nas:8090/api/strm?account=acc&pickcode=pc1234567890abcde&file_name=movie.mkv\n",
			wantName: "movie.strm",
		},
		{
			name: "子目录 + ISO",
			miss: MissingStrm{
				RelPath: filepath.Join("原盘", "007.strm"), CloudRelPath: "原盘/007.iso",
				PickCode: "pc1234567890abcde",
			},
			wantURL:  "http://nas:8090/api/strm?account=acc&pickcode=pc1234567890abcde&file_name=007.iso\n",
			wantName: filepath.Join("原盘", "007.strm"),
		},
		{
			name:     "兼容旧 ScanSummary（无 CloudRelPath）",
			miss:     MissingStrm{RelPath: "legacy.strm", PickCode: "pc1234567890abcde"},
			wantURL:  "http://nas:8090/api/strm?account=acc&pickcode=pc1234567890abcde&file_name=legacy.strm\n",
			wantName: "legacy.strm",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			strmPath, content, err := buildStrmForMissing(m, c.miss, settings)
			if err != nil {
				t.Fatalf("buildStrmForMissing: %v", err)
			}
			if content != c.wantURL {
				t.Fatalf("content = %q, want %q", content, c.wantURL)
			}
			if filepath.Base(strmPath) != filepath.Base(c.wantName) {
				t.Fatalf("strmPath = %q, want basename %q", strmPath, c.wantName)
			}
		})
	}
}

// TestBuildStrmForMissing_TemplateUsesSourceExt 模板变量 {ext}/{stem}/{file_path} 也必须是源文件形态
func TestBuildStrmForMissing_TemplateUsesSourceExt(t *testing.T) {
	m := MappingScanResult{Account: "acc", LocalPath: "/data/media", CloudPath: "电影"}
	settings := &model.Settings{
		StrmPrefix:         "http://nas:8090",
		EnablePathEncoding: true,
		Strm: model.StrmSettings{
			StrmUrlTemplate: "{prefix}/play/{stem}{ext}?pc={pickcode}&p={file_path}",
		},
	}
	_, content, err := buildStrmForMissing(m, MissingStrm{
		RelPath: "007.strm", CloudRelPath: "华语/007：大破量子危机.iso", PickCode: "pc1234567890abcde",
	}, settings)
	if err != nil {
		t.Fatalf("buildStrmForMissing: %v", err)
	}
	if !strings.Contains(content, "/play/007：大破量子危机.iso.iso?") {
		t.Fatalf("stem+ext 应为源文件形态（.iso 双扩展名），got %q", content)
	}
	// {file_path} 用 URLPathEncode：中文编码、'/' 保留（{file_path_strict} 才是 %2F）
	if !strings.Contains(content, "p=%E7%94%B5%E5%BD%B1/%E5%8D%8E%E8%AF%AD/") {
		t.Fatalf("{file_path} 应为「中文编码 + 保留 /」的云端路径，got %q", content)
	}
}

func TestCleanupSettingsForPrefix(t *testing.T) {
	original := &model.Settings{StrmPrefix: ""}
	got := cleanupSettingsForPrefix(original, "http://nas:9000/")
	if got == original {
		t.Fatal("expected a settings clone when falling back to PublicBaseURL")
	}
	if got.StrmPrefix != "http://nas:9000" {
		t.Fatalf("prefix = %q, want http://nas:9000", got.StrmPrefix)
	}
	if original.StrmPrefix != "" {
		t.Fatalf("original settings must not be mutated, got %q", original.StrmPrefix)
	}

	configured := &model.Settings{StrmPrefix: "http://configured:8090"}
	if got := cleanupSettingsForPrefix(configured, "http://nas:9000"); got != configured {
		t.Fatal("configured prefix should take precedence")
	}
}
