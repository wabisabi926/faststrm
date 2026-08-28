package monitor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ======================================================================
// Phase 1.1  —— 计数器拆分：PollCounts / EventDecision
// ======================================================================

// PollCounts 拆分后的一轮拉取统计指标（替代原有过饱和 processedCount）
//
//	Entered    : 去重后、实际进入 processEvent 的事件数（≠ 原 processedCount，后者连跳过也算"处理过"）
//	Effective  : 真的发生副作用（生成/删/挪 / 仅写 DB 如 type=17 / appendLog 成功记录处理结果 ok=true 的次数）
//	Skipped    : 明确 skip（mapping miss / 模式关闭 / 扩展名过滤 / pickcode 无效 等）
//	Errors     : processEvent 返回 err 的次数
//	Duplicates : dedup 命中次数（未进入 processEvent）
type PollCounts struct {
	Entered     int
	Effective   int
	Skipped     int
	Errors      int
	Duplicates  int
	LastError   error
	SkipReasons []string
}

// AddEntered 进入事件+1（dedup 之后）
func (p *PollCounts) AddEntered() { p.Entered++ }

// AddEffective 副作用成功+1（或 appendLog 成功 ok=true）
func (p *PollCounts) AddEffective() { p.Effective++ }

// AddSkipped 记录一次明确 skip
func (p *PollCounts) AddSkipped(reason string) {
	p.Skipped++
	// 只保留前 20 条 skip 原因，避免 memory 膨胀
	if len(p.SkipReasons) < 20 {
		p.SkipReasons = append(p.SkipReasons, reason)
	}
}

// AddError 记录一次处理失败（记录最后一个 err 供 UI 展示）
func (p *PollCounts) AddError(err error) {
	p.Errors++
	p.LastError = err
}

// AddDuplicates 累加 dedup 命中
func (p *PollCounts) AddDuplicates(n int) { p.Duplicates += n }

// Summary 生成友好的 INFO 级一行摘要
func (p PollCounts) Summary() string {
	return fmt.Sprintf("entered=%d effective=%d skipped=%d errors=%d duplicates=%d",
		p.Entered, p.Effective, p.Skipped, p.Errors, p.Duplicates)
}

// ---------------- EventDecision ----------------

// MappingType 路径映射类型（三态：媒体 / 整理 / 未识别 / 空）
type MappingType string

const (
	MappingTypeMedia        MappingType = "MEDIA"
	MappingTypeTransfer     MappingType = "TRANSFER"
	MappingTypeUnrecognized MappingType = "UNRECOGNIZED"
	MappingTypeNone         MappingType = "NONE"
)

// parseMappingType 兼容 legacy（空或其他字符串默认 MEDIA）
func parseMappingType(s string) MappingType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "transfer":
		return MappingTypeTransfer
	case "unrecognized", "unrec":
		return MappingTypeUnrecognized
	case "", "media":
		return MappingTypeMedia
	default:
		return MappingTypeMedia
	}
}

// EventDecision 单次事件的决策结果（统一日志口径，替代现在散落在各处的 Debug）
type EventDecision struct {
	EventKind          string      // create/delete/move/rename/new_folder/unknown
	Account            string      // 账号名
	EventTypeNumber    int         // 115 原始 type 数字
	FileID             string      // 事件对象 file_id（反查 DB/API 用）
	ParentID           string      // 事件对象 parent_id（路径解析关键，调试用）
	FileName           string      // 事件文件名/目录名
	CloudPath          string      // 解析后的云路径（已 normalize，空=解析失败）
	CloudPathSource    string      // CACHE/DB/ATTR_API/ANCESTORS_API/UNKNOWN（便于日志判断三级降级落在哪一层）
	OldCloudPath       string      // 仅 move/rename
	NewCloudPath       string      // 仅 move/rename
	MatchedCloudPrefix string      // matchPathMapping 命中的前缀（未命中则空）
	MatchedLocalBase   string      // 映射本地根（未命中则空）
	MappingType        MappingType // 映射分类（NONE=无映射）
	InMappingOld       bool
	InMappingNew       bool
	MediaCategory      int    // event.FileCategory：0=目录 1=文件 其它=图片之类
	FileExtension      string // 小写，无点
	IsMediaFile        bool
	PickCode           string
	IsValidPickcode    bool
	FileSize           int64
	UnderMinFileSize   bool // size<MinFileSize 命中黑名单等
	UnderBlacklist     bool
	EventTypeEnabled   bool   // EventTypesSettings 开关（create/remove/rename/move 是否启用）
	SkipReason         string // 非空则 ShouldAct=false，INFO 级会打这个
}

