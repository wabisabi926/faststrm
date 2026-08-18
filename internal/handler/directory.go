package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// DirectoryDeps 目录 API 依赖
type DirectoryDeps struct {
	Client115    *client115.Client
	AccountStore *store.AccountStore
}

// DirTreeNode 统一目录节点（remote 与 local 共用）
type DirTreeNode struct {
	Key        int           `json:"key"`
	Name       string        `json:"name"`
	Depth      int           `json:"depth"`
	ParentKey  int           `json:"parent_key"`
	IsDir      bool          `json:"is_dir,omitempty"`
	Size       int64         `json:"size,omitempty"`
	PickCode   string        `json:"pick_code,omitempty"`
	Children   []DirTreeNode `json:"children,omitempty"`
}

// HandleRemoteDirList GET /api/directory/remote/list
// query: account, path (optional), refresh
func HandleRemoteDirList(deps DirectoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		accountName := q.Get("account")
		path := q.Get("path")
		if accountName == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account required"})
			return
		}
		account := strm.FindAccount115(deps.AccountStore, accountName)
		if account == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "account not found"})
			return
		}
		if path == "" {
			path = "/"
		}
		// 先用 dir_getid 拿到 cid，再 fs_files 递归拿树
		cid, err := deps.Client115.FsDirGetID(r.Context(), path, account.Cookie)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadGateway, map[string]string{
				"error": "fs_dir_getid failed: " + err.Error(),
			})
			return
		}
		entries, err := deps.Client115.FsFiles(r.Context(), itoa64(cid), 1000, 0, account.Cookie)
		if err != nil {
			httpx.WriteJson(w, http.StatusBadGateway, map[string]string{
				"error": "fs_files failed: " + err.Error(),
			})
			return
		}
		if !entries.State {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": "fs_files state=false",
				"data":    map[string]any{"content": []DirTreeNode{}},
			})
			return
		}
		content := make([]DirTreeNode, 0, len(entries.Data))
		for _, e := range entries.Data {
			fc, _ := strconvAtoi64(anyToString(e.FC))
			isDir := fc > 0 || (e.PickCode == "" && e.Size == 0)
			content = append(content, DirTreeNode{
				Name:     e.Name,
				IsDir:    isDir,
				Size:     e.Size,
				PickCode: e.PickCode,
			})
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"code":    200,
			"message": "",
			"data":    map[string]any{"content": content},
		})
	}
}

// HandleLocalDirList GET /api/directory/local/list
// query: root (本地根目录绝对路径)，支持选择目标路径
func HandleLocalDirList(deps DirectoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		if root == "" {
			// 默认根：Windows 列盘符；类 Unix /
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    200,
				"message": "",
				"data":    map[string]any{"roots": defaultRoots()},
			})
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": err.Error(),
				"data":    map[string]any{"content": []DirTreeNode{}},
			})
			return
		}
		content := make([]DirTreeNode, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			// 跳过隐藏文件
			if strings.HasPrefix(name, ".") {
				continue
			}
			node := DirTreeNode{Name: name, IsDir: e.IsDir()}
			if !e.IsDir() {
				if info, err := e.Info(); err == nil {
					node.Size = info.Size()
				}
			}
			content = append(content, node)
		}
		sort.Slice(content, func(i, j int) bool {
			if content[i].IsDir != content[j].IsDir {
				return content[i].IsDir
			}
			return strings.ToLower(content[i].Name) < strings.ToLower(content[j].Name)
		})
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"code":    200,
			"message": "",
			"data":    map[string]any{"content": content},
		})
	}
}

// defaultRoots 返回可用根路径列表（Windows 列盘符，否则 /）
func defaultRoots() []string {
	// Windows：C:/ D:/ … 实际调用 filepath.VolumeName 成本较高；直接给常见值
	roots := []string{filepath.FromSlash("/")}
	if os.PathSeparator == '\\' {
		roots = nil
		for _, drive := range "CDEFGHIJKLMNOPQRS" {
			d := string(drive) + ":\\"
			if _, err := os.Stat(d); err == nil {
				roots = append(roots, d)
			}
		}
		if len(roots) == 0 {
			roots = []string{"C:\\"}
		}
	}
	return roots
}

func itoa64(v int64) string  { return strconvInt64(v) }
func anyToString(v any) string {
	switch x := v.(type) {
	case int64:
		return strconvInt64(x)
	case int:
		return strconvInt64(int64(x))
	case float64:
		return strconvInt64(int64(x))
	case string:
		return x
	}
	return "0"
}
func strconvAtoi64(s string) (int64, error) { return parseInt64(s) }
func parseInt64(s string) (int64, error) {
	n := int64(0)
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
func strconvInt64(n int64) string {
	// 极简 Int64 -> string（避免额外 import 冲突）
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
