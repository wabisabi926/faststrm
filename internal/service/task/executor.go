package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
)

// taskCompleteThreshold 任务完成判定阈值（99.995%）
const taskCompleteThreshold = 99.995

// defaultStrmWorkers STRM 写入默认并发数
const defaultStrmWorkers = 20

// ExecutorDeps 执行器依赖
type ExecutorDeps struct {
	Client115     *client115.Client
	AccountStore  AccountReader
	SettingsStore SettingsStore
	SQLiteDB      *sql.DB
	TasksStore    TasksReaderWriter
	StrmCache     StrmCacheWriter
	BaseURL       string // 用于拼接 strmPrefix（302模式下可留空）
	PublicBaseURL string // 公开可访问的 baseUrl（302 模式用户可配置）
}

// ExecuteTask 执行一个任务（同步执行，调用方应开 goroutine 异步调用）
// 仅执行前的抢占检查同步返回 ExecuteResult，任务本身通过 SSE 广播进度
func ExecuteTask(ctx context.Context, taskID string, deps ExecutorDeps) ExecuteResult {
	rt := GetRuntime()

	// 1) 找到任务定义
	tasks, err := deps.TasksStore.ReadTasks()
	if err != nil {
		return ExecuteResult{Success: false, Reason: "error", Message: err.Error()}
	}
	var task *Task
	for i := range tasks {
		if tasks[i].ID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return ExecuteResult{Success: false, Reason: "not_found", Message: "Task not found"}
	}

	// 2) 账号级独占锁
	ok, conflictID := rt.TryEnter(task.Account, task.ID)
	if !ok {
		return ExecuteResult{
			Success: false, Blocked: true, Reason: "account_running",
			Message: "Account already has an active task: " + conflictID,
		}
	}
	defer rt.Exit(task.Account)

	// 3) 注册为运行态（拿到 cancel 用于支持取消）
	runCtx, cancel := rt.Register(task)
	defer cancel()
	go func() {
		<-runCtx.Done()
	}()

	sseServer := sse.GetServer()
	taskStart := time.Now()

	// 4) 找 115 账号 cookie
	account := findAccount(deps.AccountStore, task.Account)
	if account == nil || account.AccountType != "115" {
		msg := "No valid 115 account: " + task.Account
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{
			TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds(),
		})
		return ExecuteResult{Success: false, Reason: "bad_account", Message: msg}
	}

	// 5) 合并全局 settings + 任务覆盖得到最终 strm 配置
	settings, err := deps.SettingsStore.ReadSettings()
	if err != nil {
		// best-effort: fall back to defaults if read fails
	}
	if settings == nil {
		settings = model.DefaultSettings()
	}
	resolved := resolveStrmSettings(task, settings, deps.BaseURL, deps.PublicBaseURL)

	// 6) 取 cid + exportDirParse → 拿到根 pickcode
	sseServer.EmitLog(task.ID, "info",
		fmt.Sprintf("Task starting: account=%s origin=%s target=%s", task.Account, task.OriginPath, task.TargetPath))

	cid, err := deps.Client115.FsDirGetID(ctx, task.OriginPath, account.Cookie)
	if err != nil {
		msg := "FsDirGetID failed: " + err.Error()
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		return ExecuteResult{Success: true, TaskID: task.ID, Message: "started but failed at dir scan"}
	}
	sseServer.EmitLog(task.ID, "info", fmt.Sprintf("Got cid=%d for path %s", cid, task.OriginPath))

	// 7) 导出目录解析（可能耗时），最多 5 分钟
	exportCtx, exportCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer exportCancel()

	rootPick, err := deps.Client115.ExportDirParse(exportCtx, itoa(cid), account.Cookie, 0)
	if err != nil {
		msg := "ExportDirParse failed: " + err.Error()
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		return ExecuteResult{Success: true, TaskID: task.ID}
	}
	sseServer.EmitLog(task.ID, "info", fmt.Sprintf("Export root pickcode=%s", rootPick))

	// 8) 构建目录树（从导出结果列目录得到文件列表）
	//    简化版：我们用 fs_files 多次分页遍历目标目录（cid 已知），获取文件条目
	fileEntries, err := listAllFilesRecursive(ctx, deps.Client115, account.Cookie, cid, task.OriginPath, resolved.StrmExtensions, resolved.DownloadExtensions)
	if err != nil {
		msg := "list files failed: " + err.Error()
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		return ExecuteResult{Success: true, TaskID: task.ID}
	}

	totalFiles := len(fileEntries)
	rt.SetState(task.ID, func(s *RuntimeState) { s.TotalFiles = totalFiles })
	sseServer.EmitLog(task.ID, "info", fmt.Sprintf("Total files: %d (strm=%d, download=%d)",
		totalFiles, countKind(fileEntries, kindStrm), countKind(fileEntries, kindDownload)))

	// 9) 写回 filePathDb（302 模式需要反查 pickcode）
	if resolved.Enable302 && deps.SQLiteDB != nil {
		entries := make([]db.FilePathEntry, 0, len(fileEntries))
		for _, f := range fileEntries {
			if f.PickCode == "" {
				continue
			}
			entries = append(entries, db.FilePathEntry{
				Path:       f.CloudPath,
				FileName:   f.Name,
				PickCode:   f.PickCode,
				UpdateTime: time.Now().Unix(),
			})
		}
		if len(entries) > 0 {
			if werr := db.UpsertFilePathEntryBatch(deps.SQLiteDB, task.Account, entries); werr != nil {
				sseServer.EmitLog(task.ID, "warn", "write filePathDb failed: "+werr.Error())
			} else {
				sseServer.EmitLog(task.ID, "info", fmt.Sprintf("wrote %d entries to filePathDb", len(entries)))
			}
		}
	}

	// 10) 下载/写 STRM：按文件类型拆分，并发执行
	if err := os.MkdirAll(task.TargetPath, 0o755); err != nil {
		msg := "mkdir target failed: " + err.Error()
		sseServer.EmitLog(task.ID, "error", msg)
		rt.SetState(task.ID, func(s *RuntimeState) { s.Status = StatusFailed; s.Error = msg; s.EndedAt = time.Now().UnixMilli() })
		sseServer.EmitComplete(sse.CompletePayload{TaskID: task.ID, Status: string(StatusFailed), Error: msg, DurationMs: time.Since(taskStart).Milliseconds()})
		return ExecuteResult{Success: true, TaskID: task.ID}
	}

	perFilePct := newPerFilePercent(totalFiles)
	downloaded := new(int64)

	// ---- 写 STRM 文件（并发数可通过 settings 配置） ----
	strmWorkers := defaultStrmWorkers
	if settings.Download.StrmMaxConcurrent != nil && *settings.Download.StrmMaxConcurrent > 0 {
		strmWorkers = *settings.Download.StrmMaxConcurrent
		if strmWorkers <= 0 {
			strmWorkers = defaultStrmWorkers
		}
	}
	strmFiles := filterKind(fileEntries, kindStrm)
	cacheEntryUUID := uuid.New().String()
	cacheRelPaths := make([]string, 0, len(strmFiles))
	cacheLocalPaths := make([]string, 0, len(strmFiles))
	var cacheMu sync.Mutex
	if len(strmFiles) > 0 {
		sseServer.EmitLog(task.ID, "info", fmt.Sprintf("writing %d strm files (concurrency=%d)", len(strmFiles), strmWorkers))
		wg := sync.WaitGroup{}
		sem := make(chan struct{}, strmWorkers)
		for _, f := range strmFiles {
			wg.Add(1)
			sem <- struct{}{}
			go func(f *fileItem) {
				defer wg.Done()
				defer func() { <-sem }()
				savePath := filepath.Join(task.TargetPath, f.RelPath+".strm")
				content, cerr := buildStrmContent(task, f, resolved)
				if cerr != nil {
					sseServer.EmitLog(task.ID, "error", fmt.Sprintf("build strm %s: %v", f.RelPath, cerr))
					return
				}
				if cerr = ensureDir(filepath.Dir(savePath)); cerr != nil {
					sseServer.EmitLog(task.ID, "error", fmt.Sprintf("mkdir %s: %v", filepath.Dir(savePath), cerr))
					return
				}
				if cerr = os.WriteFile(savePath, []byte(content), 0o644); cerr != nil {
					sseServer.EmitLog(task.ID, "error", fmt.Sprintf("write %s: %v", savePath, cerr))
					return
				}
				cacheMu.Lock()
				cacheRelPaths = append(cacheRelPaths, f.RelPath+".strm")
				cacheLocalPaths = append(cacheLocalPaths, savePath)
				cacheMu.Unlock()
				perFilePct.Mark(f.CloudPath, 100)
				atomic.AddInt64(downloaded, 1)
				done, overall := perFilePct.Overall(totalFiles)
				rt.AppendLog(task.ID, jsonLine(sse.ProgressPayload{
					TaskID: task.ID, FilePath: f.RelPath, Percent: 100, OverallPercent: overall,
				}))
				sseServer.EmitProgress(sse.ProgressPayload{
					TaskID: task.ID, FilePath: f.RelPath, Percent: 100, OverallPercent: overall, Done: done,
				})
			}(f)
		}
		wg.Wait()
	}

	// ---- 真实下载文件（设置了 downloadExtensions 且启用自动下载才做） ----
	if !settings.Download.AutoDownloadMetadata {
		sseServer.EmitLog(task.ID, "info", "自动下载元数据已关闭，跳过 nfo/jpg/png 等文件下载")
	} else {
		dlFiles := filterKind(fileEntries, kindDownload)
		if len(dlFiles) > 0 {
			dlWorkers := 10
			if settings.Download.DownloadMaxConcurrent != nil && *settings.Download.DownloadMaxConcurrent > 0 {
				dlWorkers = *settings.Download.DownloadMaxConcurrent
				if dlWorkers <= 0 {
					dlWorkers = 10
				}
			}
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("downloading %d files (concurrency=%d)", len(dlFiles), dlWorkers))
			runDownloads(runCtx, task.ID, dlFiles, task.TargetPath, task.Account, account.Cookie, deps.Client115,
				dlWorkers, perFilePct, downloaded, totalFiles, rt, sseServer)
		}
	}

	// ---- 11) removeExtraFiles：清理本地多余文件 ----
	if task.RemoveExtraFiles {
		sseServer.EmitLog(task.ID, "info", "scanning for extra files to remove")
		deleted, derr := removeExtraFiles(task.TargetPath, fileEntries, settings)
		if derr != nil {
			sseServer.EmitLog(task.ID, "warn", "removeExtraFiles err: "+derr.Error())
		} else {
			rt.SetState(task.ID, func(s *RuntimeState) { s.DeletedFiles = deleted })
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("removed %d extra files", deleted))
		}
	}

	// 11.5) 保存 STRM 生成缓存（供清理使用）
	if deps.StrmCache != nil && len(cacheRelPaths) > 0 {
		cacheMu.Lock()
		relSnapshot := append([]string(nil), cacheRelPaths...)
		localSnapshot := append([]string(nil), cacheLocalPaths...)
		cacheMu.Unlock()
		sort.Strings(relSnapshot)
		sort.Strings(localSnapshot)
		if err := deps.StrmCache.Save(StrmCacheEntryLike{
			UUID:       cacheEntryUUID,
			TaskID:     task.ID,
			Target:     task.TargetPath,
			Account:    task.Account,
			RelPaths:   relSnapshot,
			LocalPaths: localSnapshot,
		}); err != nil {
			sseServer.EmitLog(task.ID, "warn", "save strm cache failed: "+err.Error())
		} else {
			sseServer.EmitLog(task.ID, "info", fmt.Sprintf("saved strm cache uuid=%s entries=%d", cacheEntryUUID, len(relSnapshot)))
		}
	}

	// 12) 完成
	rt.SetState(task.ID, func(s *RuntimeState) {
		s.Status = StatusCompleted
		s.DownloadedFiles = int(atomic.LoadInt64(downloaded))
		s.EndedAt = time.Now().UnixMilli()
	})
	dl := atomic.LoadInt64(downloaded)
	_, overall := perFilePct.Overall(totalFiles)
	sseServer.EmitProgress(sse.ProgressPayload{
		TaskID: task.ID, Done: true, OverallPercent: overall,
	})
	sseServer.EmitComplete(sse.CompletePayload{
		TaskID: task.ID, Status: string(StatusCompleted),
		TotalFiles: totalFiles, DownloadedFiles: int(dl), DurationMs: time.Since(taskStart).Milliseconds(),
	})
	sseServer.EmitLog(task.ID, "info",
		fmt.Sprintf("Task done in %d ms: %d/%d files", time.Since(taskStart).Milliseconds(), dl, totalFiles))

	return ExecuteResult{Success: true, TaskID: task.ID}
}