// ShouldAct 决策之后是否真的调用副作用 handler
func (d EventDecision) ShouldAct() bool {
	if strings.TrimSpace(d.SkipReason) != "" {
		return false
	}
	if !d.EventTypeEnabled {
		return false
	}
	if d.MappingType != MappingTypeMedia {
		// TRANSFER / UNRECOGNIZED / NONE：默认不走到 create/delete handler（它们有自己的 transfer/rename 专用流 Phase2+）
		return false
	}
	if d.CloudPath == "" {
		return false
	}
	if !d.IsValidPickcode && d.EventKind != "new_folder" && d.EventKind != "delete" {
		// delete/new_folder 不要求 pickcode
		return false
	}
	return true
}

// String 序列化 EVENT_DECIDE INFO 日志（一行就能定位为什么不做）
func (d EventDecision) String() string {
	parts := []string{
		fmt.Sprintf("kind=%s", d.EventKind),
		fmt.Sprintf("type=%d", d.EventTypeNumber),
		fmt.Sprintf("account=%s", d.Account),
		fmt.Sprintf("fid=%s", d.FileID),
		fmt.Sprintf("pid=%s", d.ParentID),
		fmt.Sprintf("name=%q", d.FileName),
		fmt.Sprintf("category=%d", d.MediaCategory),
		fmt.Sprintf("cloudPath=%q", d.CloudPath),
		fmt.Sprintf("pathSrc=%s", d.CloudPathSource),
	}
	if d.OldCloudPath != "" || d.NewCloudPath != "" {
		parts = append(parts,
			fmt.Sprintf("oldCloudPath=%q", d.OldCloudPath),
			fmt.Sprintf("newCloudPath=%q", d.NewCloudPath),
			fmt.Sprintf("inOld=%v inNew=%v", d.InMappingOld, d.InMappingNew))
	}
	parts = append(parts,
		fmt.Sprintf("mappingType=%s", d.MappingType),
		fmt.Sprintf("prefix=%q", d.MatchedCloudPrefix),
		fmt.Sprintf("localBase=%q", d.MatchedLocalBase),
		fmt.Sprintf("ext=%q media=%v", d.FileExtension, d.IsMediaFile),
		fmt.Sprintf("pick=%q valid=%v", d.PickCode, d.IsValidPickcode),
		fmt.Sprintf("size=%d underMin=%v underBl=%v", d.FileSize, d.UnderMinFileSize, d.UnderBlacklist),
		fmt.Sprintf("eventTypeEnabled=%v", d.EventTypeEnabled),
		fmt.Sprintf("skipReason=%q", d.SkipReason),
		fmt.Sprintf("shouldAct=%v", d.ShouldAct()),
	)
	return strings.Join(parts, " ")
}

// ======================================================================
// Phase 1.3 —— normalizeCloudPath / 升级后 matchPathMapping / MappingType 字段
// ======================================================================

var multiSlashRe = regexp.MustCompile(`/+`)

// normalizeCloudPath 统一云路径格式：
//
//  1. 去首尾斜杠
//  2. 连续斜杠折叠
//  3. 空串/纯斜杠全部归为 ""
func normalizeCloudPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 统一用 "/"
	s = filepath.ToSlash(s)
	s = strings.Trim(s, "/")
	s = multiSlashRe.ReplaceAllString(s, "/")
	return strings.TrimSpace(s)
}

