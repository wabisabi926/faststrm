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

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
)

type StrmCleanupDeps struct {
	SettingsStore *store.SettingsStore
	AccountStore  *store.AccountStore
	ClientFactory func(name string) (*client115.Client, error)
	TasksStore    *store.TasksStore
	StrmCache     *store.StrmCacheStore
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
	Account          string      `json:"account"`
	CloudPath        string      `json:"cloudPath"`
	LocalPath        string      `json:"localPath"`
	RemoteFileCount  int         `json:"remoteFileCount"`
	LocalStrmCount   int         `json:"localStrmCount"`
	StaleStrms       []StaleStrm `json:"staleStrms"`
	MissingStrms     []MissingStrm `json:"missingStrms"`
	Error            string      `json:"error,omitempty"`
}

type ScanResponse struct {
	Mappings         []MappingScanResult `json:"mappings"`
	MappingsScanned  []MappingScanRequest `json:"mappingsScanned,omitempty"`
	DurationMs       int64               `json:"durationMs"`
}

type ExecuteEntry struct {
	LocalPath    string   `json:"localPath"`
	StaleRelPaths []string `json:"staleRelPaths"`
}

type ExecuteMissingItem struct {
	LocalPath string `json:"localPath"`
	RelPath   string `json:"relPath"`
	MappingID string `json:"mappingId"`
}

type ExecuteRequest struct {
	Entries         []ExecuteEntry       `json:"entries"`
	DryRun          bool                 `json:"dryRun"`
	Action          string               `json:"action"`
	MissingItems    []ExecuteMissingItem `json:"missingItems"`
	ScanSummary     *ScanResponse        `json:"scanSummary,omitempty"`
}

type ExecuteResponse struct {
	DeletedCount    int              `json:"deletedCount"`
	FailedCount     int              `json:"failedCount"`
	Errors          []map[string]string `json:"errors"`
	RemovedEmptyDirs []string         `json:"removedEmptyDirs"`
	DryRun          bool             `json:"dryRun"`
	DurationMs      int64            `json:"durationMs"`
	RegeneratedCount int             `json:"regeneratedCount,omitempty"`
}

func HandleStrmCleanupScanPOST(deps StrmCleanupDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var body struct {
			UseSettingsDefaults bool                 `json:"useSettingsDefaults"`
			Mappings            []MappingScanRequest  `json:"mappings"`
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

func executeCleanup(ctx context.Context, body ExecuteRequest, settings *model.Settings, deps StrmCleanupDeps, accountMap map[string]string) ExecuteResponse {
	resp := ExecuteResponse{
		DryRun:          body.DryRun,
		Errors:          []map[string]string{},
		RemovedEmptyDirs: []string{},
	}

	action := body.Action
	if action == "" {
		action = "delete"
	}

	for _, entry := range body.Entries {
		for _, staleRelPath := range entry.StaleRelPaths {
			stalePath := filepath.Join(entry.LocalPath, staleRelPath)
			if body.DryRun {
				resp.DeletedCount++
				continue
			}
			if err := os.Remove(stalePath); err != nil {
				resp.FailedCount++
				resp.Errors = append(resp.Errors, map[string]string{
					"path":  stalePath,
					"error": err.Error(),
				})
				logger.S().Warnf("[strmCleanup] delete %s failed: %v", stalePath, err)
			} else {
				resp.DeletedCount++
				logger.S().Infof("[strmCleanup] deleted stale strm: %s", stalePath)
			}
		}
	}

	if !body.DryRun {
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

