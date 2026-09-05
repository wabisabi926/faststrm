package server

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/server/middleware"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/sse"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

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
		{Method: http.MethodHead, Path: "/api/fs/get", Handler: middleware.CORS(handler.HandleFsGet(handler.StrmOptions{
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

		// STRM 缓存管理
		{Method: http.MethodPost, Path: "/api/strm/cache/clear", Handler: corsJWT(handler.HandleStrmCacheClear())},
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
		{Method: http.MethodDelete, Path: "/api/taskHistory", Handler: corsJWT(handler.HandleTaskHistoryDelete(handler.TaskHistoryDeps{Repo: taskHistoryRepo}))},
		{Method: http.MethodGet, Path: "/api/taskHistory/:executionId/logs", Handler: corsJWT(handler.HandleTaskHistoryLogs(handler.TaskHistoryDeps{Repo: taskHistoryRepo}))},
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
		TasksStore:  tasksStore,
		StrmCache:   strmCacheStore,
		Interaction: cleanupInteraction,
		SQLiteDB:    execDeps.SQLiteDB,
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
		// P3：STRM 内容预览（StaleStrmDialog "查看完整"调用）
		{Method: http.MethodPost, Path: "/api/strmCleanup/preview", Handler: corsJWT(handler.HandleStrmCleanupPreviewPOST(strmCleanupDeps))},
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

	// ==================== API 文档（Swagger UI，公开） ====================
	RegisterDocs(server)
}
