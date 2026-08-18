package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/server/middleware"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/internal/web"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"go.uber.org/zap/zapcore"
)

// Run 启动 go-zero HTTP 服务
func Run(cfg *config.AppConfig) error {
	// 初始化日志
	logDir := ""
	if cfg.Server.Mode == "prod" {
		logDir = cfg.Paths.LogDir
	}
	logger.InitLogger(logDir, zapcore.InfoLevel)
	defer logger.Sync()

	// go-zero log 配置（禁用其内部日志输出，统一走 zap）
	logx.DisableStat()

	restCfg := rest.RestConf{
		Host: cfg.Server.Host,
		Port: cfg.Server.Port,
		ServiceConf: service.ServiceConf{
			Log: logx.LogConf{
				Mode: "console",
			},
		},
	}

	server := rest.MustNewServer(restCfg)
	defer server.Stop()

	// 初始化依赖
	issuer := auth.NewTokenIssuer([]byte(cfg.Settings.InternalToken))
	client := client115.NewClient(cfg.Settings.UserAgent)
	accountStore := store.NewAccountStore(cfg.Salt, cfg.Paths.ConfigDir)
	settingsStore := store.NewSettingsStore(cfg.Salt, cfg.Paths.ConfigDir)
	tasksStore := store.NewTasksStore(cfg.Paths.ConfigDir)
	stateMgr := runtime.Init(cfg.Paths.ConfigDir)

	// SQLite 打开
	sqliteDB, err := db.Open(cfg.Paths.DataDir)
	if err != nil {
		logger.S().Warnf("Open sqlite failed (continue without filePathDb): %v", err)
		sqliteDB = nil
	}
	// TaskHistoryRepo / LifeEventRepo / LifeEventLogRepo / FilePathRepo
	taskHistoryRepo, err := db.NewTaskHistoryRepo(sqliteDB)
	if err != nil {
		logger.S().Warnf("NewTaskHistoryRepo failed: %v", err)
	}
	lifeEventRepo, err := db.NewLifeEventRepo(sqliteDB)
	if err != nil {
		logger.S().Warnf("NewLifeEventRepo failed: %v", err)
	}
	var lifeEventLogRepo *db.LifeEventLogRepo
	if sqliteDB != nil {
		lifeEventLogRepo, err = db.NewLifeEventLogRepo(sqliteDB)
		if err != nil {
			logger.S().Warnf("NewLifeEventLogRepo failed: %v", err)
		}
	}
	var filePathRepo *db.FilePathRepo
	if sqliteDB != nil {
		filePathRepo = db.NewFilePathRepo(sqliteDB)
	}

	// 运行时 & 调度器
	taskRuntime := task.GetRuntime()
	scheduler := task.GetScheduler()
	// 组装 baseURL：host:port (dev 默认 127.0.0.1)
	baseURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	execDeps := task.ExecutorDeps{
		Client115:     client,
		AccountStore:  accountStore,
		SettingsStore: store.NewSettingsAdapter(settingsStore),
		SQLiteDB:      sqliteDB,
		TasksStore:    tasksStore,
		BaseURL:       baseURL,
		PublicBaseURL: cfg.Settings.StrmPrefix,
	}
	_ = scheduler.Init(execDeps, tasksStore, store.NewSettingsAdapter(settingsStore))

	// ==================== 阶段6: 通知 + Emby + 生活监控依赖 ====================
	notifyDeps, embyDeps, lifeMonitorDeps, mon := initPhase6Deps(
		settingsStore, tasksStore, accountStore,
		lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
	)

	// 注册路由
	RegisterRoutes(server, cfg, issuer, client, accountStore, settingsStore, tasksStore,
		taskHistoryRepo, taskRuntime, scheduler, execDeps,
		notifyDeps, embyDeps, lifeMonitorDeps)

	// 若生活监控已在配置中启用，启动后台监控（异步）
	if mon != nil {
		go func() {
			if err := mon.StartAll(context.Background()); err != nil {
				logger.S().Warnf("[Phase6] auto-start monitor failed: %v", err)
			}
		}()
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.S().Infof("FastStrm-Go starting on http://%s (mode=%s)", addr, cfg.Server.Mode)
	logger.S().Infof("  data_dir: %s", cfg.Paths.DataDir)
	logger.S().Infof("  config_dir: %s", cfg.Paths.ConfigDir)
	logger.S().Infof("  internal_token: %s", maskToken(cfg.Settings.InternalToken))

	server.Start()
	return nil
}

// RegisterRoutes 注册全部 HTTP 路由
func RegisterRoutes(
	server *rest.Server,
	cfg *config.AppConfig,
	issuer *auth.TokenIssuer,
	client *client115.Client,
	accountStore *store.AccountStore,
	settingsStore *store.SettingsStore,
	tasksStore *store.TasksStore,
	taskHistoryRepo *db.TaskHistoryRepo,
	taskRuntime *task.Runtime,
	scheduler *task.Scheduler,
	execDeps task.ExecutorDeps,
	notifyDeps handler.NotifyDeps,
	embyDeps handler.EmbyDeps,
	lifeMonitorDeps handler.LifeMonitorDeps,
) {
	// ========== 公开路由（无需 JWT，仅 CORS） ==========
	publicRoutes := []rest.Route{
		{Method: http.MethodGet, Path: "/api/health", Handler: middleware.CORS(handler.Health)},
		{Method: http.MethodPost, Path: "/api/auth/login", Handler: middleware.CORS(handler.Login(issuer))},
		{Method: http.MethodPost, Path: "/api/auth/change-password", Handler: middleware.CORS(handler.ChangePassword())},
		{Method: http.MethodPost, Path: "/api/auth/change-credentials", Handler: middleware.CORS(handler.ChangeCredentials())},
		{Method: http.MethodPost, Path: "/api/auth/logout", Handler: middleware.CORS(handler.Logout())},
		// SSE 事件流（公开，客户端凭 taskId 订阅）
		{Method: http.MethodGet, Path: "/api/events/stream", Handler: middleware.CORS(sse.Handler())},
		// 阶段6: Emby Webhook 回调（公开，无 JWT）
		{Method: http.MethodPost, Path: "/api/emby/webhook", Handler: middleware.CORS(handler.HandleEmbyWebhook(embyDeps))},
		// 阶段6: Telegram Webhook 回调（公开，无 JWT，自带 secret_token 校验）
		{Method: http.MethodPost, Path: "/api/notify/webhook", Handler: middleware.CORS(handler.HandleTelegramWebhook(notifyDeps))},
	}
	server.AddRoutes(publicRoutes)

	// ========== 阶段4: STRM 路由（播放器无 token，公开） ==========
	jwtMW := middleware.JWTMiddleware(issuer)
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/strm", Handler: middleware.CORS(handler.HandleStrm(handler.StrmOptions{
			Cfg: cfg, Client115: client, AccountStore: accountStore,
		}))},
		{Method: http.MethodHead, Path: "/api/strm", Handler: middleware.CORS(handler.HandleStrm(handler.StrmOptions{
			Cfg: cfg, Client115: client, AccountStore: accountStore,
		}))},
		{Method: http.MethodGet, Path: "/api/fs/get", Handler: middleware.CORS(jwtMW(handler.HandleFsGet(handler.StrmOptions{
			Cfg: cfg, Client115: client, AccountStore: accountStore,
		})))},
	})

	// ========== 受保护路由（JWT + CORS） ==========

	// corsJWT 包装器：先 CORS 再 JWT
	corsJWT := func(h http.HandlerFunc) http.HandlerFunc {
		return middleware.CORS(jwtMW(h))
	}

	// ---- 账号管理 ----
	protectedRoutes := []rest.Route{
		// 账号管理
		{Method: http.MethodGet, Path: "/api/account", Handler: corsJWT(handler.ListAccounts(accountStore))},
		{Method: http.MethodPost, Path: "/api/account", Handler: corsJWT(handler.CreateAccount(accountStore))},
		{Method: http.MethodPut, Path: "/api/account", Handler: corsJWT(handler.UpdateAccount(accountStore))},
		{Method: http.MethodDelete, Path: "/api/account", Handler: corsJWT(handler.DeleteAccount(accountStore))},

		// 扫码登录
		{Method: http.MethodGet, Path: "/api/account/qrcode/token", Handler: corsJWT(handler.GetQrcodeTokenHandler(client))},
		{Method: http.MethodGet, Path: "/api/account/qrcode/status", Handler: corsJWT(handler.GetQrcodeStatusHandler(client))},
		{Method: http.MethodPost, Path: "/api/account/qrcode/cookie", Handler: corsJWT(handler.GetQrcodeCookieHandler(client, accountStore))},

		// 账号状态
		{Method: http.MethodGet, Path: "/api/account/status", Handler: corsJWT(handler.GetAccountStatus(accountStore))},
	}
	server.AddRoutes(protectedRoutes)

	// ==================== 阶段5: 任务 & 目录 & 任务历史 ====================
	taskDeps := handler.TaskHandlerDeps{
		ExecutorDeps:    execDeps,
		TasksStore:      tasksStore,
		Runtime:         taskRuntime,
		Scheduler:       scheduler,
		TaskHistoryRepo: taskHistoryRepo,
	}
	dirDeps := handler.DirectoryDeps{
		Client115:    client,
		AccountStore: accountStore,
	}

	server.AddRoutes([]rest.Route{
		// 任务
		{Method: http.MethodGet, Path: "/api/tasks", Handler: corsJWT(handler.HandleListTasks(taskDeps))},
		{Method: http.MethodPost, Path: "/api/task", Handler: corsJWT(handler.HandleUpsertTask(taskDeps, false))},
		{Method: http.MethodPut, Path: "/api/task", Handler: corsJWT(handler.HandleUpsertTask(taskDeps, true))},
		{Method: http.MethodDelete, Path: "/api/task", Handler: corsJWT(handler.HandleDeleteTask(taskDeps))},
		{Method: http.MethodPost, Path: "/api/startTask", Handler: corsJWT(handler.HandleStartTask(taskDeps))},
		{Method: http.MethodPost, Path: "/api/cancelTask", Handler: corsJWT(handler.HandleCancelTask(taskDeps))},
		{Method: http.MethodGet, Path: "/api/taskHistory", Handler: corsJWT(handler.HandleTaskHistory(handler.TaskHistoryDeps{Repo: taskHistoryRepo}))},
		{Method: http.MethodGet, Path: "/api/taskLog", Handler: corsJWT(handler.HandleTaskLog(taskDeps))},
		{Method: http.MethodGet, Path: "/api/taskLog/:taskId", Handler: corsJWT(handler.HandleTaskLog(taskDeps))},
		// 目录
		{Method: http.MethodGet, Path: "/api/directory/remote/list", Handler: corsJWT(handler.HandleRemoteDirList(dirDeps))},
		{Method: http.MethodGet, Path: "/api/directory/local/list", Handler: corsJWT(handler.HandleLocalDirList(dirDeps))},
		{Method: http.MethodPost, Path: "/api/directory/clear", Handler: corsJWT(handler.HandleClearDir(handler.ClearDeps{
			SettingsProvider: func() *model.Settings {
				s, err := settingsStore.ReadSettings()
				if err != nil {
					return nil
				}
				return s
			},
		}))},
	})

	// ==================== 阶段6: Telegram 通知 ====================
	server.AddRoutes([]rest.Route{
		// Bot 配置
		{Method: http.MethodGet, Path: "/api/notify/bot", Handler: corsJWT(handler.HandleNotifyBotGET(notifyDeps))},
		{Method: http.MethodPost, Path: "/api/notify/bot", Handler: corsJWT(handler.HandleNotifyBotPOST(notifyDeps))},
		{Method: http.MethodDelete, Path: "/api/notify/bot", Handler: corsJWT(handler.HandleNotifyBotDELETE(notifyDeps))},
		// 轮询管理
		{Method: http.MethodGet, Path: "/api/notify/polling", Handler: corsJWT(handler.HandleNotifyPollingGET(notifyDeps))},
		{Method: http.MethodPost, Path: "/api/notify/polling", Handler: corsJWT(handler.HandleNotifyPollingPOST(notifyDeps))},
		{Method: http.MethodDelete, Path: "/api/notify/polling", Handler: corsJWT(handler.HandleNotifyPollingDELETE(notifyDeps))},
		// 发送通知
		{Method: http.MethodPost, Path: "/api/notify/send", Handler: corsJWT(handler.HandleNotifySend(notifyDeps))},
		// 白名单用户
		{Method: http.MethodGet, Path: "/api/notify/users", Handler: corsJWT(handler.HandleNotifyUsersGET(notifyDeps))},
		{Method: http.MethodPost, Path: "/api/notify/users", Handler: corsJWT(handler.HandleNotifyUsersPOST(notifyDeps))},
		{Method: http.MethodDelete, Path: "/api/notify/users", Handler: corsJWT(handler.HandleNotifyUsersDELETE(notifyDeps))},
		// 账户告警
		{Method: http.MethodGet, Path: "/api/notify/alerts", Handler: corsJWT(handler.HandleNotifyAlertsGET(notifyDeps))},
		{Method: http.MethodPost, Path: "/api/notify/alerts", Handler: corsJWT(handler.HandleNotifyAlertsPOST(notifyDeps))},
	})

	// ==================== 阶段6: Emby 配置 ====================
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/emby/settings", Handler: corsJWT(handler.HandleEmbySettingsGET(embyDeps))},
		{Method: http.MethodPost, Path: "/api/emby/settings", Handler: corsJWT(handler.HandleEmbySettingsPOST(embyDeps))},
		{Method: http.MethodPost, Path: "/api/emby/test-connection", Handler: corsJWT(handler.HandleEmbyTestConnection(embyDeps))},
	})

	// ==================== 聚合 Settings API（设置页 6 tab 回填 + 保存） ====================
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/settings", Handler: corsJWT(handler.HandleSettingsGET(embyDeps))},
		{Method: http.MethodPost, Path: "/api/settings", Handler: corsJWT(handler.HandleSettingsPOST(embyDeps))},
	})

	// ==================== 阶段6: 生活监控 + 事件日志 ====================
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/lifeMonitor", Handler: corsJWT(handler.HandleLifeMonitorGET(lifeMonitorDeps))},
		{Method: http.MethodPost, Path: "/api/lifeMonitor", Handler: corsJWT(handler.HandleLifeMonitorPOST(lifeMonitorDeps))},
		{Method: http.MethodGet, Path: "/api/lifeEvents", Handler: corsJWT(handler.HandleLifeEventsGET(lifeMonitorDeps))},
		{Method: http.MethodDelete, Path: "/api/lifeEvents", Handler: corsJWT(handler.HandleLifeEventsDELETE(lifeMonitorDeps))},
	})

	// ==================== 阶段8: Web UI（Templ + HTMX + Tailwind） ====================
	// 路径严格对齐原始 Next.js 前端（frontend/src/app/ 目录名）
	webDeps := web.WebDeps{TokenIssuer: issuer}
	authMW := web.AuthRequiredMiddleware(issuer)
	server.AddRoutes([]rest.Route{
		// 静态资源（embed.FS，无外部依赖）
		{Method: http.MethodGet, Path: "/static/:file", Handler: web.StaticHandler()},
		// 登录页（公开）
		{Method: http.MethodGet, Path: "/login", Handler: web.HandleLoginPage()},
		// 根路径：根据登录态重定向（已登录 → /account）
		{Method: http.MethodGet, Path: "/", Handler: web.HandleIndex(webDeps)},
		// ---- 主菜单 ----
		{Method: http.MethodGet, Path: "/account",  Handler: authMW(web.HandleAccountsPage(webDeps))},
		{Method: http.MethodGet, Path: "/task",     Handler: authMW(web.HandleTasksPage(webDeps))},
		{Method: http.MethodGet, Path: "/settings", Handler: authMW(web.HandleSettingsPage(webDeps))},
		// ---- 通知 ----
		{Method: http.MethodGet, Path: "/account-alerts", Handler: authMW(web.HandleAccountAlertsPage(webDeps))},
		{Method: http.MethodGet, Path: "/tg-notify",      Handler: authMW(web.HandleTgNotifyPage(webDeps))},
		{Method: http.MethodGet, Path: "/emby-notify",    Handler: authMW(web.HandleEmbyNotifyPage(webDeps))},
		// ---- 日志 ----
		{Method: http.MethodGet, Path: "/history",     Handler: authMW(web.HandleTaskHistoryPage(webDeps))},
		{Method: http.MethodGet, Path: "/life-events", Handler: authMW(web.HandleLifeEventsPage(webDeps))},
	})
}

