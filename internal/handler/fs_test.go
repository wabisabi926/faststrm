package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ================================================================
// HandleFsGet — 兼容旧 STRM 文件，转发到 HandleStrm（方案 A 验证）
// 核心保证：/api/fs/get 和 /api/strm 必须返回 100% 相同的响应
// ================================================================

// TestHandleFsGet_EqualsHandleStrm_ParamValidation 验证两个端点在参数校验路径上响应完全一致
func TestHandleFsGet_EqualsHandleStrm_ParamValidation(t *testing.T) {
	cases := []struct {
		name     string
		account  string
		pickcode string
		fileName string
	}{
		{"missing account", "", "abcdef123456", ""},
		{"missing pickcode", "acc1", "", ""},
		{"invalid pickcode (too short)", "acc1", "short", ""},
		{"invalid pickcode (special chars)", "acc1", "abc!@#xyz123", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := buildBaseStrmOptions(t)
			u := buildStrmURL(c.account, c.pickcode)

			// 请求 /api/strm
			w1 := httptest.NewRecorder()
			r1 := httptest.NewRequest(http.MethodGet, u, nil)
			HandleStrm(opts).ServeHTTP(w1, r1)

			// 请求 /api/fs/get（仅路径不同）
			w2 := httptest.NewRecorder()
			r2 := httptest.NewRequest(http.MethodGet,
				"/api/fs/get"+u[len("/api/strm"):], nil)
			HandleFsGet(opts).ServeHTTP(w2, r2)

			if w1.Code != w2.Code {
				t.Errorf("status mismatch: /api/strm=%d, /api/fs/get=%d", w1.Code, w2.Code)
			}
			if w1.Body.String() != w2.Body.String() {
				t.Errorf("body mismatch:\n  strm: %q\n  fs/get: %q", w1.Body.String(), w2.Body.String())
			}
		})
	}
}

// TestHandleFsGet_SameStatusCodeAsStrm 各 status code 路径下都一致
func TestHandleFsGet_SameStatusCodeAsStrm(t *testing.T) {
	opts := buildBaseStrmOptions(t)

	cases := []struct {
		name    string
		urlPath string
		want    int
	}{
		{"missing account", "/api/strm?pickcode=abcdef123456", http.StatusBadRequest},
		{"account not found", "/api/strm?account=ghost&pickcode=abcdef123456", http.StatusNotFound},
		{"head method", "/api/strm?account=acc1&pickcode=abcdef123456", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// /api/strm
			w1 := httptest.NewRecorder()
			r1 := httptest.NewRequest(http.MethodGet, c.urlPath, nil)
			HandleStrm(opts).ServeHTTP(w1, r1)

			// /api/fs/get — 替换路径前缀
			fsPath := "/api/fs/get" + c.urlPath[len("/api/strm"):]
			w2 := httptest.NewRecorder()
			r2 := httptest.NewRequest(http.MethodGet, fsPath, nil)
			HandleFsGet(opts).ServeHTTP(w2, r2)

			if w1.Code != w2.Code {
				t.Errorf("status mismatch: strm=%d, fs/get=%d", w1.Code, w2.Code)
			}
		})
	}
}
