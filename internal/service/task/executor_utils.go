package task

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/concurrency"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// scanRetryWait 列目录失败后的重试等待窗口（给 115 侧恢复时间）。
// 包级变量以便测试注入短等待。
var scanRetryWait = 20 * time.Second

// resolvedStrm 合并 settings + 任务覆盖后的最终 STRM 配置
type resolvedStrm struct {
	StrmPrefix         string
	StrmPrefixSource   string // 前缀来源：configured / auto_lan_ip / unresolved（用于日志与报错）
	EnablePathEncoding bool
	StrmExtensions     map[string]struct{}
	DownloadExtensions map[string]struct{}
}

// resolveStrmSettings 合并全局 settings + 任务级自定义
// 对齐 TS resolveStrmSettings
func resolveStrmSettings(task *Task, s *model.Settings, baseURL, publicBaseURL string) resolvedStrm {
	r := resolvedStrm{
		StrmExtensions:     make(map[string]struct{}),
		DownloadExtensions: make(map[string]struct{}),
	}

	// 继承全局
	for _, ext := range s.StrmExtensions {
		r.StrmExtensions[normalizeExt(strings.ToLower(ext))] = struct{}{}
	}
	for _, ext := range s.DownloadExtensions {
		r.DownloadExtensions[normalizeExt(strings.ToLower(ext))] = struct{}{}
	}
	if len(r.StrmExtensions) == 0 {
		for _, ext := range model.DefaultStrmExtensions {
			r.StrmExtensions[normalizeExt(strings.ToLower(ext))] = struct{}{}
		}
	}

	// strmPrefix：任务覆盖 > 全局 > publicBaseURL > baseURL > 本机内网 IP 兜底
	//
	// 关键：回环地址（0.0.0.0 / 127.0.0.1 / localhost）会被跳过。
	// 历史行为是直接把 baseURL（默认 http://0.0.0.0:8090）写进 STRM，
	// 导致除本机外的设备全部无法播放，且用户很难定位原因。
	prefix, source := strm.ResolveStrmPrefix([]string{
		task.StrmPrefix,
		s.StrmPrefix,
		publicBaseURL,
		baseURL,
	}, strm.PortFromBaseURL(baseURL))
	r.StrmPrefix = prefix
	r.StrmPrefixSource = source

	// enablePathEncoding
	r.EnablePathEncoding = s.EnablePathEncoding
	if task.EnablePathEncoding {
		r.EnablePathEncoding = true
	}

	return r
}

// isValidPickcode / shouldGenerateStrm / getStrmFileName 的实现在 pkg/strmutil，
// monitor 侧共用同一份（此前两个包各写了一遍，逐行相同）。

func isValidPickcode(pc string) bool { return strmutil.IsValidPickcode(pc) }

func shouldGenerateStrm(fileName string, fileSize, minFileSize int64, blacklist []string, matcher ...*concurrency.StringMatcher) (string, bool) {
	return strmutil.ShouldGenerateStrm(fileName, fileSize, minFileSize, blacklist, matcher...)
}

func getStrmFileName(fileName string) string { return strmutil.StrmFileName(fileName) }

// getRelPathStrmSuffix 基于相对路径（含扩展名）生成对应的 .strm 相对路径
// 等价于替换文件名为 getStrmFileName(name)
//
// 入参是「云端相对路径」，返回是「本地相对路径」：目录段与文件名都会做非法字符清理，
// 保证写入、增量判定、路径级清理三处拿到完全一致的本地路径。
func replaceRelPathExtToStrm(relPath string) string {
	local := strmutil.SanitizeRelPath(relPath)
	dir, name := filepath.Split(local)
	return filepath.Join(dir, getStrmFileName(name))
}

// resolveStrmRelPath 计算 STRM 文件的相对路径（P1-4 文件名模板优先，否则默认替换扩展名）。
// 抽成公共函数，保证「增量跳过判定」与「实际写入」使用完全一致的路径，
// 否则判定路径与实际路径不一致会导致漏补生成。
func resolveStrmRelPath(template, account string, f *fileItem) string {
	if template == "" {
		return replaceRelPathExtToStrm(f.RelPath)
	}
	relDir, relName := filepath.Split(f.RelPath)
	ext := strings.ToLower(filepath.Ext(f.Name))
	stem := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
	if strings.EqualFold(ext, ".iso") {
		stem = stem + ".iso"
	}
	newName := model.RenderStrmFilenameTemplate(template, f.Name, ext, stem, account)
	if newName == "" {
		newName = getStrmFileName(relName)
	}
	// 目录段来自云端路径，同样需要清理（模板只负责文件名）
	return strmutil.SanitizeRelPath(filepath.Join(relDir, newName))
}

// localArtifactExists 判断增量跳过候选的本地产物是否仍存在
// （STRM → 目标 STRM 路径；下载 → 目标真实文件路径）。
// 本地被误删的条目不能跳过，否则永远补不回来（漏生成无法自愈）。
func localArtifactExists(task *Task, strmTemplate string, f *fileItem) bool {
	var rel string
	switch f.Kind {
	case kindStrm:
		rel = resolveStrmRelPath(strmTemplate, task.Account, f)
	case kindDownload:
		rel = strmutil.SanitizeRelPath(f.RelPath)
	default:
		return true
	}
	if rel == "" {
		return true
	}
	_, err := os.Stat(filepath.Join(task.TargetPath, rel))
	return err == nil
}

