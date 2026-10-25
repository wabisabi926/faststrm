package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/proc"
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
	"github.com/wabisabi926/faststrm/internal/service/rate"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/internal/web"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

var (
	shutdownMu sync.Mutex
	shutdownFn func(context.Context) error
)

// Shutdown 触发当前 Run 实例的优雅退出；未启动或已退出时为空操作，可重复调用。
func Shutdown(ctx context.Context) error {
	shutdownMu.Lock()
	fn := shutdownFn
	shutdownFn = nil // 一次性：重复调用直接返回
	shutdownMu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

func setShutdownFn(fn func(context.Context) error) {
	shutdownMu.Lock()
	shutdownFn = fn
	shutdownMu.Unlock()
}

// Run 启动 go-zero HTTP 服务
func Run(cfg *config.AppConfig) error { //nolint:cyclop // complexity: 40
	// 初始化日志
	logDir := ""
	if cfg.Server.Mode == "prod" {
		logDir = cfg.Paths.LogDir
	}
	logger.InitLogger(logDir, zapcore.InfoLevel)
	defer logger.Sync()
	// go-zero 在 Unix 上收到信号后会先排空再强杀；放宽窗口，避免任务排空被 5.5s 截断。
	proc.SetTimeToForceQuit(25 * time.Second)

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
	defer setShutdownFn(nil)

	// 初始化依赖
	issuer := auth.NewTokenIssuer([]byte(cfg.Settings.InternalToken))
	issuer.VersionFn = func() int { return config.Get().Admin.TokenVersion }
	issuer.Revocations = auth.NewRevocationList(filepath.Join(cfg.Paths.ConfigDir, ".jwt_revocations.json"))
	client := client115.NewClient(cfg.Settings.UserAgent)
	accountStore, err := store.NewAccountStore(cfg.Salt, cfg.Paths.ConfigDir)
	if err != nil {
		return fmt.Errorf("init account store: %w", err)
	}
	settingsStore := store.NewSettingsStore(cfg.Salt, cfg.Paths.ConfigDir)
	// 运行时设置快照：设置保存后由 OnSettingsSaved 原子替换，
	// 让 STRM 播放/诊断不必重启即可读到最新 UA、代理与分片参数。
	var runtimeSettings atomic.Pointer[model.Settings]
	settingsFn := func() *model.Settings {
		if s := runtimeSettings.Load(); s != nil {
			return s
		}
		return model.DefaultSettings()
	}
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
	if taskHistoryRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if n, merr := taskHistoryRepo.MarkInterrupted(ctx, "服务异常退出，执行记录已中断"); merr != nil {
			logger.S().Warnf("标记遗留执行记录失败: %v", merr)
		} else if n > 0 {
			logger.S().Infof("已将 %d 条遗留 running/pending 执行记录标记为失败", n)
		}
		cancel()
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
	if initSettings == nil {
		initSettings = model.DefaultSettings()
	}
	runtimeSettings.Store(initSettings)
	client.SetUserAgent(initSettings.EffectiveUserAgent())
	if initSettings.Emby.URL != "" && initSettings.Emby.APIKey != "" {
		embyClient = emby.NewClient(initSettings.Emby.URL, initSettings.Emby.APIKey)
	}
	// 限流参数装配：按「账号 × 用途」分桶，扫描与播放互不抢占配额。
	// 必须在任何 115 请求发生前调用，否则已创建的令牌桶会固化旧额度。
	rate.ApplySettings(initSettings)
	// 强制代理格式：单一列表控制，列表为空 = 关闭按格式代理
	if initSettings != nil {
		embyproxy.SetForceProxyFormats(initSettings.Strm.ForceProxyFormats)
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
	cloudChecker := strm.NewCloudChecker(client, accountStore)
	if cleanupInteraction != nil {
		cleanupInteraction.SetCloudChecker(cloudChecker)
	}
	execDeps := task.ExecutorDeps{
		Client115:        client,
		AccountStore:     accountStore,
		SettingsStore:    store.NewSettingsAdapter(settingsStore),
		SQLiteDB:         sqliteDB,
		TaskHistory:      taskHistoryRepo,
		TasksStore:       tasksStore,
		StrmCache:        &strmCacheWriterAdapter{inner: strmCacheStore},
		ManagedStrm:      strm.NewManagedRecorder(sqliteDB),
		CloudChecker:     cloudChecker,
		EmbyRefresh:      embyRefresh,
		CleanupSubmitter: &cleanupSubmitterAdapter{interaction: cleanupInteraction},
		BaseURL:          baseURL,
		PublicBaseURL:    cfg.Settings.StrmPrefix,
	}
	_ = scheduler.Init(execDeps, tasksStore, store.NewSettingsAdapter(settingsStore))

	// 历史 STRM 一次性纳管（异步）：只登记“内容含合法 pickcode + 账号存在 + 云端索引命中”的文件。
	// 在纳管完成前，自动清理只会跳过未登记文件，不会误删。
	if execDeps.ManagedStrm != nil {
		go func() {
			roots := make([]string, 0, 8)
			seen := make(map[string]struct{})
			addRoot := func(p string) {
				if p == "" {
					return
				}
				if _, ok := seen[p]; ok {
					return
				}
				seen[p] = struct{}{}
				roots = append(roots, p)
			}
			if initSettings != nil {
				for _, pm := range initSettings.LifeMonitor.PathMappings {
					addRoot(pm.LocalPath)
				}
			}
			if tasks, err := tasksStore.ReadTasks(); err == nil {
				for _, t := range tasks {
					addRoot(t.TargetPath)
				}
			}
			accounts := make([]string, 0, 4)
			for _, acc := range accountStore.List() {
				if acc.Name != "" {
					accounts = append(accounts, acc.Name)
				}
			}
			if len(roots) == 0 || len(accounts) == 0 {
				logger.S().Infof("[ManagedStrm] 无历史 STRM 纳管任务（roots=%d accounts=%d）", len(roots), len(accounts))
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			added, skipped, err := execDeps.ManagedStrm.Bootstrap(ctx, roots, accounts)
			if err != nil {
				logger.S().Warnf("[ManagedStrm] 历史纳管未完成: %v (added=%d skipped=%d)", err, added, skipped)
				return
			}
			logger.S().Infof("[ManagedStrm] 历史纳管完成: added=%d skipped=%d", added, skipped)
		}()
	}

	// ==================== 阶段6: 通知 + Emby + 生活监控依赖 ====================
	// 创建 EmbyProxyManager（支持热重启：用户改 ProxyPort / EmbyURL 后无需重启主程序）
	embyProxyManager := embyproxy.NewManager()
	// 自身 /api/strm 在进程内执行，避免 Emby 反代通过域名/NAT 再请求自己。
	// 外部 STRM / 自定义模板仍走 Proxy 内部的 HTTP fallback。
	embyProxyManager.SetInternalStrmHandler(handler.HandleStrm(handler.StrmOptions{
		Settings: settingsFn, Client115: client, AccountStore: accountStore,
	}))

	notifyDeps, embyDeps, lifeMonitorDeps, mon := initPhase6Deps(
		settingsStore, tasksStore, accountStore,
		lifeEventRepo, lifeEventLogRepo, filePathRepo, stateMgr,
		taskRuntime, execDeps, embyClient, embyRefresh,
		embyProxyManager, cleanupInteraction,
	)

	// 延迟注入 Notifier：dispatcher 在 initPhase6Deps 内创建
	// 1) 更新外部 execDeps（影响后续 RegisterRoutes 中 taskDeps）
	execDeps.Notifier = notifyDeps.Dispatcher
	// 2) 注入到调度器内部 deps（调度器内部持有参数副本的指针）
	scheduler.SetNotifier(notifyDeps.Dispatcher)

	// 设置保存后的运行时同步：替换 STRM 快照、热更新 115 UA、刷新生活监控配置。
	embyDeps.OnSettingsSaved = func(saved *model.Settings) {
		if saved == nil {
			return
		}
		runtimeSettings.Store(saved)
		client.SetUserAgent(saved.EffectiveUserAgent())
		if mon != nil {
			mon.Refresh()
		}
	}

	// 延迟注入 TelegramBot 到 cleanupInteraction（在 initPhase6Deps 创建 notifyDeps.TelegramBot 之后）
	if cleanupInteraction != nil && notifyDeps.TelegramBot != nil {
		cleanupInteraction.SetBot(notifyDeps.TelegramBot)
		logger.S().Infof("[Phase6] TelegramBot 已注入 cleanupInteraction，TG 按钮通知就绪")
	}

	// 注册路由
	RegisterRoutes(server, settingsFn, issuer, client, accountStore, settingsStore, tasksStore, strmCacheStore,
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

	// 账号 Cookie 主动巡检（默认每 6 小时一次，可在 settings.cookieInspect 关闭/调整）
	go startAccountInspector(context.Background(), settingsStore, accountStore, notifyDeps.Dispatcher)

	// ==================== Telegram 开机自动轮询（AutoPolling） ====================
	if initSettings != nil {
		go func() {
			// Telegram 初始化/自动轮询放入独立 goroutine：本机/内网若连不通
			// api.telegram.org，deleteWebhook / setMyCommands / GetUpdatesChan 的
			// 网络拨号会拖住主流程，导致 server.Run 迟迟不执行 server.Start()，
			// 最终 http://localhost:8090 打不开。异步化后 Web 服务立即可用。
			tg := initSettings.Telegram
			if tg.Enabled && tg.BotToken != "" && notifyDeps.TelegramBot != nil && tg.WebhookURL != "" {
				// 用户显式配置了 Webhook：重启后不能把它删掉改回轮询。
				logger.S().Infof("[Telegram] 已配置 Webhook，跳过开机自动轮询")
				return
			}
			if tg.Enabled && tg.AutoPolling && tg.BotToken != "" && notifyDeps.TelegramBot != nil {
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
					logger.S().Infof("[Telegram] AutoPolling 已自动启动（getUpdates: timeout=30, limit=100）")
				}
			} else if tg.BotToken != "" {
				logger.S().Infof("[Telegram] Bot 已配置，但 AutoPolling 未勾选，跳过自动轮询（可在设置中开启或通过 API /api/notify/polling 手动启动）")
			}
		}()
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
	// 如果用户配置了 Emby.ProxyPort，通过 manager.Start 启动反代
	// manager 封装了 Start/Stop/Restart，支持后续热重启
	if initSettings != nil && initSettings.Emby.ProxyPort > 0 && initSettings.Emby.URL != "" {
		embyURL := initSettings.Emby.URL
		proxyPort := initSettings.Emby.ProxyPort
		forceProxyUaTokens := initSettings.Strm.ForceProxyUaTokens
		if err := embyProxyManager.Start(cfg.Server.Host, proxyPort, embyURL, forceProxyUaTokens); err != nil {
			logger.S().Warnf("[EmbyProxy] 配置无效，跳过启动: %v", err)
		} else {
			proxyDisplayAddr := fmt.Sprintf("http://localhost:%d", proxyPort)
			if cfg.Server.Host != "" && cfg.Server.Host != "0.0.0.0" && cfg.Server.Host != "::" {
				proxyDisplayAddr = fmt.Sprintf("http://%s:%d", cfg.Server.Host, proxyPort)
			}
			logger.S().Infof("[EmbyProxy] 请将 Emby 客户端连接到 %s", proxyDisplayAddr)
		}
	}
	// 外部播放器开关（反代未启用时也记录，待启用后生效）
	if initSettings != nil {
		embyProxyManager.SetExternalPlayers(initSettings.Emby.ExternalPlayerEnabled)
	}

	// 优雅退出：停止接入与后台组件，取消并等待运行中任务。
	setShutdownFn(func(ctx context.Context) error {
		logger.S().Info("[Shutdown] 收到退出请求，开始排空...")

		// go-zero 的 Stop() 只关闭日志；HTTP 优雅停服由其 shutdown 监听器完成
		// （Unix 生效，Windows 为 no-op，进程退出时由系统关闭监听）。
		go proc.Shutdown()
		scheduler.Stop()
		if mon != nil {
			mon.StopAll()
		}
		embyProxyManager.StopAll()

		var errs []error
		if n := taskRuntime.CancelAll(); n > 0 {
			logger.S().Infof("[Shutdown] 已取消 %d 个运行中任务，等待退出...", n)
			if !taskRuntime.WaitAll(ctx) {
				errs = append(errs, fmt.Errorf("任务排空超时: %w", ctx.Err()))
			}
		}

		logger.Sync()
		logger.S().Info("[Shutdown] 排空完成")
		return errors.Join(errs...)
	})

	server.Start()
	return nil
}
