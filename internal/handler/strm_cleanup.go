package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

type StrmCleanupDeps struct {
	SettingsStore *store.SettingsStore
	AccountStore  *store.AccountStore
	ClientFactory func(name string) (*client115.Client, error)
	TasksStore    *store.TasksStore
	StrmCache     *store.StrmCacheStore
	// Interaction 待删队列与 TG 按钮二次确认交互器
	// 为 nil 时表示未启用待删队列（仅做 MaxThreshold 硬拒绝）
	Interaction *StrmCleanupInteraction
	// SQLiteDB 用于全量对账查询 dbRecordCount（nil 时该字段恒为 0）
	SQLiteDB *sql.DB
}

type MappingScanRequest struct {
	Account   string `json:"account"`
	CloudPath string `json:"cloudPath"`
	LocalPath string `json:"localPath"`
	UseCache  bool   `json:"useCache,omitempty"`
	CacheUUID string `json:"cacheUuid,omitempty"`
	// CacheTTLMs P3：前端自定义缓存 TTL（毫秒）。0 / 未设置时保持原先 1 小时。
	CacheTTLMs int64 `json:"cacheTTLMs,omitempty"`
}

// StrmPreviewRequest P3：POST /api/strmCleanup/preview
type StrmPreviewRequest struct {
	LocalPath string `json:"localPath"`
	RelPath   string `json:"relPath"`
	// MaxBytes 可选，默认 8192。传负数或 0 并在前端点"查看完整"时，读全量（最大 8 KB cap 保护）
	MaxBytes int `json:"maxBytes,omitempty"`
}

