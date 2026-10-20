package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
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
	// ManagedStrm STRM 托管登记/复核（nil=未启用 SQLite，自动删除会被跳过）
	ManagedStrm strm.ManagedRecorder
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
	// Reason 失效原因：not_in_cloud（云端已无对应文件）/ account_missing（STRM 引用的账号已不存在）。
	// 账号失效优先标记（更可操作：删文件后需用现有账号重建）。
	Reason string `json:"reason,omitempty"`
}

type MissingStrm struct {
	// RelPath 本地 .strm 相对路径（OS 分隔符、已做落盘清理）
	RelPath string `json:"relPath"`
	// CloudRelPath 源文件的云端相对路径（'/' 分隔、保留原扩展名）。
	// RelPath 是 .strm 路径，构建 URL 需要的是源文件名（file_name / {ext} / {stem} /
	// {file_path}）；早期实现误把 RelPath 当源文件名，导致补建出的 STRM 里
	// file_name 写成 "xxx.strm"，播放器拿不到真实容器扩展名。
	CloudRelPath string `json:"cloudRelPath,omitempty"`
	PickCode     string `json:"pickCode"`
	Size         int64  `json:"size"`
}

type MappingScanResult struct {
	Account         string `json:"account"`
	CloudPath       string `json:"cloudPath"`
	LocalPath       string `json:"localPath"`
	RemoteFileCount int    `json:"remoteFileCount"`
	LocalStrmCount  int    `json:"localStrmCount"`
	// AssociatedFileCount 本地关联文件数（.nfo/.jpg/.png/.srt/.sub/.ass/.vtt），与 STRM 分开统计（P2 语义纯净）
	AssociatedFileCount int         `json:"associatedFileCount,omitempty"`
	StaleStrms          []StaleStrm `json:"staleStrms"`
	// UnmanagedStrms 云端不存在、但不在托管表中（手写/第三方/纳管前遗留）。
	// 默认不参与删除，仅展示计数供用户判断。
	UnmanagedStrms []StaleStrm   `json:"unmanagedStrms,omitempty"`
	UnmanagedCount int           `json:"unmanagedCount,omitempty"`
	MissingStrms   []MissingStrm `json:"missingStrms"`
	Error          string        `json:"error,omitempty"`
	// ErrorCode 结构化错误码：account_not_found（账号不存在/无 Cookie，云端比对已跳过）/
	// cloud_dir_failed（云端目录获取失败）/ cloud_walk_failed（云端遍历失败）等。
	// 前端可据此做差异化提示；Error 字段保持人类可读文案兼容旧渲染。
	ErrorCode string `json:"errorCode,omitempty"`
	// DbRecordCount 该路径映射下 SQLite files 表真实记录数（=云路径前缀精确匹配）
	// 未开启 sqlite 时为 0（前端可通过是否提供过 reconcile 结果判断语义）
	DbRecordCount int `json:"dbRecordCount,omitempty"`
}

type ScanResponse struct {
	Mappings        []MappingScanResult  `json:"mappings"`
	MappingsScanned []MappingScanRequest `json:"mappingsScanned,omitempty"`
	DurationMs      int64                `json:"durationMs"`
	// ScanId 服务端扫描快照 ID（执行删除/补建时必须回传）；默认 30 分钟有效。
	ScanId        string `json:"scanId,omitempty"`
	ScanExpiresAt int64  `json:"scanExpiresAt,omitempty"`
	// TotalRemoteFiles / TotalLocalStrms / TotalStale / TotalMissing 后端聚合好的全量统计
	// 前端直接使用，避免 reduce + ?? 0 兜底导致的数字漂移或漏报
	TotalRemoteFiles int `json:"totalRemoteFiles"`
	TotalLocalStrms  int `json:"totalLocalStrms"`
	TotalStale       int `json:"totalStale"`
	TotalMissing     int `json:"totalMissing"`
	// TotalAssociatedFiles 全部 mappings 关联文件（.nfo/.jpg/.srt 等）计数之和（P2 与 STRM 分离统计）
	TotalAssociatedFiles int `json:"totalAssociatedFiles,omitempty"`
	// TotalUnmanaged 云端不存在但未纳入托管的 STRM 总数（默认不删除）
	TotalUnmanaged int `json:"totalUnmanaged,omitempty"`
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
	// ScanId 扫描接口返回的服务端快照 ID；执行时只认快照中的路径。
	ScanId string `json:"scanId"`
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
	// SkippedUnmanaged 因未托管/指纹变化而跳过的文件数（不会删除）
	SkippedUnmanaged int `json:"skippedUnmanaged,omitempty"`
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

		roots := allowedCleanupRoots(settings, deps)
		for _, m := range mappings {
			if _, err := validateCleanupRoot(m.LocalPath, roots); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
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
			totalRemote    int
			totalLocal     int
			totalAssoc     int
			totalStale     int
			totalMissing   int
			totalDb        int
			totalUnmanaged int
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
			totalUnmanaged += results[i].UnmanagedCount
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
			TotalUnmanaged:       totalUnmanaged,
			TotalMissing:         totalMissing,
		}
		if deps.SQLiteDB != nil {
			resp.TotalDbRecords = totalDb
		}
		// 保存服务端扫描快照：执行只能删除/补建本次扫描确认过的路径。
		resp.ScanId, resp.ScanExpiresAt = cleanupScanCache.Put(r.Header.Get("X-User"), resp)

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

		if strings.TrimSpace(body.ScanId) == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"code":  "SCAN_TOKEN_REQUIRED",
				"error": "缺少扫描结果标识，请重新扫描后再执行",
			})
			return
		}
		snapshot, ok := cleanupScanCache.Get(body.ScanId, r.Header.Get("X-User"))
		if !ok {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"code":  "SCAN_TOKEN_INVALID",
				"error": "扫描结果不存在或已过期，请重新扫描",
			})
			return
		}
		if err := bindExecuteToScan(&body, snapshot); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{
				"code":  "SCAN_STALE_MISMATCH",
				"error": err.Error(),
			})
			return
		}

		if err := validateCleanupRequest(body, settings, deps); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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

