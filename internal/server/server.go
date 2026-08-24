package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

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

	server := rest.MustNewServer(restCfg, rest.WithNotFoundHandler(web.SPAHandler()))
	defer server.Stop()

	// 初始化依赖
	issuer := auth.NewTokenIssuer([]byte(cfg.Settings.InternalToken))
	client := client115.NewClient(cfg.Settings.UserAgent)
	accountStore, err := store.NewAccountStore(cfg.Salt, cfg.Paths.ConfigDir)
	if err != nil {
		return fmt.Errorf("init account store: %w", err)
	}
	settingsStore := store.NewSettingsStore(cfg.Salt, cfg.Paths.ConfigDir)
	tasksStore := store.NewTasksStore(cfg.Paths.ConfigDir)
	strmCacheStore := store.NewStrmCacheStore(cfg.Paths.ConfigDir)
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

	// 提前创建 embyRefresh（需注入 execDeps 和 initPhase6Deps）
	embySettingsFn := func() model.EmbySettings {
		s, err := settingsStore.ReadSettings()
		if err != nil || s == nil {
			return model.EmbySettings{}
		}
		return s.Emby
	}
	var embyClient *emby.Client
	initSettings, _ := settingsStore.ReadSettings()
	if initSettings != nil && initSettings.Emby.URL != "" && initSettings.Emby.APIKey != "" {
		embyClient = emby.NewClient(initSettings.Emby.URL, initSettings.Emby.APIKey)
	}
	embyRefresh := emby.NewMediaServerRefresh(embyClient, embySettingsFn)

	// 提前创建 cleanupInteraction（先用 nil bot，等 notifyDeps.TelegramBot 就绪后通过 SetBot 注入）
	// 这样可以把它通过 cleanupSubmitterAdapter 注入到 execDeps.CleanupSubmitter，
	// 让 task 执行器的 removeExtraFiles 在入队延迟批次时能调用 AppendBatch 持久化到 SQLite。
	var cleanupInteraction *handler.StrmCleanupInteraction
	if sqliteDB != nil {
		cleanupInteraction, err = handler.NewStrmCleanupInteraction(sqliteDB, nil, settingsStore)
		if err != nil {
			logger.S().Warnf("[Phase6] init cleanup interaction failed (continue without pending queue): %v", err)
			cleanupInteraction = nil
		}
	} else {
		logger.S().Warnf("[Phase6] sqlite not available, cleanup interaction disabled")
	}
	execDeps := task.ExecutorDeps{
		Client115:        client,
		AccountStore:     accountStore,
		SettingsStore:    store.NewSettingsAdapter(settingsStore),
		SQLiteDB:         sqliteDB,
		TasksStore:       tasksStore,
		StrmCache:        &strmCacheWriterAdapter{inner: strmCacheStore},
		EmbyRefresh:      embyRefresh,
		CleanupSubmitter: &cleanupSubmitterAdapter{interaction: cleanupInteraction},
		BaseURL:          baseURL,
		PublicBaseURL:     cfg.Settings.StrmPrefix,
	}
	_ = scheduler.Init(execDeps, tasksStore, store.NewSettingsAdapter(settingsStore))

	// ==================== 阶段6: 通知 + Emby + 生活监控依赖 ====================
	notifyDeps, embyDeps, lifeMonitorDeps, mon := initPhase6Deps(
		settingsStore, tasksStore, accountStore,
		lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
		taskRuntime, execDeps, embyClient, embyRefresh,
	)

	// 延迟注入 Notifier：dispatcher 在 initPhase6Deps 内创建
	// 1) 更新外部 execDeps（影响后续 RegisterRoutes 中 taskDeps）
	execDeps.Notifier = notifyDeps.Dispatcher
	// 2) 注入到调度器内部 deps（调度器内部持有参数副本的指针）
	scheduler.SetNotifier(notifyDeps.Dispatcher)

	// 延迟注入 TelegramBot 到 cleanupInteraction（在 initPhase6Deps 创建 notifyDeps.TelegramBot 之后）
	if cleanupInteraction != nil && notifyDeps.TelegramBot != nil {
		cleanupInteraction.SetBot(notifyDeps.TelegramBot)
		logger.S().Infof("[Phase6] TelegramBot 已注入 cleanupInteraction，TG 按钮通知就绪")
	}

	// 注册路由
	RegisterRoutes(server, cfg, issuer, client, accountStore, settingsStore, tasksStore, strmCacheStore,
		taskHistoryRepo, taskRuntime, scheduler, execDeps,
		notifyDeps, embyDeps, lifeMonitorDeps, cleanupInteraction)

	// 若生活监控已在配置中启用，启动后台监控（异步）
	if mon != nil {
		go func() {
			if err := mon.StartAll(context.Background()); err != nil {
				logger.S().Warnf("[Phase6] auto-start monitor failed: %v", err)
			}
		}()
	}

	// ==================== Telegram 开机自动轮询（AutoPolling） ====================
	if initSettings != nil {
		tg := initSettings.Telegram
		if tg.Enabled && tg.AutoPolling && tg.BotToken != "" && notifyDeps.TelegramBot != nil {
			// Webhook 与轮询互斥：有 webhook 配置但也开了 AutoPolling 时，优先按用户勾选走轮询
			if tg.WebhookURL != "" {
				logger.S().Infof("[Telegram] AutoPolling 已启用，忽略 WebhookURL 配置")
			}
			// 1) 确保删除 webhook（轮询与 webhook 互斥）
			delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := notifyDeps.TelegramBot.DeleteWebhook(delCtx); err != nil {
				logger.S().Warnf("[Telegram] AutoPolling deleteWebhook (may be none): %v", err)
			}
			delCancel()

			// 2) 确保 PollingManager 已创建（启动时 tgBot != nil 时 initPhase6Deps 已创建，但兜底）
			pollingMgr := notifyDeps.PollingManager
			if pollingMgr == nil {
				pollingMgr = notify.NewPollingManager(notifyDeps.TelegramBot)
				notifyDeps.PollingManager = pollingMgr
			}
			// 3) 确保 CommandHandler 已创建（兜底）
			cmdHandler := notifyDeps.CommandHandler
			if cmdHandler == nil {
				cmdHandler = notify.NewCommandHandler(notifyDeps.TelegramBot, settingsStore, tasksStore, accountStore)
				notifyDeps.CommandHandler = cmdHandler
				// 兜底新建时补上 cleanup 回调（否则 STRM 清理确认按钮无响应）
				if ch := handler.SharedCleanupHandler(); ch != nil {
					cmdHandler.SetCleanupCallbackHandler(ch)
				}
			}

			// 4) 启动轮询：将 update 分发给 CommandHandler
			handlerFn := func(ctx context.Context, update notify.Update) error {
				if cmdHandler == nil {
					return nil
				}
				if update.Message != nil {
					return cmdHandler.HandleMessage(ctx, *update.Message)
				}
				if update.CallbackQuery != nil {
					return cmdHandler.HandleCallbackQuery(ctx, *update.CallbackQuery)
				}
				return nil
			}
			if err := pollingMgr.Start(context.Background(), handlerFn); err != nil {
				logger.S().Warnf("[Telegram] AutoPolling 启动失败: %v", err)
			} else {
				logger.S().Infof("[Telegram] AutoPolling 已自动启动（每 5 秒检查一次新消息）")
			}
		} else if tg.BotToken != "" {
			logger.S().Infof("[Telegram] Bot 已配置，但 AutoPolling 未勾选，跳过自动轮询（可在设置中开启或通过 API /api/notify/polling 手动启动）")
		}
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
	strmCacheStore *store.StrmCacheStore,
	taskHistoryRepo *db.TaskHistoryRepo,
	taskRuntime *task.Runtime,
	scheduler *task.Scheduler,
	execDeps task.ExecutorDeps,
	notifyDeps handler.NotifyDeps,
	embyDeps handler.EmbyDeps,
	lifeMonitorDeps handler.LifeMonitorDeps,
	cleanupInteraction *handler.StrmCleanupInteraction,
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
		{Method: http.MethodGet, Path: "/api/fs/get", Handler: middleware.CORS(handler.HandleFsGet(handler.StrmOptions{
			Cfg: cfg, Client115: client, AccountStore: accountStore,
		}))},
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
		// P0-4 STRM 执行历史：细粒度记录每次 STRM 生成/删除的操作历史
		{Method: http.MethodGet, Path: "/api/history/strm", Handler: corsJWT(handler.HandleStrmHistoryList(handler.StrmHistoryDeps{DB: execDeps.SQLiteDB}))},
		{Method: http.MethodGet, Path: "/api/history/strm/stats", Handler: corsJWT(handler.HandleStrmHistoryStats(handler.StrmHistoryDeps{DB: execDeps.SQLiteDB}))},
		{Method: http.MethodGet, Path: "/api/history/strm/:id", Handler: corsJWT(handler.HandleStrmHistoryDetail(handler.StrmHistoryDeps{DB: execDeps.SQLiteDB}))},
		// 目录
		{Method: http.MethodGet, Path: "/api/directory/remote/list", Handler: corsJWT(handler.HandleRemoteDirList(dirDeps))},
		{Method: http.MethodPost, Path: "/api/directory/local/list", Handler: corsJWT(handler.HandleLocalDirList(dirDeps))},
		{Method: http.MethodPost, Path: "/api/directory/local/listChildren", Handler: corsJWT(handler.HandleLocalDirListChildren(dirDeps))},
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

	// ==================== 媒体挂载路径同步 ====================
	mediaMountDeps := handler.MediaMountDeps{
		SettingsStore: settingsStore,
		AccountStore:  accountStore,
		TasksStore:    tasksStore,
	}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/mediaMountSync", Handler: corsJWT(handler.HandleMediaMountSyncGET(mediaMountDeps))},
		{Method: http.MethodPost, Path: "/api/mediaMountSync", Handler: corsJWT(handler.HandleMediaMountSyncPOST(mediaMountDeps))},
	})

	// ==================== STRM 清理 & 扫描 ====================
	// cleanupInteraction 已在 Run 主流程提前创建并注入到 execDeps.CleanupSubmitter（task 执行器延迟批次入队就绪）
	// 这里仅用于 strmCleanupDeps 装配 HTTP handler，并注入 TG CommandHandler 处理 inline 按钮回调
	if cleanupInteraction != nil {
		logger.S().Infof("[Phase6] cleanup interaction 复用（已注入 execDeps.CleanupSubmitter 与 HTTP handler）")
	}
	strmCleanupDeps := handler.StrmCleanupDeps{
		SettingsStore: settingsStore,
		AccountStore:  accountStore,
		ClientFactory: func(name string) (*client115.Client, error) {
			return client115.NewClient(""), nil
		},
		TasksStore:    tasksStore,
		StrmCache:     strmCacheStore,
		Interaction:   cleanupInteraction,
	}
	// P0 注入 cleanupInteraction 到 TG CommandHandler（处理 inline 按钮回调）
	if cleanupInteraction != nil && notifyDeps.CommandHandler != nil {
		notifyDeps.CommandHandler.SetCleanupCallbackHandler(cleanupInteraction)
		logger.S().Infof("[Phase6] cleanup interaction 已注入 TG CommandHandler")
	}
	// 额外：注册到 handler 包的全局 cleanup 回调，保证懒加载创建的 CommandHandler 也能拿到清理按钮回调
	// （场景：启动时未配置 Bot，后来通过 API 保存配置 → /api/notify/polling 启动轮询时懒加载 cmdHandler）
	if cleanupInteraction != nil {
		handler.SetSharedCleanupHandler(cleanupInteraction)
	}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/api/strmCleanup/scan", Handler: corsJWT(handler.HandleStrmCleanupScanPOST(strmCleanupDeps))},
		{Method: http.MethodPost, Path: "/api/strmCleanup/execute", Handler: corsJWT(handler.HandleStrmCleanupExecutePOST(strmCleanupDeps))},
		// P0 待删批次二次确认 API
		{Method: http.MethodGet, Path: "/api/strmCleanup/pending", Handler: corsJWT(handler.HandleStrmCleanupPendingListGET(strmCleanupDeps))},
		{Method: http.MethodPost, Path: "/api/strmCleanup/pending/cancel", Handler: corsJWT(handler.HandleStrmCleanupPendingCancelPOST(strmCleanupDeps))},
		{Method: http.MethodPost, Path: "/api/strmCleanup/pending/execute", Handler: corsJWT(handler.HandleStrmCleanupPendingExecutePOST(strmCleanupDeps))},
	})

	// ==================== 阶段6: 生活监控 + 事件日志 ====================
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/lifeMonitor", Handler: corsJWT(handler.HandleLifeMonitorGET(lifeMonitorDeps))},
		{Method: http.MethodPost, Path: "/api/lifeMonitor", Handler: corsJWT(handler.HandleLifeMonitorPOST(lifeMonitorDeps))},
		{Method: http.MethodGet, Path: "/api/lifeEvents", Handler: corsJWT(handler.HandleLifeEventsGET(lifeMonitorDeps))},
		{Method: http.MethodDelete, Path: "/api/lifeEvents", Handler: corsJWT(handler.HandleLifeEventsDELETE(lifeMonitorDeps))},
	})

	// ==================== 阶段8: Web UI（Vite + React SPA） ====================
	// 所有非 API 路由由 NotFoundHandler (web.SPAHandler) 处理
	// API 路由优先匹配，未匹配的 GET 请求自动 fallback 到 SPA
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
	taskRuntime *task.Runtime,
	execDeps task.ExecutorDeps,
	embyClient *emby.Client,
	embyRefresh *emby.MediaServerRefresh,
) (handler.NotifyDeps, handler.EmbyDeps, handler.LifeMonitorDeps, *monitor.Monitor) {
	// 读取启动时配置，用于初始化 TelegramBot
	initSettings, _ := settingsStore.ReadSettings()
	var tgSettings model.TelegramSettings
	if initSettings != nil {
		tgSettings = initSettings.Telegram
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

	// 延迟注入 Notifier 到 execDeps（因为 dispatcher 刚创建好）
	// execDeps 是参数值拷贝，本函数内修改影响 menuActionsAdapter
	execDeps.Notifier = dispatcher

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
		TasksStore:      tasksStore,
		AccountStore:    accountStore,
	}

	// ---------- Emby ----------
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
			return model.DefaultSettings().LifeMonitor
		}
		life := s.LifeMonitor
		// 继承全局的 STRM 配置（若 life 自身未设置）
		if life.StrmPrefix == "" {
			life.StrmPrefix = s.StrmPrefix
		}
		// 全局前缀仍为空则使用默认值
		if life.StrmPrefix == "" {
			life.StrmPrefix = "http://127.0.0.1:8090"
		}
		// Enable302/EnablePathEncoding：以全局为准（life 未单独开关的场景）
		// 若 life 自身未显式设置，继承全局
		// （注意：bool 零值无法区分"未设置"和"显式 false"，这里直接用全局 OR 覆盖即可）
		if s.Enable302 {
			life.Enable302 = true
		}
		if s.EnablePathEncoding {
			life.EnablePathEncoding = true
		}
		// 黑名单关键词：life 自身未设置（空切片）时，继承全局 Download.StrmGenerateBlacklist
		if len(life.StrmGenerateBlacklist) == 0 {
			life.StrmGenerateBlacklist = s.Download.StrmGenerateBlacklist
		}
		// OverwriteMode：life 自身未设置（空字符串）时，继承全局 Download.OverwriteMode
		if life.OverwriteMode == "" {
			life.OverwriteMode = s.Download.OverwriteMode
		}
		return life
	}
	mon := monitor.NewMonitor(
		lifeSettingsFn,
		lifeEventRepo,
		lifeEventLogRepo,
		stateMgr,
		dispatcher,
		accountStore,
		embyRefresh,
		execDeps.SQLiteDB,
	)

	// ---------- MenuActions 适配器 ----------
	if cmdHandler != nil {
		adapter := &menuActionsAdapter{
			settingsStore: settingsStore,
			tasksStore:    tasksStore,
			accountStore:  accountStore,
			monitor:       mon,
			embyRefresh:   embyRefresh,
			taskRuntime:   taskRuntime,
			execDeps:      execDeps,
		}
		cmdHandler.SetMenuActions(adapter)
	}

	lifeMonitorDeps := handler.LifeMonitorDeps{
		SettingsStore:    settingsStore,
		Monitor:          mon,
		LifeEventLogRepo: lifeEventLogRepo,
	}

	return notifyDeps, embyDeps, lifeMonitorDeps, mon
}