// StrmPreviewResponse P3：预览响应
type StrmPreviewResponse struct {
	Exists    bool   `json:"exists"`
	Size      int64  `json:"size,omitempty"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

type StaleStrm struct {
	RelPath string `json:"relPath"`
	Size    int64  `json:"size"`
	// Content P3：预览时展示 STRM 文件的前 512 字节。扫描阶段预填；空时前端可调 /preview 查完整
	Content string `json:"content,omitempty"`
	// Truncated P3：true 表示文件 > 512 字节，前端可提示"查看完整"
	Truncated bool `json:"truncated,omitempty"`
}

type MissingStrm struct {
	RelPath  string `json:"relPath"`
	PickCode string `json:"pickCode"`
	Size     int64  `json:"size"`
}

type MappingScanResult struct {
	Account         string `json:"account"`
	CloudPath       string `json:"cloudPath"`
	LocalPath       string `json:"localPath"`
	RemoteFileCount int    `json:"remoteFileCount"`
	LocalStrmCount  int    `json:"localStrmCount"`
	// AssociatedFileCount 本地关联文件数（.nfo/.jpg/.png/.srt/.sub/.ass/.vtt），与 STRM 分开统计（P2 语义纯净）
	AssociatedFileCount int           `json:"associatedFileCount,omitempty"`
	StaleStrms          []StaleStrm   `json:"staleStrms"`
	MissingStrms        []MissingStrm `json:"missingStrms"`
	Error               string        `json:"error,omitempty"`
	// DbRecordCount 该路径映射下 SQLite files 表真实记录数（=云路径前缀精确匹配）
	// 未开启 sqlite 时为 0（前端可通过是否提供过 reconcile 结果判断语义）
	DbRecordCount int `json:"dbRecordCount,omitempty"`
}

type ScanResponse struct {
	Mappings        []MappingScanResult  `json:"mappings"`
	MappingsScanned []MappingScanRequest `json:"mappingsScanned,omitempty"`
	DurationMs      int64                `json:"durationMs"`
	// TotalRemoteFiles / TotalLocalStrms / TotalStale / TotalMissing 后端聚合好的全量统计
	// 前端直接使用，避免 reduce + ?? 0 兜底导致的数字漂移或漏报
	TotalRemoteFiles int `json:"totalRemoteFiles"`
	TotalLocalStrms  int `json:"totalLocalStrms"`
	TotalStale       int `json:"totalStale"`
	TotalMissing     int `json:"totalMissing"`
	// TotalAssociatedFiles 全部 mappings 关联文件（.nfo/.jpg/.srt 等）计数之和（P2 与 STRM 分离统计）
	TotalAssociatedFiles int `json:"totalAssociatedFiles,omitempty"`
	// TotalDbRecords 全部 mappings 的 DbRecordCount 之和（未开 sqlite 时恒为 0，前端可据此决定是否展示 DB 卡片）
	TotalDbRecords int `json:"totalDbRecords,omitempty"`
}

type ExecuteEntry struct {
	LocalPath     string   `json:"localPath"`
	StaleRelPaths []string `json:"staleRelPaths"`
}

type ExecuteMissingItem struct {
	LocalPath string `json:"localPath"`
	RelPath   string `json:"relPath"`
	MappingID string `json:"mappingId"`
}

type ExecuteRequest struct {
	Entries      []ExecuteEntry       `json:"entries"`
	DryRun       bool                 `json:"dryRun"`
	Action       string               `json:"action"`
	MissingItems []ExecuteMissingItem `json:"missingItems"`
	ScanSummary  *ScanResponse        `json:"scanSummary,omitempty"`
}

// MappingLocalStats P2：execute 结束后对每个 mapping 的本地 Walk 结果，供前端覆盖增量估算
type MappingLocalStats struct {
	LocalPath           string `json:"localPath"`
	LocalStrmCount      int    `json:"localStrmCount"`
	AssociatedFileCount int    `json:"associatedFileCount,omitempty"`
}

type ExecuteResponse struct {
	DeletedCount     int                 `json:"deletedCount"`
	FailedCount      int                 `json:"failedCount"`
	Errors           []map[string]string `json:"errors"`
	RemovedEmptyDirs []string            `json:"removedEmptyDirs"`
	// RemovedRelatedFiles 删除 STRM 时一并清理的关联媒体信息文件（.nfo/.jpg/.srt 等）
	RemovedRelatedFiles []string `json:"removedRelatedFiles,omitempty"`
	DryRun              bool     `json:"dryRun"`
	DurationMs          int64    `json:"durationMs"`
	RegeneratedCount    int      `json:"regeneratedCount,omitempty"`
	// RegeneratedPaths 已成功生成的 STRM 相对路径列表（前端用于从 scanResult.missingStrms 中移除，避免重复点击）
	RegeneratedPaths []string `json:"regeneratedPaths,omitempty"`
	// DeletedAllCount delete_all / delete_and_regenerate 模式下从 ScanSummary 删除的失效 STRM 数
	DeletedAllCount int `json:"deletedAllCount,omitempty"`
	// CleanupSummary delete_and_regenerate 组合操作的汇总（前端 cleanupSummary 字段对应）
	CleanupSummary *CleanupSummary `json:"cleanupSummary,omitempty"`
	// RefreshedMappingStats P2：每个 mapping 执行后的本地 Walk 刷新值（.strm 与关联文件分开计数）
	// 前端以这里的权威值覆盖"基于 deletedCount/regeneratedCount 的增量计算"，避免累计漂移
	RefreshedMappingStats []MappingLocalStats `json:"refreshedMappingStats,omitempty"`

	// ====== 阈值与二次确认（P0 防误删）======
	// Pending 是否已入队待删队列等待用户二次确认
	Pending bool `json:"pending,omitempty"`
	// PendingRequestID 待删批次 ID（Pending=true 时返回）
	PendingRequestID string `json:"pendingRequestId,omitempty"`
	// ThresholdError 超过 MaxThreshold 时返回的错误说明（不执行删除）
	ThresholdError string `json:"thresholdError,omitempty"`
	// AppliedMaxThreshold 应用的 MaxThreshold 配置值（用于 UI 提示）
	AppliedMaxThreshold int `json:"appliedMaxThreshold,omitempty"`
	// AppliedStableThreshold 应用的 StableThreshold 配置值
	AppliedStableThreshold int `json:"appliedStableThreshold,omitempty"`
	// AppliedConfirmMode 应用的 ConfirmMode 配置值
	AppliedConfirmMode string `json:"appliedConfirmMode,omitempty"`
	// TotalStaleCount 待删总数（用于 UI 提示）
	TotalStaleCount int `json:"totalStaleCount,omitempty"`
}

// CleanupSummary 组合操作（清理+补生成）的汇总信息
// 对齐前端 types.ts ExecuteResult.cleanupSummary
type CleanupSummary struct {
	Deleted     int `json:"deleted"`
	Regenerated int `json:"regenerated"`
	Failed      int `json:"failed"`
}

// ===== 统一响应结构（v1.2.5）=====
// 「扫描路径映射」和「全量对账」后端已合并为同一 handler、同一响应结构。
// 两条语义差异：
//   - 扫描路径映射：前端在按钮提示中只展示 云/本地/失效/漏生成 4 维
//   - 全量对账：前端在 toast + 明细里额外高亮 DB 维度（dbRecordCount）
// 两者始终返回相同的 ScanResponse{ mappings, aggregates, dbRecordCount per mapping }。

func HandleStrmCleanupScanPOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var body struct {
			UseSettingsDefaults bool                 `json:"useSettingsDefaults"`
			Mappings            []MappingScanRequest `json:"mappings"`
			Action              string               `json:"action"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		accounts := deps.AccountStore.List()

		mappings := body.Mappings
		if body.UseSettingsDefaults || len(mappings) == 0 {
			mappings = getDefaultScanRequestsFromSettings(settings)
		}

		if len(mappings) == 0 {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"message": "未找到扫描配置",
				"error":   "请在设置中先添加 115 生活事件监控的路径映射",
			})
			return
		}

		accountMap := make(map[string]string)
		for _, acc := range accounts {
			accountMap[acc.Name] = acc.Cookie
		}

		results := make([]MappingScanResult, 0, len(mappings))
		for _, m := range mappings {
			if m.UseCache && deps.StrmCache != nil && deps.TasksStore != nil {
				result := scanMappingWithCacheFallback(r.Context(), m, deps, accountMap)
				results = append(results, result)
			} else {
				result := scanMapping(r.Context(), m, deps, accountMap)
				results = append(results, result)
			}
		}

		// ===== 统一：所有模式都查 dbRecordCount + 聚合 =====
		// SQLiteDB 非 nil 时每条 mapping 都查 files 表真实记录数（成本 <10ms）
		// body.Action == "reconcile" 仅用于"前端提示语义"，不再改变返回结构
		var (
			totalRemote  int
			totalLocal   int
			totalAssoc   int
			totalStale   int
			totalMissing int
			totalDb      int
		)
		for i := range results {
			results[i].DbRecordCount = 0
			if deps.SQLiteDB != nil && results[i].Account != "" {
				dbCount, qerr := db.CountFilesByPrefix(deps.SQLiteDB, results[i].Account, results[i].CloudPath)
				if qerr != nil {
					logger.S().Warnf("[strmCleanup] CountFilesByPrefix account=%s path=%s: %v", results[i].Account, results[i].CloudPath, qerr)
				} else {
					results[i].DbRecordCount = int(dbCount)
				}
			}
			totalRemote += results[i].RemoteFileCount
			totalLocal += results[i].LocalStrmCount
			totalAssoc += results[i].AssociatedFileCount
			totalStale += len(results[i].StaleStrms)
			totalMissing += len(results[i].MissingStrms)
			totalDb += results[i].DbRecordCount
		}

		resp := ScanResponse{
			Mappings:             results,
			MappingsScanned:      mappings,
			DurationMs:           time.Since(start).Milliseconds(),
			TotalRemoteFiles:     totalRemote,
			TotalLocalStrms:      totalLocal,
			TotalAssociatedFiles: totalAssoc,
			TotalStale:           totalStale,
			TotalMissing:         totalMissing,
		}
		if deps.SQLiteDB != nil {
			resp.TotalDbRecords = totalDb
		}

		httpx.WriteJson(w, http.StatusOK, resp)
	}
}

func HandleStrmCleanupExecutePOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var body ExecuteRequest
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		accounts := deps.AccountStore.List()

		accountMap := make(map[string]string)
		for _, acc := range accounts {
			accountMap[acc.Name] = acc.Cookie
		}

		resp := executeCleanup(r.Context(), body, settings, deps, accountMap)
		resp.DurationMs = time.Since(start).Milliseconds()

		httpx.WriteJson(w, http.StatusOK, resp)
	}
}

func getDefaultScanRequestsFromSettings(settings *model.Settings) []MappingScanRequest {
	if settings == nil || !settings.LifeMonitor.Enabled {
		return nil
	}
	mappings := make([]MappingScanRequest, 0, len(settings.LifeMonitor.PathMappings))
	for _, pm := range settings.LifeMonitor.PathMappings {
		if pm.Account != "" && pm.CloudPath != "" && pm.LocalPath != "" {
			mappings = append(mappings, MappingScanRequest{
				Account:   pm.Account,
				CloudPath: pm.CloudPath,
				LocalPath: pm.LocalPath,
			})
		}
	}
	return mappings
}

func scanMapping(ctx context.Context, m MappingScanRequest, deps StrmCleanupDeps, accountMap map[string]string) MappingScanResult {
	result := MappingScanResult{
		Account:   m.Account,
		CloudPath: m.CloudPath,
		LocalPath: m.LocalPath,
	}

	cookie, ok := accountMap[m.Account]
	if !ok || cookie == "" {
		// P2：即使账号/云端不可用，仍先 Walk 本地，把 localStrmCount / associatedFileCount 填上（避免空场景下 2 个计数都为 0 误导）
		result.LocalStrmCount, result.AssociatedFileCount = countLocalByRoot(m.LocalPath)
		result.Error = fmt.Sprintf("account %s not found or no cookie", m.Account)
		return result
	}

	// P2：本地 Walk 先于云端访问（避免云端失败时 localStrmCount/associatedFileCount 仍为 0）
	localStrmFiles := listLocalStrmFiles(m.LocalPath)
	result.LocalStrmCount = len(localStrmFiles)
	_, assocCount := countLocalByRoot(m.LocalPath)
	result.AssociatedFileCount = assocCount

	client := client115.NewClient("")

	cid, err := client.FsDirGetID(ctx, m.CloudPath, cookie)
	if err != nil {
		if err.Error() == "parse dir id: nil" {
			result.Error = fmt.Sprintf("路径不存在或无权限：%s", m.CloudPath)
		} else {
			result.Error = fmt.Sprintf("获取云端目录失败：%v", err)
		}
		return result
	}

	cloudFiles, err := listAllCloudFiles(ctx, client, cookie, cid)
	if err != nil {
		result.Error = fmt.Sprintf("list cloud files: %v", err)
		return result
	}
	result.RemoteFileCount = len(cloudFiles)

	cloudSet := make(map[string]client115.FsFileEntry)
	for _, f := range cloudFiles {
		cloudSet[f.Name] = f
	}

	localSet := make(map[string]os.FileInfo)
	for name, info := range localStrmFiles {
		localSet[name] = info
	}

	for name, info := range localStrmFiles {
		if _, exists := cloudSet[name]; !exists {
			content, truncated := readStrmHead(filepath.Join(m.LocalPath, name), 512)
			result.StaleStrms = append(result.StaleStrms, StaleStrm{
				RelPath:   name,
				Size:      info.Size(),
				Content:   content,
				Truncated: truncated,
			})
		}
	}

	for _, cloudFile := range cloudFiles {
		if _, exists := localSet[cloudFile.Name]; !exists && !cloudFile.IsDir {
			result.MissingStrms = append(result.MissingStrms, MissingStrm{
				RelPath:  cloudFile.Name,
				PickCode: cloudFile.PickCode,
				Size:     cloudFile.Size,
			})
		}
	}

	return result
}

func scanMappingFromCache(m MappingScanRequest, entry *store.StrmCacheEntry) MappingScanResult {
	result := MappingScanResult{
		Account:   m.Account,
		CloudPath: m.CloudPath,
		LocalPath: m.LocalPath,
	}
	// P2：先 Walk 本地（即使 cache 空也要返回本地计数）
	result.LocalStrmCount, result.AssociatedFileCount = countLocalByRoot(m.LocalPath)
	localFiles := listLocalStrmFiles(m.LocalPath)
	if entry == nil || len(entry.LocalPaths) == 0 {
		result.Error = "empty cache"
		return result
	}
	cachedSet := make(map[string]struct{}, len(entry.LocalPaths))
	for _, p := range entry.LocalPaths {
		cachedSet[filepath.Clean(p)] = struct{}{}
	}
	result.RemoteFileCount = len(entry.LocalPaths)
	// full -> relName (用于结果回显)
	actualLocalRel := make(map[string]string, len(localFiles))
	for relName := range localFiles {
		full := filepath.Clean(filepath.Join(m.LocalPath, relName))
		actualLocalRel[full] = relName
	}
	for cleanPath := range actualLocalRel {
		if _, ok := cachedSet[cleanPath]; !ok {
			rp := actualLocalRel[cleanPath]
			content, truncated := readStrmHead(filepath.Join(m.LocalPath, rp), 512)
			var size int64
			if fi, err := os.Stat(filepath.Join(m.LocalPath, rp)); err == nil {
				size = fi.Size()
			}
			result.StaleStrms = append(result.StaleStrms, StaleStrm{
				RelPath:   rp,
				Size:      size,
				Content:   content,
				Truncated: truncated,
			})
		}
	}
	for _, cachedPath := range entry.LocalPaths {
		cleanPath := filepath.Clean(cachedPath)
		if _, exists := actualLocalRel[cleanPath]; !exists {
			rel, _ := filepath.Rel(m.LocalPath, cachedPath)
			result.MissingStrms = append(result.MissingStrms, MissingStrm{
				RelPath: rel,
				Size:    0,
			})
		}
	}
	return result
}