// allowedCleanupRoots 返回允许清理/预览/扫描的本地根目录：
// 生活监控路径映射 + 任务目标目录。清理类接口不再信任前端传入的任意路径。
func allowedCleanupRoots(settings *model.Settings, deps StrmCleanupDeps) []string {
	roots := make([]string, 0, 8)
	seen := make(map[string]struct{})
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		key := p
		if runtime.GOOS == "windows" {
			key = strings.ToLower(p)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		roots = append(roots, p)
	}
	for _, m := range CleanupMappings(settings) {
		add(m.LocalPath)
	}
	if deps.TasksStore != nil {
		if tasks, err := deps.TasksStore.ReadTasks(); err == nil {
			for _, t := range tasks {
				add(t.TargetPath)
			}
		}
	}
	return roots
}

func pathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// validateCleanupRequest 校验执行/预览请求中的本地路径：
//   - LocalPath 必须是已配置的清理映射根（或其子目录）
//   - RelPath 必须是相对路径，归一化后不得逃出根目录
//   - 目标必须是 .strm，且现有文件不能是符号链接
//
// 这是路径防呆，不替代托管所有权；它能阻止前端伪造路径删除映射外的文件。
func validateCleanupRequest(body ExecuteRequest, settings *model.Settings, deps StrmCleanupDeps) error {
	roots := allowedCleanupRoots(settings, deps)
	if len(roots) == 0 {
		return fmt.Errorf("没有可用的清理映射，请先在设置中添加生活监控路径映射或任务目标目录")
	}

	for _, entry := range body.Entries {
		root, err := validateCleanupRoot(entry.LocalPath, roots)
		if err != nil {
			return err
		}
		for _, rel := range entry.StaleRelPaths {
			if err := validateCleanupRelPath(root, rel); err != nil {
				return err
			}
		}
	}
	if body.ScanSummary != nil {
		for _, m := range body.ScanSummary.Mappings {
			root, err := validateCleanupRoot(m.LocalPath, roots)
			if err != nil {
				return err
			}
			for _, s := range m.StaleStrms {
				if err := validateCleanupRelPath(root, s.RelPath); err != nil {
					return err
				}
			}
			for _, s := range m.MissingStrms {
				if err := validateCleanupRelPathAny(root, s.RelPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCleanupRoot(localPath string, roots []string) (string, error) {
	if strings.TrimSpace(localPath) == "" {
		return "", fmt.Errorf("localPath 不能为空")
	}
	absRoot, err := filepath.Abs(localPath)
	if err != nil {
		return "", fmt.Errorf("localPath 无效: %w", err)
	}
	for _, root := range roots {
		if pathWithinRoot(root, absRoot) {
			return absRoot, nil
		}
	}
	return "", fmt.Errorf("localPath 不在已配置的清理映射内: %s", localPath)
}

func validateCleanupRelPath(root, rel string) error {
	if err := validateCleanupRelPathAny(root, rel); err != nil {
		return err
	}
	full := filepath.Join(root, rel)
	if !strings.EqualFold(filepath.Ext(full), ".strm") {
		return fmt.Errorf("只允许清理 .strm 文件: %q", rel)
	}
	if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("拒绝清理符号链接: %q", rel)
	}
	return nil
}

// validateCleanupRelPathAny 只校验相对路径是否越界；补生成的目标可能由源文件
// 路径派生，允许非 .strm 扩展名。
func validateCleanupRelPathAny(root, rel string) error {
	if strings.TrimSpace(rel) == "" || filepath.IsAbs(rel) {
		return fmt.Errorf("relPath 必须是相对路径: %q", rel)
	}
	full := filepath.Join(root, rel)
	if !pathWithinRoot(root, full) {
		return fmt.Errorf("relPath 越出映射根目录: %q", rel)
	}
	return nil
}

func getDefaultScanRequestsFromSettings(settings *model.Settings) []MappingScanRequest {
	if settings == nil || !settings.LifeMonitor.Enabled {
		return nil
	}
	return CleanupMappings(settings)
}

// CleanupMappings 返回可用于清理扫描的路径映射（来自生活监控路径映射）。
// 与 getDefaultScanRequestsFromSettings 的区别：不要求监控已启用——
// 清理是独立动作，监控关着也可能需要清理历史残留。
func CleanupMappings(settings *model.Settings) []MappingScanRequest {
	if settings == nil {
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

// ScanCleanupMapping 扫描单个路径映射（供 TG 等非 HTTP 入口复用同一套扫描逻辑）。
// 调用方负责异步执行与超时控制：全量遍历云端目录可能耗时数分钟。
func ScanCleanupMapping(ctx context.Context, deps StrmCleanupDeps, req MappingScanRequest) (MappingScanResult, error) {
	if deps.SettingsStore == nil || deps.AccountStore == nil {
		return MappingScanResult{}, fmt.Errorf("cleanup deps 未初始化")
	}
	accounts := deps.AccountStore.List()
	accountMap := make(map[string]string, len(accounts))
	for _, acc := range accounts {
		accountMap[acc.Name] = acc.Cookie
	}
	if _, ok := accountMap[req.Account]; !ok {
		return MappingScanResult{}, fmt.Errorf("账号不存在: %s", req.Account)
	}
	return scanMapping(ctx, req, deps, accountMap), nil
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
		localStrmFiles := listLocalStrmFiles(m.LocalPath)
		result.LocalStrmCount = len(localStrmFiles)
		_, assocCount := countLocalByRoot(m.LocalPath)
		result.AssociatedFileCount = assocCount
		result.ErrorCode = "account_not_found"
		// 云端比对无法进行，但本地仍能扫出「引用了失效账号」的 STRM（改名/删除账号后的典型残留）
		mergeAccountMismatch(&result, m.LocalPath, localStrmFiles, accountMap)
		if len(result.StaleStrms) > 0 {
			result.Error = fmt.Sprintf("账号 %q 不存在或无 Cookie（云端比对已跳过）；已检出 %d 个 STRM 引用的账号已失效，可清理后用现有账号重建", m.Account, len(result.StaleStrms))
		} else {
			result.Error = fmt.Sprintf("账号 %q 不存在或无 Cookie（云端比对已跳过）", m.Account)
		}
		splitManagedStale(&result, deps)
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
		result.ErrorCode = "cloud_dir_failed"
		mergeAccountMismatch(&result, m.LocalPath, localStrmFiles, accountMap)
		if err.Error() == "parse dir id: nil" {
			result.Error = fmt.Sprintf("路径不存在或无权限：%s", m.CloudPath)
		} else {
			result.Error = fmt.Sprintf("获取云端目录失败：%v", err)
		}
		splitManagedStale(&result, deps)
		return result
	}

	// 网络扫描：递归抓取云端子树，构造与本地一致的带目录相对路径后再比对，
	// 否则带子目录的本地 strm（如 片名/电影.strm）会与扁平的云端列表对不上而被误判全部失效
	cloudItems := make([]cloudWalkItem, 0, 32)
	if err := walkCloudTree(ctx, client, cookie, cid, "", &cloudItems); err != nil {
		result.ErrorCode = "cloud_walk_failed"
		result.Error = fmt.Sprintf("list cloud files: %v", err)
		// 云端遍历失败也要做本地账号引用检查：账号改名/删除导致的坏 STRM 不依赖云端结果。
		mergeAccountMismatch(&result, m.LocalPath, localStrmFiles, accountMap)
		splitManagedStale(&result, deps)
		return result
	}
	result.RemoteFileCount = len(cloudItems)

	// cloudStrmSet：云端文件对应的 .strm 名集合
	// 云端相对路径（/ 分隔）→ 按生成侧规则换 .strm → 再做落盘清理（冒号转全角等）
	// → 统一为本地 OS 分隔符，与 listLocalStrmFiles 返回的本地相对路径逐字节对齐。
	// 必须走 cloudStrmRelKey（内部 SanitizeRelPath），只换扩展名会对不上含 ':' 的本地文件名。
	cloudStrmSet := make(map[string]struct{}, len(cloudItems))
	for _, it := range cloudItems {
		if it.IsDir {
			continue
		}
		// 关联文件（图片 / 字幕 / nfo）永远不生成 STRM，也不该参与比对：
		// 跳过它们，历史误生成的 backdrop.strm 之类才会被判为残留并被清理。
		if strmutil.IsRelatedAssetPath(it.RelPath) {
			continue
		}
		cloudStrmSet[cloudStrmRelKey(it.RelPath)] = struct{}{}
	}

	localSet := make(map[string]os.FileInfo)
	for name, info := range localStrmFiles {
		localSet[name] = info
	}

	for name, info := range localStrmFiles {
		if _, exists := cloudStrmSet[name]; !exists {
			content, truncated := readStrmHead(filepath.Join(m.LocalPath, name), 512)
			result.StaleStrms = append(result.StaleStrms, StaleStrm{
				RelPath:   name,
				Size:      info.Size(),
				Content:   content,
				Truncated: truncated,
				Reason:    "not_in_cloud",
			})
		}
	}

	for _, it := range cloudItems {
		if it.IsDir {
			continue
		}
		// 关联文件不生成 STRM（见 cloudStrmSet 同处说明）
		if strmutil.IsRelatedAssetPath(it.RelPath) {
			continue
		}
		strmRel := cloudStrmRelKey(it.RelPath)
		if _, exists := localSet[strmRel]; !exists {
			result.MissingStrms = append(result.MissingStrms, MissingStrm{
				RelPath:      strmRel,
				CloudRelPath: it.RelPath,
				PickCode:     it.PickCode,
				Size:         it.Size,
			})
		}
	}

	// 账号改名/删除后残留的 STRM：内容引用的账户已不存在。
	// 正常云端比对路径也必须合并，不能只在云端失败时才检出。
	mergeAccountMismatch(&result, m.LocalPath, localStrmFiles, accountMap)
	splitManagedStale(&result, deps)

	return result
}

// splitManagedStale 把“云端不存在”的本地 STRM 分成托管孤儿与未托管两类。
// SQLite 不可用时保持旧行为（全部视为 stale）；启用后未登记文件只展示、不进入删除候选。
func splitManagedStale(result *MappingScanResult, deps StrmCleanupDeps) {
	if result == nil || len(result.StaleStrms) == 0 || deps.SQLiteDB == nil || deps.ManagedStrm == nil {
		return
	}
	managed, err := db.ListManagedStrmsByRoot(deps.SQLiteDB, result.LocalPath)
	if err != nil {
		logger.S().Warnf("[strmCleanup] 读取托管索引失败（本次全部按未托管处理）: %v", err)
		managed = nil
	}
	kept := result.StaleStrms[:0]
	unmanaged := make([]StaleStrm, 0)
	for _, s := range result.StaleStrms {
		full := db.NormalizeManagedLocalPath(filepath.Join(result.LocalPath, s.RelPath))
		if _, ok := managed[full]; ok {
			kept = append(kept, s)
			continue
		}
		s.Reason = "unmanaged"
		unmanaged = append(unmanaged, s)
	}
	result.StaleStrms = kept
	result.UnmanagedStrms = unmanaged
	result.UnmanagedCount = len(unmanaged)
}

func scanMappingFromCache(m MappingScanRequest, entry *store.StrmCacheEntry, accountMap map[string]string) MappingScanResult {
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
		mergeAccountMismatch(&result, m.LocalPath, localFiles, accountMap)
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
				Reason:    "not_in_cloud",
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
	// 缓存路径同样做账号引用检查（本地内容即可判定，无需云端请求）。
	mergeAccountMismatch(&result, m.LocalPath, localFiles, accountMap)
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
	result := scanMappingFromCache(m, entry, accountMap)
	splitManagedStale(&result, deps)
	return result
}

// strmAccountFromContent 从 STRM 内容（播放 URL）中解析 account 查询参数。
// 非 URL、缺少 scheme 或没有该参数时返回空字符串（无法判定，不作为失效依据）。
func strmAccountFromContent(content string) string {
	s := strings.TrimSpace(content)
	if s == "" {
		return ""
	}
	// 只取第一行，容忍文件末尾换行
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return ""
	}
	return u.Query().Get("account")
}

// collectAccountMismatchStrms 扫描本地 STRM 内容，找出引用了「已不存在账号」的文件。
// validAccounts 为当前存在的账户名集合；返回按 RelPath 升序排列的失效项（reason=account_missing）。
// 成本：每个 STRM 读前 256 字节（播放 URL 约 150 字节内），仅解析 query，无网络访问。
func collectAccountMismatchStrms(localPath string, localFiles map[string]os.FileInfo, validAccounts map[string]struct{}) []StaleStrm {
	result := make([]StaleStrm, 0)
	for rel, info := range localFiles {
		content, truncated := readStrmHead(filepath.Join(localPath, rel), 256)
		if content == "" {
			continue
		}
		acc := strmAccountFromContent(content)
		if acc == "" {
			continue
		}
		if _, ok := validAccounts[acc]; ok {
			continue
		}
		result = append(result, StaleStrm{
			RelPath:   rel,
			Size:      info.Size(),
			Content:   content,
			Truncated: truncated,
			Reason:    "account_missing",
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RelPath < result[j].RelPath })
	return result
}

// mergeAccountMismatch 把「引用失效账号」的 STRM 合并进扫描结果（去重，账号失效原因优先），
// 并统一按 RelPath 排序。所有扫描路径（正常 / 云端失败 / 账号缺失 / 缓存）都调用它，
// 保证「账号改名/删除导致的坏 STRM」在任何情况下都能被扫出来。
func mergeAccountMismatch(result *MappingScanResult, localPath string, localFiles map[string]os.FileInfo, accountMap map[string]string) {
	// accountMap 为 nil 表示调用方无法提供「当前有效账户」集合（例如缓存扫描单测），
	// 此时无法判定账号是否失效，跳过以免误报。
	if len(localFiles) == 0 || accountMap == nil {
		return
	}
	valid := make(map[string]struct{}, len(accountMap))
	for name := range accountMap {
		valid[name] = struct{}{}
	}
	mismatch := collectAccountMismatchStrms(localPath, localFiles, valid)
	if len(mismatch) > 0 {
		index := make(map[string]int, len(result.StaleStrms))
		for i, s := range result.StaleStrms {
			index[s.RelPath] = i
		}
		for _, mm := range mismatch {
			if i, ok := index[mm.RelPath]; ok {
				// 双重失效：账号失效比「云端不存在」更可操作，覆盖标记
				result.StaleStrms[i].Reason = mm.Reason
				continue
			}
			result.StaleStrms = append(result.StaleStrms, mm)
		}
	}
	sort.Slice(result.StaleStrms, func(i, j int) bool { return result.StaleStrms[i].RelPath < result.StaleStrms[j].RelPath })
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
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			resp.Error = "读取配置失败"
			writeJSON(w, http.StatusInternalServerError, resp)
			return
		}
		if err := validateCleanupRequest(ExecuteRequest{
			Entries: []ExecuteEntry{{LocalPath: req.LocalPath, StaleRelPaths: []string{req.RelPath}}},
		}, settings, deps); err != nil {
			resp.Error = err.Error()
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

// cloudWalkItem 递归列云端时记录的扁平条目，带相对路径（/ 分隔）
type cloudWalkItem struct {
	Name     string
	RelPath  string
	IsDir    bool
	PickCode string
	Size     int64
}

// walkCloudTree 递归列出云端目录子树（从 cid 根出发），返回带目录相对路径的扁平条目。
// 目录下钻用目录自身的 CID；相对路径统一用 / 分隔，后续比对时再转回本地 OS 分隔符。
func walkCloudTree(ctx context.Context, client *client115.Client, cookie string, cid int64, relBase string, out *[]cloudWalkItem) error {
	offset := 0
	for {
		resp, err := client.FsFiles(ctx, strconv.FormatInt(cid, 10), 1000, offset, cookie)
		if err != nil {
			return fmt.Errorf("walk cloud dir cid=%d: %w", cid, err)
		}
		for i := range resp.Data {
			f := resp.Data[i]
			// 115 文件/目录名可含 '/'，拼入 RelPath 前必须结构转义，
			// 与生成侧（task/monitor 的 JoinCloudPath）同一规则，否则该文件的本地 STRM
			// 会因与云端名单对不上而被误判失效删除。Name 字段保留原始名供 pickcode 对账。
			rel := strmutil.JoinCloudPath(relBase, f.Name)
			*out = append(*out, cloudWalkItem{
				Name:     f.Name,
				RelPath:  rel,
				IsDir:    f.IsDir,
				PickCode: f.PickCode,
				Size:     f.Size,
			})
			if f.IsDir {
				subCID, ok := anyInt64(f.CID)
				if !ok || subCID <= 0 {
					continue
				}
				if err := walkCloudTree(ctx, client, cookie, subCID, rel, out); err != nil {
					return err
				}
			}
		}
		if len(resp.Data) < 1000 {
			break
		}
		offset += len(resp.Data)
		if offset >= resp.Count {
			break
		}
	}
	return nil
}

// anyInt64 将 JSON 里任意类型的 id/数字转为 int64
func anyInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	}
	return 0, false
}

// isRelatedMediaFile 判断文件是否为关联媒体信息文件（与删除白名单共用一套扩展名）
func isRelatedMediaFile(name string) bool {
	return strmutil.IsRelatedAssetPath(name)
}

// cleanupStrmFileName 将云端源文件名换算为对应的 .strm 文件名
// 与生成侧 task.getStrmFileName 保持同一规则（.iso 保留双扩展名），
// 保证网络扫描时"本地 .strm 是否存在对应云端文件"能正确匹配，避免误判全部失效
func cleanupStrmFileName(fileName string) string {
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

// cloudStrmRelKey 把云端相对路径换算成本地 STRM 相对路径（比对键）。
//
// 与生成侧 replaceRelPathExtToStrm 同规则：换扩展名 → SanitizeRelPath 逐段落盘清理
// （':' → '：'、'/' '\' → '_' 等）→ 当前平台 OS 分隔符。
// 两边必须逐字节一致：只换扩展名而不做落盘清理，含 ':' 的片名会被误判为「本地多余」而删除。
func cloudStrmRelKey(cloudRel string) string {
	return strmutil.SanitizeRelPath(cleanupStrmFileName(cloudRel))
}

// listLocalStrmFiles 返回本地 .strm 相对路径→文件信息（保持签名不变，避免 StaleStrm/MissingStrm 匹配逻辑改动）
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
			if info.Name() == strmutil.TrashDirName {
				return filepath.SkipDir
			}
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
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == strmutil.TrashDirName {
				return filepath.SkipDir
			}
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

// filterManagedStale 只保留「已托管且指纹未变」的待删项。
// 未登记/内容被改过的 STRM 一律从删除请求中剔除，防止前端伪造或手写文件被误删。
func filterManagedStale(ctx context.Context, body *ExecuteRequest, managed strm.ManagedRecorder) int {
	if managed == nil || body == nil {
		return 0
	}
	skipped := 0
	check := func(fullPath string) bool {
		pc, err := strmutil.ExtractPickcode(fullPath)
		if err != nil || pc == "" {
			skipped++
			return false
		}
		ok, verr := managed.VerifyFile(ctx, fullPath, pc)
		if verr != nil || !ok {
			skipped++
			return false
		}
		return true
	}
	for i := range body.Entries {
		entry := &body.Entries[i]
		kept := entry.StaleRelPaths[:0]
		for _, rel := range entry.StaleRelPaths {
			if check(filepath.Join(entry.LocalPath, rel)) {
				kept = append(kept, rel)
			}
		}
		entry.StaleRelPaths = kept
	}
	if body.ScanSummary != nil {
		for i := range body.ScanSummary.Mappings {
			m := &body.ScanSummary.Mappings[i]
			kept := m.StaleStrms[:0]
			for _, s := range m.StaleStrms {
				if check(filepath.Join(m.LocalPath, s.RelPath)) {
					kept = append(kept, s)
				}
			}
			m.StaleStrms = kept
		}
	}
	return skipped
}

func cleanupCandidateStats(body ExecuteRequest, action string) (total, totalLocal, dirs int) {
	seenDirs := make(map[string]struct{})
	add := func(root, rel string) {
		total++
		seenDirs[filepath.Dir(filepath.Join(root, rel))] = struct{}{}
	}
	if action == "delete_all" && body.ScanSummary != nil {
		for _, m := range body.ScanSummary.Mappings {
			n, _ := countLocalByRoot(m.LocalPath)
			totalLocal += n
			for _, s := range m.StaleStrms {
				add(m.LocalPath, s.RelPath)
			}
		}
		return total, totalLocal, len(seenDirs)
	}
	for _, entry := range body.Entries {
		for _, rel := range entry.StaleRelPaths {
			add(entry.LocalPath, rel)
		}
	}
	return total, totalLocal, len(seenDirs)
}

func purgeTrashForRequest(body ExecuteRequest) {
	roots := make(map[string]struct{})
	for _, entry := range body.Entries {
		if entry.LocalPath != "" {
			roots[entry.LocalPath] = struct{}{}
		}
	}
	if body.ScanSummary != nil {
		for _, m := range body.ScanSummary.Mappings {
			if m.LocalPath != "" {
				roots[m.LocalPath] = struct{}{}
			}
		}
	}
	for root := range roots {
		strmutil.PurgeTrashTree(root, 7*24*time.Hour)
	}
}

func cleanupCandidatePaths(body ExecuteRequest, action string) []string {
	paths := make([]string, 0)
	if action == "delete_all" && body.ScanSummary != nil {
		for _, m := range body.ScanSummary.Mappings {
			for _, s := range m.StaleStrms {
				paths = append(paths, filepath.Join(m.LocalPath, s.RelPath))
			}
		}
		return paths
	}
	for _, entry := range body.Entries {
		for _, rel := range entry.StaleRelPaths {
			paths = append(paths, filepath.Join(entry.LocalPath, rel))
		}
	}
	return paths
}

func executeCleanup(ctx context.Context, body ExecuteRequest, settings *model.Settings, deps StrmCleanupDeps, accountMap map[string]string) ExecuteResponse { //nolint:cyclop // complexity: 34
	resp := ExecuteResponse{
		DryRun:              body.DryRun,
		Errors:              []map[string]string{},
		RemovedEmptyDirs:    []string{},
		RemovedRelatedFiles: []string{},
	}

	// 删除前先做托管过滤：未托管/指纹变化的文件不进入阈值计算、待确认队列或删除循环。
	resp.SkippedUnmanaged = filterManagedStale(ctx, &body, deps.ManagedStrm)
	if !body.DryRun {
		purgeTrashForRequest(body)
	}

	// ====== 三重保护 + 二次确认（比例 + 绝对 + 目录数）======
	cleanup := settings.Cleanup
	maxAbs, maxRatio, maxDirs, stableThreshold := task.EffectiveCleanupLimits(cleanup)
	confirmMode := cleanup.ConfirmMode
	if confirmMode == "" {
		confirmMode = "none"
	}
	resp.AppliedMaxThreshold = maxAbs
	resp.AppliedStableThreshold = stableThreshold
	resp.AppliedConfirmMode = confirmMode

	action := body.Action
	if action == "" {
		action = "delete"
	}

	// 统计待删总数：delete_all 从扫描摘要统计，其余从选中条目统计。
	totalStale, totalLocal, dirs := cleanupCandidateStats(body, action)
	resp.TotalStaleCount = totalStale

	// DryRun 模式下不做阈值拦截，直接走模拟流程。
	// delete_all 使用完整三重保护（含本地总数比例）；手动选中项跳过比例、保留绝对/目录数保护。
	if !body.DryRun {
		verdict, reason := task.EvaluateCleanup(task.CleanupInput{
			Candidates: totalStale,
			TotalLocal: totalLocal,
			Dirs:       dirs,
			Cfg:        cleanup,
		})
		if verdict == task.CleanupProtected {
			resp.ThresholdError = fmt.Sprintf("%s，已拒绝执行。可在 settings.cleanup 调整 maxRatio/maxThreshold/maxDirs，或在 UI 重新筛选后分批删除", reason)
			logger.S().Warnf("[strmCleanup] 进入保护态，拒绝执行: %s", reason)
			return resp
		}
		if verdict == task.CleanupConfirm && confirmMode == "none" {
			logger.S().Infof("[strmCleanup] 待删 %d 个超过稳定阈值 %d（ratio上限 %.0f%%, abs上限 %d, dirs上限 %d），ConfirmMode=none 直接执行",
				totalStale, stableThreshold, maxRatio*100, maxAbs, maxDirs)
		}
	}

	// 稳定阈值拦截：超过 StableThreshold 且开启了二次确认 → 入队待删
	if !body.DryRun && totalStale > stableThreshold && confirmMode != "none" && deps.Interaction != nil {
		// 收集所有待删完整路径
		paths := cleanupCandidatePaths(body, action)
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
		deleteSelectedStale(ctx, body, &resp, &cleanup, deps.ManagedStrm)
	}

	// ====== delete_all / regenerate / delete_and_regenerate 分支 ======
	// 修复前 bug：原 delete_all 遍历 body.Entries（前端 delete_all 传 []），实际删除 0 个
	// 修复后：从 body.ScanSummary.Mappings 取真实失效/漏项路径
	switch action {
	case "delete_all":
		deleted, failed := deleteAllStaleFromScan(ctx, body, &resp, &cleanup, deps.ManagedStrm)
		resp.DeletedCount += deleted
		resp.DeletedAllCount = deleted
		resp.FailedCount += failed
	case "regenerate":
		regenCount, regenFailed := regenerateMissingStrms(ctx, body, settings, &resp, deps.ManagedStrm)
		resp.RegeneratedCount = regenCount
		resp.FailedCount += regenFailed
	case "delete_and_regenerate":
		// deleteSelectedStale 已在上方处理（只删选中条目），这里仅做补生成 + 汇总
		deleted := resp.DeletedCount
		delFailed := resp.FailedCount
		regenCount, regenFailed := regenerateMissingStrms(ctx, body, settings, &resp, deps.ManagedStrm)
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
// 删除前复核托管归属/指纹，移入回收站；未托管项跳过。
func deleteSelectedStale(ctx context.Context, body ExecuteRequest, resp *ExecuteResponse, cleanup *model.CleanupSettings, managed strm.ManagedRecorder) {
	for _, entry := range body.Entries {
		trashDir := ""
		if !body.DryRun {
			trashDir = strmutil.TrashSessionDir(entry.LocalPath)
		}
		for _, staleRelPath := range entry.StaleRelPaths {
			stalePath := filepath.Join(entry.LocalPath, staleRelPath)
			if managed != nil {
				pc, perr := strmutil.ExtractPickcode(stalePath)
				if perr != nil || pc == "" {
					resp.SkippedUnmanaged++
					continue
				}
				ok, verr := managed.VerifyFile(ctx, stalePath, pc)
				if verr != nil || !ok {
					resp.SkippedUnmanaged++
					logger.S().Warnf("[strmCleanup] 删除前复核未通过，保留 %s", stalePath)
					continue
				}
			}
			if body.DryRun {
				resp.DeletedCount++
				continue
			}
			// 阶段 1：把 STRM 文件移入回收站（RemoveStrm 默认 true）
			if cleanup.RemoveStrm {
				if dest, err := strmutil.MoveToTrash(stalePath, entry.LocalPath, trashDir); err != nil {
					resp.FailedCount++
					resp.Errors = append(resp.Errors, map[string]string{
						"path":  stalePath,
						"error": err.Error(),
					})
					logger.S().Warnf("[strmCleanup] trash %s failed: %v", stalePath, err)
				} else {
					resp.DeletedCount++
					if managed != nil {
						_ = managed.MarkDeleted(ctx, stalePath)
					}
					logger.S().Infof("[strmCleanup] stale strm moved to trash: %s -> %s", stalePath, dest)
				}
			}
			// 阶段 2：关联文件移入回收站（RemoveRelatedFiles 默认 false）
			if cleanup.RemoveRelatedFiles {
				moved := strmutil.MoveRelatedFilesToTrash(stalePath, entry.LocalPath, trashDir, strmutil.DefaultRelatedExts)
				resp.RemovedRelatedFiles = append(resp.RemovedRelatedFiles, moved...)
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
func deleteAllStaleFromScan(ctx context.Context, body ExecuteRequest, resp *ExecuteResponse, cleanup *model.CleanupSettings, managed strm.ManagedRecorder) (int, int) {
	if body.ScanSummary == nil {
		logger.S().Warnf("[strmCleanup] delete_all: ScanSummary 为空，无法定位失效 STRM")
		return 0, 0
	}
	deleted, failed := 0, 0
	for _, m := range body.ScanSummary.Mappings {
		trashDir := ""
		if !body.DryRun {
			trashDir = strmutil.TrashSessionDir(m.LocalPath)
		}
		for _, stale := range m.StaleStrms {
			stalePath := filepath.Join(m.LocalPath, stale.RelPath)
			if managed != nil {
				pc, perr := strmutil.ExtractPickcode(stalePath)
				if perr != nil || pc == "" {
					resp.SkippedUnmanaged++
					continue
				}
				ok, verr := managed.VerifyFile(ctx, stalePath, pc)
				if verr != nil || !ok {
					resp.SkippedUnmanaged++
					logger.S().Warnf("[strmCleanup] delete_all 删除前复核未通过，保留 %s", stalePath)
					continue
				}
			}
			if body.DryRun {
				deleted++
				continue
			}
			if cleanup.RemoveStrm {
				if dest, err := strmutil.MoveToTrash(stalePath, m.LocalPath, trashDir); err != nil {
					failed++
					resp.Errors = append(resp.Errors, map[string]string{
						"path":  stalePath,
						"error": err.Error(),
					})
					logger.S().Warnf("[strmCleanup] delete_all trash %s failed: %v", stalePath, err)
				} else {
					deleted++
					if managed != nil {
						_ = managed.MarkDeleted(ctx, stalePath)
					}
					logger.S().Infof("[strmCleanup] delete_all moved to trash: %s -> %s", stalePath, dest)
				}
			}
			if cleanup.RemoveRelatedFiles {
				moved := strmutil.MoveRelatedFilesToTrash(stalePath, m.LocalPath, trashDir, strmutil.DefaultRelatedExts)
				resp.RemovedRelatedFiles = append(resp.RemovedRelatedFiles, moved...)
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
func regenerateMissingStrms(ctx context.Context, body ExecuteRequest, settings *model.Settings, resp *ExecuteResponse, managed strm.ManagedRecorder) (int, int) {
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
			// 统一走 strmutil.WriteStrmFile：内容未变时跳过写盘，
			// 避免补建动作无谓刷新 mtime 触发 Emby 假重扫
			if _, err := strmutil.WriteStrmFile(strmPath, content); err != nil {
				failed++
				resp.Errors = append(resp.Errors, map[string]string{
					"path":  strmPath,
					"error": err.Error(),
				})
				logger.S().Warnf("[strmCleanup] regenerate write failed: %s: %v", strmPath, err)
				continue
			}
			if managed != nil {
				if merr := managed.Record(ctx, strm.ManagedInput{
					LocalPath: strmPath, Account: m.Account, CloudPath: miss.CloudRelPath,
					Pickcode: miss.PickCode, Content: content, Source: "cleanup-rebuild",
				}); merr != nil {
					logger.S().Debugf("[strmCleanup] regenerate 托管登记失败 %s: %v", strmPath, merr)
				}
			}
			regen++
			regenPaths = append(regenPaths, miss.RelPath)
			logger.S().Infof("[strmCleanup] regenerate created: %s", strmPath)
		}
	}
	resp.RegeneratedPaths = append(resp.RegeneratedPaths, regenPaths...)
	return regen, failed
}

// buildStrmForMissing 为漏项构造 STRM 完整路径和 URL 内容。
// 文件名策略：去掉原扩展名，追加 .strm（对齐 strmutil.StrmFileName）。
// URL 内容由 strm.BuildContent 统一生成，与全量任务 / 生活事件逐字节一致。
func buildStrmForMissing(m MappingScanResult, miss MissingStrm, settings *model.Settings) (string, string, error) {
	// 1. STRM 完整路径：localPath + relPath 目录部分 + stem + ".strm"
	relDir := filepath.Dir(miss.RelPath)
	base := filepath.Base(miss.RelPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	strmName := stem + ".strm"
	strmPath := filepath.Join(m.LocalPath, relDir, strmName)

	// 2. 前缀：清理链路只有全局配置可用，交给统一解析器处理
	//    （空 / 回环 / 0.0.0.0 → 本机内网 IP；仍不可用则报错，不再写 127.0.0.1 死链）
	prefix := settings.StrmPrefix
	if settings.Strm.StrmUrlTemplate == "" {
		resolved, _ := strm.ResolveStrmPrefix([]string{settings.StrmPrefix}, 0)
		if resolved == "" {
			return strmPath, "", fmt.Errorf("%w：未配置 STRM 前缀且无法探测内网地址，请在设置中填写可访问地址",
				strm.ErrUnusablePrefix)
		}
		prefix = resolved
	}

	// 3. 源文件的云端形态路径。
	//    优先用扫描带回来的 CloudRelPath（'/' 分隔、保留原扩展名 .mkv/.iso）；
	//    旧版 ScanSummary 没有该字段时，退回由 RelPath 反推（此时拿不到真实扩展名，
	//    只能得到 xxx.strm，属于降级行为）。
	srcRel := miss.CloudRelPath
	if srcRel == "" {
		srcRel = strmutil.LocalRelToCloudRel(miss.RelPath)
	}
	// 云端文件名用于 file_name / {filename} / {ext} / {stem} 等变量
	srcName := path.Base(srcRel)
	cloudPath := strings.TrimRight(m.CloudPath, "/") + "/" + srcRel

	content, err := strm.BuildContent(strm.ContentInput{
		Template:  settings.Strm.StrmUrlTemplate,
		Prefix:    prefix,
		Account:   m.Account,
		PickCode:  miss.PickCode,
		FileName:  srcName,
		CloudPath: cloudPath,
		// MissingStrm 无 file_id（云端比对只带 pickcode），模板引用 {file_id} 时为空
		FileID:        "",
		DisableEncode: !settings.EnablePathEncoding,
	})
	if err != nil {
		return strmPath, "", err
	}
	return strmPath, content, nil
}

// removeEmptyDirs 递归清理 root 下的空目录（批量删除后调用）
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

// ==================== 待删批次执行（二次确认后调用）====================

// executePendingBatch 执行单个待删批次（用户二次确认通过后调用）
// 复用 executeCleanup 的三阶段删除逻辑（RemoveStrm/RemoveRelatedFiles/RemoveEmptyDirs）；
// 删除前再次复核托管归属与文件指纹，并移入回收站而非硬删。
func executePendingBatch(ctx context.Context, batch CleanupBatch, managed strm.ManagedRecorder) ExecuteResponse {
	resp := ExecuteResponse{
		Errors:              []map[string]string{},
		RemovedEmptyDirs:    []string{},
		RemovedRelatedFiles: []string{},
		PendingRequestID:    batch.RequestID,
		TotalStaleCount:     batch.PathCount,
	}
	trashDirs := make(map[string]string)

	for _, p := range batch.Paths {
		pickcode, _ := strmutil.ExtractPickcode(p)
		if managed != nil {
			ok, verr := managed.VerifyFile(ctx, p, pickcode)
			if verr != nil || !ok {
				resp.SkippedUnmanaged++
				logger.S().Warnf("[strmCleanup] pending batch 删除前复核未通过，保留 %s", p)
				continue
			}
		}
		root := filepath.Dir(p)
		trashDir := trashDirs[root]
		if trashDir == "" {
			trashDir = strmutil.TrashSessionDir(root)
			trashDirs[root] = trashDir
		}
		// 阶段 1：把 STRM 文件移入回收站
		if batch.RemoveStrm {
			dest, terr := strmutil.MoveToTrash(p, root, trashDir)
			if terr != nil {
				resp.FailedCount++
				resp.Errors = append(resp.Errors, map[string]string{"path": p, "error": terr.Error()})
				logger.S().Warnf("[strmCleanup] pending batch trash %s failed: %v", p, terr)
			} else {
				if dest != "" {
					resp.DeletedCount++
				}
				if managed != nil {
					_ = managed.MarkDeleted(ctx, p)
				}
				logger.S().Infof("[strmCleanup] pending batch moved to trash: %s -> %s", p, dest)
			}
		}
		// 阶段 2：关联文件移入回收站
		if batch.RemoveRelatedFiles {
			moved := strmutil.MoveRelatedFilesToTrash(p, root, trashDir, strmutil.DefaultRelatedExts)
			resp.RemovedRelatedFiles = append(resp.RemovedRelatedFiles, moved...)
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
		resp := executePendingBatch(r.Context(), *batch, deps.ManagedStrm)
		resp.DurationMs = time.Since(start).Milliseconds()
		logger.S().Infof("[strmCleanup] pending batch %s executed: deleted=%d failed=%d duration=%dms",
			body.RequestID, resp.DeletedCount, resp.FailedCount, resp.DurationMs)
		httpx.WriteJson(w, http.StatusOK, resp)
	}
}
