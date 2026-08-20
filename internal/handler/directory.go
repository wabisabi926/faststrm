package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
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
		account := deps.AccountStore.Get(accountName)
		if account == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "account not found"})
			return
		}
		if path == "" {
			path = "/"
		}
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
				"data":    []map[string]any{},
			})
			return
		}
		frontendNodes := make([]map[string]any, 0, len(entries.Data))
		for i, e := range entries.Data {
			fc, _ := strconvAtoi64(anyToString(e.FC))
			isDir := fc > 0 || (e.PickCode == "" && e.Size == 0)
			// 使用序号作为 id（保证唯一性，前端用 id 做 React key 和展开状态追踪）
			// 同时提取 e.CID 作为目录跳转的 category ID
			cidVal, _ := strconvAtoi64(anyToString(e.CID))
			fidVal, _ := strconvAtoi64(anyToString(e.FID))
			uniqueID := int64(i + 1)
			if isDir {
				// 目录优先用 cid，保证稳定性
				if cidVal > 0 {
					uniqueID = cidVal
				} else if fc > 0 {
					uniqueID = fc
				}
			} else {
				// 文件用 fid
				if fidVal > 0 {
					uniqueID = fidVal
				}
			}
			frontendNodes = append(frontendNodes, map[string]any{
				"id":       uniqueID,
				"name":     e.Name,
				"isDir":    isDir,
				"size":     e.Size,
				"pickCode": e.PickCode,
				"cid":      cidVal,
				"fid":      fidVal,
			})
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"code":    200,
			"message": "",
			"data":    frontendNodes,
		})
	}
}

// HandleLocalDirList POST /api/directory/local/list
// Body: { basePath: string } - basePath 为空时返回默认根路径列表
// 也兼容 GET ?root=xxx 查询参数
func HandleLocalDirList(deps DirectoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var root string

		// 优先从 POST JSON body 读取 basePath
		if r.Method == http.MethodPost {
			var body struct {
				BasePath string `json:"basePath"`
				Root     string `json:"root"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				root = body.BasePath
				if root == "" {
					root = body.Root
				}
			}
		}

		// 兼容 GET query 参数
		if root == "" {
			root = r.URL.Query().Get("root")
		}

		if root == "" {
			roots := defaultRoots()
			frontendNodes := make([]map[string]any, 0, len(roots))
			for _, r := range roots {
				frontendNodes = append(frontendNodes, map[string]any{
					"id":    r,
					"name":  r,
					"isDir": true,
				})
			}
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    200,
				"message": "",
				"data":    frontendNodes,
			})
			return
		}

		// 清理路径，防止路径穿越
		cleaned := filepath.Clean(root)
		if !filepath.IsAbs(cleaned) {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": "path must be absolute",
				"data":    []map[string]any{},
			})
			return
		}

		info, err := os.Stat(cleaned)
		if err != nil || !info.IsDir() {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": "path not accessible: " + err.Error(),
				"data":    []map[string]any{},
			})
			return
		}

		entries, err := os.ReadDir(cleaned)
		if err != nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": "read dir failed: " + err.Error(),
				"data":    []map[string]any{},
			})
			return
		}

		type localNode struct {
			name  string
			isDir bool
			size  int64
		}
		var nodes []localNode
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			node := localNode{name: name, isDir: e.IsDir()}
			if !e.IsDir() {
				if info, err := e.Info(); err == nil {
					node.size = info.Size()
				}
			}
			nodes = append(nodes, node)
		}

		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].isDir != nodes[j].isDir {
				return nodes[i].isDir
			}
			return strings.ToLower(nodes[i].name) < strings.ToLower(nodes[j].name)
		})

		frontendNodes := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			fullPath := filepath.Join(cleaned, n.name)
			frontendNodes = append(frontendNodes, map[string]any{
				"id":    fullPath,
				"name":  n.name,
				"isDir": n.isDir,
				"size":  n.size,
			})
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{
			"code":    200,
			"message": "",
			"data":    frontendNodes,
		})
	}
}

// defaultRoots 返回可用根路径列表
// Windows: 枚举盘符
// fNOS/容器: 返回数据目录、配置目录、常见挂载点
// 其他 Linux: 返回 /mnt、/media、/home 等常见目录
func defaultRoots() []string {
	if os.PathSeparator == '\\' {
		roots := []string{}
		for _, drive := range "CDEFGHIJKLMNOPQRS" {
			d := string(drive) + ":\\"
			if _, err := os.Stat(d); err == nil {
				roots = append(roots, d)
			}
		}
		if len(roots) == 0 {
			roots = []string{"C:\\"}
		}
		return roots
	}

	// Linux / fNOS 环境
	var roots []string

	// 1. fNOS 环境变量暴露的目录
	fnosDirs := []string{
		os.Getenv("FNOS_APP_DATA_DIR"),
		os.Getenv("FNOS_APP_CONFIG_DIR"),
		os.Getenv("FNOS_APP_LOG_DIR"),
		os.Getenv("DATA_DIR"),
		os.Getenv("DEFAULT_CONFIG_DIR"),
	}
	for _, d := range fnosDirs {
		if d == "" {
			continue
		}
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			roots = appendUnique(roots, d)
		}
	}

	// 2. 常见挂载点
	commonDirs := []string{
		"/mnt",
		"/media",
		"/home",
		"/opt",
		"/srv",
		"/data",
		"/app",
		"/root",
		"/tmp",
	}
	for _, d := range commonDirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			roots = appendUnique(roots, d)
		}
	}

	// 3. 如果以上都没有，返回 /
	if len(roots) == 0 {
		roots = []string{"/"}
	}

	return roots
}

func appendUnique(roots []string, newPath string) []string {
	for _, r := range roots {
		if r == newPath {
			return roots
		}
	}
	return append(roots, newPath)
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
