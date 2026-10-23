package task

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// ==================== cur=0 全量清单扫描（M5） ====================
//
// 用一次 cur=0 的递归列目录拉全量文件清单，代替「每个目录一次 FsFiles」的 DFS。
// 目录结构不返回，靠条目的 pid 还原（folders 表 → 祖先链接口 → 失败则整体回退 DFS）。
//
// 安全底线：
//   - 清单完整性校验失败（实得 < count）→ 返回错误，由调用方回退 DFS，绝不用残缺清单做清理依据
//   - 路径翻译失败 → 同样整体回退 DFS（不做"部分条目跳过"：漏掉的 pickcode 会被当孤儿删掉）
//   - 失败目录的 STRM 清理保护由 DFS 的 ScanStats.FailedDirs 机制负责；本实现失败即不产出结果

const (
	fullListPageSize       = 1000
	fullListEmptyRetryWait = 250 * time.Millisecond
	// fullListMaxPages 防御性上限，避免服务端忽略 offset 时死循环（1000 万文件）
	fullListMaxPages = 10000
)

// fullListFetcher 单页拉取函数（测试可注入）
type fullListFetcher func(ctx context.Context, offset, limit int) (*client115.FsFilesAllResp, error)

// fetchFullListPages 分页拉全量清单。
//
// 终止条件只用「空页」（250ms 重试一次），**不用 count 也不用短页**：
// web API 的 count 语义/时效不保证，短页也可能只是服务端单页封顶。
// count 只在结束时做完整性校验：实得 < count → 判定清单不完整并返回错误。
func fetchFullListPages(
	ctx context.Context,
	fetch fullListFetcher,
	limit int,
	onPage func([]client115.FullListEntry) error,
) (seen int64, expected int64, err error) {
	if limit <= 0 {
		limit = fullListPageSize
	}
	offset := 0
	lastFirstFID := int64(-1)
	lastLen := -1
	for page := 0; ; page++ {
		if page >= fullListMaxPages {
			return seen, expected, fmt.Errorf("全量清单分页超过上限 %d 页，已中止", fullListMaxPages)
		}
		if cerr := ctx.Err(); cerr != nil {
			return seen, expected, cerr
		}

		resp, ferr := fetch(ctx, offset, limit)
		if ferr != nil {
			return seen, expected, ferr
		}
		if resp == nil {
			return seen, expected, fmt.Errorf("全量清单返回空响应 (offset=%d)", offset)
		}
		if int64(resp.Count) > expected {
			expected = int64(resp.Count)
		}

		if len(resp.Data) == 0 {
			// 空页：等一个恢复窗口再拉一次，仍空才认为结束
			select {
			case <-ctx.Done():
				return seen, expected, ctx.Err()
			case <-time.After(fullListEmptyRetryWait):
			}
			resp, ferr = fetch(ctx, offset, limit)
			if ferr != nil {
				return seen, expected, ferr
			}
			if int64(resp.Count) > expected {
				expected = int64(resp.Count)
			}
			if len(resp.Data) == 0 {
				break
			}
		}

		// 重复页检测：服务端忽略 offset 时会无限返回同一页
		firstFID := resp.Data[0].FID
		if firstFID != 0 && firstFID == lastFirstFID && len(resp.Data) == lastLen {
			return seen, expected, fmt.Errorf("全量清单分页重复（offset=%d 与上一页相同），已中止", offset)
		}
		lastFirstFID, lastLen = firstFID, len(resp.Data)

		if onPage != nil {
			if perr := onPage(resp.Data); perr != nil {
				return seen, expected, perr
			}
		}
		seen += int64(len(resp.Data))
		offset += len(resp.Data)
	}

	if expected == 0 && seen > 0 {
		// count 缺失时无法证明清单完整；清理对账宁可用较慢的 DFS，也不能用可能残缺的集合。
		return seen, expected, fmt.Errorf("全量清单缺少 count，无法确认完整性（实得 %d 个）", seen)
	}
	if expected > 0 && seen < expected {
		return seen, expected, fmt.Errorf("全量清单不完整：服务端声明 %d 个文件，实际只取到 %d 个", expected, seen)
	}
	return seen, expected, nil
}

