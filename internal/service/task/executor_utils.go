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
	"sync"
	"sync/atomic"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/pkg/logger"
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
	// 拼接 account
	prefix = strings.TrimRight(prefix, "/")
	if !strings.Contains(prefix, "account=") {
		sep := "?"
		if strings.Contains(prefix, "?") {
			sep = "&"
		}
		prefix = fmt.Sprintf("%s%saccount=%s", prefix, sep, task.Account)
	}
	r.StrmPrefix = prefix

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

// buildStrmContent 生成 .strm 文件内容
// 302 模式：{{StrmPrefix}}/api/fs/get?pickcode=xxx&file_name=URLencode(name)&enable302=1
// 否则：   {{StrmPrefix}}/api/strm?pickcode=xxx&file_name=URLencode(name)
func buildStrmContent(task *Task, f *fileItem, r resolvedStrm) (string, error) {
	var u string
	prefix := strings.TrimRight(r.StrmPrefix, "/")
	if r.Enable302 {
		u = fmt.Sprintf("%s/api/fs/get?account=%s&pickcode=%s", prefix, task.Account, f.PickCode)
		if f.Name != "" {
			u += "&file_name=" + urlPathEncode(f.Name)
		}
	} else {
		u = fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s", prefix, task.Account, f.PickCode)
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
func listAllFilesRecursive(
	ctx context.Context,
	c115 *client115.Client,
	cookie string,
	rootCID int64,
	originPath string,
	strmExts map[string]struct{},
	downloadExts map[string]struct{},
) ([]*fileItem, error) {
	type stackEntry struct {
		cid     int64
		relPath string // 相对 originPath 的前缀路径（为空表示在 originPath 下）
	}
	var out []*fileItem
	stk := []stackEntry{{cid: rootCID, relPath: ""}}

	cidSeen := make(map[int64]struct{})
	for len(stk) > 0 {
		top := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if _, seen := cidSeen[top.cid]; seen {
			continue
		}
		cidSeen[top.cid] = struct{}{}

		var offset int
		for {
			resp, err := c115.FsFiles(ctx, strconv.FormatInt(top.cid, 10), 1000, offset, cookie)
			if err != nil {
				return nil, err
			}
			if !resp.State {
				return nil, fmt.Errorf("fs_files state=false cid=%d", top.cid)
			}
			if len(resp.Data) == 0 {
				break
			}
			for _, e := range resp.Data {
				relName := e.Name
				if top.relPath != "" {
					relName = top.relPath + "/" + e.Name
				}
				// isDir 判断：fc > 0 或者 pickcode 为空 + size 为 0
				fc, _ := strconv.ParseInt(fmt.Sprintf("%v", e.FC), 10, 64)
				isDir := fc > 0 || (e.PickCode == "" && e.Size == 0)
				if isDir {
					cid, _ := strconv.ParseInt(fmt.Sprintf("%v", e.CID), 10, 64)
					if cid > 0 {
						stk = append(stk, stackEntry{cid: cid, relPath: relName})
					}
					continue
				}
				ext := strings.ToLower(filepath.Ext(e.Name))
				kind := kindSkip
				if _, ok := strmExts[ext]; ok {
					kind = kindStrm
				} else if _, ok := downloadExts[ext]; ok {
					kind = kindDownload
				}
				if kind == kindSkip {
					continue
				}
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
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(f *fileItem) {
			defer wg.Done()
			defer func() { <-sem }()
			select {
			case <-ctx.Done():
				return
			default:
			}
			savePath := filepath.Join(saveDir, f.RelPath)
			if err := ensureDir(filepath.Dir(savePath)); err != nil {
				sseServer.EmitLog(taskID, "error", "mkdir "+filepath.Dir(savePath)+": "+err.Error())
				return
			}
			meta, err := c115.GetDownloadUrlWebFull(ctx, f.PickCode, cookie)
			if err != nil {
				sseServer.EmitLog(taskID, "error", fmt.Sprintf("resolve dl url %s: %v", f.RelPath, err))
				return
			}
			if err := downloadFileWithProgress(ctx, meta.URL, savePath, taskID, f, cookie, c115.UserAgent,
				perFile, rt, sseServer, downloaded, total); err != nil {
				sseServer.EmitLog(taskID, "error", fmt.Sprintf("download %s: %v", f.RelPath, err))
			}
		}(f)
	}
	wg.Wait()
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
// 对齐 TS removeExtraFiles
func removeExtraFiles(targetPath string, entries []*fileItem, s *model.Settings) (int, error) {
	keep := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		// 保留 strm 文件
		sp := filepath.Join(targetPath, e.RelPath+".strm")
		keep[filepath.Clean(sp)] = struct{}{}
		// 保留真实下载文件
		dp := filepath.Join(targetPath, e.RelPath)
		keep[filepath.Clean(dp)] = struct{}{}
	}
	var deleted int
	err := filepath.Walk(targetPath, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			// 空目录最后处理
			return nil
		}
		clean := filepath.Clean(p)
		// 跳过隐藏文件
		if strings.HasPrefix(filepath.Base(clean), ".") {
			return nil
		}
		if _, ok := keep[clean]; ok {
			return nil
		}
		logger.S().Infof("[removeExtraFiles] delete %s", clean)
		if rerr := os.Remove(clean); rerr != nil {
			logger.S().Warnf("[removeExtraFiles] remove %s failed: %v", clean, rerr)
			return nil
		}
		deleted++
		return nil
	})
	return deleted, err
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
