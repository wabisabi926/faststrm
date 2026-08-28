package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
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
}

type MappingScanRequest struct {
	Account   string `json:"account"`
	CloudPath string `json:"cloudPath"`
	LocalPath string `json:"localPath"`
	UseCache  bool   `json:"useCache,omitempty"`
	CacheUUID string `json:"cacheUuid,omitempty"`
}

type StaleStrm struct {
	RelPath string `json:"relPath"`
	Size    int64  `json:"size"`
}

type MissingStrm struct {
	RelPath  string `json:"relPath"`
	PickCode string `json:"pickCode"`
	Size     int64  `json:"size"`
}

type MappingScanResult struct {
	Account         string        `json:"account"`
	CloudPath       string        `json:"cloudPath"`
	LocalPath       string        `json:"localPath"`
	RemoteFileCount int           `json:"remoteFileCount"`
	LocalStrmCount  int           `json:"localStrmCount"`
	StaleStrms      []StaleStrm   `json:"staleStrms"`
	MissingStrms    []MissingStrm `json:"missingStrms"`
	Error           string        `json:"error,omitempty"`
}

type ScanResponse struct {
	Mappings        []MappingScanResult  `json:"mappings"`
	MappingsScanned []MappingScanRequest `json:"mappingsScanned,omitempty"`
	DurationMs      int64                `json:"durationMs"`
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

		resp := ScanResponse{
			Mappings:        results,
			MappingsScanned: mappings,
			DurationMs:      time.Since(start).Milliseconds(),
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
		result.Error = fmt.Sprintf("account %s not found or no cookie", m.Account)
		return result
	}

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

	localStrmFiles := listLocalStrmFiles(m.LocalPath)
	result.LocalStrmCount = len(localStrmFiles)

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
			result.StaleStrms = append(result.StaleStrms, StaleStrm{
				RelPath: name,
				Size:    info.Size(),
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
	if entry == nil || len(entry.LocalPaths) == 0 {
		result.Error = "empty cache"
		return result
	}
	cachedSet := make(map[string]struct{}, len(entry.LocalPaths))
	for _, p := range entry.LocalPaths {
		cachedSet[filepath.Clean(p)] = struct{}{}
	}
	localFiles := listLocalStrmFiles(m.LocalPath)
	result.LocalStrmCount = len(localFiles)
	result.RemoteFileCount = len(entry.LocalPaths)
	// full -> relName (用于结果回显)
	actualLocalRel := make(map[string]string, len(localFiles))
	for relName := range localFiles {
		full := filepath.Clean(filepath.Join(m.LocalPath, relName))
		actualLocalRel[full] = relName
	}
	for cleanPath := range actualLocalRel {
		if _, ok := cachedSet[cleanPath]; !ok {
			result.StaleStrms = append(result.StaleStrms, StaleStrm{
				RelPath: actualLocalRel[cleanPath],
				Size:    0,
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
	// Check cache expiry (1 hour)
	if time.Since(time.UnixMilli(entry.CreatedAt)) > time.Hour {
		r := MappingScanResult{Account: m.Account, CloudPath: m.CloudPath, LocalPath: m.LocalPath}
		r.Error = "cache expired, fallback to network scan"
		return scanMapping(ctx, m, deps, accountMap)
	}
	return scanMappingFromCache(m, entry)
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

func listLocalStrmFiles(localPath string) map[string]os.FileInfo {
	result := make(map[string]os.FileInfo)
	if localPath == "" {
		return result
	}
	root := localPath
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
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

	if action == "delete_all" {
		for _, entry := range body.Entries {
			if body.DryRun {
				resp.DeletedCount++
				continue
			}
			pattern := filepath.Join(entry.LocalPath, "*.strm")
			matches, _ := filepath.Glob(pattern)
			for _, m := range matches {
				if err := os.Remove(m); err != nil {
					resp.FailedCount++
				} else {
					resp.DeletedCount++
				}
			}
		}
	}

	return resp
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
