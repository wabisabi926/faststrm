package server

// 阶段 7.2 E2E 流程测试：完整用户流程端到端验证
// 覆盖计划中的核心路径：
//   流程A：登录 → 账号 CRUD → 建任务配置 → 启动任务(无真实115) → 任务历史查询
//   流程B：SSE 进度推送 → 任务日志查询
//   流程C：Emby webhook 事件 → LifeMonitor 查询
//   流程D：错误路径（无 JWT、错误参数、未配置外部服务）

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/server/middleware"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
)

// e2eFixture E2E 测试公共夹具
type e2eFixture struct {
	t             *testing.T
	cfgDir        string
	dataDir       string
	salt          string
	accountStore  *store.AccountStore
	tasksStore    *store.TasksStore
	settingsStore *store.SettingsStore
	stateMgr      *runtime.StateManager
	issuer        *auth.TokenIssuer
	token         string
	sqliteDB      *sql.DB
	taskHistory   *db.TaskHistoryRepo
	lifeEventRepo *db.LifeEventRepo
	lifeEventLog  *db.LifeEventLogRepo
	filePathRepo  *db.FilePathRepo
	taskRuntime   *task.Runtime
	scheduler     *task.Scheduler
}

// setupE2E 创建 E2E 测试夹具
func setupE2E(t *testing.T) *e2eFixture {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfgDir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	salt := "e2e_flow_test_salt_32b_pad_____"

	// 配置全局 config（login handler 依赖）
	settings := model.DefaultSettings()
	settings.InternalToken = "e2e-jwt-secret-key-32bytes!!"
	cfg := &config.AppConfig{
		Salt:     salt,
		Admin:    &model.AppConfig{Username: "admin", Password: pwdcrypto.HashPassword(salt, "admin")},
		Settings: settings,
	}
	config.SetForTest(cfg)

	f := &e2eFixture{
		t:             t,
		cfgDir:        cfgDir,
		dataDir:       dataDir,
		salt:          salt,
		accountStore:  (func() *store.AccountStore {
		as, err := store.NewAccountStore(salt, cfgDir)
		if err != nil { t.Fatalf("NewAccountStore: %v", err) }
		return as
	})(),
		tasksStore:    store.NewTasksStore(cfgDir),
		settingsStore: store.NewSettingsStore(salt, cfgDir),
		stateMgr:      runtime.Init(cfgDir),
		issuer:        auth.NewTokenIssuer([]byte(settings.InternalToken)),
	}

	// 登录获取 token
	_, raw := doReq(handler.Login(f.issuer), "POST", "/api/auth/login",
		map[string]string{"username": "admin", "password": "admin"}, "")
	m := decodeAsMap(t, raw)
	f.token, _ = m["token"].(string)
	if f.token == "" {
		t.Fatal("login failed: no token")
	}

	// SQLite
	sqliteDB, err := db.OpenNew(dataDir)
	if err != nil {
		t.Fatalf("db.OpenNew: %v", err)
	}
	f.sqliteDB = sqliteDB
	f.taskHistory, err = db.NewTaskHistoryRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewTaskHistoryRepo: %v", err)
	}
	f.lifeEventRepo, err = db.NewLifeEventRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewLifeEventRepo: %v", err)
	}
	f.lifeEventLog, err = db.NewLifeEventLogRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewLifeEventLogRepo: %v", err)
	}
	f.filePathRepo = db.NewFilePathRepo(sqliteDB)

	f.taskRuntime = task.GetRuntime()
	f.scheduler = task.GetScheduler()

	t.Cleanup(func() {
		if sqliteDB != nil {
			sqliteDB.Close()
		}
	})
	return f
}

