package handler

import (
	"strings"
	"testing"
)

// TestPickOneFileName_ThreeTiers 验证三档文件名优先级：URL(file_name) > 115 API(meta.FileName) > CDN URL path
// 对应 GitHub v1.2.1 fs/get ISO default_redirect 致命漏判的修复：
// - 如果 Emby for Kodi Gen URL 没传 file_name 且 115 API meta.FileName 为空
// - 但实际 CDN URL path 最后一段是 xxx.iso / xxx.bdmv / xxx.mkv
// - 以前：三档都空 → finalName="" → 扩展名识别漏判 → default_redirect → ISO 直连不支持 seek → "没有兼容的流"
// - 修复后：CDN path 兜底 → 扩展名识别 → .iso 走 proxy，其他 redirect（保持零带宽）
func TestPickOneFileName_ThreeTiers(t *testing.T) {
	tests := []struct {
		name    string
		a       string // URL file_name
		b       string // 115 API meta.FileName
		cdnURL  string // CDN URL
		want    string
	}{
		{
			name:   "Tier1 优先 URL file_name（中文 .iso）",
			a:      "杜比视界测试：双层 FEL.iso",
			b:      "Whatever.mp4",
			cdnURL: "https://cdnwhfile.115cdn.net/xxhash/SomeOther.mkv?t=123",
			want:   "杜比视界测试：双层 FEL.iso",
		},
		{
			name:   "Tier2 URL 空 用 meta.FileName",
			a:      "",
			b:      "老九门.The.Mystic.Nine.2016.2160P.WEB-DL.H265.AAC.mp4",
			cdnURL: "https://cdnwhfile.115cdn.net/xxhash/Whatever.iso?t=123",
			want:   "老九门.The.Mystic.Nine.2016.2160P.WEB-DL.H265.AAC.mp4",
		},
		{
			name:   "Tier3 CDN URL path（URL 编码中文 .iso），前两档都空",
			a:      "",
			b:      "",
			cdnURL: "https://cdnwhfile.115cdn.net/5ea5f978603ed2240bdb18d1c5ad170a2e0dec/%E6%9D%9C%E6%AF%94%E8%A7%86%E7%95%8C%E6%B5%8B%E8%AF%95%EF%BC%9A%E5%8F%8C%E5%B1%82%20FEL.iso?t=1788222774&u=341871580&s=26214400",
			want:   "杜比视界测试：双层 FEL.iso",
		},
		{
			name:   "Tier3 CDN path 英文 .mkv",
			a:      "",
			b:      "",
			cdnURL: "https://cdnfhnfile.115cdn.net/abc/def/The.Mystic.Nine.S01E01.mkv?t=123",
			want:   "The.Mystic.Nine.S01E01.mkv",
		},
		{
			name:   "Tier3 CDN path 是纯 hash 无扩展名 → 不兜底（避免误判）",
			a:      "",
			b:      "",
			cdnURL: "https://cdnwhfile.115cdn.net/path/0ea5f978603ed2240bdb18d1c5ad?t=123",
			want:   "",
		},
		{
			name:   "Tier3 CDN path 是路径末尾斜杠（根路径）→ 不兜底",
			a:      "",
			b:      "",
			cdnURL: "https://115.com/",
			want:   "",
		},
		{
			name:   "全空 URL → 空串",
			a:      "",
			b:      "",
			cdnURL: "",
			want:   "",
		},
		{
			name:   "Invalid URL（非 URL 字符串）→ 安全不 panic 返回空",
			a:      "",
			b:      "",
			cdnURL: "://::::not a valid url:::",
			want:   "",
		},
		{
			name:   "Tier1 是 URL percent 编码中文 .iso → URL file_name 有自己的 ResolveFileName 解码正确",
			a:      "%E6%9D%9C%E6%AF%94%E8%A7%86%E7%95%8C.iso",
			b:      "",
			cdnURL: "",
			want:   "杜比视界.iso",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickOneFileName(tc.a, tc.b, tc.cdnURL)
			if got != tc.want {
				t.Errorf("pickOneFileName()=%q, want=%q", got, tc.want)
			}
			// 额外断言：如果 want 不是空，必须包含扩展名（有 "xxx.xxx" 形式）——否则 Tier3 纯 hash 误判漏网
			if tc.want != "" {
				idx := strings.LastIndexByte(tc.want, '.')
				if idx <= 0 || idx >= len(tc.want)-1 {
					t.Errorf("want=%q 不包含合法扩展名，但实际 want 却不是空串", tc.want)
				}
			}
		})
	}
}