func scanMappingWithCacheFallback(ctx context.Context, m MappingScanRequest, deps StrmCleanupDeps, accountMap map[string]string) MappingScanResult {
	if deps.StrmCache == nil || deps.TasksStore == nil {
		return scanMapping(ctx, m, deps, accountMap)
	}
	tasks, err := deps.TasksStore.ReadTasks()
	if err != nil {
		return scanMapping(ctx, m, deps, accountMap)
	}
	var matchedTaskID string
	for _, t := range tasks {
		if t.Account == m.Account && t.TargetPath == m.LocalPath {
			matchedTaskID = t.ID
			break
		}
	}
	if matchedTaskID == "" {
		r := MappingScanResult{Account: m.Account, CloudPath: m.CloudPath, LocalPath: m.LocalPath}
		r.Error = "no matching task for mapping, fallback to network scan"
		return scanMapping(ctx, m, deps, accountMap)
	}
	var entry *store.StrmCacheEntry
	if m.CacheUUID != "" {
		entry = deps.StrmCache.Get(m.CacheUUID)
	} else {
		entry = deps.StrmCache.LatestByTaskID(matchedTaskID)
	}
	if entry == nil {
		r := MappingScanResult{Account: m.Account, CloudPath: m.CloudPath, LocalPath: m.LocalPath}
		r.Error = "no cache, fallback to network scan"
		return scanMapping(ctx, m, deps, accountMap)
	}
	// Check cache expiry: if caller specified CacheTTLMs use it, otherwise keep legacy 1 hour
	ttl := time.Hour
	if m.CacheTTLMs > 0 {
		ttl = time.Duration(m.CacheTTLMs) * time.Millisecond
	}
	if time.Since(time.UnixMilli(entry.CreatedAt)) > ttl {
		r := MappingScanResult{Account: m.Account, CloudPath: m.CloudPath, LocalPath: m.LocalPath}
		r.Error = fmt.Sprintf("cache expired, fallback to network scan (ttl=%s)", ttl.Round(time.Second))
		return scanMapping(ctx, m, deps, accountMap)
	}
	return scanMappingFromCache(m, entry)
}

// readStrmHead P3：读取本地 .strm 文件前 maxBytes 字节，返回 (content, truncated)
// 失败时返回空字符串（不把 preview 失败上升为扫描失败），内容里 CR/LF 保留原样
func readStrmHead(fullPath string, maxBytes int) (string, bool) {
	fi, err := os.Open(fullPath)
	if err != nil {
		return "", false
	}
	defer fi.Close()
	st, err := fi.Stat()
	if err != nil {
		return "", false
	}
	if maxBytes <= 0 {
		maxBytes = 512
	}
	size := st.Size()
	buf := make([]byte, maxBytes)
	n, _ := fi.Read(buf)
	if n == 0 {
		return "", size > 0
	}
	// 只保留 UTF-8 可见性：替换 0 字节，避免某些二进制 .strm 污染 JSON
	clean := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		b := buf[i]
		if b == 0x00 {
			clean = append(clean, ' ')
		} else {
			clean = append(clean, b)
		}
	}
	return string(clean), size > int64(maxBytes)
}

// HandleStrmCleanupPreviewPOST P3：POST /api/strmCleanup/preview — 按 localPath+relPath 读完整/部分 STRM 内容
func HandleStrmCleanupPreviewPOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req StrmPreviewRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := StrmPreviewResponse{}
		if req.LocalPath == "" || req.RelPath == "" {
			resp.Error = "localPath and relPath required"
			writeJSON(w, http.StatusBadRequest, resp)
			return
		}
		full := filepath.Join(req.LocalPath, req.RelPath)
		st, err := os.Stat(full)
		if err != nil {
			if os.IsNotExist(err) {
				resp.Exists = false
				writeJSON(w, http.StatusOK, resp)
				return
			}
			resp.Error = err.Error()
			writeJSON(w, http.StatusOK, resp)
			return
		}
		resp.Exists = true
		resp.Size = st.Size()
		maxBytes := req.MaxBytes
		if maxBytes <= 0 {
			// "查看完整"：默认 cap 8 KB 防止超大 .strm 拖垮响应（实际 .strm 都远小于此）
			if maxBytes == 0 {
				maxBytes = 8192
			} else {
				maxBytes = int(resp.Size) // 负数 → 读整个
			}
		}
		if maxBytes >= int(resp.Size) {
			content, err := os.ReadFile(full)
			if err != nil {
				resp.Error = err.Error()
			} else {
				resp.Content = string(content)
				resp.Truncated = false
			}
		} else {
			fi, err := os.Open(full)
			if err != nil {
				resp.Error = err.Error()
			} else {
				defer fi.Close()
				buf := make([]byte, maxBytes)
				n, _ := fi.Read(buf)
				if n > 0 {
					resp.Content = string(buf[:n])
				}
				resp.Truncated = true
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
func listAllCloudFiles(ctx context.Context, client *client115.Client, cookie string, cid int64) ([]client115.FsFileEntry, error) {
	var allFiles []client115.FsFileEntry
	offset := 0
	for {
		resp, err := client.FsFiles(ctx, fmt.Sprintf("%d", cid), 1000, offset, cookie)
		if err != nil {
			return nil, err
		}
		allFiles = append(allFiles, resp.Data...)
		if len(resp.Data) < 1000 {
			break
		}
		offset += len(resp.Data)
		if offset >= resp.Count {
			break
		}
	}
	return allFiles, nil
}

// relatedMediaExts P2：与 STRM 同目录的媒体关联文件扩展名集合（对齐 deleteRelatedFilesByStem 列表，额外加 .vtt）
var relatedMediaExts = map[string]bool{
	".srt": true, ".ass": true, ".sub": true, ".vtt": true,
	".nfo": true, ".jpg": true, ".png": true,
}

// isRelatedMediaFile 判断文件是否为关联媒体信息文件
func isRelatedMediaFile(name string) bool {
	return relatedMediaExts[strings.ToLower(filepath.Ext(name))]
}

// listLocalStrmFiles 返回本地 .strm 相对路径→文件信息（保持签名不变，避免 StaleStrm/MissingStrm 匹配逻辑改动）
func listLocalStrmFiles(localPath string) map[string]os.FileInfo {
	result := make(map[string]os.FileInfo)
	if localPath == "" {
		return result
	}
	root := localPath
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".strm") {
			relPath, err := filepath.Rel(root, path)
			if err != nil {
				relPath = info.Name()
			}
			result[relPath] = info
		}
		return nil
	})
	return result
}

