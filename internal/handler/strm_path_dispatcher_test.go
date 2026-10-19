package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type flagHandler struct{ called bool }

func (h *flagHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

// TestStrmPathDispatcher 分发规则：/api/strm/ 前缀的 GET/HEAD 交给 STRM，
// 其余（前端路由、其他 API、非 GET）回落 SPA。
func TestStrmPathDispatcher(t *testing.T) {
	spa := &flagHandler{}
	strmH := &flagHandler{}
	d := NewStrmPathDispatcher(spa)
	d.SetStrmHandler(strmH)

	cases := []struct {
		name     string
		method   string
		path     string
		wantStrm bool
		wantSPA  bool
	}{
		{"路径式 GET", http.MethodGet, "/api/strm/电影/x.iso", true, false},
		{"路径式 HEAD", http.MethodHead, "/api/strm/电影/x.mkv", true, false},
		{"前端路由", http.MethodGet, "/settings", false, true},
		{"标准 STRM（由正常路由处理，不进 404）", http.MethodGet, "/api/strm", false, true},
		{"非 GET 的 strm 子路径", http.MethodPost, "/api/strm/cache/clear", false, true},
		{"根路径", http.MethodGet, "/", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			strmH.called, spa.called = false, false
			req := httptest.NewRequest(c.method, c.path, nil)
			rec := httptest.NewRecorder()
			d.ServeHTTP(rec, req)
			if strmH.called != c.wantStrm {
				t.Fatalf("strm handler called=%v, want %v", strmH.called, c.wantStrm)
			}
			if spa.called != c.wantSPA {
				t.Fatalf("spa handler called=%v, want %v", spa.called, c.wantSPA)
			}
		})
	}
}

// TestStrmPathDispatcher_NoStrmHandler 未注入 STRM handler 时全部回落 SPA（防御性）
func TestStrmPathDispatcher_NoStrmHandler(t *testing.T) {
	spa := &flagHandler{}
	d := NewStrmPathDispatcher(spa)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/strm/x.iso", nil))
	if !spa.called {
		t.Fatal("未注入 STRM handler 时应回落 SPA")
	}
}

// TestFileNameFromStrmPath 路径末段提取：中文、特殊字符、穿越防护
func TestFileNameFromStrmPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"普通", "/api/strm/电影/沙丘.mkv", "沙丘.mkv"},
		{"中文与冒号", "/api/strm/电影/SGNB集/007：大破量子危机-SGNB第122部.iso", "007：大破量子危机-SGNB第122部.iso"},
		{"多级", "/api/strm/a/b/c/x.iso", "x.iso"},
		{"非前缀", "/settings", ""},
		{"空前缀", "/api/strm/", ""},
		{"穿越归一化", "/api/strm/a/../../../etc/passwd", "passwd"},
		{"仅斜杠", "/api/strm////", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fileNameFromStrmPath(c.path); got != c.want {
				t.Fatalf("fileNameFromStrmPath(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}