// anyToInt64 把 115 返回的 any 型 id（可能是 string / float64 / int）安全转成 int64。
// 115 大 cid（19 位）可能超出 float64 精度：一旦被解码成 float64，%v 会输出
// 科学计数法（如 3.49e+18），再 ParseInt 必然失败并把 cid 当成 0，
// 导致整棵子树被静默丢弃（表现为大库漏生成）。因此字符串优先、按原样解析。
func anyToInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int64:
		return x, true
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case float64:
		return int64(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", x))
		if s == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
}

// idString 把 any 型 id 规范化为十进制字符串（避免科学计数法写进 DB）。
// 无法解析时回退 %v，保持与旧行为兼容。
func idString(v any) string {
	if n, ok := anyToInt64(v); ok {
		return strconv.FormatInt(n, 10)
	}
	return fmt.Sprintf("%v", v)
}

// writeStrmFile 原子写入 STRM 文件（先写 tmp 再 rename）。
// 读-比较-写：磁盘内容与待写内容一致时跳过写入，unchanged=true。
// 调用方应据此跳过下游刷库等动作——否则 mtime 变化会让 Emby
// 文件监视器把未变更的 STRM 当作更新，触发媒体库假重扫。
// 注意：跳过写入≠跳过生成，调用方的清理对账清单仍需包含该文件。
func writeStrmFile(strmPath, content string) (unchanged bool, err error) {
	return strmutil.WriteStrmFile(strmPath, content)
}

// buildStrmContent 生成 .strm 文件内容
// 302 模式：{{StrmPrefix}}/api/fs/get?account=xxx&pickcode=xxx&file_name=URLencode(name)
// 否则：   {{StrmPrefix}}/api/strm?account=xxx&pickcode=xxx&file_name=URLencode(name)
// P1-4：urlTemplate 非空时优先用 model.RenderStrmUrlTemplate（变量自动 QueryEscape）
func buildStrmContent(task *Task, f *fileItem, r resolvedStrm, urlTemplate ...string) (string, error) {
	// pickcode 严格校验（对齐 MoviePilot len=17 && isalnum）
	if !isValidPickcode(f.PickCode) {
		return "", fmt.Errorf("pickcode 无效(需17位字母数字): %q file=%q", f.PickCode, f.Name)
	}
	// —— P1-4 高级 URL 模板优先 ——
	if len(urlTemplate) > 0 && urlTemplate[0] != "" {
		ext := strings.ToLower(filepath.Ext(f.Name))
		stem := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		if strings.EqualFold(ext, ".iso") {
			stem = stem + ".iso"
		}
		if rendered := model.RenderStrmUrlTemplate(urlTemplate[0], r.StrmPrefix, task.Account, f.PickCode, f.Name, ext, stem); rendered != "" {
			return rendered, nil
		}
	}
	var u string
	prefix := strings.TrimRight(r.StrmPrefix, "/")
	// 统一硬编码 /api/strm（带智能路由 + proxy 模式）
	u = fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s", prefix, urlPathEncode(task.Account), f.PickCode)
	if f.Name != "" {
		u += "&file_name=" + urlPathEncode(f.Name)
	}
	return u + "\n", nil
}

// urlPathEncode 对文件名做 URL 编码（保留扩展名点号、兼容中文）
func urlPathEncode(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') ||
			b == '-' || b == '_' || b == '.' || b == '~' || b == '/' {
			sb.WriteByte(b)
		} else {
			const h = "0123456789ABCDEF"
			sb.WriteByte('%')
			sb.WriteByte(h[b>>4])
			sb.WriteByte(h[b&0x0F])
		}
	}
	return sb.String()
}

// ScanStats 扫描阶段统计信息（供任务日志/结果展示）
type ScanStats struct {
	// Dirs 遍历的目录数
	Dirs int
	// Files 见到的文件数
	Files int
	// Matched 命中 STRM/下载扩展名的文件数
	Matched int
	// BdmvSkipped 因位于 BDMV/STREAM 而跳过的视频流数量
	BdmvSkipped int
	// FailedDirs 列目录最终失败（含重试）的目录相对路径。
	// 这些目录的内容未知，调用方必须：
	//  1) 在清理对账时跳过其前缀下的本地 STRM（防止误判孤儿而清库）
	//  2) 在任务结果中告知用户，建议重跑
	FailedDirs []string
}

