package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"go.uber.org/zap/zapcore"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/embyproxy"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/internal/web"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// Run 启动 go-zero HTTP 服务
func Run(cfg *config.AppConfig) error { //nolint:cyclop // complexity: 40
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
	// 仅当 sqliteDB 可用时创建，避免 nil pointer 风险
	var taskHistoryRepo *db.TaskHistoryRepo
	if sqliteDB != nil {
		taskHistoryRepo, err = db.NewTaskHistoryRepo(sqliteDB)
		if err != nil {
			logger.S().Warnf("NewTaskHistoryRepo failed: %v", err)
			taskHistoryRepo = nil
		}
	} else {
		logger.S().Warnf("sqlite not available, TaskHistoryRepo disabled")
	}
	var lifeEventRepo *db.LifeEventRepo
	if sqliteDB != nil {
		lifeEventRepo, err = db.NewLifeEventRepo(sqliteDB)
		if err != nil {
			logger.S().Warnf("NewLifeEventRepo failed: %v", err)
			lifeEventRepo = nil
		}
	} else {
		logger.S().Warnf("sqlite not available, LifeEventRepo disabled")
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
		TaskHistory:      taskHistoryRepo,
		TasksStore:       tasksStore,
		StrmCache:        &strmCacheWriterAdapter{inner: strmCacheStore},
		EmbyRefresh:      embyRefresh,
		CleanupSubmitter: &cleanupSubmitterAdapter{interaction: cleanupInteraction},
		BaseURL:          baseURL,
		PublicBaseURL:    cfg.Settings.StrmPrefix,
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

			// 3.5) 注册 Bot 命令菜单（SetMyCommands）— 让 /start /help 等出现在 Bot 菜单
			{
				regCtx, regCancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := notifyDeps.TelegramBot.SetMyCommands(regCtx, cmdHandler.BotCommandList()); err != nil {
					logger.S().Warnf("[Telegram] SetMyCommands 失败: %v", err)
				} else {
					logger.S().Infof("[Telegram] Bot 命令菜单已注册（%d 个命令）", len(cmdHandler.BotCommandList()))
				}
				regCancel()
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
				logger.S().Infof("[Telegram] AutoPolling 已自动启动（GetUpdatesChan: timeout=60, limit=100）")
			}
		} else if tg.BotToken != "" {
			logger.S().Infof("[Telegram] Bot 已配置，但 AutoPolling 未勾选，跳过自动轮询（可在设置中开启或通过 API /api/notify/polling 手动启动）")
		}
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	// 展示用地址：监听 host=0.0.0.0/:: 时用户实际访问要用 localhost，
	// 这里不影响实际绑定（Start() 仍用 addr 即 cfg.Server.Host 绑定）。
	displayHost := cfg.Server.Host
	switch cfg.Server.Host {
	case "", "0.0.0.0", "::", "[::]":
		displayHost = "localhost"
	}
	displayAddr := fmt.Sprintf("http://%s:%d", displayHost, cfg.Server.Port)
	logger.S().Infof("FastStrm-Go starting on %s (mode=%s)", displayAddr, cfg.Server.Mode)
	if displayAddr != fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port) {
		logger.S().Infof("           listen on http://%s (局域网/外网可访问)", addr)
	}
	logger.S().Infof("         data_dir: %s", cfg.Paths.DataDir)
	logger.S().Infof("       config_dir: %s", cfg.Paths.ConfigDir)
	logger.S().Infof("   internal_token: %s", maskToken(cfg.Settings.InternalToken))

	// ==================== Emby 反向代理（可选） ====================
	// 如果用户配置了 Emby.ProxyPort，启动反代 server 拦截 PlaybackInfo 强制 STRM DirectPlay
	if initSettings != nil && initSettings.Emby.ProxyPort > 0 && initSettings.Emby.URL != "" {
		embyURL := initSettings.Emby.URL
		proxyPort := initSettings.Emby.ProxyPort
		proxy, err := embyproxy.New(embyURL)
		if err != nil {
			logger.S().Warnf("[EmbyProxy] 配置无效，跳过启动: %v", err)
		} else {
			proxyHandler := proxy.Handler()

			proxyAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, proxyPort)
			proxyDisplayAddr := fmt.Sprintf("http://localhost:%d", proxyPort)
			if cfg.Server.Host != "" && cfg.Server.Host != "0.0.0.0" && cfg.Server.Host != "::" {
				proxyDisplayAddr = fmt.Sprintf("http://%s:%d", cfg.Server.Host, proxyPort)
			}

			go func() {
				logger.S().Infof("[EmbyProxy] 启动中: %s → %s", proxyAddr, embyURL)
				logger.S().Infof("[EmbyProxy] 请将 Emby 客户端连接到 %s", proxyDisplayAddr)
				logger.S().Infof("[EmbyProxy] STRM ISO/MKV 自动强制 DirectPlay，绕过 Emby 转码限制")
				proxySrv := &http.Server{
					Addr:         proxyAddr,
					Handler:      proxyHandler,
					ReadTimeout:  120 * time.Second,
					WriteTimeout: 120 * time.Second,
					IdleTimeout:  60 * time.Second,
				}
				if err := proxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.S().Warnf("[EmbyProxy] 启动失败 %s: %v", proxyAddr, err)
				}
			}()
		}
	}

	server.Start()
	return nil
}