// resolveRelPathFromChain 在祖先链（从根到目标目录，含目标自身）中定位 rootCID，
// 返回 rootCID 之后的相对路径。链中找不到 rootCID 时返回 false。
func resolveRelPathFromChain(chain []client115.FsFilesPathNode, rootCID int64) (string, bool) {
	idx := -1
	for i := range chain {
		if int64(chain[i].Cid) == rootCID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	names := make([]string, 0, len(chain)-idx-1)
	for _, n := range chain[idx+1:] {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			// 中间层缺名字会让整条路径错位，宁可失败回退 DFS
			return "", false
		}
		// 目录名同样可能含 '/'（115 允许），拼入路径前必须结构转义，
		// 与文件级 EscapeName 保持同一不变量：路径中的 '/' 均为真实分隔符
		names = append(names, strmutil.EscapeName(name))
	}
	return strings.Join(names, "/"), true
}

// cloudParent 取云端路径（/ 分隔）的父路径
func cloudParent(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	if i := strings.LastIndex(p, "/"); i == 0 {
		return "/"
	}
	return ""
}

// dirPathResolver 把全量清单条目的 pid 还原为「相对任务根目录的路径」。
//
// 三级回退：
//  1. folders 表（带一致性校验：条目的 parent_id 必须等于父路径那条记录的 cid）
//  2. 祖先链接口 FsFiles(pid, limit=1) 的顶层 path（顺带回填 folders，含链上祖先）
//  3. 失败返回错误 → 调用方整体回退 DFS
type dirPathResolver struct {
	c115       *client115.Client
	cookie     string
	sqliteDB   *sql.DB
	account    string
	originPath string
	rootCID    int64
	folders    map[int64]db.FolderPath
	cache      map[int64]string
}

func newDirPathResolver(c115 *client115.Client, cookie string, sqliteDB *sql.DB, account, originPath string, rootCID int64) *dirPathResolver {
	r := &dirPathResolver{
		c115:       c115,
		cookie:     cookie,
		sqliteDB:   sqliteDB,
		account:    account,
		originPath: strings.TrimRight(originPath, "/"),
		rootCID:    rootCID,
		cache:      make(map[int64]string, 256),
	}
	if sqliteDB != nil && account != "" {
		if m, err := db.ListFolderEntries(sqliteDB, account); err == nil {
			r.folders = m
		} else {
			logger.S().Warnf("[fulllist] 读取 folders 表失败，改用祖先链接口: %v", err)
		}
	}
	return r
}

// resolve 返回 pid 对应的相对路径（相对 originPath）
func (r *dirPathResolver) resolve(ctx context.Context, pid int64) (string, error) {
	if pid == r.rootCID {
		return "", nil
	}
	if rel, ok := r.cache[pid]; ok {
		return rel, nil
	}

	// 1) folders 表
	if rel, ok := r.fromCacheTable(pid); ok {
		r.cache[pid] = rel
		return rel, nil
	}

	// 2) 祖先链接口
	rel, err := r.fromAncestors(ctx, pid)
	if err != nil {
		return "", err
	}
	r.cache[pid] = rel
	return rel, nil
}