// listAllFilesRecursive 递归遍历目录，返回所有需要处理的文件（带 Kind 分类）
// minFileSize：仅对 STRM 文件生效；0 表示不限制。
// blacklist：STRM 文件名黑名单关键词列表（空 = 不启用）。
// 对齐 MoviePilot StrmGenerater.should_generate_strm + not_blacklist_key。
// pickcode 校验：STRM 文件必须是 17 位字母数字的有效 pickcode
// 新增：taskID/rt/sseServer 用于扫描阶段的进度心跳广播（大库场景下避免 UI 显示 0% 卡死）
// 新增：sqliteDB/account 用于顺手维护 folders 表（目录 → cid），供生活事件按 parent_id 离线反查
func listAllFilesRecursive( //nolint:cyclop // complexity: 56（缺口 2 失败容忍+重试轮后）
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
	type stackEntry struct {
		cid       int64
		relPath   string // 相对 originPath 的前缀路径（为空表示在 originPath 下）
		parentCID int64  // 父目录 cid（0 表示未知）
	}
	var out []*fileItem
	stk := []stackEntry{{cid: rootCID, relPath: "", parentCID: 0}}

	// P2-2：黑名单条目数 ≥ 阈值时构建一次 AC 自动机复用，避免 per-file 重构建
	var blMatcher *concurrency.StringMatcher
	if concurrency.ShouldUseAC(blacklist) {
		blMatcher = concurrency.NewStringMatcher(blacklist)
	}

	cidSeen := make(map[int64]struct{})
	dirCount, fileCount, matchCount := 0, 0, 0
	bdmvSkipped := 0
	logger.S().Infof("[listAllFilesRecursive] start cid=%d originPath=%s", rootCID, originPath)

	// ---- 扫描进度心跳：每 3 秒广播一次（让 UI 知道后台还活着） ----
	heartbeatStop := make(chan struct{})
	defer close(heartbeatStop)
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		lastDir, lastFile, lastMatch := -1, -1, -1
		for {
			select {
			case <-heartbeatStop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 只有数字有变化时才广播，减少无意义流量
				if dirCount == lastDir && fileCount == lastFile && matchCount == lastMatch {
					continue
				}
				lastDir, lastFile, lastMatch = dirCount, fileCount, matchCount
				detail := fmt.Sprintf("已扫描 %d 个目录, %d 个文件 (命中 %d 个媒体)", dirCount, fileCount, matchCount)
				if taskID != "" && rt != nil {
					rt.SetState(taskID, func(s *RuntimeState) {
						s.Stage = StageScanning
						s.StageDetail = detail
					})
				}
				if sseServer != nil && taskID != "" {
					sseServer.EmitProgress(sse.ProgressPayload{
						TaskID:         taskID,
						OverallPercent: "0.00",
						Stage:          StageScanning,
						StageDetail:    detail,
					})
				}
				logger.S().Debugf("[listAllFilesRecursive] heartbeat task=%s: %s", taskID, detail)
			}
		}
	}()

	// ---- 失败容忍（缺口 2）----
	// 列目录失败不再中断整个任务：记入 pendingRetry 继续扫，
	// 全部扫完后等待恢复窗口统一重试一轮；仍失败的进 failedDirs，
	// 由调用方做清理保护（防误判孤儿清库）。根目录失败仍是致命错误。
	pendingRetry := make([]stackEntry, 0, 8)
	failedDirs := make([]string, 0, 4)

	defer func() {
		logger.S().Infof("[listAllFilesRecursive] done dirs=%d files=%d matched=%d totalOut=%d failedDirs=%d", dirCount, fileCount, matchCount, len(out), len(failedDirs))
		if bdmvSkipped > 0 {
			logger.S().Infof("[listAllFilesRecursive] 跳过 %d 个 BDMV/STREAM 原盘视频流（不生成 STRM）", bdmvSkipped)
		}
		if len(failedDirs) > 0 {
			logger.S().Warnf("[listAllFilesRecursive] %d 个目录列目录最终失败（其下 STRM 本次不参与清理对账）: %v", len(failedDirs), failedDirs)
		}
		if stats != nil {
			stats.Dirs = dirCount
			stats.Files = fileCount
			stats.Matched = matchCount
			stats.BdmvSkipped = bdmvSkipped
			stats.FailedDirs = failedDirs
		}
	}()

	const retryRounds = 1
	// 包级变量便于测试注入短等待
	retryWait := scanRetryWait

	for round := 0; ; round++ {
		for len(stk) > 0 {
			top := stk[len(stk)-1]
			stk = stk[:len(stk)-1]
			if _, seen := cidSeen[top.cid]; seen {
				continue
			}
			cidSeen[top.cid] = struct{}{}

			// P1-1：全量扫描顺手维护 folders 表（目录 cid → 云端路径），对齐参考项目 parent_id_paths 目录路径库。
			// 生活事件的 parent_id 就是父目录自身的 cid，该表命中后事件路径反查可完全离线完成（零 115 接口）。
			if sqliteDB != nil && account != "" {
				dirPath := strings.TrimRight(originPath, "/")
				if top.relPath != "" {
					dirPath = strings.TrimRight(originPath, "/") + "/" + top.relPath
				}
				if dirPath != "" {
					parentID := ""
					if top.parentCID > 0 {
						parentID = strconv.FormatInt(top.parentCID, 10)
					}
					if ferr := db.UpsertFolderEntry(sqliteDB, account, db.FilePathEntry{
						FileID:   strconv.FormatInt(top.cid, 10),
						Path:     dirPath,
						FileName: filepath.Base(dirPath),
						ParentID: parentID,
					}); ferr != nil {
						logger.S().Warnf("[listAllFilesRecursive] 写 folders 表失败 cid=%d path=%s: %v", top.cid, dirPath, ferr)
					}
				}
			}

			var offset int
			expectedCount := 0
			pageMatched := 0
			for page := 0; ; page++ {
				resp, err := c115.FsFiles(ctx, strconv.FormatInt(top.cid, 10), 1000, offset, cookie)
				if err != nil || !resp.State {
					isRoot := top.cid == rootCID && top.relPath == ""
					kind := "err"
					detail := ""
					if err != nil {
						detail = err.Error()
					} else {
						kind = "state=false"
					}
					if isRoot {
						// 根目录失败无法定位扫描范围，保持致命错误
						logger.S().Errorf("[listAllFilesRecursive] 根目录列目录失败(kind=%s) cid=%d relPath=%s: %s", kind, top.cid, top.relPath, detail)
						return nil, fmt.Errorf("fs_files root list failed (kind=%s): %s", kind, detail)
					}
					// 非根目录：容忍失败，稍后重试（从 cidSeen 摘除以便重试入队）
					logger.S().Warnf("[listAllFilesRecursive] 目录列目录失败(kind=%s, round=%d) cid=%d relPath=%s: %s，稍后重试", kind, round, top.cid, top.relPath, detail)
					delete(cidSeen, top.cid)
					pendingRetry = append(pendingRetry, top)
					break
				}
				if len(resp.Data) == 0 {
					break
				}
				if resp.Count > 0 {
					expectedCount = resp.Count
				}
				logger.S().Debugf("[listAllFilesRecursive] cid=%d relPath=%q page=%d offset=%d returned=%d count=%d", top.cid, top.relPath, page, offset, len(resp.Data), resp.Count)
				for _, e := range resp.Data {
					logger.S().Debugf("[listAllFilesRecursive] entry name=%q cid=%v fid=%v fc=%v size=%d pickcode=%q isDir=%v", e.Name, e.CID, e.FID, e.FC, e.Size, e.PickCode, e.IsDir)
					relName := e.Name
					if top.relPath != "" {
						relName = top.relPath + "/" + e.Name
					}
					cloudPath := originPath
					if relName != "" {
						cloudPath = strings.TrimRight(originPath, "/") + "/" + relName
					}
					// BDMV 原盘：跳过 BDMV/STREAM 层级（含其目录本身，不再向下遍历），
					// 避免一个蓝光原盘被拆成上百个 m2ts STRM。ISO 原盘不受影响。
					if model.IsBdmvStreamPath(cloudPath) {
						bdmvSkipped++
						continue
					}
					// isDir 判断：FsFiles 已经基于 cid/fid 做了可靠判定，这里直接复用，不再用 fc 覆盖。
					isDir := e.IsDir
					if isDir {
						cid, ok := anyToInt64(e.CID)
						if !ok || cid <= 0 {
							// 解析失败不能再静默 cid=0：那会把整棵子树悄悄丢掉（大库漏生成的直接原因之一）。
							logger.S().Warnf("[listAllFilesRecursive] 目录 cid 解析失败，跳过整棵子树: cid=%v name=%q relPath=%q", e.CID, e.Name, relName)
							continue
						}
						stk = append(stk, stackEntry{cid: cid, relPath: relName, parentCID: top.cid})
						dirCount++
						continue
					}
					fileCount++
					ext := strings.ToLower(filepath.Ext(e.Name))
					kind := kindSkip
					if _, ok := strmExts[ext]; ok {
						// STRM：pickcode 必须有效（17位字母数字），并通过 shouldGenerateStrm（黑名单 + 最小文件大小）
						if !isValidPickcode(e.PickCode) {
							logger.S().Warnf("[listAllFilesRecursive] 跳过无效pickcode的媒体文件: %q pickcode=%q", e.Name, e.PickCode)
							continue
						}
						if reason, pass := shouldGenerateStrm(e.Name, e.Size, minFileSize, blacklist, blMatcher); !pass {
							logger.S().Warnf("[listAllFilesRecursive] 跳过%s的媒体文件: %q", reason, e.Name)
							continue
						}
						kind = kindStrm
					} else if _, ok := downloadExts[ext]; ok {
						kind = kindDownload
					}
					if kind == kindSkip {
						continue
					}
					pageMatched++
					matchCount++
					out = append(out, &fileItem{
						CloudPath: cloudPath,
						RelPath:   relName,
						Name:      e.Name,
						PickCode:  e.PickCode,
						Size:      e.Size,
						Ext:       ext,
						Kind:      kind,
						FID:       idString(e.FID),
						ParentID:  strconv.FormatInt(top.cid, 10),
					})
				}
				// 翻页：优先用 count 判定是否还有下一页。
				// 仅靠 len<limit 判定，会在 115 未按 limit 返回（单页封顶小于 1000）时提前退出，导致大库漏生成。
				offset += len(resp.Data)
				if resp.Count > 0 {
					if offset >= resp.Count {
						break
					}
					continue
				}
				if len(resp.Data) < 1000 {
					break
				}
			}
			// 每目录对账：累计返回条目数与 API 的 count 不一致时告警，便于现场定位漏扫
			if expectedCount > 0 && offset < expectedCount {
				logger.S().Warnf("[listAllFilesRecursive] cid=%d relPath=%q 分页未取全: returned=%d count=%d（疑似漏扫）", top.cid, top.relPath, offset, expectedCount)
			}
			logger.S().Infof("[listAllFilesRecursive] cid=%d relPath=%q pageMatched=%d returned=%d count=%d", top.cid, top.relPath, pageMatched, offset, expectedCount)
		}

		// ---- 重试轮控制：扫完全量后，对失败目录统一重试 ----
		if len(pendingRetry) == 0 {
			return out, nil
		}
		if round >= retryRounds {
			for _, e := range pendingRetry {
				failedDirs = append(failedDirs, e.relPath)
			}
			return out, nil
		}
		logger.S().Infof("[listAllFilesRecursive] %d 个目录列目录失败，等待 %s 后重试一轮", len(pendingRetry), retryWait)
		select {
		case <-ctx.Done():
			for _, e := range pendingRetry {
				failedDirs = append(failedDirs, e.relPath)
			}
			return out, nil
		case <-time.After(retryWait):
		}
		stk = append(stk, pendingRetry...)
		pendingRetry = pendingRetry[:0]
	}
	// unreachable：for round 循环只在内部 return
}

