package server

import (
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

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
		var err error
		tgBot, err = notify.CreateBotFromSettings(tgSettings)
		if err != nil {
			logger.S().Errorf("[Telegram] 创建 Bot 失败: %v", err)
		}
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
		SettingsStore:  settingsStore,
		Dispatcher:     dispatcher,
		TelegramBot:    tgBot,
		PollingManager: pollingMgr,
		CommandHandler: cmdHandler,
		TasksStore:     tasksStore,
		AccountStore:   accountStore,
	}

	// ---------- Emby ----------
	embySettingsFn := func() model.EmbySettings {
		s, err := settingsStore.ReadSettings()
		if err != nil || s == nil {
			return model.EmbySettings{}
		}
		return s.Emby
	}
	embyNotifier := emby.NewNotifier(dispatcher, embySettingsFn)
	embySyncDel := emby.NewSyncDelete(dispatcher, embySettingsFn)
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