// eventTypeBelongsTo 纯逻辑：将 115 原始 type 归类到 create/move/rename/delete 家族
// 对齐 MP BEHAVIOR_TYPE_TO_NAME
func eventTypeBelongsTo(typ int, family string) bool {
	switch strings.ToLower(family) {
	case "create":
		switch typ {
		case 1, 2, 14, 17, 18, 23: // 上传图/上传文件/接收文件/新建目录/复制目录/复制文件
			return true
		}
	case "move":
		return typ == 5 || typ == 6
	case "rename":
		return typ == 20 || typ == 24
	case "delete", "remove":
		return typ == 22
	}
	return false
}

// isNewFolderOnly 当事件 type=17(new_folder) 时返回 true：只写 DB 不生成 STRM
func isNewFolderOnly(typ int) bool { return typ == 17 }

// eventKindLabel 返回 create/delete/move/rename/new_folder/unknown 文本标签
func eventKindLabel(typ int) string {
	switch {
	case eventTypeBelongsTo(typ, "create"):
		if typ == 17 {
			return "new_folder"
		}
		return "create"
	case eventTypeBelongsTo(typ, "delete"):
		return "delete"
	case eventTypeBelongsTo(typ, "move"):
		return "move"
	case eventTypeBelongsTo(typ, "rename"):
		return "rename"
	}
	return "unknown"
}

// upgradedPathMapping 包装 MonitorPathMapping，增加 Phase 1.3 新增的 MappingType 字段
//
//	（暂时不往 model/settings.go 加结构体字段以免 JSON 序列化老配置冲突，先在解析层兼容）
type upgradedPathMapping struct {
	Account     string
	CloudPath   string
	LocalPath   string
	MappingType MappingType
}

// parseUpgradedMappings 从 legacy []MonitorPathMapping 解析，支持 JSON 中用户自定义的
//
//	"mappingType": "transfer" / "unrecognized" / "media"
//	未配置或缺省 → MEDIA
func parseUpgradedMappings(raw []model.MonitorPathMapping) []upgradedPathMapping {
	out := make([]upgradedPathMapping, 0, len(raw))
	for _, m := range raw {
		out = append(out, upgradedPathMapping{
			Account:     m.Account,
			CloudPath:   normalizeCloudPath(m.CloudPath),
			LocalPath:   m.LocalPath,
			MappingType: parseMappingType(m.MappingType),
		})
	}
	return out
}

// pathMappingResult matchPathMapping 返回值（nil = 没有任何 mappings 配置 / Account 没命中 mappings）
type pathMappingResult struct {
	MappingType MappingType
	CloudPrefix string // normalize 后的命中云路径前缀（不含首尾斜杠）
	LocalPath   string // 用户配置的本地根目录
	Account     string
	// matchedMapping 指针：仅在命中具体一项时非空
	Matched bool
}