// runDownloads 真实下载文件：并发 + 进度上报 + SSE 广播
func runDownloads(
	ctx context.Context,
	taskID string,
	files []*fileItem,
	saveDir string,
	account string,
	cookie string,
	c115 *client115.Client,
	workers int,
	perFile *perFilePercent,
	downloaded *int64,
	total int,
	rt *Runtime,
	sseServer *sse.Server,
) {
	// P2-1：统一用 WorkerPool，不再每处手写 sem+wg 模板
	pool := concurrency.NewPool(workers)
	for _, f := range files {
		f := f
		pool.Submit(func() error {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			savePath := filepath.Join(saveDir, strmutil.SanitizeRelPath(f.RelPath))
			if err := ensureDir(filepath.Dir(savePath)); err != nil {
				sseServer.EmitLog(taskID, "error", "mkdir "+filepath.Dir(savePath)+": "+err.Error())
				return nil
			}
			meta, err := c115.GetDownloadUrlWebFull(ctx, f.PickCode, cookie, c115.UserAgent)
			if err != nil {
				sseServer.EmitLog(taskID, "error", fmt.Sprintf("resolve dl url %s: %v", f.RelPath, err))
				return nil
			}
			if err := downloadFileWithProgress(ctx, meta.URL, savePath, taskID, f, cookie, c115.UserAgent,
				perFile, rt, sseServer, downloaded, total); err != nil {
				sseServer.EmitLog(taskID, "error", fmt.Sprintf("download %s: %v", f.RelPath, err))
			}
			return nil
		})
	}
	pool.Wait()
}

