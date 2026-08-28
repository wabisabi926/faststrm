package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

// dirJSONBody 安全地构造 JSON body 字符串（处理 Windows 路径中的反斜杠）
func dirJSONBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// === HandleLocalDirList ===

func TestHandleLocalDirList_DefaultRoots(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")
	t.Setenv("FNOS_APP_DATA_DIR", "")
	t.Setenv("FNOS_APP_CONFIG_DIR", "")

	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200", resp["code"])
	}
	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatalf("data should be array, got %T", resp["data"])
	}
	if len(data) == 0 {
		t.Fatal("default roots should not be empty")
	}
}

func TestHandleLocalDirList_GETQueryRoot(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	tmp := t.TempDir()
	req := httptest.NewRequest("GET", "/?root="+tmp, nil)
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200", resp["code"])
	}
}

func TestHandleLocalDirList_PostJSON(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	tmp := t.TempDir()
	body := dirJSONBody(t, map[string]string{"basePath": tmp})
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200", resp["code"])
	}
}

func TestHandleLocalDirList_RelativePath(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	req := httptest.NewRequest("GET", "/?root=relative/path", nil)
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(500) {
		t.Fatalf("code: got %v, want 500 (path must be absolute)", resp["code"])
	}
	if !strings.Contains(resp["message"].(string), "absolute") {
		t.Fatalf("message should mention 'absolute', got: %v", resp["message"])
	}
}

func TestHandleLocalDirList_NonExistentPath(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	nonExist := filepath.Join(t.TempDir(), "no_such_dir")
	req := httptest.NewRequest("GET", "/?root="+nonExist, nil)
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(500) {
		t.Fatalf("code: got %v, want 500", resp["code"])
	}
}

func TestHandleLocalDirList_ValidDirWithEntries(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "subdir"), 0755)                      //nolint:gosec // G306 — 测试夹具
	_ = os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hi"), 0o644)    //nolint:gosec // G306 — 测试夹具
	_ = os.WriteFile(filepath.Join(tmp, ".hidden"), []byte("secret"), 0o644) //nolint:gosec // G306 — 测试夹具

	req := httptest.NewRequest("GET", "/?root="+tmp, nil)
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200", resp["code"])
	}
	data := resp["data"].([]any)
	// 应包含 subdir 和 file.txt，不含 .hidden
	if len(data) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(data), data)
	}

	// 验证排序：目录在前
	first := data[0].(map[string]any)
	if first["name"] != "subdir" {
		t.Fatalf("expected 'subdir' first (dirs first), got %v", first["name"])
	}
	if !first["isDir"].(bool) {
		t.Fatal("subdir should be isDir=true")
	}
}

func TestHandleLocalDirList_FnOS_DeniedPath(t *testing.T) {
	// 模拟 fNOS 环境并启用严格路径
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "/allowed")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")
	t.Setenv("FASTSTRM_FNOS_STRICT_PATH", "1")

	tmp := t.TempDir()
	req := httptest.NewRequest("GET", "/?root="+tmp, nil)
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(403) {
		t.Fatalf("code: got %v, want 403 (fnOS strict path denied)", resp["code"])
	}
}

func TestHandleLocalDirList_FnOS_AllowedPath(t *testing.T) {
	// fNOS 环境但路径在白名单中
	// getAllowedPaths() 使用 ":" 作为分隔符，Windows 路径 "C:\..." 中的冒号会破坏解析
	// fNOS 是 Linux 专属功能，此测试仅在 Linux 上运行
	if runtime.GOOS != "linux" {
		t.Skip("fNOS path whitelist uses ':' separator, not compatible with Windows drive letters")
	}

	tmp := t.TempDir()
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", tmp)
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")
	t.Setenv("FASTSTRM_FNOS_STRICT_PATH", "1")

	req := httptest.NewRequest("GET", "/?root="+tmp, nil)
	w := httptest.NewRecorder()

	HandleLocalDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200 (path allowed)", resp["code"])
	}
}

// === HandleLocalDirListChildren ===

func TestHandleLocalDirListChildren_MalformedJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirListChildren(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(400) {
		t.Fatalf("code: got %v, want 400", resp["code"])
	}
}