// countLocalByRoot P2：Walk 一次，同时返回 .strm 数量 与 关联媒体文件数量（.nfo/.jpg/.png/.srt/.sub/.ass/.vtt）
// 用于：1) 扫描侧 MappingScanResult.AssociatedFileCount 填充；2) execute 结尾轻量 re-scan 生成 RefreshedMappingStats
func countLocalByRoot(localPath string) (strmCount int, assocCount int) {
	if localPath == "" {
		return 0, 0
	}
	_ = filepath.Walk(localPath, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := strings.ToLower(info.Name())
		if strings.HasSuffix(name, ".strm") {
			strmCount++
		} else if isRelatedMediaFile(info.Name()) {
			assocCount++
		}
		return nil
	})
	return strmCount, assocCount
}

func executeCleanup(ctx context.Context, body ExecuteRequest, settings *model.Settings, deps StrmCleanupDeps, accountMap map[string]string) ExecuteResponse { //nolint:cyclop // complexity: 34
	resp := ExecuteResponse{
		DryRun:              body.DryRun,
		Errors:              []map[string]string{},
		RemovedEmptyDirs:    []string{},
		RemovedRelatedFiles: []string{},
	}

	// ====== P0 双阈值 + 二次确认 ======
	cleanup := settings.Cleanup
	maxThreshold := cleanup.MaxThreshold
	if maxThreshold <= 0 {
		maxThreshold = 10 // 默认值保护
	}
	stableThreshold := cleanup.StableThreshold
	if stableThreshold < 0 {
		stableThreshold = 5
	}
	confirmMode := cleanup.ConfirmMode
	if confirmMode == "" {
		confirmMode = "none"
	}
	resp.AppliedMaxThreshold = maxThreshold
	resp.AppliedStableThreshold = stableThreshold
	resp.AppliedConfirmMode = confirmMode

	action := body.Action
	if action == "" {
		action = "delete"
	}

	// 统计待删总数（不包括 delete_all 模式批量扫描）
	totalStale := 0
	for _, entry := range body.Entries {
		totalStale += len(entry.StaleRelPaths)
	}
	resp.TotalStaleCount = totalStale

	// DryRun 模式下不做阈值拦截，直接走原模拟流程
	if !body.DryRun && totalStale > maxThreshold && action != "delete_all" {
		resp.ThresholdError = fmt.Sprintf("待删 %d 个超过最大阈值 %d，已拒绝执行。请在 settings.cleanup.maxThreshold 调高或在 UI 重新筛选", totalStale, maxThreshold)
		logger.S().Warnf("[strmCleanup] 拒绝执行: 待删 %d 超过 MaxThreshold %d", totalStale, maxThreshold)
		return resp
	}

	// 稳定阈值拦截：超过 StableThreshold 且开启了二次确认 → 入队待删
	if !body.DryRun && totalStale > stableThreshold && confirmMode != "none" && action != "delete_all" && deps.Interaction != nil {
		// 收集所有待删完整路径
		paths := make([]string, 0, totalStale)
		for _, entry := range body.Entries {
			for _, rel := range entry.StaleRelPaths {
				paths = append(paths, filepath.Join(entry.LocalPath, rel))
			}
		}
		requestID := GenerateRequestID()
		batch := CleanupBatch{
			RequestID:          requestID,
			CreatedAt:          time.Now().UnixNano() / 1e6,
			Paths:              paths,
			SamplePaths:        BuildSamplePaths(paths),
			PathCount:          totalStale,
			RemoveStrm:         cleanup.RemoveStrm,
			RemoveEmptyDirs:    cleanup.RemoveEmptyDirs,
			RemoveRelatedFiles: cleanup.RemoveRelatedFiles,
		}
		if err := deps.Interaction.AppendBatch(ctx, batch); err != nil {
			logger.S().Errorf("[strmCleanup] AppendBatch failed: %v", err)
			resp.ThresholdError = fmt.Sprintf("入队待删批次失败: %v", err)
			return resp
		}
		resp.Pending = true
		resp.PendingRequestID = requestID
		logger.S().Infof("[strmCleanup] 待删 %d 超过 StableThreshold %d，已入队 %s，等待 %s 二次确认",
			totalStale, stableThreshold, requestID, confirmMode)
		// 按 ConfirmMode 派发通知
		if confirmMode == "telegram" {
			if err := deps.Interaction.NotifyTelegramPending(ctx, batch); err != nil {
				logger.S().Warnf("[strmCleanup] NotifyTelegramPending failed: %v", err)
			}
		}
		return resp
	}

	// ====== 执行删除（三阶段开关）======
	// delete（按选中条目删除）和 delete_and_regenerate（组合操作）都走 deleteSelectedStale
	// 前者要求阈值检查通过（已在上方完成），后者复用同样语义：只删用户选中的失效 STRM
	// 注：delete_all 不走这里，走 deleteAllStaleFromScan（从 ScanSummary 全删）
	if action == "delete" || action == "delete_and_regenerate" {
		deleteSelectedStale(body, &resp, &cleanup)
	}

	// ====== delete_all / regenerate / delete_and_regenerate 分支 ======
	// 修复前 bug：原 delete_all 遍历 body.Entries（前端 delete_all 传 []），实际删除 0 个
	// 修复后：从 body.ScanSummary.Mappings 取真实失效/漏项路径
	switch action {
	case "delete_all":
		deleted, failed := deleteAllStaleFromScan(body, &resp, &cleanup)
		resp.DeletedCount += deleted
		resp.DeletedAllCount = deleted
		resp.FailedCount += failed
	case "regenerate":
		regenCount, regenFailed := regenerateMissingStrms(body, settings, &resp)
		resp.RegeneratedCount = regenCount
		resp.FailedCount += regenFailed
	case "delete_and_regenerate":
		// deleteSelectedStale 已在上方处理（只删选中条目），这里仅做补生成 + 汇总
		deleted := resp.DeletedCount
		delFailed := resp.FailedCount
		regenCount, regenFailed := regenerateMissingStrms(body, settings, &resp)
		resp.RegeneratedCount = regenCount
		resp.FailedCount += regenFailed
		resp.CleanupSummary = &CleanupSummary{
			Deleted:     deleted,
			Regenerated: regenCount,
			Failed:      delFailed + regenFailed,
		}
	}

	// ====== P2：轻量本地 re-scan（Walk 一次，只刷新 localStrmCount / associatedFileCount）======
	// 避免前端基于 deletedCount/regeneratedCount 的增量估算造成"累计漂移"
	if !body.DryRun && body.ScanSummary != nil && len(body.ScanSummary.Mappings) > 0 &&
		(resp.DeletedCount > 0 || resp.RegeneratedCount > 0 || len(resp.RemovedRelatedFiles) > 0) {
		stats := make([]MappingLocalStats, 0, len(body.ScanSummary.Mappings))
		seen := make(map[string]struct{}, len(body.ScanSummary.Mappings))
		for _, m := range body.ScanSummary.Mappings {
			if m.LocalPath == "" {
				continue
			}
			if _, ok := seen[m.LocalPath]; ok {
				continue
			}
			seen[m.LocalPath] = struct{}{}
			strmCnt, assocCnt := countLocalByRoot(m.LocalPath)
			stats = append(stats, MappingLocalStats{
				LocalPath:           m.LocalPath,
				LocalStrmCount:      strmCnt,
				AssociatedFileCount: assocCnt,
			})
		}
		if len(stats) > 0 {
			resp.RefreshedMappingStats = stats
		}
	}

	return resp
}