// downloadFileWithProgress 下载单个文件并广播进度
func downloadFileWithProgress(
	ctx context.Context,
	urlStr string,
	savePath string,
	taskID string,
	f *fileItem,
	cookie string,
	userAgent string,
	perFile *perFilePercent,
	rt *Runtime,
	sseServer *sse.Server,
	downloaded *int64,
	total int,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	tmpPath := savePath + ".tmp"
	fp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		fp.Close()
		_ = os.Remove(tmpPath)
	}()

	totalLen := f.Size
	if totalLen <= 0 && resp.ContentLength > 0 {
		totalLen = resp.ContentLength
	}
	var (
		written int64
		buf     = make([]byte, 64*1024)
		lastPct = -1
	)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		nr, rerr := resp.Body.Read(buf)
		if nr > 0 {
			nw, werr := fp.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
				if totalLen > 0 {
					pct := int(written * 100 / totalLen)
					if pct != lastPct {
						lastPct = pct
						perFile.Update(f.CloudPath, pct)
						_, overall := perFile.Overall(total)
						sseServer.EmitProgress(sse.ProgressPayload{
							TaskID: taskID, FilePath: f.RelPath, Percent: pct, OverallPercent: overall,
						})
						rt.AppendLog(taskID, jsonLine(sse.ProgressPayload{
							TaskID: taskID, FilePath: f.RelPath, Percent: pct, OverallPercent: overall,
						}))
					}
				}
			}
			if werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err := fp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, savePath); err != nil {
		return err
	}
	perFile.Mark(f.CloudPath, 100)
	atomic.AddInt64(downloaded, 1)
	_, overall := perFile.Overall(total)
	sseServer.EmitProgress(sse.ProgressPayload{
		TaskID: taskID, FilePath: f.RelPath, Percent: 100, OverallPercent: overall,
	})
	rt.AppendLog(taskID, jsonLine(sse.ProgressPayload{
		TaskID: taskID, FilePath: f.RelPath, Percent: 100, OverallPercent: overall,
	}))
	return nil
}