// ==================== 阶段6 依赖装配 ====================

// initPhase6Deps 装配 Telegram / Emby / LifeMonitor 三组 handler 依赖，并返回 Monitor 实例
// - Telegram: 优先复用 settings 中已配置的 BotToken 构造 TelegramBot + Dispatcher + PollingManager + CommandHandler
// - Emby: 按 settings 中的 emby.url/apiKey 构造 Client；Notifier 与 SyncDelete 使用 settingsFn 热重载
// - Monitor: 注入 lifeEventRepo / lifeEventLogRepo / stateMgr / dispatcher / accountStore
func initPhase6Deps(
	settingsStore *store.SettingsStore,
	tasksStore *store.TasksStore,
	accountStore *store.AccountStore,
	lifeEventRepo *db.LifeEventRepo,
	lifeEventLogRepo *db.LifeEventLogRepo,
	filePathRepo *db.FilePathRepo,
	stateMgr *runtime.StateManager,
) (handler.NotifyDeps, handler.EmbyDeps, handler.LifeMonitorDeps, *monitor.Monitor) {
	// 读取启动时配置，用于初始化 TelegramBot / EmbyClient
	initSettings, _ := settingsStore.ReadSettings()
	var tgSettings model.TelegramSettings
	var embySettings model.EmbySettings
	if initSettings != nil {
		tgSettings = initSettings.Telegram
		embySettings = initSettings.Emby
	}

	// ---------- Telegram / Notify ----------
	// 优先构造持久 TelegramBot（若启动时已配置 BotToken）；否则留 nil，由 handler 在请求时按 settings 临时构造
	var tgBot *notify.TelegramBot
	if tgSettings.BotToken != "" {
		tgBot = notify.NewTelegramBot(tgSettings.BotToken, tgSettings.ChatID)
	}
	dispatcher := notify.NewDispatcher(tgBot)
	if tgSettings.Enabled {
		dispatcher.SetEnabled(true)
	}
	dispatcher.SetWebhook(tgSettings.WebhookURL)

	var pollingMgr *notify.PollingManager
	var cmdHandler *notify.CommandHandler
	if tgBot != nil {
		pollingMgr = notify.NewPollingManager(tgBot)
		cmdHandler = notify.NewCommandHandler(tgBot, settingsStore, tasksStore, accountStore)
	}

	notifyDeps := handler.NotifyDeps{
		SettingsStore:   settingsStore,
		Dispatcher:      dispatcher,
		TelegramBot:     tgBot,
		PollingManager:  pollingMgr,
		CommandHandler: cmdHandler,
	}

	// ---------- Emby ----------
	var embyClient *emby.Client
	if embySettings.URL != "" && embySettings.APIKey != "" {
		embyClient = emby.NewClient(embySettings.URL, embySettings.APIKey)
	}
	embySettingsFn := func() model.EmbySettings {
		s, err := settingsStore.ReadSettings()
		if err != nil || s == nil {
			return model.EmbySettings{}
		}
		return s.Emby
	}
	embyNotifier := emby.NewNotifier(embyClient, dispatcher, embySettingsFn)
	embySyncDel := emby.NewSyncDelete(embyClient, dispatcher, embySettingsFn)
	embyNotifier.SetSyncDelete(embySyncDel)
	if filePathRepo != nil {
		embySyncDel.SetFilePathDb(filePathRepo)
	}

	embyDeps := handler.EmbyDeps{
		SettingsStore: settingsStore,
		EmbyNotifier:  embyNotifier,
		EmbyClient:    embyClient,
		SyncDelete:    embySyncDel,
	}

	// ---------- LifeMonitor ----------
	lifeSettingsFn := func() model.LifeMonitorSettings {
		s, err := settingsStore.ReadSettings()
		if err != nil || s == nil {
			return model.LifeMonitorSettings{}
		}
		return s.LifeMonitor
	}
	mon := monitor.NewMonitor(
		lifeSettingsFn,
		lifeEventRepo,
		lifeEventLogRepo,
		stateMgr,
		dispatcher,
		accountStore,
	)

	lifeMonitorDeps := handler.LifeMonitorDeps{
		SettingsStore:    settingsStore,
		Monitor:          mon,
		LifeEventLogRepo: lifeEventLogRepo,
	}

	return notifyDeps, embyDeps, lifeMonitorDeps, mon
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}