// deleteSelectedStale 按 body.Entries 中选中的条目执行三阶段删除
// 用于 action=delete 和 action=delete_and_regenerate（只删选中而非全删）
// 复用原 executeCleanup 主流程的内联删除逻辑（500-537 行旧代码移入此函数）
func deleteSelectedStale(body ExecuteRequest, resp *ExecuteResponse, cleanup *model.CleanupSettings) {
	for _, entry := range body.Entries {
		for _, staleRelPath := range entry.StaleRelPaths {
			stalePath := filepath.Join(entry.LocalPath, staleRelPath)
			if body.DryRun {
				resp.DeletedCount++
				continue
			}
			// 阶段 1：删 STRM 文件本身（RemoveStrm 默认 true）
			if cleanup.RemoveStrm {
				if err := os.Remove(stalePath); err != nil {
					if !os.IsNotExist(err) {
						resp.FailedCount++
						resp.Errors = append(resp.Errors, map[string]string{
							"path":  stalePath,
							"error": err.Error(),
						})
						logger.S().Warnf("[strmCleanup] delete %s failed: %v", stalePath, err)
					}
				} else {
					resp.DeletedCount++
					logger.S().Infof("[strmCleanup] deleted stale strm: %s", stalePath)
				}
			}
			// 阶段 2：删关联媒体信息文件（RemoveRelatedFiles 默认 false）
			if cleanup.RemoveRelatedFiles {
				removed := deleteRelatedFilesByStem(stalePath)
				resp.RemovedRelatedFiles = append(resp.RemovedRelatedFiles, removed...)
			}
		}
	}
	// 阶段 3：删无 STRM 的空目录（RemoveEmptyDirs 默认 false）
	if !body.DryRun && cleanup.RemoveEmptyDirs {
		for _, entry := range body.Entries {
			removeEmptyDirs(entry.LocalPath, &resp.RemovedEmptyDirs)
		}
	}
}