// removeExtraFiles 扫描 targetPath，删除不在 fileEntries 相对路径清单里的本地多余文件
// 对齐 MoviePilot full_sync_remove_unless_strm + cleanup_confirm_mode：
//   - MaxThreshold：超过此数量则拒绝执行（防误删大批量）
//   - StableThreshold：超过此数量但未超 MaxThreshold 时，若 ConfirmMode != "none" 则延迟删除
//   - ConfirmMode："none" 立即删除 / "plugin_ui" 插件内确认 / "telegram" 通知按钮确认
//
// ctx 用于 SubmitDeferredBatch 调用（持久化到 SQLite 时透传）。
// submitter 为 nil 时退化为立即删除（用于单元测试或未启用持久化场景）。
// taskID 仅用于批次关联日志，可空。
// 返回：(已删除数, 延迟批次ID, 误差)。延迟批次ID 非空时表示已入队待二次确认，未执行删除。
func removeExtraFiles(ctx context.Context, submitter CleanupBatchSubmitter, taskID, targetPath string, entries []*fileItem, s *model.Settings) (CleanupOutcome, error) {
	keep := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		// 保留 strm 文件（.iso 文件需保留双扩展名 game.iso.strm）
		sp := filepath.Join(targetPath, replaceRelPathExtToStrm(e.RelPath))
		keep[filepath.Clean(sp)] = struct{}{}
		// 保留真实下载文件
		dp := filepath.Join(targetPath, strmutil.SanitizeRelPath(e.RelPath))
		keep[filepath.Clean(dp)] = struct{}{}
	}

	// 第一遍扫描：收集待删文件路径（同时统计本地文件总数，供比例阈值判定）
	var toDelete []string
	localTotal := 0
	err := filepath.Walk(targetPath, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			return nil
		}
		clean := filepath.Clean(p)
		if strings.HasPrefix(filepath.Base(clean), ".") {
			return nil
		}
		localTotal++
		if _, ok := keep[clean]; ok {
			return nil
		}
		toDelete = append(toDelete, clean)
		return nil
	})
	if err != nil {
		return CleanupOutcome{}, err
	}

	if len(toDelete) == 0 {
		return CleanupOutcome{Verdict: CleanupAllow}, nil
	}

	// 三重保护判定（比例 + 绝对 + 目录数），替代原「单阈值 10 个」硬拒绝
	cleanup := s.Cleanup
	verdict, reason := EvaluateCleanup(CleanupInput{
		Candidates: len(toDelete),
		TotalLocal: localTotal,
		Dirs:       countUniqueDirs(toDelete),
		Cfg:        cleanup,
	})
	confirmMode := cleanup.ConfirmMode
	if confirmMode == "" {
		confirmMode = "none"
	}

	// 保护态：只停删除不停生成，明确告知原因（原实现是静默返回 0，用户无从判断）
	if verdict == CleanupProtected {
		logger.S().Warnf("[removeExtraFiles] 进入清理保护态，本次不删除任何文件: %s (taskID=%s)",
			reason, taskID)
		return CleanupOutcome{Verdict: CleanupProtected, Reason: reason}, nil
	}

	// 延迟确认模式：需二次确认且 ConfirmMode != "none" 且 submitter 已注入
	if verdict == CleanupConfirm && confirmMode != "none" {
		if submitter == nil {
			// 未注入持久化提交器，退化为立即删除并记录警告
			logger.S().Warnf("[removeExtraFiles] ConfirmMode=%s 但 submitter 未注入，退化为立即删除 %d 个文件 (taskID=%s)",
				confirmMode, len(toDelete), taskID)
		} else {
			batch := DeferredCleanupBatch{
				RequestID:       GenerateCleanupRequestID(),
				TaskID:          taskID,
				TargetPath:      targetPath,
				Paths:           toDelete,
				RemoveStrm:      cleanup.RemoveStrm,
				RemoveRelated:   cleanup.RemoveRelatedFiles,
				RemoveEmptyDirs: cleanup.RemoveEmptyDirs,
				ConfirmMode:     confirmMode,
				CreatedAt:       time.Now(),
			}
			reqID, serr := submitter.SubmitDeferredBatch(ctx, batch)
			if serr != nil {
				logger.S().Errorf("[removeExtraFiles] SubmitDeferredBatch failed (taskID=%s): %v，退化为立即删除",
					taskID, serr)
			} else {
				logger.S().Infof("[removeExtraFiles] 已入队待确认: %s (requestID=%s taskID=%s)",
					reason, reqID, taskID)
				return CleanupOutcome{Verdict: CleanupConfirm, RequestID: reqID, Reason: reason}, nil
			}
		}
	}

	// 立即删除模式
	var deleted int
	for _, p := range toDelete {
		logger.S().Infof("[removeExtraFiles] delete %s", p)
		if rerr := os.Remove(p); rerr != nil {
			logger.S().Warnf("[removeExtraFiles] remove %s failed: %v", p, rerr)
			continue
		}
		deleted++
	}
	return CleanupOutcome{Deleted: deleted, Verdict: verdict, Reason: reason}, nil
}