// fromCacheTable 命中 folders 表且一致性校验通过时返回相对路径
func (r *dirPathResolver) fromCacheTable(pid int64) (string, bool) {
	if r.folders == nil {
		return "", false
	}
	row, ok := r.folders[pid]
	if !ok {
		return "", false
	}
	// 必须落在任务根目录下
	var rel string
	switch {
	case row.Path == r.originPath:
		rel = ""
	case strings.HasPrefix(row.Path, r.originPath+"/"):
		rel = row.Path[len(r.originPath)+1:]
	default:
		return "", false
	}
	// 一致性校验：父路径那条记录的 cid 必须等于本条的 parent_id。
	// 云端改名/移动后（监控未开）缓存会陈旧，此时宁可走接口重解析。
	if row.ParentID != 0 {
		parent, ok := r.folders[row.ParentID]
		if !ok || parent.Path != cloudParent(row.Path) {
			return "", false
		}
	}
	return rel, true
}

// fromAncestors 用列目录响应的顶层 path 还原路径，并回填 folders 表
func (r *dirPathResolver) fromAncestors(ctx context.Context, pid int64) (string, error) {
	resp, err := r.c115.FsFiles(ctx, strconv.FormatInt(pid, 10), 1, 0, r.cookie)
	if err != nil {
		return "", fmt.Errorf("解析目录 %d 路径失败: %w", pid, err)
	}
	if resp == nil || !resp.State || len(resp.Path) == 0 {
		return "", fmt.Errorf("解析目录 %d 路径失败: 祖先链为空", pid)
	}
	// 祖先链必须包含目标目录自身（115 /files 响应的 path 语义）。
	// 若末项不是 pid，说明语义变了或链路被截断，此时拼出来的路径会整体错一级，必须失败。
	if last := resp.Path[len(resp.Path)-1]; int64(last.Cid) != pid {
		return "", fmt.Errorf("目录 %d 的祖先链末项是 %d，语义不符", pid, int64(last.Cid))
	}
	rel, ok := resolveRelPathFromChain(resp.Path, r.rootCID)
	if !ok {
		return "", fmt.Errorf("目录 %d 不在任务根目录 %d 之下", pid, r.rootCID)
	}

	// 回填 folders：从根开始逐级拼路径，顺带把链上祖先也补齐，
	// 否则 M5 会停掉 DFS 原本"每目录 Upsert"的缓存维护。
	if r.sqliteDB != nil && r.account != "" {
		r.upsertChain(resp.Path, r.rootCID)
	}
	return rel, nil
}