// deleteAllStaleFromScan 从 body.ScanSummary.Mappings 收集失效 STRM 完整路径并删除
// 复用三阶段删除（RemoveStrm / RemoveRelatedFiles / RemoveEmptyDirs）
// 返回 (deletedCount, failedCount)
func deleteAllStaleFromScan(body ExecuteRequest, resp *ExecuteResponse, cleanup *model.CleanupSettings) (int, int) {
	if body.ScanSummary == nil {
		logger.S().Warnf("[strmCleanup] delete_all: ScanSummary 为空，无法定位失效 STRM")
		return 0, 0
	}
	deleted, failed := 0, 0
	for _, m := range body.ScanSummary.Mappings {
		for _, stale := range m.StaleStrms {
			stalePath := filepath.Join(m.LocalPath, stale.RelPath)
			if body.DryRun {
				deleted++
				continue
			}
			if cleanup.RemoveStrm {
				if err := os.Remove(stalePath); err != nil {
					if !os.IsNotExist(err) {
						failed++
						resp.Errors = append(resp.Errors, map[string]string{
							"path":  stalePath,
							"error": err.Error(),
						})
						logger.S().Warnf("[strmCleanup] delete_all %s failed: %v", stalePath, err)
					}
				} else {
					deleted++
					logger.S().Infof("[strmCleanup] delete_all deleted: %s", stalePath)
				}
			}
			if cleanup.RemoveRelatedFiles {
				removed := deleteRelatedFilesByStem(stalePath)
				resp.RemovedRelatedFiles = append(resp.RemovedRelatedFiles, removed...)
			}
		}
	}
	if !body.DryRun && cleanup.RemoveEmptyDirs {
		for _, m := range body.ScanSummary.Mappings {
			removeEmptyDirs(m.LocalPath, &resp.RemovedEmptyDirs)
		}
	}
	return deleted, failed
}

// regenerateMissingStrms 从 body.ScanSummary.Mappings 的 MissingStrms 生成 STRM 文件
// 返回 (regeneratedCount, failedCount)
// STRM URL 内容生成规则：
//  1. settings.Strm.StrmUrlTemplate 非空 → 调用 model.RenderStrmUrlTemplate
//  2. 否则 fallback: {StrmPrefix}/api/strm?account={account}&pickcode={pickcode}
//
// 文件名：默认 {stem}.strm（与 service/task/executor_utils.go getStrmFileName 行为一致）
func regenerateMissingStrms(body ExecuteRequest, settings *model.Settings, resp *ExecuteResponse) (int, int) {
	if body.ScanSummary == nil {
		logger.S().Warnf("[strmCleanup] regenerate: ScanSummary 为空，无法定位漏项")
		return 0, 0
	}
	regen, failed := 0, 0
	regenPaths := make([]string, 0, 8)
	for _, m := range body.ScanSummary.Mappings {
		for _, miss := range m.MissingStrms {
			if miss.PickCode == "" {
				failed++
				resp.Errors = append(resp.Errors, map[string]string{
					"path":  filepath.Join(m.LocalPath, miss.RelPath),
					"error": "pickcode 为空，无法生成 STRM",
				})
				logger.S().Warnf("[strmCleanup] regenerate skip (no pickcode): %s", miss.RelPath)
				continue
			}
			strmPath, content, err := buildStrmForMissing(m, miss, settings)
			if err != nil {
				failed++
				resp.Errors = append(resp.Errors, map[string]string{
					"path":  strmPath,
					"error": err.Error(),
				})
				logger.S().Warnf("[strmCleanup] regenerate build failed: %s: %v", miss.RelPath, err)
				continue
			}
			if body.DryRun {
				regen++
				regenPaths = append(regenPaths, miss.RelPath)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(strmPath), 0o755); err != nil {
				failed++
				resp.Errors = append(resp.Errors, map[string]string{
					"path":  strmPath,
					"error": fmt.Sprintf("mkdir 失败: %v", err),
				})
				continue
			}
			if err := writeStrmFileAtomic(strmPath, content); err != nil {
				failed++
				resp.Errors = append(resp.Errors, map[string]string{
					"path":  strmPath,
					"error": err.Error(),
				})
				logger.S().Warnf("[strmCleanup] regenerate write failed: %s: %v", strmPath, err)
				continue
			}
			regen++
			regenPaths = append(regenPaths, miss.RelPath)
			logger.S().Infof("[strmCleanup] regenerate created: %s", strmPath)
		}
	}
	resp.RegeneratedPaths = append(resp.RegeneratedPaths, regenPaths...)
	return regen, failed
}

// buildStrmForMissing 为漏项构造 STRM 完整路径和 URL 内容
// 文件名策略：去掉原扩展名，追加 .strm（对齐 task.executor_utils.go getStrmFileName）
func buildStrmForMissing(m MappingScanResult, miss MissingStrm, settings *model.Settings) (string, string, error) {
	// 1. STRM 完整路径：localPath + relPath 目录部分 + stem + ".strm"
	relDir := filepath.Dir(miss.RelPath)
	base := filepath.Base(miss.RelPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	strmName := stem + ".strm"
	strmPath := filepath.Join(m.LocalPath, relDir, strmName)

	// 2. URL 内容
	prefix := strings.TrimRight(settings.StrmPrefix, "/")
	if prefix == "" {
		prefix = "http://127.0.0.1:8090"
	}
	tmpl := settings.Strm.StrmUrlTemplate
	fileName := base

	var content string
	if tmpl != "" {
		ext := strings.ToLower(filepath.Ext(fileName))
		stemForTmpl := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		content = model.RenderStrmUrlTemplate(tmpl, prefix, m.Account, miss.PickCode, fileName, ext, stemForTmpl)
	}
	if content == "" {
		// fallback：默认拼接 {prefix}/api/strm?account=...&pickcode=...
		content = fmt.Sprintf("%s/api/strm?account=%s&pickcode=%s",
			prefix, url.QueryEscape(m.Account), url.QueryEscape(miss.PickCode))
	}
	return strmPath, content, nil
}

// writeStrmFileAtomic 原子写入 STRM 文件（先写 tmp 再 rename）
// 与 service/task/executor_utils.go 的 writeStrmFile 行为一致
func writeStrmFileAtomic(strmPath, content string) error {
	tmpPath := strmPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, strmPath)
}