// decideMappingResult 升级版决策：在 legacy matchPathMapping 之外再做 MappingType / normalize 归类，
// 并对 legacy matchPathMapping 返回 nil 的场景统一返回 MappingType=NONE 结构，便于 EVENT_DECIDE 日志。
//
//  1. 先调用 legacy matchPathMapping(cloudPath, mappings, account)；
//  2. 再结合 monitorPathMapping.MappingType 字段（若为空则视为 MEDIA）；
//  3. 返回标准化 MappingType / CloudPrefix / LocalPath。
func decideMappingResult(account, cloudPath string, mappings []model.MonitorPathMapping) *pathMappingResult {
	legacy := matchPathMapping(cloudPath, mappings, account)
	upgraded := parseUpgradedMappings(mappings)
	if legacy != nil {
		// 找 legacy 命中的 upgraded 项（按 cloudPath+localPath+account 对齐）
		var chosen *upgradedPathMapping
		legacyPrefix := normalizeCloudPath(legacy.cloudPath)
		for i := range upgraded {
			u := &upgraded[i]
			if u.Account != "" && !strings.EqualFold(strings.TrimSpace(u.Account), strings.TrimSpace(account)) {
				continue
			}
			if normalizeCloudPath(u.CloudPath) == legacyPrefix {
				chosen = u
				break
			}
		}
		mtype := MappingTypeMedia
		if chosen != nil {
			mtype = chosen.MappingType
		}
		return &pathMappingResult{
			MappingType: mtype,
			CloudPrefix: legacyPrefix,
			LocalPath:   legacy.localPath,
			Account:     account,
			Matched:     true,
		}
	}
	// legacy 未命中：检查是否属于 TRANSFER / UNRECOGNIZED（prefix 规则跟 MEDIA 一样，但 mappingType 不同）
	normalizedCloud := normalizeCloudPath(cloudPath)
	var (
		prefixBest *upgradedPathMapping
		prefixLen  int
		exactMatch *upgradedPathMapping
	)
	for i := range upgraded {
		u := &upgraded[i]
		if u.Account != "" && !strings.EqualFold(strings.TrimSpace(u.Account), strings.TrimSpace(account)) {
			continue
		}
		if u.CloudPath == normalizedCloud && exactMatch == nil {
			exactMatch = u
		}
		if strings.HasPrefix(normalizedCloud, u.CloudPath+"/") && len(u.CloudPath) > prefixLen {
			prefixBest = u
			prefixLen = len(u.CloudPath)
		}
	}
	var found *upgradedPathMapping
	switch {
	case exactMatch != nil:
		found = exactMatch
	case prefixBest != nil:
		found = prefixBest
	}
	if found != nil {
		return &pathMappingResult{
			MappingType: found.MappingType,
			CloudPrefix: found.CloudPath,
			LocalPath:   found.LocalPath,
			Account:     found.Account,
			Matched:     true,
		}
	}
	// 全部 miss：映射配置为空也返回 NONE（而不是 nil）
	return &pathMappingResult{MappingType: MappingTypeNone, Matched: false, Account: account}
}

// _ 占位：保证 decideMappingResult 被引用（linter 友好）
var _ = decideMappingResult

// ======================================================================
// Phase 1.1 —— 新增：processEvent 开头做决策并打 INFO 级 EVENT_DECIDE 日志
//   （此处只放决策辅助函数；真正接入 processEvent 的 switch 代码在 monitor.go / event_handler.go 中改动）
// ======================================================================

// validatePickcode 纯逻辑 pickcode 校验（沿用现有 isValidPickcode 行为，此处再抽一遍避免对 handleCreateEvent 私有依赖）
func validatePickcode(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 10 || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// mediaExtensionMatch 纯逻辑：扩展名集合（配置值，可能带点、可能大小写）判定；
//
//	空集合 + includeAll=true → 全部视为媒体（用于用户未配置时兼容默认）
func mediaExtensionMatch(fileName string, extensions []string) (extNoDot string, ok bool) {
	extNoDot = strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if len(extensions) == 0 {
		// 空集合 → 走默认（调用方会用 model.DefaultStrmExtensions 兜底）；此处判 false，让调用方自己补默认
		return extNoDot, false
	}
	norm := make(map[string]struct{}, len(extensions))
	for _, e := range extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		e = strings.TrimPrefix(e, ".")
		if e != "" {
			norm[e] = struct{}{}
		}
	}
	_, ok = norm[extNoDot]
	return extNoDot, ok
}

// blacklistMatch 纯逻辑：黑名单 glob 匹配
func blacklistMatch(fileName string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	lower := strings.ToLower(fileName)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		matched, err := filepath.Match(p, lower)
		if err != nil {
			continue
		}
		if matched {
			return true
		}
		// 兜底：patterns 只是关键词时也按 Contains 判
		if !strings.ContainsAny(p, "*?[") {
			if strings.Contains(lower, p) {
				return true
			}
		}
	}
	return false
}

// ======================================================================
// Phase 1.2 辅助 —— Write-Ahead DB Entry 封装
// ======================================================================