// TestE2E_FlowA_AccountTaskHistory 流程A：登录→账号→任务→历史
func TestE2E_FlowA_AccountTaskHistory(t *testing.T) {
	f := setupE2E(t)

	t.Run("step1_create_account", func(t *testing.T) {
		code, raw := doReq(handler.CreateAccount(f.accountStore), "POST", "/api/account",
			map[string]string{
				"accountType": "115",
				"name":        "e2e-115",
				"cookie":      "UID=12345; CID=abc; SEID=xyz",
			}, f.token)
		if code != 201 {
			t.Fatalf("want 201, got %d, body=%s", code, raw)
		}
	})

	t.Run("step2_verify_account_in_list", func(t *testing.T) {
		code, raw := doReq(handler.ListAccounts(f.accountStore), "GET", "/api/account", nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		var arr []map[string]any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(arr) != 1 {
			t.Fatalf("want 1 account, got %d", len(arr))
		}
		if arr[0]["name"] != "e2e-115" {
			t.Errorf("name want 'e2e-115', got %v", arr[0]["name"])
		}
		// cookie 必须是明文（解密后）
		if arr[0]["cookie"] != "UID=12345; CID=abc; SEID=xyz" {
			t.Errorf("cookie mismatch: %v", arr[0]["cookie"])
		}
	})

	t.Run("step3_save_task_config", func(t *testing.T) {
		// 保存任务配置到 tasks.json
		tasks := []task.Task{
			{
				ID:         "e2e-task-001",
				Name:       "E2E 测试任务",
				Account:    "e2e-115",
				AccountType: "115",
				OriginPath: "/movies",
				TargetPath: "/data/strm/movies",
				StrmPrefix: "http://127.0.0.1:8090/api/strm",
				Schedule: &task.TaskSchedule{
					Enabled: false,
					Mode:    "manual",
				},
				CreatedAt: time.Now().UnixMilli(),
			},
		}
		if err := f.tasksStore.SaveTasks(tasks); err != nil {
			t.Fatalf("SaveTasks: %v", err)
		}
	})

	t.Run("step4_list_tasks_with_config", func(t *testing.T) {
		taskDeps := handler.TaskHandlerDeps{
			TasksStore: f.tasksStore,
			Runtime:    f.taskRuntime,
			Scheduler:  f.scheduler,
		}
		code, raw := doReq(handler.HandleListTasks(taskDeps), "GET", "/api/tasks", nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d, body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		tasks, ok := m["tasks"].([]any)
		if !ok {
			t.Fatalf("tasks must be array, got %T", m["tasks"])
		}
		if len(tasks) != 1 {
			t.Fatalf("want 1 task, got %d", len(tasks))
		}
		taskObj := tasks[0].(map[string]any)
		if taskObj["id"] != "e2e-task-001" {
			t.Errorf("task id want 'e2e-task-001', got %v", taskObj["id"])
		}
		// scheduler 字段必须存在
		if _, ok := m["scheduler"]; !ok {
			t.Error("missing 'scheduler' field in tasks response")
		}
	})

	t.Run("step5_start_task_missing_id_400", func(t *testing.T) {
		// 不传 taskId → 400
		taskDeps := handler.TaskHandlerDeps{
			TasksStore: f.tasksStore,
			Runtime:    f.taskRuntime,
			Scheduler:  f.scheduler,
		}
		code, raw := doReq(handler.HandleStartTask(taskDeps), "POST", "/api/startTask",
			map[string]string{}, f.token)
		if code != 400 {
			t.Fatalf("want 400 for missing taskId, got %d body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "reason", m["reason"])
		if m["reason"] != "missing_task_id" {
			t.Errorf("reason want 'missing_task_id', got %v", m["reason"])
		}
	})

	t.Run("step6_cancel_nonexistent_task", func(t *testing.T) {
		// 取消不存在的任务 → 200 + reason: not_running
		taskDeps := handler.TaskHandlerDeps{
			TasksStore: f.tasksStore,
			Runtime:    f.taskRuntime,
			Scheduler:  f.scheduler,
		}
		code, raw := doReq(handler.HandleCancelTask(taskDeps), "POST", "/api/cancelTask",
			map[string]string{"taskId": "nonexistent"}, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		m := decodeAsMap(t, raw)
		if m["reason"] != "not_running" {
			t.Errorf("reason want 'not_running', got %v", m["reason"])
		}
	})

	t.Run("step7_task_history_empty", func(t *testing.T) {
		// 初始任务历史为空
		code, raw := doReq(handler.HandleTaskHistory(handler.TaskHistoryDeps{Repo: f.taskHistory}),
			"GET", "/api/taskHistory?limit=10", nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		m := decodeAsMap(t, raw)
		// items 可能为 nil（Go nil slice 序列化为 null）；验证为 null 或空数组均可
		items := m["items"]
		if items != nil {
			if arr, ok := items.([]any); ok && len(arr) != 0 {
				t.Errorf("want empty history, got %d items", len(arr))
			}
		}
	})

	t.Run("step8_task_history_with_record", func(t *testing.T) {
		// 写入一条历史记录后查询
		ctx := context.Background()
		_, err := f.taskHistory.CreateExecution(ctx, db.TaskExecution{
			TaskID:    "e2e-task-001",
			Account:   "e2e-115",
			Status:    "completed",
			StartedAt: time.Now().UnixMilli(),
			EndedAt:   time.Now().UnixMilli() + 5000,
		})
		if err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}

		code, raw := doReq(handler.HandleTaskHistory(handler.TaskHistoryDeps{Repo: f.taskHistory}),
			"GET", "/api/taskHistory?taskId=e2e-task-001", nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		m := decodeAsMap(t, raw)
		items, _ := m["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("want 1 item, got %d", len(items))
		}
		item := items[0].(map[string]any)
		if item["taskId"] != "e2e-task-001" {
			t.Errorf("taskId want 'e2e-task-001', got %v", item["taskId"])
		}
	})

	t.Run("step9_task_log_empty", func(t *testing.T) {
		// 查询不存在的任务日志 → 空文本
		taskDeps := handler.TaskHandlerDeps{
			TasksStore: f.tasksStore,
			Runtime:    f.taskRuntime,
			Scheduler:  f.scheduler,
		}
		req := httptest.NewRequest("GET", "/api/taskLog?taskId=nonexistent", nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rr := httptest.NewRecorder()
		handler.HandleTaskLog(taskDeps)(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		if rr.Body.Len() != 0 && strings.TrimSpace(rr.Body.String()) != "" {
			t.Errorf("want empty log, got %q", rr.Body.String())
		}
	})
}

// TestE2E_FlowB_SSEProgress 流程B：SSE 进度推送与日志
func TestE2E_FlowB_SSEProgress(t *testing.T) {
	setupE2E(t)
	srv := sse.GetServer()
	taskID := "e2e-sse-task"

	t.Run("emit_progress_and_verify_frame", func(t *testing.T) {
		srv.ClearTaskLogs(taskID)
		srv.EmitProgress(sse.ProgressPayload{
			Event:          "progress",
			TaskID:         taskID,
			Percent:        75,
			OverallPercent: "75.00",
			FilePath:       "/cloud/movie.mkv",
		})
		frames := srv.GetTaskLogs(taskID)
		if len(frames) == 0 {
			t.Fatal("no frames")
		}
		// 验证帧格式
		if !strings.HasPrefix(frames[0], "data: {") {
			t.Errorf("frame format: %q", frames[0][:min(30, len(frames[0]))])
		}
		if !strings.HasSuffix(frames[0], "\n\n") {
			t.Error("frame missing \\n\\n suffix")
		}
	})

	t.Run("emit_log_and_verify", func(t *testing.T) {
		srv.ClearTaskLogs(taskID)
		srv.EmitLog(taskID, "info", "downloading file 1 of 10")
		frames := srv.GetTaskLogs(taskID)
		if len(frames) == 0 {
			t.Fatal("no log frames")
		}
		jsonPart := strings.TrimSuffix(strings.TrimPrefix(frames[0], "data: "), "\n\n")
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload["event"] != "log" {
			t.Errorf("event want 'log', got %v", payload["event"])
		}
		if payload["message"] != "downloading file 1 of 10" {
			t.Errorf("message mismatch: %v", payload["message"])
		}
	})

	t.Run("emit_complete_and_verify", func(t *testing.T) {
		srv.ClearTaskLogs(taskID)
		srv.EmitComplete(sse.CompletePayload{
			Event:           "complete",
			TaskID:          taskID,
			Status:          "completed",
			TotalFiles:      10,
			DownloadedFiles: 10,
			DurationMs:      12000,
		})
		frames := srv.GetTaskLogs(taskID)
		if len(frames) == 0 {
			t.Fatal("no complete frames")
		}
		jsonPart := strings.TrimSuffix(strings.TrimPrefix(frames[0], "data: "), "\n\n")
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload["status"] != "completed" {
			t.Errorf("status want 'completed', got %v", payload["status"])
		}
	})
}

// TestE2E_FlowC_EmbyWebhookLifeMonitor 流程C：Emby webhook → LifeMonitor
func TestE2E_FlowC_EmbyWebhookLifeMonitor(t *testing.T) {
	f := setupE2E(t)

	// 初始写入空 settings
	if err := f.settingsStore.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	execDeps := task.ExecutorDeps{
		AccountStore:  f.accountStore,
		SettingsStore: store.NewSettingsAdapter(f.settingsStore),
		TasksStore:    f.tasksStore,
	}

	_, embyDeps, lifeMonitorDeps, _ := initPhase6Deps(
		f.settingsStore, f.tasksStore, f.accountStore,
		f.lifeEventRepo, f.lifeEventLog, f.filePathRepo, f.stateMgr,
		f.taskRuntime, execDeps, nil, nil,
	)

	t.Run("emby_webhook_library_new", func(t *testing.T) {
		// 未配置 Emby → 应安全跳过（不崩溃）
		event := emby.WebhookEvent{
			Event: "library.new",
			Item: &emby.ItemInfo{
				ID:   "100",
				Name: "E2E Movie",
				Type: "Movie",
			},
		}
		raw, _ := json.Marshal(event)
		req := httptest.NewRequest("POST", "/api/emby/webhook", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.HandleEmbyWebhook(embyDeps)(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]bool
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !resp["ok"] {
			t.Error("want ok=true")
		}
	})

	t.Run("emby_webhook_playback_start", func(t *testing.T) {
		event := emby.WebhookEvent{
			Event: "playback.start",
			Item: &emby.ItemInfo{
				ID:   "101",
				Name: "Playing Movie",
				Type: "Movie",
			},
		}
		raw, _ := json.Marshal(event)
		req := httptest.NewRequest("POST", "/api/emby/webhook", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.HandleEmbyWebhook(embyDeps)(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d", rr.Code)
		}
	})

	t.Run("life_monitor_get_initial", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/lifeMonitor", nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rr := httptest.NewRecorder()
		handler.HandleLifeMonitorGET(lifeMonitorDeps)(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := resp["config"]; !ok {
			t.Error("missing 'config' field")
		}
		if _, ok := resp["accounts"]; !ok {
			t.Error("missing 'accounts' field")
		}
	})

	t.Run("life_events_get_empty", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/lifeEvents?limit=5", nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rr := httptest.NewRecorder()
		handler.HandleLifeEventsGET(lifeMonitorDeps)(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if total, _ := resp["total"].(float64); total != 0 {
			t.Errorf("want total=0, got %v", resp["total"])
		}
	})

	t.Run("life_events_append_then_query", func(t *testing.T) {
		ctx := context.Background()
		_, err := f.lifeEventLog.AppendLog(ctx, db.LifeEventLog{
			Timestamp: time.Now().UnixMilli(),
			Account:   "e2e-115",
			EventType: "create",
			Success:   true,
			FilePath:  "/cloud/new-file.mkv",
			Message:   "auto-created via life monitor",
		})
		if err != nil {
			t.Fatalf("AppendLog: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/lifeEvents?account=e2e-115", nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rr := httptest.NewRecorder()
		handler.HandleLifeEventsGET(lifeMonitorDeps)(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if total, _ := resp["total"].(float64); total != 1 {
			t.Errorf("want total=1, got %v", resp["total"])
		}
	})
}

// TestE2E_FlowD_ErrorPaths 流程D：错误路径验证
func TestE2E_FlowD_ErrorPaths(t *testing.T) {
	f := setupE2E(t)
	jwtMW := middleware.JWTMiddleware(f.issuer)

	t.Run("protected_route_without_jwt_returns_401", func(t *testing.T) {
		// 无 JWT 访问受保护路由 → 401（通过中间件）
		h := jwtMW(handler.ListAccounts(f.accountStore))
		code, _ := doReq(h, "GET", "/api/account", nil, "")
		if code != 401 {
			t.Errorf("want 401 without JWT, got %d", code)
		}
	})

	t.Run("protected_route_with_invalid_jwt_returns_401", func(t *testing.T) {
		// 无效 JWT → 401
		h := jwtMW(handler.ListAccounts(f.accountStore))
		code, _ := doReq(h, "GET", "/api/account", nil, "invalid.token.here")
		if code != 401 {
			t.Errorf("want 401 with invalid JWT, got %d", code)
		}
	})

	t.Run("login_wrong_password_returns_401", func(t *testing.T) {
		code, raw := doReq(handler.Login(f.issuer), "POST", "/api/auth/login",
			map[string]string{"username": "admin", "password": "wrongpass"}, "")
		if code != 401 {
			t.Fatalf("want 401, got %d body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		assertIsString(t, "error", m["error"])
	})

	t.Run("login_empty_username_returns_401", func(t *testing.T) {
		// 空用户名 → 进入密码校验 → 不匹配 → 401
		code, _ := doReq(handler.Login(f.issuer), "POST", "/api/auth/login",
			map[string]string{"username": "", "password": ""}, "")
		if code != 401 {
			t.Errorf("want 401 for empty creds (auth mismatch), got %d", code)
		}
	})

	t.Run("login_malformed_json_returns_400", func(t *testing.T) {
		// 非 JSON body → 400
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader([]byte("not json")))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.Login(f.issuer)(rr, req)
		if rr.Code != 400 {
			t.Errorf("want 400 for malformed JSON, got %d", rr.Code)
		}
	})

	t.Run("create_account_duplicate_name_400", func(t *testing.T) {
		// 先创建一个
		_, _ = doReq(handler.CreateAccount(f.accountStore), "POST", "/api/account",
			map[string]string{"accountType": "115", "name": "dup", "cookie": "UID=x"}, f.token)
		// 再创建同名 → 400
		code, raw := doReq(handler.CreateAccount(f.accountStore), "POST", "/api/account",
			map[string]string{"accountType": "115", "name": "dup", "cookie": "UID=y"}, f.token)
		if code != 400 {
			t.Fatalf("want 400 for duplicate, got %d body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		if !strings.Contains(m["error"].(string), "exists") {
			t.Errorf("error should mention 'exists', got %v", m["error"])
		}
	})

	t.Run("create_115_account_without_cookie_400", func(t *testing.T) {
		code, _ := doReq(handler.CreateAccount(f.accountStore), "POST", "/api/account",
			map[string]string{"accountType": "115", "name": "nocookie"}, f.token)
		if code != 400 {
			t.Errorf("want 400 for 115 without cookie, got %d", code)
		}
	})

	t.Run("create_openlist_without_credentials_400", func(t *testing.T) {
		code, _ := doReq(handler.CreateAccount(f.accountStore), "POST", "/api/account",
			map[string]string{"accountType": "openlist", "name": "incomplete"}, f.token)
		if code != 400 {
			t.Errorf("want 400 for openlist without creds, got %d", code)
		}
	})

	t.Run("startTask_without_jwt_401", func(t *testing.T) {
		taskDeps := handler.TaskHandlerDeps{
			TasksStore: f.tasksStore,
			Runtime:    f.taskRuntime,
			Scheduler:  f.scheduler,
		}
		h := jwtMW(handler.HandleStartTask(taskDeps))
		code, _ := doReq(h, "POST", "/api/startTask",
			map[string]string{"taskId": "x"}, "")
		if code != 401 {
			t.Errorf("want 401 without JWT, got %d", code)
		}
	})
}

// TestE2E_FlowE_DirectoryLocal 流程E：本地目录浏览
func TestE2E_FlowE_DirectoryLocal(t *testing.T) {
	f := setupE2E(t)

	// 创建临时目录结构
	testDir := filepath.Join(f.dataDir, "strm_output")
	if err := os.MkdirAll(filepath.Join(testDir, "movies"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(testDir, "tv"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dirDeps := handler.DirectoryDeps{
		AccountStore: f.accountStore,
	}

	t.Run("list_local_without_root_returns_drives", func(t *testing.T) {
		// 不传 root → 返回盘符列表（数组格式）
		code, raw := doReq(handler.HandleLocalDirList(dirDeps), "GET",
			"/api/directory/local/list", nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		if m["code"].(float64) != 200 {
			t.Errorf("code want 200, got %v", m["code"])
		}
		data, ok := m["data"].([]any)
		if !ok {
			t.Fatalf("data must be array, got %T", m["data"])
		}
		if len(data) == 0 {
			t.Error("data should not be empty for root listing")
		}
	})

	t.Run("list_local_with_root", func(t *testing.T) {
		// 传 root=testDir → 返回子目录列表
		code, raw := doReq(handler.HandleLocalDirList(dirDeps), "GET",
			"/api/directory/local/list?root="+testDir, nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d body=%s", code, raw)
		}
		m := decodeAsMap(t, raw)
		data, ok := m["data"].([]any)
		if !ok {
			t.Fatalf("data must be array, got %T", m["data"])
		}
		// 应包含 movies 和 tv 两个目录
		names := map[string]bool{}
		for _, c := range data {
			if node, ok := c.(map[string]any); ok {
				names[node["name"].(string)] = true
			}
		}
		if !names["movies"] {
			t.Errorf("expected movies in data, got: %v", names)
		}
		if !names["tv"] {
			t.Errorf("expected tv in data, got: %v", names)
		}
		})

	t.Run("list_local_invalid_root", func(t *testing.T) {
		// 不存在的路径 → code=500 + 空 content
		code, raw := doReq(handler.HandleLocalDirList(dirDeps), "GET",
			"/api/directory/local/list?root=/nonexistent/path/xyz", nil, f.token)
		if code != 200 {
			t.Fatalf("want 200, got %d", code)
		}
		m := decodeAsMap(t, raw)
		if m["code"].(float64) != 500 {
			t.Errorf("code want 500 for invalid path, got %v", m["code"])
		}
	})
}