// ==================== MenuActions 适配器 ====================

// menuActionsAdapter 实现 notify.MenuActions 接口
// 将 Telegram 菜单动作委托给 Monitor / Task / Emby 等服务
type menuActionsAdapter struct {
	settingsStore *store.SettingsStore
	tasksStore    *store.TasksStore
	accountStore  *store.AccountStore
	monitor       *monitor.Monitor
	embyRefresh   *emby.MediaServerRefresh
	taskRuntime   *task.Runtime
	execDeps      task.ExecutorDeps
}

// GetSystemStatus 聚合账号、监控、运行任务和 Emby 状态
func (a *menuActionsAdapter) GetSystemStatus() (map[string]any, error) {
	accounts := a.accountStore.List()
	var accountList []map[string]any
	for _, acc := range accounts {
		accountList = append(accountList, map[string]any{
			"name":     acc.Name,
			"hasCookie": acc.Cookie != "",
		})
	}

	monitorStatus := a.internalGetMonitorStatus()
	runningTasks := a.internalListRunningTasks()

	embyStatus := map[string]any{"connected": false}
	if a.embyRefresh != nil {
		embyStatus = a.embyRefresh.GetStatus()
	}

	return map[string]any{
		"accounts":     accountList,
		"monitors":     monitorStatus["monitors"],
		"runningTasks": runningTasks,
		"emby":         embyStatus,
	}, nil
}