func (r *dirPathResolver) upsertChain(chain []client115.FsFilesPathNode, rootCID int64) {
	idx := -1
	for i := range chain {
		if int64(chain[i].Cid) == rootCID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	rel := ""
	for _, node := range chain[idx:] {
		name := strings.TrimSpace(node.Name)
		if name != "" && int64(node.Cid) != rootCID {
			// 目录名含 '/' 时先结构转义再拼路径（与 resolveRelPathFromChain、文件级转义一致），
			// 保证 folders 表记录的 Path 与文件 RelPath 遵循同一规则；
			// DB 的 FileName 字段保留原始名（与「Name 字段保留原始值」约定一致）
			if rel == "" {
				rel = strmutil.EscapeName(name)
			} else {
				rel = rel + "/" + strmutil.EscapeName(name)
			}
		}
		full := r.originPath
		if rel != "" {
			full = r.originPath + "/" + rel
		}
		if full == "" {
			continue
		}
		if err := db.UpsertFolderEntry(r.sqliteDB, r.account, db.FilePathEntry{
			FileID:     strconv.FormatInt(int64(node.Cid), 10),
			Path:       full,
			FileName:   name,
			ParentID:   strconv.FormatInt(int64(node.Pid), 10),
			UpdateTime: time.Now().Unix(),
		}); err != nil {
			logger.S().Warnf("[fulllist] 回填 folders 失败 cid=%d path=%s: %v", int64(node.Cid), full, err)
		}
		if r.folders == nil {
			r.folders = make(map[int64]db.FolderPath, 64)
		}
		r.folders[int64(node.Cid)] = db.FolderPath{Path: full, ParentID: int64(node.Pid)}
	}
}

// listAllFilesViaFullList 用 cur=0 全量清单构建文件列表。
// 任何一步失败都返回 error，由 scanAllFiles 回退到 DFS。
func listAllFilesViaFullList(
	ctx context.Context,
	c115 *client115.Client,
	cookie string,
	rootCID int64,
	originPath string,
	strmExts map[string]struct{},
	downloadExts map[string]struct{},
	minFileSize int64,
	blacklist []string,
	taskID string,
	rt *Runtime,
	sseServer *sse.Server,
	sqliteDB *sql.DB,
	account string,
	stats *ScanStats,
) ([]*fileItem, error) {
	resolver := newDirPathResolver(c115, cookie, sqliteDB, account, originPath, rootCID)

	type matched struct {
		entry client115.FullListEntry
		kind  fileKind
		ext   string
	}
	var matchedEntries []matched
	var fileCount, matchCount, bdmvSkipped int

	fetch := func(ctx context.Context, offset, limit int) (*client115.FsFilesAllResp, error) {
		return c115.FsFilesAll(ctx, strconv.FormatInt(rootCID, 10), limit, offset, cookie)
	}

	start := time.Now()
	pageNo := 0
	seen, expected, err := fetchFullListPages(ctx, fetch, fullListPageSize, func(page []client115.FullListEntry) error {
		pageNo++
		for i := range page {
			e := page[i]
			if e.AID == 7 || e.AID == 120 { // 回收站
				continue
			}
			fileCount++
			name := strings.TrimSpace(e.Name)
			if name == "" {
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			kind := kindSkip
			if _, ok := strmExts[ext]; ok {
				if !isValidPickcode(e.PickCode) {
					logger.S().Debugf("[fulllist] 跳过无效pickcode: %q pickcode=%q", name, e.PickCode)
					continue
				}
				if reason, pass := shouldGenerateStrm(name, e.Size, minFileSize, blacklist); !pass {
					logger.S().Debugf("[fulllist] 跳过%s的媒体文件: %q", reason, name)
					continue
				}
				kind = kindStrm
			} else if _, ok := downloadExts[ext]; ok {
				kind = kindDownload
			}
			if kind == kindSkip {
				continue
			}
			matchCount++
			matchedEntries = append(matchedEntries, matched{entry: e, kind: kind, ext: ext})
		}
		// 每 5 页推一次进度（大库拉取可能持续数分钟，避免 UI 显示 0% 卡死）
		if pageNo%5 == 0 && rt != nil && taskID != "" {
			detail := fmt.Sprintf("全量清单：已拉取 %d 个文件（匹配 %d 个媒体）", fileCount, matchCount)
			rt.SetState(taskID, func(s *RuntimeState) {
				s.Stage = StageScanning
				s.StageDetail = detail
			})
			if sseServer != nil {
				sseServer.EmitProgress(sse.ProgressPayload{
					TaskID:         taskID,
					OverallPercent: "0.00",
					Stage:          StageScanning,
					StageDetail:    detail,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("全量清单拉取失败: %w", err)
	}
	if seen == 0 {
		// 空清单不做任何判断：可能是服务端异常，也可能是任务目录真的空。
		// 交给 DFS 复核一次（空目录下 DFS 也很便宜），避免用空集合参与清理。
		return nil, fmt.Errorf("全量清单为空（cid=%d）", rootCID)
	}
	if len(matchedEntries) == 0 {
		// 有文件但一个都没命中扩展名：属于正常结果（例如纯字幕目录），
		// 直接返回空列表；清理逻辑会因 pickcode 集合为空而跳过。
		logger.S().Infof("[fulllist] 清单内无匹配文件（seen=%d expected=%d），本次不生成任何文件", seen, expected)
		stats.Files, stats.Matched = fileCount, 0
		return nil, nil
	}

	// 路径翻译：任何一棵目录翻译失败 → 整体失败回退 DFS。
	// 不做"部分条目跳过"：漏掉的 pickcode 会被清理逻辑当成云端已删而误删本地 STRM。
	dirRels := make(map[int64]string, 256)
	for i := range matchedEntries {
		pid := matchedEntries[i].entry.PID
		if _, done := dirRels[pid]; done {
			continue
		}
		rel, rerr := resolver.resolve(ctx, pid)
		if rerr != nil {
			return nil, fmt.Errorf("路径还原失败（回退 DFS）: %w", rerr)
		}
		dirRels[pid] = rel
	}

	out := make([]*fileItem, 0, len(matchedEntries))
	for i := range matchedEntries {
		m := matchedEntries[i]
		dirRel := dirRels[m.entry.PID]
		// 115 文件名本身可含 '/'，拼入路径后会被误当目录分隔符；
		// 统一走 JoinCloudPath（内部做结构转义），避免一个文件被拆成多级目录。
		relName := strmutil.JoinCloudPath(dirRel, m.entry.Name)
		cloudPath := originPath
		if relName != "" {
			cloudPath = strings.TrimRight(originPath, "/") + "/" + relName
		}
		if model.IsBdmvStreamPath(cloudPath) {
			bdmvSkipped++
			continue
		}
		out = append(out, &fileItem{
			CloudPath: cloudPath,
			RelPath:   relName,
			Name:      m.entry.Name,
			PickCode:  m.entry.PickCode,
			Size:      m.entry.Size,
			Ext:       m.ext,
			Kind:      m.kind,
			FID:       strconv.FormatInt(m.entry.FID, 10),
			ParentID:  strconv.FormatInt(m.entry.PID, 10),
		})
	}

	if stats != nil {
		stats.Dirs = len(dirRels)
		stats.Files = fileCount
		stats.Matched = matchCount
		stats.BdmvSkipped = bdmvSkipped
	}
	logger.S().Infof("[fulllist] done files=%d matched=%d dirs=%d bdmvSkipped=%d seen=%d expected=%d elapsed=%s",
		fileCount, matchCount, len(dirRels), bdmvSkipped, seen, expected, time.Since(start).Round(time.Millisecond))
	return out, nil
}

// scanAllFiles 扫描入口：优先 cur=0 全量清单，任何异常自动回退 DFS。
//
// 为什么整体回退而不是部分降级：清单/路径不完整时，缺失条目的 pickcode 不在云端集合里，
// 清理逻辑会把本地已有的 STRM 判成孤儿删掉。宁可慢一次，也不能删错。
func scanAllFiles(
	ctx context.Context,
	c115 *client115.Client,
	cookie string,
	rootCID int64,
	originPath string,
	strmExts map[string]struct{},
	downloadExts map[string]struct{},
	minFileSize int64,
	blacklist []string,
	taskID string,
	rt *Runtime,
	sseServer *sse.Server,
	sqliteDB *sql.DB,
	account string,
	stats *ScanStats,
) ([]*fileItem, error) {
	items, err := listAllFilesViaFullList(ctx, c115, cookie, rootCID, originPath,
		strmExts, downloadExts, minFileSize, blacklist, taskID, rt, sseServer, sqliteDB, account, stats)
	if err == nil {
		return items, nil
	}
	// 认证/限流/重试耗尽时回退 DFS 会把请求放大到整棵目录树，必须直接失败。
	if isFatalScanError(err) {
		return nil, err
	}

	logger.S().Warnf("[scan] 全量清单不可用，回退 DFS 扫描: %v", err)
	if sseServer != nil && taskID != "" {
		sseServer.EmitLog(taskID, "warn",
			"全量清单不可用，已回退逐目录扫描（本次可能较慢，原因见日志）: "+err.Error())
	}
	if stats != nil {
		*stats = ScanStats{}
	}
	return listAllFilesRecursive(ctx, c115, cookie, rootCID, originPath,
		strmExts, downloadExts, minFileSize, blacklist, taskID, rt, sseServer, sqliteDB, account, stats)
}
