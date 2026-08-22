package monitor

import (
	"errors"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// ======================================================================
// Phase 1.1 RED  —— 事件决策对象 / 计数器拆分 纯逻辑测试
// ======================================================================

// TestPollCounts_Add_Semantic 验证新的 PollCounts 累积语义
//  进入 entered +1
//  effective/skipped/error 只能一个 +1（互斥）
//  dedup 不计入 entered
func TestPollCounts_Add_Semantic(t *testing.T) {
	var c PollCounts
	// 进入 1 件，最终副作用成功
	c.AddEntered()
	c.AddEffective()
	// 进入 1 件，跳过（mapping miss、扩展名过滤等）
	c.AddEntered()
	c.AddSkipped("无路径映射")
	// 进入 1 件，处理失败
	c.AddEntered()
	err := errors.New("pickcode api 403")
	c.AddError(err)
	// 重复 5 件
	c.AddDuplicates(5)

	if c.Entered != 3 {
		t.Fatalf("Entered want 3 got %d", c.Entered)
	}
	if c.Effective != 1 {
		t.Fatalf("Effective want 1 got %d", c.Effective)
	}
	if c.Skipped != 1 {
		t.Fatalf("Skipped want 1 got %d", c.Skipped)
	}
	if c.Errors != 1 {
		t.Fatalf("Errors want 1 got %d", c.Errors)
	}
	if c.Duplicates != 5 {
		t.Fatalf("Duplicates want 5 got %d", c.Duplicates)
	}
	if c.LastError == nil || !errors.Is(c.LastError, err) {
		t.Fatalf("LastError want %v got %v", err, c.LastError)
	}
	if c.SkipReasons[0] != "无路径映射" {
		t.Fatalf("SkipReasons[0] want '无路径映射' got %q", c.SkipReasons[0])
	}
	if c.Summary() == "" {
		t.Fatalf("Summary want non-empty got empty")
	}
	// 日志字符串里必须包含 entered/effective/skipped/error/duplicate 这 5 个关键指标
	s := c.Summary()
	for _, kw := range []string{"entered", "effective", "skipped", "errors", "duplicates"} {
		if !strings.Contains(s, kw) {
			t.Fatalf("Summary missing keyword %q: %s", kw, s)
		}
	}
}

// TestEventDecision_Validation 验证 EventDecision 的决策辅助字段语义
func TestEventDecision_Validation(t *testing.T) {
	d := EventDecision{}
	// 默认 should_act=true 不对，显式设置 SkipReason 后 ShouldAct 应为 false
	d.SkipReason = "未启用 create 模式"
	if d.ShouldAct() {
		t.Fatalf("SkipReason non-empty → ShouldAct must be false")
	}
	d.SkipReason = ""
	// 空结构：MappingType 默认 "" → !MappingTypeMedia，ShouldAct 返回 false（不能在没有任何映射/路径时盲操作）
	if d.ShouldAct() {
		t.Fatalf("empty EventDecision (MappingType empty, cloudPath empty, pickcode invalid) → ShouldAct must be false")
	}
	// 构造一个 fully-valid create 事件：MappingType=MEDIA + valid pickcode + path非空 + type启用
	d = EventDecision{
		EventKind:        "create",
		MappingType:      MappingTypeMedia,
		CloudPath:        "电影/foo/a.mkv",
		IsValidPickcode:  true,
		EventTypeEnabled: true,
	}
	if !d.ShouldAct() {
		t.Fatalf("fully-valid create decision → ShouldAct must be true")
	}
	// new_folder 不要求 pickcode
	d = EventDecision{
		EventKind:        "new_folder",
		MappingType:      MappingTypeMedia,
		CloudPath:        "电影/foo",
		IsValidPickcode:  false,
		EventTypeEnabled: true,
	}
	if !d.ShouldAct() {
		t.Fatalf("new_folder even with invalid pickcode → ShouldAct must be true")
	}
	// MappingType 三态
	d = EventDecision{MappingType: MappingTypeTransfer}
	if !strings.Contains(d.String(), "TRANSFER") {
		t.Fatalf("MappingType=TRANSFER stringify missing: %s", d.String())
	}
}

// ======================================================================
// Phase 1.3 RED  —— normalizeCloudPath / matchPathMapping 三态
// ======================================================================

// TestNormalizeCloudPath_Cases 覆盖斜杠、空串、各种前后缀场景
func TestNormalizeCloudPath_Cases(t *testing.T) {
	cases := []struct {
		in  string
		out string
	}{
		{"电影/爱丽丝梦游仙境/a.mkv", "电影/爱丽丝梦游仙境/a.mkv"},
		{"/电影/爱丽丝梦游仙境/a.mkv", "电影/爱丽丝梦游仙境/a.mkv"},      // 前导 / 去掉
		{"电影/爱丽丝梦游仙境/a.mkv/", "电影/爱丽丝梦游仙境/a.mkv"},      // 尾随 / 去掉
		{"/电影/爱丽丝梦游仙境/a.mkv/", "电影/爱丽丝梦游仙境/a.mkv"},     // 两侧都去
		{"//电影//a.mkv//", "电影/a.mkv"},                            // 重复斜杠也折叠（等价 normalize）
		{"", ""},
		{"/", ""},
		{"///", ""},
		{" / a / b / ", "a / b"},                                     // 保留中间空格但清首尾斜杠
	}
	for i, c := range cases {
		got := normalizeCloudPath(c.in)
		if got != c.out {
			t.Fatalf("case %d: normalizeCloudPath(%q)=%q, want %q",
				i, c.in, got, c.out)
		}
	}
}

// TestMatchPathMapping_NormalizesBothSides 验证 mapping/cloudPath 两侧都 normalize
//  并且 MappingType 输出正确
func TestMatchPathMapping_NormalizesBothSides(t *testing.T) {
	mappings := []model.MonitorPathMapping{
		{CloudPath: "/电影/", LocalPath: `C:\Videos\电影`, Account: "acc1", MappingType: "media"},
		{CloudPath: "整理", LocalPath: `C:\Videos\_transfer`, Account: "acc1", MappingType: "transfer"},
		{CloudPath: "/未识别目录", LocalPath: `C:\Videos\_unrec`, Account: "acc1", MappingType: "unrecognized"},
	}

	// 命中 MEDIA 前缀（cloudPath 无前导，mapping 有前导都能命中）
	result := decideMappingResult("acc1", "电影/爱丽丝梦游仙境/a.mkv", mappings)
	if result == nil {
		t.Fatalf("expected MEDIA mapping match, got nil")
	}
	if result.MappingType != MappingTypeMedia {
		t.Fatalf("want MappingType MEDIA got %s", result.MappingType)
	}
	// 根目录精确匹配 + 前导斜杠 normalize
	result = decideMappingResult("acc1", "/电影", mappings)
	if result == nil {
		t.Fatalf("exact '/电影' should match MEDIA, got nil")
	}
	if result.MappingType != MappingTypeMedia {
		t.Fatalf("want MEDIA exact match got %s", result.MappingType)
	}
	// 命中 TRANSFER（mapping 无点，传入有点）
	result = decideMappingResult("acc1", "整理/sub/a.mkv", mappings)
	if result == nil {
		t.Fatalf("expected TRANSFER mapping match, got nil")
	}
	if result.MappingType != MappingTypeTransfer {
		t.Fatalf("want TRANSFER got %s", result.MappingType)
	}
	// 命中 UNRECOGNIZED 前缀
	result = decideMappingResult("acc1", "/未识别目录/xxx.mkv", mappings)
	if result == nil {
		t.Fatalf("expected UNRECOGNIZED match, got nil")
	}
	if result.MappingType != MappingTypeUnrecognized {
		t.Fatalf("want UNRECOGNIZED got %s", result.MappingType)
	}
	// 账号不匹配 → MappingType=NONE
	result = decideMappingResult("acc2", "电影/foo.mkv", mappings)
	if result == nil {
		t.Fatalf("account mismatch should also return result struct (MappingType=NONE), got nil")
	}
	if result.Matched {
		t.Fatalf("account mismatch → Matched=false, got Matched=true result=%+v", result)
	}
	// 无 mappings → NONE 映射
	result = decideMappingResult("acc1", "随便一个/路径", nil)
	if result == nil {
		// 我们的新实现要返回 NONE 空结构，而不是 nil，方便上层统一记录
		t.Fatalf("empty mappings should return MappingType=NONE result, got nil")
	}
	if result.MappingType != MappingTypeNone {
		t.Fatalf("no mappings → MappingType=NONE, got %s", result.MappingType)
	}
	// 未知 mapping_type 字符串，默认 fallback 为 MEDIA（兼容旧配置）
	legacy := []model.MonitorPathMapping{
		{CloudPath: "老配置/没有MappingType字段", LocalPath: `/tmp/old`, Account: "acc1"},
	}
	result = decideMappingResult("acc1", "老配置/没有MappingType字段/a.mkv", legacy)
	if result == nil || result.MappingType != MappingTypeMedia {
		t.Fatalf("legacy mapping without MappingType field should fallback to MEDIA, got %+v", result)
	}
}

// ======================================================================
// Phase 1.2 RED 辅助：事件类型判断函数的单元测试（纯逻辑，无依赖）
// ======================================================================

func TestEventTypeBelongsTo(t *testing.T) {
	cases := []struct {
		typ  int
		set  string
		want bool
	}{
		{1, "create", true},
		{2, "create", true},
		{14, "create", true},
		{18, "create", true},
		{23, "create", true},
		{17, "create", true}, // new_folder 也算 create 家族，便于模式判断
		{5, "move", true},
		{6, "move", true},
		{20, "rename", true},
		{24, "rename", true},
		{22, "delete", true},
		{99, "create", false},
		{1, "move", false},
		{22, "rename", false},
	}
	for i, c := range cases {
		got := eventTypeBelongsTo(c.typ, c.set)
		if got != c.want {
			t.Fatalf("case %d typ=%d set=%s: got %v want %v",
				i, c.typ, c.set, got, c.want)
		}
	}
}

func TestIsNewFolderOnly(t *testing.T) {
	if !isNewFolderOnly(17) {
		t.Fatalf("type=17 must be new_folder only")
	}
	if isNewFolderOnly(2) {
		t.Fatalf("type=2 not new_folder only")
	}
}