// StartMonitor 启动指定账号的监控
func (a *menuActionsAdapter) StartMonitor(ctx context.Context, account string) error {
	if a.monitor == nil {
		return fmt.Errorf("monitor not initialized")
	}
	return a.monitor.Start(ctx, account)
}

// StopMonitor 停止指定账号的监控
func (a *menuActionsAdapter) StopMonitor(ctx context.Context, account string) error {
	if a.monitor == nil {
		return fmt.Errorf("monitor not initialized")
	}
	a.monitor.Stop(account)
	return nil
}

// StopAllMonitors 停止所有监控
func (a *menuActionsAdapter) StopAllMonitors(ctx context.Context) error {
	if a.monitor == nil {
		return fmt.Errorf("monitor not initialized")
	}
	a.monitor.StopAll()
	return nil
}

// GetMonitorStatus 获取监控状态
func (a *menuActionsAdapter) GetMonitorStatus() (map[string]any, error) {
	return a.internalGetMonitorStatus(), nil
}

func (a *menuActionsAdapter) internalGetMonitorStatus() map[string]any {
	result := map[string]any{"monitors": []map[string]any{}}
	if a.monitor == nil {
		return result
	}
	status := a.monitor.Status()
	var monitors []map[string]any
	for _, s := range status {
		monitors = append(monitors, map[string]any{
			"account": s.Account,
			"running": s.Running,
		})
	}
	result["monitors"] = monitors

	// 返回事件开关状态（供 TG 菜单动态显示）
	if s, err := a.settingsStore.ReadSettings(); err == nil {
		et := s.LifeMonitor.EventTypes
		result["eventTypes"] = map[string]bool{
			"create":  et.Create,
			"remove":  et.Remove,
			"rename":  et.Rename,
			"move":    et.Move,
		}
	}
	return result
}