func removeEmptyDirs(root string, removed *[]string) {
	for {
		done := true
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || p == root || !info.IsDir() {
				return nil
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil
			}
			if len(entries) == 0 {
				if err := os.Remove(p); err == nil {
					*removed = append(*removed, p)
					done = false
				}
			}
			return nil
		})
		if done {
			return
		}
	}
}

// deleteRelatedFilesByStem 删除与 STRM 文件同基名（stem）的关联媒体信息文件
// 关联扩展名：.srt/.ass/.sub/.nfo/.jpg/.png（对齐 syncdel.go deleteStrmFile 的清理逻辑）
// 返回实际删除的文件绝对路径列表
//
// 注意：传入的 strmPath 可以已经删除，本函数只读取同目录条目按 stem 匹配。
func deleteRelatedFilesByStem(strmPath string) []string {
	dir := filepath.Dir(strmPath)
	base := strings.TrimSuffix(filepath.Base(strmPath), filepath.Ext(strmPath))
	strmName := filepath.Base(strmPath)
	relatedExts := map[string]bool{
		".srt": true, ".ass": true, ".sub": true,
		".nfo": true, ".jpg": true, ".png": true,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var removed []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == strmName {
			continue
		}
		nameBase := strings.TrimSuffix(name, filepath.Ext(name))
		ext := strings.ToLower(filepath.Ext(name))
		if nameBase == base && relatedExts[ext] {
			full := filepath.Join(dir, name)
			if err := os.Remove(full); err == nil {
				removed = append(removed, full)
				logger.S().Infof("[strmCleanup] deleted related file: %s", full)
			}
		}
	}
	return removed
}

// ==================== 待删批次执行（二次确认后调用）====================

// executePendingBatch 执行单个待删批次（用户二次确认通过后调用）
// 复用 executeCleanup 的三阶段删除逻辑（RemoveStrm/RemoveRelatedFiles/RemoveEmptyDirs）
func executePendingBatch(ctx context.Context, batch CleanupBatch) ExecuteResponse {
	resp := ExecuteResponse{
		Errors:              []map[string]string{},
		RemovedEmptyDirs:    []string{},
		RemovedRelatedFiles: []string{},
		PendingRequestID:    batch.RequestID,
		TotalStaleCount:     batch.PathCount,
	}

	for _, p := range batch.Paths {
		// 阶段 1：删 STRM 文件本身
		if batch.RemoveStrm {
			if err := os.Remove(p); err != nil {
				if !os.IsNotExist(err) {
					resp.FailedCount++
					resp.Errors = append(resp.Errors, map[string]string{
						"path":  p,
						"error": err.Error(),
					})
					logger.S().Warnf("[strmCleanup] pending batch delete %s failed: %v", p, err)
				}
			} else {
				resp.DeletedCount++
				logger.S().Infof("[strmCleanup] pending batch deleted: %s", p)
			}
		}
		// 阶段 2：删关联媒体信息文件
		if batch.RemoveRelatedFiles {
			removed := deleteRelatedFilesByStem(p)
			resp.RemovedRelatedFiles = append(resp.RemovedRelatedFiles, removed...)
		}
	}

	// 阶段 3：删无 STRM 的空目录
	if batch.RemoveEmptyDirs {
		dirSet := make(map[string]bool)
		for _, p := range batch.Paths {
			dirSet[filepath.Dir(p)] = true
		}
		for dir := range dirSet {
			removeEmptyDirs(dir, &resp.RemovedEmptyDirs)
		}
	}

	return resp
}

// HandleStrmCleanupPendingListGET 列出所有待删批次
// GET /api/strmCleanup/pending
func HandleStrmCleanupPendingListGET(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Interaction == nil {
			httpx.WriteJson(w, http.StatusOK, map[string]any{"batches": []any{}, "enabled": false})
			return
		}
		batches, err := deps.Interaction.ListBatches(r.Context())
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httpx.WriteJson(w, http.StatusOK, map[string]any{"batches": batches, "enabled": true})
	}
}

// HandleStrmCleanupPendingCancelPOST 取消一个待删批次（仅从队列移除，不删文件）
// POST /api/strmCleanup/pending/cancel  body: {"requestId": "xxx"}
func HandleStrmCleanupPendingCancelPOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Interaction == nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "interaction not enabled"})
			return
		}
		var body struct {
			RequestID string `json:"requestId"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		if body.RequestID == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "requestId is required"})
			return
		}
		ok, err := deps.Interaction.CancelBatch(r.Context(), body.RequestID)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !ok {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "batch not found"})
			return
		}
		httpx.WriteJson(w, http.StatusOK, map[string]bool{"canceled": true})
	}
}

// HandleStrmCleanupPendingExecutePOST 执行一个待删批次（用户二次确认通过后调用）
// POST /api/strmCleanup/pending/execute  body: {"requestId": "xxx"}
func HandleStrmCleanupPendingExecutePOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Interaction == nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "interaction not enabled"})
			return
		}
		var body struct {
			RequestID string `json:"requestId"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		if body.RequestID == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "requestId is required"})
			return
		}

		start := time.Now()
		batch, err := deps.Interaction.PopBatch(r.Context(), body.RequestID)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if batch == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "batch not found or already executed"})
			return
		}
		resp := executePendingBatch(r.Context(), *batch)
		resp.DurationMs = time.Since(start).Milliseconds()
		logger.S().Infof("[strmCleanup] pending batch %s executed: deleted=%d failed=%d duration=%dms",
			body.RequestID, resp.DeletedCount, resp.FailedCount, resp.DurationMs)
		httpx.WriteJson(w, http.StatusOK, resp)
	}
}
