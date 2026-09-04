package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/task"
)

// ============ 1. ptrBool helper (form path) ============
func TestPtrBool_AllVariants(t *testing.T) {
	cases := []struct {
		in   string
		want *bool
	}{
		// nil 三态：空串=未传
		{"", nil},
		// → true
		{"on", ptrBool("true")},
		{"On", ptrBool("true")},
		{"ON", ptrBool("true")},
		{"true", ptrBool("true")},
		{"True", ptrBool("true")},
		{"TRUE", ptrBool("true")},
		{"1", ptrBool("true")},
		{"yes", ptrBool("true")},
		{"checked", ptrBool("true")},
		// → false
		{"off", ptrBool("false")},
		{"Off", ptrBool("false")},
		{"false", ptrBool("false")},
		{"False", ptrBool("false")},
		{"FALSE", ptrBool("false")},
		{"0", ptrBool("false")},
		{"no", ptrBool("false")},
		{"unchecked", ptrBool("false")},
		{"随机乱码", ptrBool("false")},
	}
	for _, c := range cases {
		got := ptrBool(c.in)
		if c.want == nil {
			if got != nil {
				t.Fatalf("ptrBool(%q) = %v, want nil", c.in, *got)
			}
			continue
		}
		if got == nil {
			t.Fatalf("ptrBool(%q) = nil, want %v", c.in, *c.want)
		}
		if (*got) != (*c.want) {
			t.Fatalf("ptrBool(%q) = %v, want %v", c.in, *got, *c.want)
		}
	}
}

// ============ 2. JSON 解码：axios PUT 发 bool → Go *bool ============
func TestUpsertTaskRequest_JSONDecode_BoolFields(t *testing.T) {
	// 2.1 显式 true
	bodyT := `{"name":"t1","removeExtraFiles":true,"enablePathEncoding":true,"enabled":true}`
	var reqT UpsertTaskRequest
	if err := json.Unmarshal([]byte(bodyT), &reqT); err != nil {
		t.Fatalf("unmarshal true err=%v", err)
	}
	if reqT.RemoveExtra == nil || !*reqT.RemoveExtra {
		t.Fatalf("RemoveExtra true got nil/%v", reqT.RemoveExtra)
	}
	if reqT.EnableEnc == nil || !*reqT.EnableEnc {
		t.Fatalf("EnableEnc true got nil/%v", reqT.EnableEnc)
	}
	if reqT.Enabled == nil || !*reqT.Enabled {
		t.Fatalf("Enabled true got nil/%v", reqT.Enabled)
	}

	// 2.2 显式 false
	bodyF := `{"name":"t2","removeExtraFiles":false,"enablePathEncoding":false,"enabled":false}`
	var reqF UpsertTaskRequest
	if err := json.Unmarshal([]byte(bodyF), &reqF); err != nil {
		t.Fatalf("unmarshal false err=%v", err)
	}
	if reqF.RemoveExtra == nil || *reqF.RemoveExtra {
		t.Fatalf("RemoveExtra false got nil/%v", reqF.RemoveExtra)
	}
	if reqF.EnableEnc == nil || *reqF.EnableEnc {
		t.Fatalf("EnableEnc false got nil/%v", reqF.EnableEnc)
	}
	if reqF.Enabled == nil || *reqF.Enabled {
		t.Fatalf("Enabled false got nil/%v", reqF.Enabled)
	}

	// 2.3 缺字段 → 必须 nil（三态关键！）
	bodyN := `{"name":"t3"}`
	var reqN UpsertTaskRequest
	if err := json.Unmarshal([]byte(bodyN), &reqN); err != nil {
		t.Fatalf("unmarshal empty err=%v", err)
	}
	if reqN.RemoveExtra != nil {
		t.Fatalf("RemoveExtra missing = %v, want nil", *reqN.RemoveExtra)
	}
	if reqN.EnableEnc != nil {
		t.Fatalf("EnableEnc missing = %v, want nil", *reqN.EnableEnc)
	}
	if reqN.Enabled != nil {
		t.Fatalf("Enabled missing = %v, want nil", *reqN.Enabled)
	}
}

