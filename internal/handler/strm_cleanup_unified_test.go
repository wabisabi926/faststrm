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