// ==================== 内部：辅助 ====================

type fileKind int

const (
	kindStrm fileKind = 1
	kindDownload fileKind = 2
	kindSkip fileKind = 3
)

type fileItem struct {
	CloudPath string   // 绝对云端路径：originPath + "/" + rel
	RelPath   string   // 相对路径（不含 originPath 前缀）
	Name      string
	PickCode  string
	Size      int64
	Ext       string
	Kind      fileKind
}

func countKind(items []*fileItem, k fileKind) int {
	n := 0
	for _, it := range items {
		if it.Kind == k {
			n++
		}
	}
	return n
}
func filterKind(items []*fileItem, k fileKind) []*fileItem {
	out := make([]*fileItem, 0, len(items))
	for _, it := range items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

func findAccount(s AccountReader, name string) *model.AccountInfo {
	return s.Get(name)
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func jsonLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// perFilePercent 每文件进度追踪（简单版：STRM 完成即 100，下载会渐进更新）
type perFilePercent struct {
	mu sync.Mutex
	m  map[string]int
}

func newPerFilePercent(total int) *perFilePercent {
	return &perFilePercent{m: make(map[string]int, total)}
}

func (p *perFilePercent) Mark(key string, pct int) {
	p.mu.Lock()
	p.m[key] = pct
	p.mu.Unlock()
}

func (p *perFilePercent) Update(key string, pct int) {
	p.mu.Lock()
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	p.m[key] = pct
	p.mu.Unlock()
}

// Overall 返回 (是否全部100, overallPercent 字符串 "xx.xx")
func (p *perFilePercent) Overall(total int) (bool, string) {
	p.mu.Lock()
	sum := 0
	cnt := 0
	for _, v := range p.m {
		sum += v
		cnt++
	}
	p.mu.Unlock()
	if total <= 0 {
		return true, "100.00"
	}
	// 未标记的文件按 0 算
	sum += (total - cnt) * 0
	overall := float64(sum) / float64(total)
	done := overall >= taskCompleteThreshold
	return done, fmt.Sprintf("%.2f", overall)
}