// ============ 3. toTask：4 条覆盖"保留 / 翻转 / 新建"组合 ============
func TestToTask_BoolMatrix(t *testing.T) {
	type Row struct {
		name      string
		existing  *task.Task // 可能 nil（新建）
		reqExtra  *bool      // RemoveExtra
		reqEnc    *bool      // EnableEnc
		reqSched  *bool      // Enabled (when ScheduleMode=interval set)
		wantExtra bool
		wantEnc   bool
		wantSched bool // Schedule.Enabled
	}
	tT, tF := true, false

	existingSched := &task.TaskSchedule{Mode: "daily", Time: "03:00", Enabled: true}
	rows := []Row{
		// Bug case A：用户截图——existing=true + 前端没改（req nil）→ 必须保留 true（Schedule 也一并保留）
		{"A_existingTrue_nil_keepTrue",
			&task.Task{RemoveExtraFiles: true, EnablePathEncoding: true, Account: "a", OriginPath: "/o", TargetPath: "/t", Schedule: existingSched},
			nil, nil, nil,
			true, true, false /* 保留 existing.Schedule，wantSched 不比较 */},
		// Bug case B：existing=false + 前端没改 → 保留 false
		{"B_existingFalse_nil_keepFalse",
			&task.Task{RemoveExtraFiles: false, EnablePathEncoding: false, Account: "a", OriginPath: "/o", TargetPath: "/t", Schedule: existingSched},
			nil, nil, nil,
			false, false, false},
		// Explicit false over existing true：用户主动取消勾选 → 变 false
		{"C_existingTrue_explicitFalse_override",
			&task.Task{RemoveExtraFiles: true, EnablePathEncoding: true, Account: "a", OriginPath: "/o", TargetPath: "/t", Schedule: existingSched},
			&tF, &tF, nil,
			false, false, false},
		// Explicit true over existing false：用户主动勾选 → 变 true
		{"D_existingFalse_explicitTrue_override",
			&task.Task{RemoveExtraFiles: false, EnablePathEncoding: false, Account: "a", OriginPath: "/o", TargetPath: "/t", Schedule: existingSched},
			&tT, &tT, nil,
			true, true, false},
		// 新建 POST：全传 true → 全部 true
		{"E_create_allTrue",
			nil, &tT, &tT, &tT,
			true, true, true /* interval 新建，Enabled 用传入 */},
		// 新建 POST：全缺（nil）→ 默认 false
		{"F_create_allNil_defaultFalse",
			nil, nil, nil, nil,
			false, false, false /* interval 新建，Enabled 缺 → false */},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			req := &UpsertTaskRequest{
				Name:        "n",
				RemoveExtra: r.reqExtra,
				EnableEnc:   r.reqEnc,
			}
			// Schedule 分支：row E/F 触发 interval→创建新 Schedule；A-D 留空走保留 existing
			if r.reqSched != nil || (r.existing == nil) {
				req.ScheduleMode = "interval"
				req.ScheduleValue = "10"
				req.Enabled = r.reqSched // nil 时 F 新建 → 默认 false；E=*true→true
			}
			got := req.toTask(r.existing)
			if got.RemoveExtraFiles != r.wantExtra {
				t.Fatalf("RemoveExtraFiles=%v want %v", got.RemoveExtraFiles, r.wantExtra)
			}
			if got.EnablePathEncoding != r.wantEnc {
				t.Fatalf("EnablePathEncoding=%v want %v", got.EnablePathEncoding, r.wantEnc)
			}
			// Schedule.Enabled check（A/B/C/D existing 有 Schedule 就保留真实值；新建的按 req.Enabled/false）
			if r.existing != nil && r.existing.Schedule != nil && req.ScheduleMode == "" {
				// A-D 没改调度，Schedule 应保留 existing
				if got.Schedule == nil {
					t.Fatalf("Schedule unexpectedly nil (should preserve existing)")
				}
				// 不对 Schedule.Enabled 断言（existing.Schedule.Enabled 依赖测试数据），仅确认 Schedule 存在非 nil
			} else {
				// E/F 新建 interval 模式
				if got.Schedule == nil {
					t.Fatalf("Schedule should be created for interval mode")
				}
				if got.Schedule.Enabled != r.wantSched {
					t.Fatalf("Schedule.Enabled=%v want %v (req.Enabled=%v)",
						got.Schedule.Enabled, r.wantSched, r.reqSched)
				}
			}
		})
	}
}

