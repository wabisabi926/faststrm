package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
// query: account, path (optional), cid (optional), refresh
// 优先使用 cid 直接导航（对齐 qmediasync 方式），path 作为后备方案
func HandleRemoteDirList(deps DirectoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		accountName := q.Get("account")
		path := q.Get("path")
		cidParam := q.Get("cid")
		if accountName == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account required"})
			return
		}
		account := deps.AccountStore.Get(accountName)
		if account == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "account not found"})
			return
		}

		if account.Cookie == "" {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    401,
				"message": "Cookie未设置，请先登录115账号",
				"data":    []map[string]any{},
			})
			return
		}

		// 确定 CID：优先使用 cid 参数，其次用 path 转换
		var cid int64
		if cidParam != "" {
			// 直接用 CID（对齐 qmediasync 的 parentId 方式）
			cidVal, err := strconvAtoi64(cidParam)
			if err != nil {
				// 尝试作为字符串 "0" 处理
				cid = 0
			} else {
				cid = cidVal
			}
		} else if path == "" || path == "/" || path == "0" || path == "." {
			// 根目录直接用 cid=0
			cid = 0
		} else {
			// 清理路径，确保格式正确
			// 115 API 的 getid 需要路径以 / 开头
			cleanedPath := strings.TrimPrefix(path, "/")
			cleanedPath = strings.TrimSuffix(cleanedPath, "/")
			if cleanedPath == "" {
				cid = 0
			} else {
				if !strings.HasPrefix(cleanedPath, "/") {
					cleanedPath = "/" + cleanedPath
				}
				var err error
				cid, err = deps.Client115.FsDirGetID(r.Context(), cleanedPath, account.Cookie)
				if err != nil {
					httpx.WriteJson(w, http.StatusOK, map[string]any{
						"code":    500,
						"message": "获取目录ID失败: " + err.Error(),
						"data":    []map[string]any{},
					})
					return
				}
			}
		}

		entries, err := deps.Client115.FsFiles(r.Context(), itoa64(cid), 1000, 0, account.Cookie)
		if err != nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": "获取文件列表失败: " + err.Error(),
				"data":    []map[string]any{},
			})
			return
		}
		if !entries.State {
			errMsg := "115 API返回错误，请检查Cookie是否有效"
			if entries.ErrMsg != "" {
				errMsg = entries.ErrMsg
			}
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": errMsg,
				"data":    []map[string]any{},
			})
			return
		}

		// 如果是根目录，且返回为空，可能Cookie无效
		if cid == 0 && len(entries.Data) == 0 {
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    200,
				"message": "",
				"data":    []map[string]any{},
			})
			return
		}

		frontendNodes := make([]map[string]any, 0, len(entries.Data))
		for i, e := range entries.Data {
			fc, _ := strconvAtoi64(anyToString(e.FC))
			// 修正 isDir 判断：fc > 0 表示有子项数，是目录；否则看 pickCode 和 size
			isDir := fc > 0
			if !isDir {
				pc := anyToString(e.PickCode)
				sz, _ := strconvAtoi64(anyToString(e.Size))
				isDir = pc == "" && sz == 0
			}
			cidVal, _ := strconvAtoi64(anyToString(e.CID))
			fidVal, _ := strconvAtoi64(anyToString(e.FID))
			uniqueID := int64(i + 1)
			if isDir {
				if cidVal > 0 {
					uniqueID = cidVal
				} else if fc > 0 {
					uniqueID = fc
				}
			} else {
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
// 参考 qmediasync 实现：
//   Windows: 使用 gopsutil/disk 枚举盘符
//   fNOS环境: 使用 TRIM_DATA_ACCESSIBLE_PATHS + TRIM_DATA_SHARE_PATHS 白名单
//   其他Linux: 返回 / 根目录及常见挂载点
func HandleLocalDirList(deps DirectoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var root string

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

		if root == "" {
			root = r.URL.Query().Get("root")
		}

		isFnOS := detectFnOS()

		if root == "" {
			// 根路径 - 返回可用的根目录列表
			roots := defaultRoots(isFnOS)
			frontendNodes := make([]map[string]any, 0, len(roots))
			for _, rt := range roots {
				frontendNodes = append(frontendNodes, map[string]any{
					"id":    rt,
					"name":  rt,
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

		// fNOS 环境下校验路径白名单
		if isFnOS {
			allowedPaths := getAllowedPaths()
			if !isPathAllowed(root, allowedPaths) {
				httpx.WriteJson(w, http.StatusOK, map[string]any{
					"code":    403,
					"message": "无权限访问该目录，路径必须在允许的范围内",
					"data":    []map[string]any{},
				})
				return
			}
		}

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
			errMsg := "path not accessible"
			if err != nil {
				errMsg += ": " + err.Error()
			}
			httpx.WriteJson(w, http.StatusOK, map[string]any{
				"code":    500,
				"message": errMsg,
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
				if finfo, err := e.Info(); err == nil {
					node.size = finfo.Size()
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

// detectFnOS 检测是否为飞牛 fNOS 环境
// 参考 qmediasync: 通过 TRIM_DATA_ACCESSIBLE_PATHS 环境变量判断
func detectFnOS() bool {
	return os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS") != "" ||
		os.Getenv("TRIM_DATA_SHARE_PATHS") != "" ||
		os.Getenv("FNOS_APP_DATA_DIR") != "" ||
		os.Getenv("FNOS_APP_CONFIG_DIR") != ""
}

// getAllowedPaths 返回 fNOS 环境下允许访问的路径列表
func getAllowedPaths() []string {
	var paths []string
	accessiblePaths := os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS")
	sharePaths := os.Getenv("TRIM_DATA_SHARE_PATHS")
	for _, p := range strings.Split(accessiblePaths+":"+sharePaths, ":") {
		p = strings.TrimSpace(p)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// isPathAllowed 检查路径是否在允许的白名单目录内
// 说明：fNOS 环境下默认采用"宽松模式"——只打 warning 日志不拦截，避免 manifest 未声明共享路径时
// 用户完全无法浏览媒体目录。若需严格限制，请在环境变量中设置 FASTSTRM_FNOS_STRICT_PATH=1。
// 非 fNOS 环境直接放行。
func isPathAllowed(targetPath string, allowedPaths []string) bool {
	if !detectFnOS() {
		return true
	}
	if os.Getenv("FASTSTRM_FNOS_STRICT_PATH") != "1" {
		return true
	}
	cleanPath := filepath.Clean(targetPath)
	for _, ap := range allowedPaths {
		ap = strings.TrimSpace(filepath.Clean(ap))
		if ap == "" {
			continue
		}
		if cleanPath == ap || strings.HasPrefix(cleanPath, ap+string(os.PathSeparator)) {
			return true
		}
	}
	fmt.Printf("[WARN] fNOS strict path mode: %q not in allowed paths %v\n", cleanPath, allowedPaths)
	return false
}

// defaultRoots 返回可用根路径列表
// 优先级（从高到低）：
//   1) FASTSTRM_LOCAL_DIR_ROOTS 环境变量（英文逗号分隔，用户/管理员可完全覆盖）
//   2) Windows: 枚举逻辑盘符
//      Linux  : 读 /proc/mounts 获取真实挂载点（过滤 /proc /sys 等虚拟 FS）
//   3) fNOS 附加：TRIM_DATA_* 白名单 + 常见卷根 (/vol1 /volume1 /mnt/user ...)
//   4) 兜底：硬编码常见挂载点 + "/"
func defaultRoots(isFnOS bool) []string {
	// ---- 1) 最高优先级：用户显式覆盖 ----
	if custom := os.Getenv("FASTSTRM_LOCAL_DIR_ROOTS"); custom != "" {
		var out []string
		for _, p := range strings.Split(custom, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				out = appendUnique(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// ---- 2a) Windows：盘符 ----
	if os.PathSeparator == '\\' {
		roots := getWindowsDrives()
		if len(roots) == 0 {
			roots = []string{"C:\\"}
		}
		return roots
	}

	// ---- 2b) Linux：真实挂载点 ----
	var roots []string
	for _, mp := range readRealMountpoints() {
		if info, err := os.Stat(mp); err == nil && info.IsDir() {
			roots = appendUnique(roots, mp)
		}
	}

	// ---- 3) fNOS：并集注入白名单 + 常见 NAS 卷根 ----
	if isFnOS {
		for _, p := range getAllowedPaths() {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				roots = appendUnique(roots, p)
			}
		}
		// 飞牛（飞牛OS/海康/极空间/群晖）常见卷根
		nasRoots := []string{
			"/vol1", "/vol2", "/vol3", "/vol4",
			"/volume1", "/volume2", "/volume3", "/volume4",
			"/mnt/user", "/mnt/ssd", "/mnt/cache", "/mnt/disk",
			"/share", "/public", "/home",
		}
		for _, p := range nasRoots {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				roots = appendUnique(roots, p)
			}
		}
		// 兜底确保至少有 "/"
		roots = appendUnique(roots, "/")
		return roots
	}

	// ---- 4) 普通 Linux 兜底：硬编码常见目录（和 /proc/mounts 并集）----
	commonDirs := []string{
		"/", "/mnt", "/media", "/home", "/opt", "/srv",
		"/data", "/app", "/root", "/tmp", "/storage",
	}
	for _, d := range commonDirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			roots = appendUnique(roots, d)
		}
	}
	if len(roots) == 0 {
		roots = []string{"/"}
	}
	return roots
}

// readRealMountpoints 读取 Linux /proc/mounts，返回真实的块设备挂载点
// 过滤掉 tmpfs / proc / sys / devpts / cgroup / overlay 等虚拟/容器文件系统
// 非 Linux 环境直接返回 nil
func readRealMountpoints() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	virtualPrefixes := []string{
		"/proc/", "/sys/", "/dev/", "/run/", "/tmp/",
		"/var/lib/", "/var/run/", "/sys/fs/",
	}
	virtualFSTypes := map[string]struct{}{
		"proc": {}, "sysfs": {}, "devtmpfs": {}, "devpts": {},
		"tmpfs": {}, "cgroup": {}, "cgroup2": {}, "pstore": {},
		"securityfs": {}, "debugfs": {}, "tracefs": {},
		"bpf": {}, "mqueue": {}, "hugetlbfs": {}, "fusectl": {},
		"configfs": {}, "binfmt_misc": {}, "autofs": {},
		"overlay": {}, "squashfs": {}, "ramfs": {},
		"nfsd": {}, "selinuxfs": {},
	}

	var mounts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		// fields: device mountpoint fstype options freq passno
		mp := fields[1]
		fsType := fields[2]
		// 跳过虚拟 FS 类型
		if _, ok := virtualFSTypes[fsType]; ok {
			continue
		}
		// 跳过 /proc /sys /dev 等前缀
		skip := false
		for _, vp := range virtualPrefixes {
			if mp == vp[:len(vp)-1] || strings.HasPrefix(mp, vp) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// 跳过容器 / 启动相关的特定挂载点
		if mp == "/" || mp == "/etc/resolv.conf" || mp == "/etc/hostname" || mp == "/etc/hosts" {
			if mp == "/" {
				// 根单独追加
			} else {
				continue
			}
		}
		mounts = appendUnique(mounts, mp)
	}
	return mounts
}

// diskPartition 模拟 gopsutil/disk.Partition 结构
type diskPartition struct {
	Mountpoint string
}

// diskPartitions 获取磁盘分区列表
// 在没有 gopsutil 依赖时返回空列表，回退到盘符扫描
func diskPartitions(all bool) ([]diskPartition, error) {
	// 简单实现：直接扫描 Windows 盘符
	// 如果未来需要更详细的分区信息，可以引入 gopsutil
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	var partitions []diskPartition
	for _, drive := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		d := string(drive) + ":\\"
		if _, err := os.Stat(d); err == nil {
			partitions = append(partitions, diskPartition{Mountpoint: d})
		}
	}
	return partitions, nil
}

// getWindowsDrives 获取 Windows 盘符列表
// 参考 qmediasync: 使用 gopsutil/disk 枚举盘符
func getWindowsDrives() []string {
	partitions, err := diskPartitions(false)
	if err == nil && len(partitions) > 0 {
		roots := make([]string, 0, len(partitions))
		for _, p := range partitions {
			if _, err := os.Stat(p.Mountpoint); err == nil {
				roots = appendUnique(roots, p.Mountpoint)
			}
		}
		if len(roots) > 0 {
			return roots
		}
	}

	// 回退：扫描所有盘符
	roots := []string{}
	for _, drive := range "CDEFGHIJKLMNOPQRSTUVWXYZ" {
		d := string(drive) + ":\\"
		if _, err := os.Stat(d); err == nil {
			roots = append(roots, d)
		}
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