// normalizeExt 确保扩展名前有 "."
func normalizeExt(ext string) string {
	if ext == "" {
		return ext
	}
	if strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

// ==================== P0-1/P0-2 孤儿 STRM 对账清理 ====================
//
// 共享逻辑：扫描 targetPath 下所有 .strm 文件，提取内容中 pickcode 与
// cloudPickcodes 比对，孤儿 STRM（pickcode 不在云端集合中）按 mode 处理。
//   - P0-1 增量对账：增量同步结束后调用，cloudPickcodes 来自本次 fileEntries
//   - P0-2 全量预扫：全量任务开始前调用，cloudPickcodes 来自 DB 快照 + 本次 fileEntries
// 对齐参考项目 increment_sync_remove_unless_strm / full_sync_remove_unless_strm
//
// mode: "off"(跳过) / "mark_only"(仅日志) / "auto_clean"(软删 + 超阈值二次确认)
// 返回：(孤儿数, 延迟批次ID, error)。延迟批次ID 非空表示已入队待二次确认。

func cleanupOrphanStrms( //nolint:cyclop // complexity: 47（缺口 2 失败目录保护后）
	ctx context.Context,
	submitter CleanupBatchSubmitter,
	sseServer *sse.Server,
	taskID, targetPath string,
	cloudPickcodes map[string]struct{},
	failedDirPrefixes []string,
	mode string,
	cleanup model.CleanupSettings,
	embyRefresh StrmRefresher,
) (int, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "off" {
		return 0, "", nil
	}
	if len(cloudPickcodes) == 0 {
		// 云端集合为空时不做对账：避免在 fileEntries 还未填充阶段误删全部本地 STRM
		return 0, "", nil
	}

	// 第一遍扫描：收集孤儿 STRM 路径（同时统计本地 STRM 总数，供比例阈值判定）
	var orphans []string
	strmTotal := 0
	failedSkipped := 0
	werr := filepath.Walk(targetPath, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			return nil
		}
		// 跳过非 .strm 文件
		if !strings.EqualFold(filepath.Ext(p), ".strm") {
			return nil
		}
		// 跳过软删备份
		if strmutil.IsDeletedBak(p) {
			return nil
		}
		// 跳过隐藏文件
		if strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		strmTotal++
		// 失败目录保护（缺口 2）：位于列目录失败目录（及其子目录）下的 STRM，
		// 其云端内容未知（pickcode 集合不含它们），按孤儿删除有误判风险，
		// 本次对账跳过，等下次扫描成功后再对账。
		if len(failedDirPrefixes) > 0 {
			relDir := filepath.ToSlash(filepath.Dir(p))
			if rel, rerr := filepath.Rel(targetPath, relDir); rerr == nil && rel != "." {
				relSlash := filepath.ToSlash(rel)
				for _, pfx := range failedDirPrefixes {
					pfx = strings.Trim(pfx, "/")
					if pfx != "" && (relSlash == pfx || strings.HasPrefix(relSlash, pfx+"/")) {
						failedSkipped++
						logger.S().Debugf("[cleanupOrphanStrms] 跳过失败目录下的 STRM（本次不对账）: %s", p)
						return nil
					}
				}
			}
		}
		// 提取 pickcode
		pc, perr := strmutil.ExtractPickcode(p)
		if perr != nil {
			logger.S().Debugf("[cleanupOrphanStrms] 提取 pickcode 失败 %s: %v", p, perr)
			return nil
		}
		if pc == "" {
			// 非 faststrm 生成的 STRM，跳过（避免误删手动创建的 STRM）
			return nil
		}
		if _, exists := cloudPickcodes[pc]; !exists {
			orphans = append(orphans, p)
		}
		return nil
	})
	if werr != nil {
		return 0, "", werr
	}

	if len(orphans) == 0 {
		if failedSkipped > 0 && sseServer != nil {
			sseServer.EmitLog(taskID, "info", fmt.Sprintf(
				"对账清理：%d 个 STRM 位于列目录失败的目录下，本次跳过对账（防止误删）", failedSkipped))
		}
		return 0, "", nil
	}
	if failedSkipped > 0 && sseServer != nil {
		sseServer.EmitLog(taskID, "info", fmt.Sprintf(
			"对账清理：%d 个 STRM 位于列目录失败的目录下，本次跳过对账（防止误删）", failedSkipped))
	}

	// 三重保护判定（比例 + 绝对 + 目录数）
	confirmMode := cleanup.ConfirmMode
	if confirmMode == "" {
		confirmMode = "none"
	}
	verdict, reason := EvaluateCleanup(CleanupInput{
		Candidates: len(orphans),
		TotalLocal: strmTotal,
		Dirs:       countUniqueDirs(orphans),
		Cfg:        cleanup,
	})

	if sseServer != nil {
		sseServer.EmitLog(taskID, "info", fmt.Sprintf(
			"对账清理：检测到 %d 个孤儿 STRM（本地共 %d 个，mode=%s，confirm=%s）判定=%s %s",
			len(orphans), strmTotal, mode, confirmMode, verdict, reason))
	}

	// 保护态：只停删除，不停生成（原实现静默返回，用户无从判断为什么没清理）
	if verdict == CleanupProtected {
		msg := fmt.Sprintf("孤儿 STRM 清理进入保护态，本次不删除任何文件：%s (taskID=%s)", reason, taskID)
		logger.S().Warnf("[cleanupOrphanStrms] %s", msg)
		if sseServer != nil {
			sseServer.EmitLog(taskID, "warn", msg)
			sseServer.EmitLog(taskID, "info", "如需强制清理，请到 Web UI 清理页手动执行")
		}
		// 即使拒绝也返回孤儿数（供调用方统计）
		return len(orphans), "", nil
	}

	// mark_only 模式：仅日志
	if mode == "mark_only" {
		for _, p := range orphans {
			logger.S().Infof("[cleanupOrphanStrms] 孤儿 STRM (mark_only) %s", p)
			if sseServer != nil {
				sseServer.EmitLog(taskID, "info", "  孤儿STRM: "+p)
			}
		}
		return len(orphans), "", nil
	}

	// auto_clean 模式：需二次确认且 ConfirmMode != "none" → 入队
	if mode == "auto_clean" && verdict == CleanupConfirm && confirmMode != "none" && submitter != nil {
		batch := DeferredCleanupBatch{
			RequestID:       GenerateCleanupRequestID(),
			TaskID:          taskID,
			TargetPath:      targetPath,
			Paths:           orphans,
			RemoveStrm:      cleanup.RemoveStrm,
			RemoveRelated:   cleanup.RemoveRelatedFiles,
			RemoveEmptyDirs: cleanup.RemoveEmptyDirs,
			ConfirmMode:     confirmMode,
			CreatedAt:       time.Now(),
		}
		reqID, serr := submitter.SubmitDeferredBatch(ctx, batch)
		if serr != nil {
			logger.S().Errorf("[cleanupOrphanStrms] SubmitDeferredBatch failed (taskID=%s): %v，退化为立即软删",
				taskID, serr)
			if sseServer != nil {
				sseServer.EmitLog(taskID, "error", "对账入队失败: "+serr.Error())
			}
		} else {
			logger.S().Infof("[cleanupOrphanStrms] 已入队待确认: %s (requestID=%s taskID=%s)",
				reason, reqID, taskID)
			if sseServer != nil {
				sseServer.EmitLog(taskID, "warn", fmt.Sprintf(
					"对账清理：孤儿 %d 个超稳定阈值，已入队待二次确认 (requestID=%s)", len(orphans), reqID))
			}
			return len(orphans), reqID, nil
		}
	}

	// auto_clean 直接硬删（不再生成 .deleted.bak 备份）
	if mode == "auto_clean" {
		deleted := 0
		for _, p := range orphans {
			if derr := strmutil.DeleteStrmFile(p); derr != nil {
				logger.S().Warnf("[cleanupOrphanStrms] 硬删孤儿 STRM 失败 %s: %v", p, derr)
				if sseServer != nil {
					sseServer.EmitLog(taskID, "warn", fmt.Sprintf("硬删孤儿STRM失败 %s: %v", p, derr))
				}
				continue
			}
			deleted++
			logger.S().Infof("[cleanupOrphanStrms] 硬删孤儿 STRM %s", p)
		}
		if sseServer != nil {
			sseServer.EmitLog(taskID, "info", fmt.Sprintf("对账清理：已硬删 %d/%d 个孤儿 STRM", deleted, len(orphans)))
		}
		// Emby 刷库（删除后刷新整个目标目录，对齐 MoviePilot full_sync_media_server_refresh）
		if embyRefresh != nil && deleted > 0 {
			if rerr := embyRefresh.RefreshOnDelete(ctx, targetPath); rerr != nil {
				logger.S().Debugf("[cleanupOrphanStrms] Emby 刷库失败 path=%s: %v", targetPath, rerr)
			}
		}
		return deleted, "", nil
	}

	return 0, "", nil
}