// ============ 4. fillUpsertFromBody 集成：真实 HTTP Request JSON & form ============
func TestFillUpsertFromBody_RealRequests(t *testing.T) {
	// 4.1 JSON PUT：三个 bool 显式 true
	jsonBody := `{"name":"json-true","account":"acc","originPath":"/o","targetPath":"/t","removeExtraFiles":true,"enablePathEncoding":true,"enabled":true}`
	r1 := httptest.NewRequest("PUT", "/api/task", bytes.NewBufferString(jsonBody))
	r1.Header.Set("Content-Type", "application/json")
	req1 := &UpsertTaskRequest{}
	fillUpsertFromBody(r1, req1)
	if req1.RemoveExtra == nil || !*req1.RemoveExtra {
		t.Fatalf("JSON RemoveExtra want true")
	}
	if req1.EnableEnc == nil || !*req1.EnableEnc {
		t.Fatalf("JSON EnableEnc want true")
	}
	if req1.Enabled == nil || !*req1.Enabled {
		t.Fatalf("JSON Enabled want true")
	}

	// 4.2 JSON PUT：三个 bool 显式 false
	jsonF := `{"name":"json-false","account":"acc","originPath":"/o","targetPath":"/t","removeExtraFiles":false,"enablePathEncoding":false,"enabled":false}`
	r2 := httptest.NewRequest("PUT", "/api/task", bytes.NewBufferString(jsonF))
	r2.Header.Set("Content-Type", "application/json")
	req2 := &UpsertTaskRequest{}
	fillUpsertFromBody(r2, req2)
	if req2.RemoveExtra == nil || *req2.RemoveExtra {
		t.Fatalf("JSON RemoveExtra want false")
	}
	if req2.EnableEnc == nil || *req2.EnableEnc {
		t.Fatalf("JSON EnableEnc want false")
	}
	if req2.Enabled == nil || *req2.Enabled {
		t.Fatalf("JSON Enabled want false")
	}

	// 4.3 JSON PUT：编辑任务没改动 bool 字段 → 三个缺省 → 全部 nil（关键！）
	jsonN := `{"name":"json-nil","account":"acc","originPath":"/o","targetPath":"/t"}`
	r3 := httptest.NewRequest("PUT", "/api/task", bytes.NewBufferString(jsonN))
	r3.Header.Set("Content-Type", "application/json")
	req3 := &UpsertTaskRequest{}
	fillUpsertFromBody(r3, req3)
	if req3.RemoveExtra != nil {
		t.Fatalf("JSON RemoveExtra missing want nil, got %v", *req3.RemoveExtra)
	}
	if req3.EnableEnc != nil {
		t.Fatalf("JSON EnableEnc missing want nil, got %v", *req3.EnableEnc)
	}
	if req3.Enabled != nil {
		t.Fatalf("JSON Enabled missing want nil, got %v", *req3.Enabled)
	}

	// 4.4 form POST：checkbox on / 缺
	form := url.Values{}
	form.Set("name", "form-ok")
	form.Set("account", "acc")
	form.Set("originPath", "/o")
	form.Set("targetPath", "/t")
	form.Set("removeExtraFiles", "on")      // checkbox on
	form.Set("enablePathEncoding", "false") // explicit false string
	// Enabled 缺
	r4 := httptest.NewRequest("POST", "/api/task", strings.NewReader(form.Encode()))
	r4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req4 := &UpsertTaskRequest{}
	fillUpsertFromBody(r4, req4)
	if req4.RemoveExtra == nil || !*req4.RemoveExtra {
		t.Fatalf("form RemoveExtra on want true, got %v", req4.RemoveExtra)
	}
	if req4.EnableEnc == nil || *req4.EnableEnc {
		t.Fatalf("form EnableEnc false want false, got %v", req4.EnableEnc)
	}
	if req4.Enabled != nil {
		t.Fatalf("form Enabled missing want nil, got %v", *req4.Enabled)
	}

	// 4.5 multipart/form-data：checkbox checked=on（浏览器默认）
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "mp")
	_ = w.WriteField("account", "acc")
	_ = w.WriteField("originPath", "/o")
	_ = w.WriteField("targetPath", "/t")
	_ = w.WriteField("removeExtraFiles", "checked")
	_ = w.WriteField("enablePathEncoding", "0")
	_ = w.WriteField("enabled", "1")
	_ = w.Close()
	r5 := httptest.NewRequest("POST", "/api/task", &buf)
	r5.Header.Set("Content-Type", w.FormDataContentType())
	req5 := &UpsertTaskRequest{}
	fillUpsertFromBody(r5, req5)
	if req5.RemoveExtra == nil || !*req5.RemoveExtra {
		t.Fatalf("mp RemoveExtra checked want true")
	}
	if req5.EnableEnc == nil || *req5.EnableEnc {
		t.Fatalf("mp EnableEnc 0 want false")
	}
	if req5.Enabled == nil || !*req5.Enabled {
		t.Fatalf("mp Enabled 1 want true")
	}
}