// buildWriteAheadEntry 把事件 + cloudPath + 决策 → FilePathEntry
//
//	注意：调用方要确保 cloudPath 已 normalize（空串则不写 DB 返回 nil, false）
func buildWriteAheadEntry(event client115.LifeEventItem, cloudPath, pickCode string) (db.FilePathEntry, bool) {
	if strings.TrimSpace(event.FileID) == "" || strings.TrimSpace(cloudPath) == "" {
		return db.FilePathEntry{}, false
	}
	return db.FilePathEntry{
		FileID:     event.FileID,
		Path:       normalizeCloudPath(cloudPath),
		FileName:   event.FileName,
		ParentID:   event.ParentID,
		PickCode:   strings.ToLower(strings.TrimSpace(pickCode)),
		UpdateTime: event.UpdateTime,
	}, true
}

// writeAheadFilePath 真正写 DB；失败只 Warn 不阻塞后续 handler（反查写回尽力而为）
func (m *Monitor) writeAheadFilePath(ctx context.Context, account string, entry db.FilePathEntry) {
	if m.sqliteDB == nil {
		return
	}
	if err := db.UpsertFilePathEntry(m.sqliteDB, account, entry); err != nil {
		logger.S().Warnf("[Monitor] Write-Ahead DB upsert 失败 account=%s file=%s path=%q: %v",
			account, entry.FileID, entry.Path, err)
	}
}

// tryEventTypeEnabled 检查某类事件是否启用（create/remove/rename/move 按 EventTypesSettings）
//
//	type=17(new_folder) 视为"写入型记录"，只要 EventTypes.Create 开就视为启用（写 DB，无副作用）
func tryEventTypeEnabled(types model.EventTypesSettings, eventTypeNumber int) bool {
	switch {
	case eventTypeBelongsTo(eventTypeNumber, "create"):
		return types.Create
	case eventTypeBelongsTo(eventTypeNumber, "remove"):
		return types.Remove
	case eventTypeBelongsTo(eventTypeNumber, "move"):
		return types.Move
	case eventTypeBelongsTo(eventTypeNumber, "rename"):
		return types.Rename
	}
	return false
}

// ======================================================================
// Phase 1.1 —— PollCounts 通过 context 下沉到 processEvent
// ======================================================================

type pollCountsCtxKeyT struct{}

var pollCountsCtxKey = pollCountsCtxKeyT{}

// WithPollCounts 把 *PollCounts 挂到 context（pollOnce 入口处调用）
func WithPollCounts(ctx context.Context, c *PollCounts) context.Context {
	return context.WithValue(ctx, pollCountsCtxKey, c)
}

func pollCountsFromCtx(ctx context.Context) (*PollCounts, bool) {
	if ctx == nil {
		return nil, false
	}
	v, ok := ctx.Value(pollCountsCtxKey).(*PollCounts)
	return v, ok
}

// pollCountsAddEffective 累计 effective=1（ctx 无 counts 则 no-op）
func pollCountsAddEffective(ctx context.Context) {
	if c, ok := pollCountsFromCtx(ctx); ok {
		c.AddEffective()
	}
}

// pollCountsAddSkipped 累计 skipped=1 并记录 reason
func pollCountsAddSkipped(ctx context.Context, reason string) {
	if c, ok := pollCountsFromCtx(ctx); ok {
		c.AddSkipped(reason)
	}
}

// ======================================================================
// Phase 1.2 —— 事件决策 + Write-Ahead 接入点（processEvent 内部调用）
// ======================================================================

// preProcessEvent 承担：
//
//  1. 路径 normalize + ResolvePath 的 "/unknown/" 兜底（改成空串+error）
//
//  2. 统一调用 decideMappingResult()
//
//  3. 计算扩展名、黑名单、pickcode、最小文件大小、EventTypeEnabled 等
//
//  4. 生成 EventDecision 对象并**立即打 INFO 级 EVENT_DECIDE 日志**
//
//  5. Write-Ahead DB（调用 buildWriteAheadEntry + writeAheadFilePath）
//
//  6. 如果 type=17(new_folder) 且 mapping 命中 → 直接 return（不生成 STRM），caller 视为 effective
//
//     返回 (decision, handled)：handled=true 表示 preProcessEvent 已经"消化"了这个事件
//     （如 new_folder 只写 DB 不进 handler），caller 不再走原 switch handler。
func (m *Monitor) preProcessEvent(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	rawCloudPath string,
	config model.LifeMonitorSettings,
) (EventDecision, bool) {
	return m.preProcessEventWithSource(ctx, account, event, rawCloudPath, "API_ANCESTORS", config)
}