// ToggleMonitorEvent 切换监控事件类型（持久化到 settings.json）
func (a *menuActionsAdapter) ToggleMonitorEvent(ctx context.Context, account, eventType string, enabled bool) error {
	s, err := a.settingsStore.ReadSettings()
	if err != nil {
		return fmt.Errorf("读取设置失败: %w", err)
	}
	switch eventType {
	case "create":
		s.LifeMonitor.EventTypes.Create = enabled
	case "remove":
		s.LifeMonitor.EventTypes.Remove = enabled
	case "rename":
		s.LifeMonitor.EventTypes.Rename = enabled
	case "move":
		s.LifeMonitor.EventTypes.Move = enabled
	default:
		return fmt.Errorf("未知事件类型: %s", eventType)
	}
	if err := a.settingsStore.SaveSettings(s); err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	return nil
}

// ExecuteTask 执行指定任务
func (a *menuActionsAdapter) ExecuteTask(ctx context.Context, taskID string) (map[string]any, error) {
	result := task.ExecuteTask(ctx, taskID, a.execDeps)
	return map[string]any{
		"success": result.Success,
		"message": result.Message,
		"taskId":  result.TaskID,
	}, nil
}

// CancelTask 取消指定任务
func (a *menuActionsAdapter) CancelTask(ctx context.Context, taskID string) error {
	if a.taskRuntime == nil {
		return fmt.Errorf("task runtime not initialized")
	}
	found := a.taskRuntime.Cancel(taskID)
	if !found {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// ListRunningTasks 列出运行中任务
func (a *menuActionsAdapter) ListRunningTasks() ([]map[string]any, error) {
	return a.internalListRunningTasks(), nil
}

func (a *menuActionsAdapter) internalListRunningTasks() []map[string]any {
	if a.taskRuntime == nil {
		return []map[string]any{}
	}
	running := a.taskRuntime.RunningTasks()
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return []map[string]any{}
	}

	var result []map[string]any
	for id, state := range running {
		var taskName string
		for _, t := range tasks {
			if t.ID == id {
				taskName = t.Name
				break
			}
		}
		progress := string(state.Status)
		if state.TotalFiles > 0 {
			pct := float64(state.DownloadedFiles) / float64(state.TotalFiles) * 100
			progress = fmt.Sprintf("%.0f%% (%d/%d)", pct, state.DownloadedFiles, state.TotalFiles)
		}
		result = append(result, map[string]any{
			"id":       id,
			"name":     taskName,
			"progress": progress,
		})
	}
	return result
}

// ListScheduledTasks 列出定时任务
func (a *menuActionsAdapter) ListScheduledTasks() ([]map[string]any, error) {
	tasks, err := a.tasksStore.ReadTasks()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, t := range tasks {
		if t.Schedule != nil && t.Schedule.Enabled {
			schedule := a.formatSchedule(t.Schedule)
			result = append(result, map[string]any{
				"id":       t.ID,
				"name":     t.Name,
				"schedule": schedule,
			})
		}
	}
	return result, nil
}

func (a *menuActionsAdapter) formatSchedule(s *task.TaskSchedule) string {
	switch s.Mode {
	case "interval":
		return fmt.Sprintf("每 %d 分钟", s.IntervalMinutes)
	case "daily":
		return fmt.Sprintf("每天 %s", s.Time)
	case "weekly":
		return fmt.Sprintf("每周 %s", s.Time)
	default:
		return "未知"
	}
}

// RefreshEmbyByPath 按路径刷新 Emby
func (a *menuActionsAdapter) RefreshEmbyByPath(ctx context.Context, path string) error {
	if a.embyRefresh == nil {
		return fmt.Errorf("emby refresh not initialized")
	}
	return a.embyRefresh.RefreshByPath(ctx, path)
}

// RefreshEmbyLibrary 刷新 Emby 媒体库
func (a *menuActionsAdapter) RefreshEmbyLibrary(ctx context.Context, libraryType string) error {
	if a.embyRefresh == nil {
		return fmt.Errorf("emby refresh not initialized")
	}
	return a.embyRefresh.RefreshLibrary(ctx, libraryType)
}

// GetEmbyStatus 获取 Emby 状态
func (a *menuActionsAdapter) GetEmbyStatus() (map[string]any, error) {
	if a.embyRefresh == nil {
		return map[string]any{"connected": false}, nil
	}
	return a.embyRefresh.GetStatus(), nil
}

// RunFullSync 全量同步（占位实现）
func (a *menuActionsAdapter) RunFullSync(ctx context.Context) error {
	return nil
}

// RunCleanup 清理孤儿（占位实现）
func (a *menuActionsAdapter) RunCleanup(ctx context.Context) error {
	return nil
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

// strmCacheWriterAdapter adapts *store.StrmCacheStore to task.StrmCacheWriter
type strmCacheWriterAdapter struct{ inner *store.StrmCacheStore }

func (a *strmCacheWriterAdapter) Save(entry task.StrmCacheEntryLike) error {
	return a.inner.Save(&store.StrmCacheEntry{
		UUID: entry.UUID, TaskID: entry.TaskID, Target: entry.Target,
		Account: entry.Account, RelPaths: entry.RelPaths, LocalPaths: entry.LocalPaths,
		CreatedAt: entry.CreatedAt,
	})
}

// cleanupSubmitterAdapter 把 handler.StrmCleanupInteraction 适配为 task.CleanupBatchSubmitter
// 在 Run 主流程提前创建 cleanupInteraction 时注入，让 task 执行器 removeExtraFiles
// 能调用 AppendBatch 把延迟批次持久化到 SQLite，按 ConfirmMode=="telegram" 派发 TG 通知。
type cleanupSubmitterAdapter struct {
	interaction *handler.StrmCleanupInteraction
}

// SubmitDeferredBatch 实现 task.CleanupBatchSubmitter 接口
// interaction 为 nil（SQLite 不可用）时返回错误，调用方（removeExtraFiles）会退化为立即删除。
func (a *cleanupSubmitterAdapter) SubmitDeferredBatch(ctx context.Context, b task.DeferredCleanupBatch) (string, error) {
	if a.interaction == nil {
		return "", fmt.Errorf("cleanup interaction not initialized")
	}
	batch := handler.CleanupBatch{
		RequestID:          b.RequestID,
		CreatedAt:          b.CreatedAt.UnixMilli(),
		Paths:              b.Paths,
		SamplePaths:        handler.BuildSamplePaths(b.Paths),
		PathCount:          len(b.Paths),
		RemoveStrm:         b.RemoveStrm,
		RemoveEmptyDirs:    b.RemoveEmptyDirs,
		RemoveRelatedFiles: b.RemoveRelated,
	}
	if err := a.interaction.AppendBatch(ctx, batch); err != nil {
		return "", fmt.Errorf("AppendBatch: %w", err)
	}
	// 按 ConfirmMode 派发通知（仅 telegram 模式发 TG 按钮；plugin_ui 由前端轮询 /pending）
	if b.ConfirmMode == "telegram" {
		if err := a.interaction.NotifyTelegramPending(ctx, batch); err != nil {
			// 通知失败不影响入队结果，仅记录警告
			logger.S().Warnf("[cleanupSubmitterAdapter] NotifyTelegramPending failed (requestID=%s): %v", b.RequestID, err)
		}
	}
	return b.RequestID, nil
}