// buildCloudPickcodeSet 从 fileEntries 构建云端 pickcode 集合
// fileEntries 是本次拉取的云端最新列表（含 kindStrm/kindDownload/kindSkip），
// 所有合法 pickcode 都应纳入云端集合，避免增量跳过的条目被误判为孤儿。
func buildCloudPickcodeSet(entries []*fileItem) map[string]struct{} {
	set := make(map[string]struct{}, len(entries))
	for _, f := range entries {
		if isValidPickcode(f.PickCode) {
			set[f.PickCode] = struct{}{}
		}
	}
	return set
}

// buildCloudPickcodeSetFromSnapshot 从 DB 快照构建云端 pickcode 集合
func buildCloudPickcodeSetFromSnapshot(snap map[string]db.SnapshotEntry) map[string]struct{} {
	set := make(map[string]struct{}, len(snap))
	for _, se := range snap {
		if isValidPickcode(se.PickCode) {
			set[se.PickCode] = struct{}{}
		}
	}
	return set
}

// mergePickcodeSets 将多个 pickcode 集合并集（用于全量预扫：DB 快照 + 本次 fileEntries）
func mergePickcodeSets(sets ...map[string]struct{}) map[string]struct{} {
	total := 0
	for _, s := range sets {
		total += len(s)
	}
	out := make(map[string]struct{}, total)
	for _, s := range sets {
		for k := range s {
			out[k] = struct{}{}
		}
	}
	return out
}