func (m *Monitor) preProcessEventWithSource( //nolint:cyclop // complexity: 34
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	rawCloudPath string,
	cloudPathSourceIn string,
	config model.LifeMonitorSettings,
) (EventDecision, bool) {
	kind := eventKindLabel(event.Type)

	// —— 路径 normalize & /unknown/ 清理
	cloudPath := normalizeCloudPath(rawCloudPath)
	// legacy：rawCloudPath 如果是 /unknown 前缀视为解析失败
	if strings.HasPrefix(strings.ToLower(normalizeCloudPath("/"+strings.Trim(rawCloudPath, "/"))), "unknown") {
		cloudPath = ""
	}
	cloudPathSource := strings.TrimSpace(cloudPathSourceIn)
	if cloudPathSource == "" {
		cloudPathSource = "UNKNOWN"
	}
	if cloudPath == "" {
		// 空路径：保持调用方 source 信息（用于诊断是 DB_REJECT 后没解出、还是 API 解出空）
		if cloudPathSource == "API_ANCESTORS" || cloudPathSource == "EVENT_RAW" {
			cloudPathSource = "UNKNOWN"
		}
	}

	// —— 扩展名 / 黑名单 / pickcode / 文件大小
	ext, extOK := mediaExtensionMatch(event.FileName, model.DefaultStrmExtensions)
	// 如果用户 settings.StrmExtensions 非空（通过 config 传不到 monitor里，Phase 2 会修）
	// 这里先用默认，后面 settingsFn 会覆盖。为保持测试独立，Phase 1 先用默认兜底。
	pick := strings.ToLower(strings.TrimSpace(event.PickCode))
	validPick := validatePickcode(pick)
	underMin := false
	if config.MinFileSize > 0 && event.FileCategory != 0 && event.FileSize > 0 && event.FileSize < config.MinFileSize {
		underMin = true
	}
	underBl := blacklistMatch(event.FileName, config.StrmGenerateBlacklist)
	typeEnabled := tryEventTypeEnabled(config.EventTypes, event.Type)

	// —— 路径映射：新版本 decideMappingResult（总是非 nil）
	mr := decideMappingResult(account, cloudPath, config.PathMappings)

	// 计算 skipReason（优先级顺序：先错误/未开启/路径解析失败，再映射/扩展名/pickcode/大小/黑名单）
	var skipReason string
	switch {
	case !typeEnabled:
		skipReason = "event_type_disabled_" + kind
	case cloudPath == "":
		skipReason = "cloud_path_unresolved"
	case mr.MappingType == MappingTypeNone:
		skipReason = "no_path_mapping"
	case mr.MappingType == MappingTypeTransfer:
		skipReason = "mapping_transfer_Phase2+_not_yet_handled"
	case mr.MappingType == MappingTypeUnrecognized:
		skipReason = "mapping_unrecognized"
	case mr.MappingType != MappingTypeMedia:
		skipReason = "mapping_type_other_" + string(mr.MappingType)
	case kind != "new_folder" && kind != "delete" && !validPick:
		skipReason = "invalid_pickcode"
	case kind != "new_folder" && kind != "delete" && event.FileCategory != 0 && !extOK:
		skipReason = "not_media_extension_" + ext
	case underMin:
		skipReason = "under_min_file_size"
	case underBl:
		skipReason = "blacklist_hit"
	}

	decision := EventDecision{
		EventKind:          kind,
		Account:            account,
		EventTypeNumber:    event.Type,
		FileID:             event.FileID,
		ParentID:           event.ParentID,
		FileName:           event.FileName,
		CloudPath:          cloudPath,
		CloudPathSource:    cloudPathSource,
		MatchedCloudPrefix: mr.CloudPrefix,
		MatchedLocalBase:   mr.LocalPath,
		MappingType:        mr.MappingType,
		MediaCategory:      event.FileCategory,
		FileExtension:      ext,
		IsMediaFile:        extOK,
		PickCode:           pick,
		IsValidPickcode:    validPick,
		FileSize:           event.FileSize,
		UnderMinFileSize:   underMin,
		UnderBlacklist:     underBl,
		EventTypeEnabled:   typeEnabled,
		SkipReason:         skipReason,
	}
	// —— 永远 INFO 级：诊断第一！
	logger.S().Infof("[Monitor] EVENT_DECIDE %s", decision.String())

	// —— Write-Ahead DB：无论 ShouldAct 与否，只要 cloudPath+file_id 有就写（反查前置）
	if entry, ok := buildWriteAheadEntry(event, cloudPath, pick); ok {
		m.writeAheadFilePath(ctx, account, entry)
	}

	// —— new_folder type=17 专用分支：命中 MEDIA → 写 folders 表 + 记一条 success=true 的 lifeLog，然后"消化掉"
	if isNewFolderOnly(event.Type) {
		if mr.MappingType == MappingTypeMedia && cloudPath != "" {
			// P0-2: 文件夹路径写入 folders 表（对齐参考项目 process_life_dir_item）
			if m.sqliteDB != nil && event.FileID != "" {
				fid := event.FileID
				pid := event.ParentID
				if pid == "" {
					pid = "0"
				}
				if err := db.UpsertFolderEntry(m.sqliteDB, account, db.FilePathEntry{
					FileID:     fid,
					Path:       cloudPath,
					FileName:   event.FileName,
					ParentID:   pid,
					UpdateTime: time.Now().Unix(),
				}); err != nil {
					logger.S().Warnf("[Monitor] type=17 folders 表写入失败 fid=%s: %v", fid, err)
				}
			}
			m.appendLog(ctx, account, "new_folder", true, cloudPath, mr.LocalPath,
				"folders 表已写入 (type=17 new_folder，不生成 STRM)")
			return decision, true // handled=true，caller 直接视为 effective
		}
		// 未命中 MEDIA：依然视为"已处理"（但作为 skipped 处理，caller 负责 AddSkipped）
		if skipReason == "" {
			skipReason = "new_folder_not_in_media_mapping"
			decision.SkipReason = skipReason
			// 重打一次修正后的 EVENT_DECIDE（最小成本，避免后续判断逻辑分支漂移）
			logger.S().Infof("[Monitor] EVENT_DECIDE_corrected %s", decision.String())
		}
		m.appendLog(ctx, account, "new_folder", false, cloudPath, mr.LocalPath, fmt.Sprintf("跳过: %s", skipReason))
		return decision, true // 仍然"消化掉"，让 caller 按 skipped 计
	}

	return decision, false
}

// makeWriteAheadDecision_ForTest 仅用于 Phase 1.2 单元测试：等价于调用 preProcessEvent 并返回 (decision, handled)
//
//	（Monitor 上挂这个 method 仅为了测试可访问，生产代码同样使用 preProcessEvent。）
func (m *Monitor) makeWriteAheadDecision_ForTest(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	rawCloudPath string,
	config model.LifeMonitorSettings,
) (EventDecision, bool) {
	return m.preProcessEvent(ctx, account, event, rawCloudPath, config)
}

// isCloudPathInMediaMapping 判断 cloudPath 是否在媒体映射范围内
// 对齐参考项目 PathUtils.get_media_path(monitor_life_paths, file_path)
func isCloudPathInMediaMapping(cloudPath string, mappings []model.MonitorPathMapping, account string) bool {
	np := normalizeCloudPath(cloudPath)
	if np == "" {
		return false
	}
	mr := decideMappingResult(account, np, mappings)
	return mr != nil && mr.MappingType == MappingTypeMedia && mr.Matched
}
