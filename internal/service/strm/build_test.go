package strm

import (
	"errors"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// TestBuildContent_Standard 标准格式：必要参数齐全、编码开启（默认）
func TestBuildContent_Standard(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Prefix:   "http://nas:8090/",
		Account:  "副卡",
		PickCode: "abcdefghij1234567",
		FileName: "美国狙击手 2024.mkv",
	})
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	want := "http://nas:8090/api/strm?account=" + strmutil.URLPathEncode("副卡") +
		"&pickcode=abcdefghij1234567&file_name=" + strmutil.URLPathEncode("美国狙击手 2024.mkv") + "\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// 编码规则：空格必须是 %20（router 不还原 '+'），不能出现 '+'
	if strings.Contains(got, "+") {
		t.Fatalf("不应出现 '+'：%q", got)
	}
	if !strings.Contains(got, "%20") {
		t.Fatalf("空格应编码为 %%20：%q", got)
	}
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Fatalf("末尾应只有一个换行：%q", got)
	}
}

// TestBuildContent_PathSlashPreserved 编码开启时 '/' 保留（file_name 里的目录段不拆参数）
func TestBuildContent_PathSlashPreserved(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Prefix:   "http://nas:8090",
		Account:  "acc",
		PickCode: "abcdefghij1234567",
		FileName: "合集/第01集.mkv",
	})
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	if !strings.Contains(got, "file_name=%E5%90%88%E9%9B%86/%E7%AC%AC01%E9%9B%86.mkv") {
		t.Fatalf("'/' 应保留、中文应编码：%q", got)
	}
}

// TestBuildContent_DisableEncode 关闭编码：参数原样写入
func TestBuildContent_DisableEncode(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Prefix:        "http://nas:8090",
		Account:       "副卡",
		PickCode:      "abcdefghij1234567",
		FileName:      "电影 2024.mkv",
		DisableEncode: true,
	})
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	if !strings.Contains(got, "account=副卡") || !strings.Contains(got, "file_name=电影 2024.mkv") {
		t.Fatalf("关闭编码应原样输出：%q", got)
	}
}

// TestBuildContent_EmptyFileName 文件名为空时不携带 file_name 参数
func TestBuildContent_EmptyFileName(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Prefix:   "http://nas:8090",
		Account:  "acc",
		PickCode: "abcdefghij1234567",
	})
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	if strings.Contains(got, "file_name=") {
		t.Fatalf("空文件名不应追加 file_name：%q", got)
	}
	want := "http://nas:8090/api/strm?account=acc&pickcode=abcdefghij1234567\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestBuildContent_TemplateFirst 模板优先，且模板不引用 {prefix} 时前缀不可用也能渲染
func TestBuildContent_TemplateFirst(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Template:  "http://gw:12333/115/url/video{ext}?pickcode={pickcode}&path={file_path_strict}",
		Prefix:    "", // 模板不依赖前缀，不应因此失败
		Account:   "acc",
		PickCode:  "a7nds6qtempqehtnn",
		FileName:  "东邪西毒.mkv",
		CloudPath: "电影/华语/东邪西毒.mkv",
	})
	if err != nil {
		t.Fatalf("模板分支不应因前缀为空失败: %v", err)
	}
	if !strings.HasPrefix(got, "http://gw:12333/115/url/video.mkv?pickcode=a7nds6qtempqehtnn") {
		t.Fatalf("模板渲染结果异常：%q", got)
	}
	// path 参数内 '/' 必须是 %2F
	if strings.Contains(got, "path=电影") || strings.Contains(got[strings.Index(got, "path="):], "/") {
		t.Fatalf("path 参数内不应出现裸 '/'：%q", got)
	}
}

// TestBuildContent_TemplateIsoStem .iso 的主名保留 ".iso"（原盘双扩展名）
func TestBuildContent_TemplateIsoStem(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Template: "{prefix}/play/{stem}.strm?pc={pickcode}",
		Prefix:   "http://nas:8090",
		Account:  "acc",
		PickCode: "abcdefghij1234567",
		FileName: "007.iso",
	})
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	if !strings.Contains(got, "/play/007.iso.strm") {
		t.Fatalf(".iso 的 stem 应含 .iso：%q", got)
	}
}

// TestBuildContent_InvalidPickcode 非 17 位 pickcode 必须拒绝（播放侧同样 400）
func TestBuildContent_InvalidPickcode(t *testing.T) {
	for _, pc := range []string{"", "abc", "abcdefghij12345678", "abcdefghij123456-"} {
		_, err := BuildContent(ContentInput{
			Prefix:   "http://nas:8090",
			Account:  "acc",
			PickCode: pc,
			FileName: "x.mkv",
		})
		if !errors.Is(err, ErrInvalidPickcode) {
			t.Fatalf("pickcode %q 应返回 ErrInvalidPickcode，got %v", pc, err)
		}
	}
}

// TestBuildContent_UnusablePrefix 空 / 回环 / 0.0.0.0 前缀必须拒绝，不再写 127.0.0.1 死链
func TestBuildContent_UnusablePrefix(t *testing.T) {
	for _, p := range []string{"", "  ", "http://127.0.0.1:8090", "http://0.0.0.0:8090", "http://localhost:8090", "not-a-url"} {
		_, err := BuildContent(ContentInput{
			Prefix:   p,
			Account:  "acc",
			PickCode: "abcdefghij1234567",
			FileName: "x.mkv",
		})
		if !errors.Is(err, ErrUnusablePrefix) {
			t.Fatalf("prefix %q 应返回 ErrUnusablePrefix，got %v", p, err)
		}
	}
}

// TestBuildContent_TrailingSlashNormalized 前缀末尾多余的 '/' 不应产生双斜杠
func TestBuildContent_TrailingSlashNormalized(t *testing.T) {
	got, err := BuildContent(ContentInput{
		Prefix:   "http://nas:8090///",
		Account:  "acc",
		PickCode: "abcdefghij1234567",
	})
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	if strings.Contains(got, "8090///") || strings.Contains(got, "//api") {
		t.Fatalf("前缀末尾 '/' 应被规范化：%q", got)
	}
}
