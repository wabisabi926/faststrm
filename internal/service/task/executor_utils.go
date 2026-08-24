package task

import (
	"context"
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
	"github.com/wabisabi926/faststrm/pkg/concurrency"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// resolvedStrm 合并 settings + 任务覆盖后的最终 STRM 配置
type resolvedStrm struct {
	StrmPrefix         string
	EnablePathEncoding bool
	Enable302          bool
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

	// strmPrefix：任务覆盖 > 全局 > 默认（publicBaseURL 或 baseURL）
	prefix := s.StrmPrefix
	if task.StrmPrefix != "" {
		prefix = task.StrmPrefix
	}
	if prefix == "" {
		if publicBaseURL != "" {
			prefix = publicBaseURL
		} else if baseURL != "" {
			prefix = baseURL
		} else {
			prefix = "http://127.0.0.1:8090"
		}
	}
	r.StrmPrefix = strings.TrimRight(prefix, "/")

	// enablePathEncoding
	r.EnablePathEncoding = s.EnablePathEncoding
	if task.EnablePathEncoding {
		r.EnablePathEncoding = true
	}

	// enable302
	r.Enable302 = s.Enable302
	if task.Enable302 {
		r.Enable302 = true
	}

	return r
}

// isValidPickcode 校验 115 pickcode 合法性
// 对齐 MoviePilot：17 位字母数字混合字符串
func isValidPickcode(pc string) bool {
	if len(pc) != 17 {
		return false
	}
	for i := 0; i < len(pc); i++ {
		c := pc[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// shouldGenerateStrm 统一判断文件是否应该生成 STRM
// 对齐 MoviePilot StrmGenerater.should_generate_strm：
//   1. 黑名单关键词检查（不区分大小写子串匹配，若任一条目匹配则拒绝）
//   2. 最小文件大小检查（minFileSize>0 且 fileSize>0 且 fileSize<minFileSize 则拒绝）
// P2-2：matcher 非空时走 AC 自动机；否则阈值≥8 临时构建；<8 时 contains 线性扫常数项更低
// 返回：(拒绝原因, 是否通过)；通过时拒绝原因为空
func shouldGenerateStrm(fileName string, fileSize, minFileSize int64, blacklist []string, matcher ...*concurrency.StringMatcher) (string, bool) {
	// 1) 黑名单检查：不区分大小写子串匹配
	// 对齐 MoviePilot not_blacklist_key / not_blacklist_key_automaton
	if len(blacklist) > 0 {
		var m *concurrency.StringMatcher
		if len(matcher) > 0 && matcher[0] != nil {
			m = matcher[0]
		} else if concurrency.ShouldUseAC(blacklist) {
			m = concurrency.NewStringMatcher(blacklist)
		}
		if m != nil {
			if kw, ok := m.MatchAny(fileName); ok {
				return fmt.Sprintf("匹配黑名单关键词 %q", kw), false
			}
		} else {
			lowerName := strings.ToLower(fileName)
			for _, kw := range blacklist {
				if kw == "" {
					continue
				}
				if strings.Contains(lowerName, strings.ToLower(kw)) {
					return fmt.Sprintf("匹配黑名单关键词 %q", kw), false
				}
			}
		}
	}
	// 2) 最小文件大小限制
	// 对齐 MoviePilot not_min_limit：fileSize=0(未知)时默认通过
	if minFileSize > 0 && fileSize > 0 && fileSize < minFileSize {
		return "小于最小文件大小", false
	}
	return "", true
}

// getStrmFileName 将文件名转换为 .strm 扩展名
// 对齐 MoviePilot StrmGenerater.get_strm_filename:
//   - 普通文件：movie.mkv → movie.strm
//   - ISO 镜像保留双扩展名：game.iso → game.iso.strm
func getStrmFileName(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return fileName + ".strm"
	}
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if ext == ".iso" {
		return stem + ".iso.strm"
	}
	return stem + ".strm"
}

// getRelPathStrmSuffix 基于相对路径（含扩展名）生成对应的 .strm 相对路径
// 等价于替换文件名为 getStrmFileName(name)
func replaceRelPathExtToStrm(relPath string) string {
	dir, name := filepath.Split(relPath)
	return filepath.Join(dir, getStrmFileName(name))
}

// writeStrmFile 原子写入 STRM 文件（先写 tmp 再 rename）
func writeStrmFile(strmPath, content string) error {
	tmpPath := strmPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, strmPath)
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
	if r.Enable302 {
		u = fmt.Sprintf("%s/api/fs/get?account=%s&pickcode=%s", prefix, urlPathEncode(task.Account), f.PickCode)
		if f.Name != "" {
			u += "&file_name=" + urlPathEncode(f.Name)
		}
	} else {
		u = fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s", prefix, urlPathEncode(task.Account), f.PickCode)
		if f.Name != "" {
			u += "&file_name=" + urlPathEncode(f.Name)
		}
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

// listAllFilesRecursive 递归遍历目录，返回所有需要处理的文件（带 Kind 分类）
// minFileSize：仅对 STRM 文件生效；0 表示不限制。
// blacklist：STRM 文件名黑名单关键词列表（空 = 不启用）。
// 对齐 MoviePilot StrmGenerater.should_generate_strm + not_blacklist_key。
// pickcode 校验：STRM 文件必须是 17 位字母数字的有效 pickcode
// 新增：taskID/rt/sseServer 用于扫描阶段的进度心跳广播（大库场景下避免 UI 显示 0% 卡死）
func listAllFilesRecursive(
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
) ([]*fileItem, error) {
	type stackEntry struct {
		cid     int64
		relPath string // 相对 originPath 的前缀路径（为空表示在 originPath 下）
	}
	var out []*fileItem
	stk := []stackEntry{{cid: rootCID, relPath: ""}}

	// P2-2：黑名单条目数 ≥ 阈值时构建一次 AC 自动机复用，避免 per-file 重构建
	var blMatcher *concurrency.StringMatcher
	if concurrency.ShouldUseAC(blacklist) {
		blMatcher = concurrency.NewStringMatcher(blacklist)
	}

	cidSeen := make(map[int64]struct{})
	dirCount, fileCount, matchCount := 0, 0, 0
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

	defer func() {
		logger.S().Infof("[listAllFilesRecursive] done dirs=%d files=%d matched=%d totalOut=%d", dirCount, fileCount, matchCount, len(out))
	}()
	for len(stk) > 0 {
		top := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if _, seen := cidSeen[top.cid]; seen {
			continue
		}
		cidSeen[top.cid] = struct{}{}

		var offset int
		pageMatched := 0
		for page := 0; ; page++ {
			resp, err := c115.FsFiles(ctx, strconv.FormatInt(top.cid, 10), 1000, offset, cookie)
			if err != nil {
				logger.S().Errorf("[listAllFilesRecursive] cid=%d relPath=%s FsFiles err: %v", top.cid, top.relPath, err)
				return nil, err
			}
			if !resp.State {
				logger.S().Errorf("[listAllFilesRecursive] cid=%d relPath=%s state=false", top.cid, top.relPath)
				return nil, fmt.Errorf("fs_files state=false cid=%d", top.cid)
			}
			if len(resp.Data) == 0 {
				break
			}
			logger.S().Infof("[listAllFilesRecursive] cid=%d relPath=%q page=%d offset=%d returned=%d", top.cid, top.relPath, page, offset, len(resp.Data))
			for _, e := range resp.Data {
				logger.S().Infof("[listAllFilesRecursive] entry name=%q cid=%v fid=%v fc=%v size=%d pickcode=%q isDir=%v", e.Name, e.CID, e.FID, e.FC, e.Size, e.PickCode, e.IsDir)
				relName := e.Name
				if top.relPath != "" {
					relName = top.relPath + "/" + e.Name
				}
				// isDir 判断：FsFiles 已经基于 cid/fid 做了可靠判定，这里直接复用，不再用 fc 覆盖。
				isDir := e.IsDir
				if isDir {
					cid, _ := strconv.ParseInt(fmt.Sprintf("%v", e.CID), 10, 64)
					if cid > 0 {
						stk = append(stk, stackEntry{cid: cid, relPath: relName})
						dirCount++
					}
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
				cloudPath := originPath
				if relName != "" {
					cloudPath = strings.TrimRight(originPath, "/") + "/" + relName
				}
				out = append(out, &fileItem{
					CloudPath: cloudPath,
					RelPath:   relName,
					Name:      e.Name,
					PickCode:  e.PickCode,
					Size:      e.Size,
					Ext:       ext,
					Kind:      kind,
				})
			}
			// 翻页
			offset += len(resp.Data)
			if len(resp.Data) < 1000 {
				break
			}
		}
		logger.S().Infof("[listAllFilesRecursive] cid=%d relPath=%q pageMatched=%d", top.cid, top.relPath, pageMatched)
	}
	return out, nil
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
			savePath := filepath.Join(saveDir, f.RelPath)
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
func removeExtraFiles(ctx context.Context, submitter CleanupBatchSubmitter, taskID, targetPath string, entries []*fileItem, s *model.Settings) (int, string, error) {
	keep := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		// 保留 strm 文件（.iso 文件需保留双扩展名 game.iso.strm）
		sp := filepath.Join(targetPath, replaceRelPathExtToStrm(e.RelPath))
		keep[filepath.Clean(sp)] = struct{}{}
		// 保留真实下载文件
		dp := filepath.Join(targetPath, e.RelPath)
		keep[filepath.Clean(dp)] = struct{}{}
	}

	// 第一遍扫描：收集待删文件路径
	var toDelete []string
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
		if _, ok := keep[clean]; ok {
			return nil
		}
		toDelete = append(toDelete, clean)
		return nil
	})
	if err != nil {
		return 0, "", err
	}

	if len(toDelete) == 0 {
		return 0, "", nil
	}

	// 阈值检查（对齐 MoviePilot full_sync_remove_unless_max_threshold / stable_threshold）
	cleanup := s.Cleanup
	maxThreshold := cleanup.MaxThreshold
	if maxThreshold <= 0 {
		maxThreshold = 10 // 默认安全上限
	}
	stableThreshold := cleanup.StableThreshold
	if stableThreshold <= 0 {
		stableThreshold = 5 // 默认稳定阈值
	}
	confirmMode := cleanup.ConfirmMode
	if confirmMode == "" {
		confirmMode = "none"
	}

	if len(toDelete) > maxThreshold {
		logger.S().Warnf("[removeExtraFiles] 待删 %d 个文件超过安全阈值 %d，拒绝执行 (taskID=%s)",
			len(toDelete), maxThreshold, taskID)
		return 0, "", nil
	}

	// 延迟确认模式：超过 stableThreshold 且 ConfirmMode != "none" 且 submitter 已注入
	if len(toDelete) > stableThreshold && confirmMode != "none" {
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
				logger.S().Infof("[removeExtraFiles] 待删 %d 个文件超过稳定阈值 %d，ConfirmMode=%s，已延迟 (requestID=%s taskID=%s)",
					len(toDelete), stableThreshold, confirmMode, reqID, taskID)
				return 0, reqID, nil
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
	return deleted, "", nil
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

// sanitizeCloudRelPath 清理云端相对路径（/ 分隔）中的 Windows 非法字符，
// 等价于 monitor.sanitizePathParts，避免 task 循环引用 monitor。
func sanitizeCloudRelPath(rel string) string {
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		part = strings.ReplaceAll(part, ":", "：")
		for _, c := range []string{"<", ">", "\"", "|", "?", "*"} {
			part = strings.ReplaceAll(part, c, "_")
		}
		parts[i] = part
	}
	return strings.Join(parts, string(filepath.Separator))
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

func cleanupOrphanStrms(
	ctx context.Context,
	submitter CleanupBatchSubmitter,
	sseServer *sse.Server,
	taskID, targetPath string,
	cloudPickcodes map[string]struct{},
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

	// 第一遍扫描：收集孤儿 STRM 路径
	var orphans []string
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
		return 0, "", nil
	}

	// 阈值检查（对齐 MoviePilot full_sync_remove_unless_max_threshold / stable_threshold）
	maxThreshold := cleanup.MaxThreshold
	if maxThreshold <= 0 {
		maxThreshold = 10
	}
	stableThreshold := cleanup.StableThreshold
	if stableThreshold <= 0 {
		stableThreshold = 5
	}
	confirmMode := cleanup.ConfirmMode
	if confirmMode == "" {
		confirmMode = "none"
	}

	if sseServer != nil {
		sseServer.EmitLog(taskID, "info", fmt.Sprintf(
			"对账清理：检测到 %d 个孤儿 STRM (mode=%s, max=%d, stable=%d, confirm=%s)",
			len(orphans), mode, maxThreshold, stableThreshold, confirmMode))
	}

	// 超过最大阈值：拒绝执行（防误删大批量）
	if len(orphans) > maxThreshold {
		msg := fmt.Sprintf("孤儿 STRM 数 %d 超过安全阈值 %d，拒绝执行清理 (taskID=%s)",
			len(orphans), maxThreshold, taskID)
		logger.S().Warnf("[cleanupOrphanStrms] %s", msg)
		if sseServer != nil {
			sseServer.EmitLog(taskID, "warn", msg)
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

	// auto_clean 模式：超稳定阈值且 ConfirmMode != "none" → 入队二次确认
	if mode == "auto_clean" && len(orphans) > stableThreshold && confirmMode != "none" && submitter != nil {
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
			logger.S().Infof("[cleanupOrphanStrms] 孤儿 %d 个超稳定阈值 %d，ConfirmMode=%s，已延迟 (requestID=%s taskID=%s)",
				len(orphans), stableThreshold, confirmMode, reqID, taskID)
			if sseServer != nil {
				sseServer.EmitLog(taskID, "warn", fmt.Sprintf(
					"对账清理：孤儿 %d 个超稳定阈值，已入队待二次确认 (requestID=%s)", len(orphans), reqID))
			}
			return len(orphans), reqID, nil
		}
	}

	// auto_clean 直接软删
	if mode == "auto_clean" {
		hardDelete := false // 默认软删，避免误删
		deleted := 0
		for _, p := range orphans {
			if derr := strmutil.SafeDeleteStrmFile(p, hardDelete); derr != nil {
				logger.S().Warnf("[cleanupOrphanStrms] 软删孤儿 STRM 失败 %s: %v", p, derr)
				if sseServer != nil {
					sseServer.EmitLog(taskID, "warn", fmt.Sprintf("软删孤儿STRM失败 %s: %v", p, derr))
				}
				continue
			}
			deleted++
			logger.S().Infof("[cleanupOrphanStrms] 软删孤儿 STRM %s", p)
		}
		if sseServer != nil {
			sseServer.EmitLog(taskID, "info", fmt.Sprintf("对账清理：已软删 %d/%d 个孤儿 STRM", deleted, len(orphans)))
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
