package monitor

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/strm"
)

// TestGenerateStrmContent_MatchesUnified 生活事件链路必须与统一生成器逐字节一致。
// 历史教训：三条链路各写一份实现，URL 编码规则漂移后 monitor 生成的名字回读带 '+'，
// 全量任务侧却正常，问题只在播放器里才暴露。这里锁死「同一输入 → 同一输出」。
func TestGenerateStrmContent_MatchesUnified(t *testing.T) {
	cases := []struct {
		name       string
		cloudPath  string
		prefix     string
		account    string
		pickcode   string
		fileName   string
		fileID     string
		encodePath bool
		template   string
	}{
		{
			name: "标准格式-编码开", cloudPath: "电影/科幻/x.mkv", prefix: "http://nas:8090",
			account: "副卡", pickcode: "abcdefghij1234567", fileName: "美国狙击手 2024.mkv",
			encodePath: true,
		},
		{
			name: "标准格式-编码关", cloudPath: "电影/x.mkv", prefix: "http://nas:8090",
			account: "副卡", pickcode: "abcdefghij1234567", fileName: "电影 2024.mkv",
			encodePath: false,
		},
		{
			name: "ISO 双扩展名", cloudPath: "原盘/x.iso", prefix: "http://nas:8090",
			account: "acc", pickcode: "abcdefghij1234567", fileName: "007.iso", encodePath: true,
		},
		{
			name: "模板变量", cloudPath: "电影/华语/东邪西毒.mkv", prefix: "http://nas:8090",
			account: "acc", pickcode: "a7nds6qtempqehtnn", fileName: "东邪西毒.mkv", fileID: "12345",
			encodePath: true, template: "{prefix}/api/strm?account={account}&pickcode={pickcode}&path={file_path_strict}&id={file_id}",
		},
		{
			name: "前缀末尾斜杠", cloudPath: "电影/x.mkv", prefix: "http://nas:8090/",
			account: "acc", pickcode: "abcdefghij1234567", fileName: "x.mkv", encodePath: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var tmplArg []string
			if c.template != "" {
				tmplArg = []string{c.template}
			}
			got := generateStrmContent(
				c.cloudPath, c.prefix, c.account, c.pickcode, c.fileName, c.fileID, c.encodePath, tmplArg...,
			)
			want, err := strm.BuildContent(strm.ContentInput{
				Template:      c.template,
				Prefix:        c.prefix,
				Account:       c.account,
				PickCode:      c.pickcode,
				FileName:      c.fileName,
				CloudPath:     c.cloudPath,
				FileID:        c.fileID,
				DisableEncode: !c.encodePath,
			})
			if err != nil {
				t.Fatalf("统一生成器返回错误: %v", err)
			}
			if got != want {
				t.Fatalf("monitor 与统一生成器输出不一致:\nmonitor=%q\nunified=%q", got, want)
			}
		})
	}
}

// TestGenerateStrmContent_InvalidPickcodeSkips 非法 pickcode 不再生成死链，返回空串
func TestGenerateStrmContent_InvalidPickcodeSkips(t *testing.T) {
	for _, pc := range []string{"", "abc", "pickcode1234567890"} {
		if out := generateStrmContent(
			"电影/x.mkv", "http://nas:8090", "acc", pc, "x.mkv", "", true,
		); out != "" {
			t.Fatalf("pickcode %q 应跳过生成，got %q", pc, out)
		}
	}
}