func TestHandleLocalDirListChildren_EmptyTargetPath(t *testing.T) {
	body := `{"basePath":"/tmp","targetPath":""}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirListChildren(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(400) {
		t.Fatalf("code: got %v, want 400", resp["code"])
	}
}

func TestHandleLocalDirListChildren_NonExistentPath(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	nonExist := filepath.Join(t.TempDir(), "no_such_dir")
	body := dirJSONBody(t, map[string]string{"targetPath": nonExist})
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirListChildren(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(404) {
		t.Fatalf("code: got %v, want 404", resp["code"])
	}
}

func TestHandleLocalDirListChildren_ValidDirChildren(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "child_dir1"), 0755)                 //nolint:gosec // G306 — 测试夹具
	_ = os.MkdirAll(filepath.Join(tmp, "child_dir2"), 0755)                 //nolint:gosec // G306 — 测试夹具
	_ = os.WriteFile(filepath.Join(tmp, "not_dir.txt"), []byte("x"), 0o644) //nolint:gosec // G306 — 测试夹具

	body := dirJSONBody(t, map[string]string{"targetPath": tmp})
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirListChildren(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200", resp["code"])
	}
	data, ok := resp["data"].([]any)
	if !ok || data == nil {
		t.Fatalf("data should be non-nil array, got %T", resp["data"])
	}
	// 应只有 2 个目录，文件被过滤
	if len(data) != 2 {
		t.Fatalf("expected 2 child dirs, got %d: %v", len(data), data)
	}
	for _, d := range data {
		m := d.(map[string]any)
		if !m["isDir"].(bool) {
			t.Fatal("all children should be isDir=true")
		}
	}
}

func TestHandleLocalDirListChildren_FileNotDir(t *testing.T) {
	t.Setenv("TRIM_DATA_ACCESSIBLE_PATHS", "")
	t.Setenv("TRIM_DATA_SHARE_PATHS", "")

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "afile.txt")
	_ = os.WriteFile(filePath, []byte("x"), 0o644) //nolint:gosec // G306 — 测试夹具

	body := dirJSONBody(t, map[string]string{"targetPath": filePath})
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleLocalDirListChildren(DirectoryDeps{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(200) {
		t.Fatalf("code: got %v, want 200 (file exists, just not a dir)", resp["code"])
	}
	// 文件不是目录，children 应为 nil
	data := resp["data"]
	if data != nil {
		if arr, ok := data.([]any); ok && len(arr) > 0 {
			t.Fatalf("file should have no children, got %d", len(arr))
		}
	}
}

// === HandleRemoteDirList ===

func newTestDirAccountStore(t *testing.T) *store.AccountStore {
	t.Helper()
	s, err := store.NewAccountStore("test_salt_32b_padding______", t.TempDir())
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHandleRemoteDirList_MissingAccount(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	HandleRemoteDirList(DirectoryDeps{})(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoteDirList_AccountNotFound(t *testing.T) {
	as := newTestDirAccountStore(t)

	req := httptest.NewRequest("GET", "/?account=ghost", nil)
	w := httptest.NewRecorder()

	HandleRemoteDirList(DirectoryDeps{AccountStore: as})(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleRemoteDirList_EmptyCookie(t *testing.T) {
	as := newTestDirAccountStore(t)
	_ = as.Upsert(&model.AccountInfo{
		Name:   "testuser",
		Cookie: "",
	})

	req := httptest.NewRequest("GET", "/?account=testuser", nil)
	w := httptest.NewRecorder()

	HandleRemoteDirList(DirectoryDeps{AccountStore: as})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != float64(401) {
		t.Fatalf("code: got %v, want 401 (cookie not set)", resp["code"])
	}
}

func TestHandleRemoteDirList_RootPathVariations(t *testing.T) {
	as := newTestDirAccountStore(t)
	_ = as.Upsert(&model.AccountInfo{
		Name:   "testuser",
		Cookie: "",
	})

	// 这些 path 值都应该映射到 cid=0，不触发 FsDirGetID
	// 但 Cookie 为空，会先返回 401
	paths := []string{"/", "0", ".", ""}
	for _, p := range paths {
		t.Run("path="+p, func(t *testing.T) {
			url := "/?account=testuser"
			if p != "" {
				url += "&path=" + p
			}
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			HandleRemoteDirList(DirectoryDeps{AccountStore: as})(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status: got %d", w.Code)
			}
			var resp map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["code"] != float64(401) {
				t.Fatalf("code: got %v, want 401 (empty cookie)", resp["code"])
			}
		})
	}
}
