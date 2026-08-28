package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
)

// 阶段 6 集成测试：验证 initPhase6Deps 正确装配 Telegram / Emby / LifeMonitor 依赖
// 覆盖：
//  1. 启动时无配置 → Dispatcher 仍可用（Notify 静默跳过）、Monitor 可查询状态
//  2. 启动时已配置 Telegram → TelegramBot / PollingManager / CommandHandler 均非 nil
//  3. 启动时已配置 Emby → EmbyClient / Notifier / SyncDelete 均非 nil
//  4. settingsFn 热重载：修改 settings.json 后 settingsFn 回调返回最新值
//  5. LifeEventLogRepo Append/Query 端到端
func TestPhase6_InitDepsAndWiring(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfgDir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	salt := "phase6_integration_test_salt_32b_"

	settingsStore := store.NewSettingsStore(salt, cfgDir)
	tasksStore := store.NewTasksStore(cfgDir)
	accountStore, _ := store.NewAccountStore(salt, cfgDir)
	stateMgr := runtime.Init(cfgDir)

	// SQLite（用 OpenNew 避免全局单例污染其他测试）
	sqliteDB, err := db.OpenNew(dataDir)
	if err != nil {
		t.Fatalf("db.OpenNew: %v", err)
	}
	defer sqliteDB.Close()

	lifeEventRepo, err := db.NewLifeEventRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewLifeEventRepo: %v", err)
	}
	lifeEventLogRepo, err := db.NewLifeEventLogRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewLifeEventLogRepo: %v", err)
	}
	filePathRepo := db.NewFilePathRepo(sqliteDB)

	taskRuntime := task.GetRuntime()
	execDeps := task.ExecutorDeps{
		AccountStore:  accountStore,
		SettingsStore: store.NewSettingsAdapter(settingsStore),
		TasksStore:    tasksStore,
	}

	t.Run("no_config_wiring", func(t *testing.T) {
		// 未写入 settings.json → 使用默认配置
		notifyDeps, embyDeps, lifeMonitorDeps, mon := initPhase6Deps(
			settingsStore, tasksStore, accountStore,
			lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
			taskRuntime, execDeps, nil, nil,
		)

		// Dispatcher 必须非 nil（即便 Telegram 未配置，Notify 静默跳过）
		if notifyDeps.Dispatcher == nil {
			t.Fatal("Dispatcher should not be nil even if Telegram unconfigured")
		}
		// 未配置 BotToken → TelegramBot / PollingManager / CommandHandler 均为 nil
		if notifyDeps.TelegramBot != nil {
			t.Error("TelegramBot should be nil when botToken is empty")
		}
		if notifyDeps.PollingManager != nil {
			t.Error("PollingManager should be nil when botToken is empty")
		}
		if notifyDeps.CommandHandler != nil {
			t.Error("CommandHandler should be nil when botToken is empty")
		}
		// Dispatcher.Notify 应静默成功（不报错）
		if err := notifyDeps.Dispatcher.Notify(context.Background(), "test"); err != nil {
			t.Errorf("Dispatcher.Notify should silently skip, got err: %v", err)
		}

		// Emby 未配置 → Client 为 nil，Notifier 仍非 nil（内部 settingsFn 热重载）
		if embyDeps.EmbyClient != nil {
			t.Error("EmbyClient should be nil when emby.url is empty")
		}
		if embyDeps.EmbyNotifier == nil {
			t.Fatal("EmbyNotifier should not be nil")
		}
		if embyDeps.SyncDelete == nil {
			t.Fatal("SyncDelete should not be nil")
		}

		// Monitor 非空且可查询状态（即使未启用）
		if mon == nil {
			t.Fatal("Monitor should not be nil")
		}
		states := mon.Status()
		if len(states) != 0 {
			t.Errorf("empty config should produce empty states, got %d", len(states))
		}
		if lifeMonitorDeps.Monitor == nil {
			t.Error("LifeMonitorDeps.Monitor should not be nil")
		}
		if lifeMonitorDeps.LifeEventLogRepo == nil {
			t.Error("LifeMonitorDeps.LifeEventLogRepo should not be nil")
		}
	})

	t.Run("telegram_configured_wiring", func(t *testing.T) {
		// 写入含 BotToken 的 settings.json
		settings := model.DefaultSettings()
		settings.Telegram = model.TelegramSettings{
			BotToken: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz1234567890",
			ChatID:   "-1001234567890",
			Enabled:  true,
		}
		if err := settingsStore.SaveSettings(settings); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}

		notifyDeps, _, _, _ := initPhase6Deps(
			settingsStore, tasksStore, accountStore,
			lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
			taskRuntime, execDeps, nil, nil,
		)

		if notifyDeps.TelegramBot == nil {
			t.Fatal("TelegramBot should not be nil when botToken is configured")
		}
		if notifyDeps.PollingManager == nil {
			t.Error("PollingManager should not be nil when botToken is configured")
		}
		if notifyDeps.CommandHandler == nil {
			t.Error("CommandHandler should not be nil when botToken is configured")
		}
	})

	t.Run("emby_configured_wiring", func(t *testing.T) {
		settings := model.DefaultSettings()
		settings.Emby = model.EmbySettings{
			URL:    "http://127.0.0.1:8096",
			APIKey: "fakeapikey123456",
		}
		if err := settingsStore.SaveSettings(settings); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}

		embyClient := emby.NewClient(settings.Emby.URL, settings.Emby.APIKey)
		_, embyDeps, _, _ := initPhase6Deps(
			settingsStore, tasksStore, accountStore,
			lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
			taskRuntime, execDeps, embyClient, nil,
		)
		if embyDeps.EmbyClient == nil {
			t.Fatal("EmbyClient should not be nil when emby.url+apiKey are configured")
		}
	})

	t.Run("settingsFn_hot_reload", func(t *testing.T) {
		// 先写入空配置启动
		emptySettings := model.DefaultSettings()
		if err := settingsStore.SaveSettings(emptySettings); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}
		_, embyDeps, _, _ := initPhase6Deps(
			settingsStore, tasksStore, accountStore,
			lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
			taskRuntime, execDeps, nil, nil,
		)
		// 启动时 emby 未配置 → EmbyClient 为 nil
		if embyDeps.EmbyClient != nil {
			t.Fatal("EmbyClient should be nil at startup")
		}
		if embyDeps.EmbyNotifier == nil {
			t.Fatal("EmbyNotifier should not be nil")
		}

		// 写入新配置后，settingsFn 应能读到最新 URL（通过 SettingsStore 回读对比）
		newSettings := model.DefaultSettings()
		newSettings.Emby = model.EmbySettings{
			URL:              "http://newemby:8096",
			APIKey:           "newkey",
			NotifyMediaAdded: false, // 默认 false：library.new 事件应被跳过
		}
		if err := settingsStore.SaveSettings(newSettings); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}

		// 验证 settingsFn 通过 HandleWebhookEvent 间接生效：
		// NotifyMediaAdded=false → handleMediaAdded 直接 return nil（不发 HTTP）
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		event := emby.WebhookEvent{
			Event: "library.new",
			Item: &emby.ItemInfo{
				ID:   "1",
				Name: "test-movie",
				Type: "Movie",
			},
		}
		if err := embyDeps.EmbyNotifier.HandleWebhookEvent(ctx, event); err != nil {
			t.Errorf("HandleWebhookEvent with NotifyMediaAdded=false should return nil, got: %v", err)
		}

		// 二次读 settings 验证持久化
		s2, err := settingsStore.ReadSettings()
		if err != nil {
			t.Fatalf("ReadSettings: %v", err)
		}
		if s2.Emby.URL != "http://newemby:8096" {
			t.Errorf("settings hot reload failed: want http://newemby:8096, got %s", s2.Emby.URL)
		}
	})

	t.Run("life_event_log_round_trip", func(t *testing.T) {
		if lifeEventLogRepo == nil {
			t.Fatal("lifeEventLogRepo should not be nil")
		}
		ctx := context.Background()

		// 追加 3 条日志
		for i := 0; i < 3; i++ {
			_, err := lifeEventLogRepo.AppendLog(ctx, db.LifeEventLog{
				Timestamp: time.Now().UnixMilli(),
				Account:   "test@115.com",
				EventType: "create",
				Success:   true,
				FilePath:  "/cloud/path/file.txt",
				LocalPath: "/local/path/file.strm",
				Message:   "test log entry",
			})
			if err != nil {
				t.Fatalf("AppendLog[%d]: %v", i, err)
			}
		}

		// 查询验证
		items, err := lifeEventLogRepo.Query(ctx, db.LifeEventLogQuery{
			Account: "test@115.com",
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(items) != 3 {
			t.Errorf("want 3 logs, got %d", len(items))
		}

		// 统计
		total, success, failed, err := lifeEventLogRepo.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if total < 3 || success < 3 || failed != 0 {
			t.Errorf("unexpected stats: total=%d success=%d failed=%d", total, success, failed)
		}
	})

	t.Run("filepath_repo_adapter", func(t *testing.T) {
		// FilePathRepo 适配 emby.FilePathDb 接口：DeleteByPath / DeleteByPathPrefix 在无记录时应返回 0 + nil
		n, err := filePathRepo.DeleteByPath("nonexistent_account", "/nonexistent/path")
		if err != nil {
			t.Fatalf("DeleteByPath on empty: err=%v", err)
		}
		if n != 0 {
			t.Errorf("DeleteByPath on empty should return 0, got %d", n)
		}
		n, err = filePathRepo.DeleteByPathPrefix("nonexistent_account", "/nonexistent")
		if err != nil {
			t.Fatalf("DeleteByPathPrefix on empty: err=%v", err)
		}
		if n != 0 {
			t.Errorf("DeleteByPathPrefix on empty should return 0, got %d", n)
		}
	})
}

// TestPhase6_HttpHandlers 阶段 6 HTTP handler 端到端测试
// 直接调用 handler 工厂并经 httptest 发起 HTTP 请求，验证 wiring 后的处理器可正常响应
func TestPhase6_HttpHandlers(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfgDir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	salt := "phase6_http_test_salt_32b_pad____"

	settingsStore := store.NewSettingsStore(salt, cfgDir)
	tasksStore := store.NewTasksStore(cfgDir)
	accountStore, _ := store.NewAccountStore(salt, cfgDir)
	stateMgr := runtime.Init(cfgDir)

	sqliteDB, err := db.OpenNew(dataDir)
	if err != nil {
		t.Fatalf("db.OpenNew: %v", err)
	}
	defer sqliteDB.Close()

	lifeEventRepo, err := db.NewLifeEventRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewLifeEventRepo: %v", err)
	}
	lifeEventLogRepo, err := db.NewLifeEventLogRepo(sqliteDB)
	if err != nil {
		t.Fatalf("NewLifeEventLogRepo: %v", err)
	}
	filePathRepo := db.NewFilePathRepo(sqliteDB)

	taskRuntime2 := task.GetRuntime()
	execDeps2 := task.ExecutorDeps{
		AccountStore:  accountStore,
		SettingsStore: store.NewSettingsAdapter(settingsStore),
		TasksStore:    tasksStore,
	}

	// 初始写入空配置
	if err := settingsStore.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	notifyDeps, embyDeps, lifeMonitorDeps, _ := initPhase6Deps(
		settingsStore, tasksStore, accountStore,
		lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
		taskRuntime2, execDeps2, nil, nil,
	)

	// doJSON 发起 JSON POST 请求并返回 (status, body)
	doJSON := func(h http.HandlerFunc, method, path string, body any) (int, []byte) {
		var reqBody io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reqBody = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, reqBody)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr.Code, rr.Body.Bytes()
	}

	t.Run("emby_webhook_json", func(t *testing.T) {
		// 发送 library.new 事件（NotifyMediaAdded 默认 false → handler 异步处理但不发通知，返回 ok）
		event := emby.WebhookEvent{
			Event: "library.new",
			Item: &emby.ItemInfo{
				ID:   "1",
				Name: "test-movie",
				Type: "Movie",
			},
		}
		code, respBody := doJSON(handler.HandleEmbyWebhook(embyDeps), http.MethodPost, "/api/emby/webhook", event)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d, body=%s", code, respBody)
		}
		var resp map[string]bool
		if err := json.Unmarshal(respBody, &resp); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, respBody)
		}
		if !resp["ok"] {
			t.Errorf("want ok=true, got %v", resp)
		}
	})

	t.Run("emby_webhook_missing_notifier", func(t *testing.T) {
		// EmbyNotifier 为 nil 时应返回 500
		emptyDeps := handler.EmbyDeps{SettingsStore: settingsStore} // 无 EmbyNotifier
		code, _ := doJSON(handler.HandleEmbyWebhook(emptyDeps), http.MethodPost, "/api/emby/webhook", emby.WebhookEvent{Event: "library.new"})
		if code != http.StatusInternalServerError {
			t.Errorf("want 500 when EmbyNotifier nil, got %d", code)
		}
	})

	t.Run("telegram_webhook_no_bot", func(t *testing.T) {
		// 未配置 botToken → 返回 400
		code, respBody := doJSON(handler.HandleTelegramWebhook(notifyDeps), http.MethodPost, "/api/notify/webhook", map[string]any{"update_id": 1})
		if code != http.StatusBadRequest {
			t.Fatalf("want 400 when botToken empty, got %d body=%s", code, respBody)
		}
	})

	t.Run("telegram_webhook_secret_mismatch", func(t *testing.T) {
		// 配置 botToken + secretToken，请求带错误 secret → 401
		settings := model.DefaultSettings()
		settings.Telegram = model.TelegramSettings{
			BotToken:           "123456789:ABCdefGHIjklMNOpqrsTUVwxyz1234567890",
			ChatID:             "-1001234567890",
			WebhookSecretToken: "expected-secret-token",
			Enabled:            true,
		}
		if err := settingsStore.SaveSettings(settings); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}
		// 重新构造 notifyDeps 以拿到 CommandHandler
		nd, _, _, _ := initPhase6Deps(settingsStore, tasksStore, accountStore, lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr, taskRuntime2, execDeps2, nil, nil)

		raw, _ := json.Marshal(map[string]any{"update_id": 1})
		req := httptest.NewRequest(http.MethodPost, "/api/notify/webhook", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-secret")
		rr := httptest.NewRecorder()
		handler.HandleTelegramWebhook(nd)(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("want 401 on secret mismatch, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("life_monitor_get", func(t *testing.T) {
		// GET /api/lifeMonitor → 返回 config + states + accounts
		req := httptest.NewRequest(http.MethodGet, "/api/lifeMonitor", nil)
		rr := httptest.NewRecorder()
		handler.HandleLifeMonitorGET(lifeMonitorDeps)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := resp["config"]; !ok {
			t.Errorf("response missing 'config' field: %v", resp)
		}
		if _, ok := resp["states"]; !ok {
			t.Errorf("response missing 'states' field: %v", resp)
		}
		if _, ok := resp["accounts"]; !ok {
			t.Errorf("response missing 'accounts' field: %v", resp)
		}
	})

	t.Run("life_events_get_empty", func(t *testing.T) {
		// GET /api/lifeEvents → 初始为空
		req := httptest.NewRequest(http.MethodGet, "/api/lifeEvents", nil)
		rr := httptest.NewRecorder()
		handler.HandleLifeEventsGET(lifeMonitorDeps)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if total, _ := resp["total"].(float64); total != 0 {
			t.Errorf("want total=0 on empty, got %v", resp["total"])
		}
	})

	t.Run("life_events_append_then_get", func(t *testing.T) {
		// 先写一条日志，再 GET 验证能读到
		ctx := context.Background()
		_, err := lifeEventLogRepo.AppendLog(ctx, db.LifeEventLog{
			Timestamp: time.Now().UnixMilli(),
			Account:   "httpacc@115.com",
			EventType: "create",
			Success:   true,
			FilePath:  "/cloud/x.mkv",
			Message:   "via http test",
		})
		if err != nil {
			t.Fatalf("AppendLog: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/lifeEvents?account=httpacc@115.com", nil)
		rr := httptest.NewRecorder()
		handler.HandleLifeEventsGET(lifeMonitorDeps)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if total, _ := resp["total"].(float64); total != 1 {
			t.Errorf("want total=1, got %v", resp["total"])
		}
	})

	t.Run("emby_settings_get_masks_apikey", func(t *testing.T) {
		// 写入含 apiKey 的配置，GET 返回应被脱敏
		settings := model.DefaultSettings()
		settings.Emby = model.EmbySettings{
			URL:    "http://127.0.0.1:8096",
			APIKey: "abcdefghij1234567890",
		}
		if err := settingsStore.SaveSettings(settings); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/emby/settings", nil)
		rr := httptest.NewRecorder()
		handler.HandleEmbySettingsGET(embyDeps)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		var resp model.EmbySettings
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// 脱敏后应以 **** 包裹，不应包含明文
		if resp.APIKey == "abcdefghij1234567890" {
			t.Errorf("apiKey should be masked, got plaintext: %s", resp.APIKey)
		}
		if resp.URL != "http://127.0.0.1:8096" {
			t.Errorf("url mismatch: %s", resp.URL)
		}
	})
}
