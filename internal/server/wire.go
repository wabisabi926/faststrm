package server

import (
	"github.com/wabisabi926/faststrm/internal/handler"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/emby"
	"github.com/wabisabi926/faststrm/internal/service/embyproxy"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/runtime"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// newTelegramDispatcher 按启动配置构造通知分发器。
// NewDispatcher 在 Token+ChatID 存在时会默认启用；这里必须按用户开关覆盖，
// 否则「启用通知=false」只在保存设置后生效，重启后又会继续发送。
func newTelegramDispatcher(tg model.TelegramSettings) (*notify.Dispatcher, *notify.TelegramBot) {
	var tgBot *notify.TelegramBot
	if tg.BotToken != "" {
		var err error
		tgBot, err = notify.CreateBotFromSettings(tg)
		if err != nil {
			logger.S().Errorf("[Telegram] 创建 Bot 失败: %v", err)
		}
	}
	dispatcher := notify.NewDispatcher(tgBot)
	dispatcher.SetEnabled(tg.Enabled)
	return dispatcher, tgBot
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
	embyProxyManager *embyproxy.Manager,
	cleanupInteraction *handler.StrmCleanupInteraction,
) (handler.NotifyDeps, handler.EmbyDeps, handler.LifeMonitorDeps, *monitor.Monitor) {
	// 读取启动时配置，用于初始化 TelegramBot
	initSettings, _ := settingsStore.ReadSettings()
	var tgSettings model.TelegramSettings
	if initSettings != nil {
		tgSettings = initSettings.Telegram
	}

	// ---------- Telegram / Notify ----------
	dispatcher, tgBot := newTelegramDispatcher(tgSettings)

	// 延迟注入 Notifier 到 execDeps（因为 dispatcher 刚创建好）
	// execDeps 是参数值拷贝，本函数内修改影响 menuActionsAdapter
	execDeps.Notifier = dispatcher

	var pollingMgr *notify.PollingManager
	if tgBot != nil {
		pollingMgr = notify.NewPollingManager(tgBot)
	}
	// 即使启动时未配置 Bot 也创建 CommandHandler：Webhook 模式下用户之后补配 Bot 时，
	// 消息需要立即有处理器接管，不能等到重启。
	cmdHandler := notify.NewCommandHandler(tgBot, settingsStore, tasksStore, accountStore)

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
		SettingsStore:    settingsStore,
		AccountStore:     accountStore,
		EmbyNotifier:     embyNotifier,
		EmbyClient:       embyClient,
		SyncDelete:       embySyncDel,
		EmbyProxyManager: embyProxyManager,
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
		// 仍不可用（空 / 回环 / 0.0.0.0）时，用本机内网 IP 兜底，
		// 避免写入 http://127.0.0.1:8090 这类只有本机能播的地址。
		// 与任务侧共用同一套解析逻辑，保证两边生成的前缀一致。
		if strm.IsUnusablePrefix(life.StrmPrefix) {
			prefix, source := strm.ResolveStrmPrefix([]string{life.StrmPrefix}, strm.PortFromBaseURL(execDeps.BaseURL))
			if source == "auto_lan_ip" {
				logger.S().Warnf("[LifeMonitor] STRM 前缀未配置，已自动使用内网地址 %s", prefix)
			}
			life.StrmPrefix = prefix
		}
		// URL 参数编码开关：life 侧没有独立 UI，始终继承全局设置，
		// 保证监控与任务生成的 URL 编码规则一致（都编码 / 都不编码）
		life.EnablePathEncoding = s.EnablePathEncoding
		// 黑名单关键词：life 自身未设置（空切片）时，继承全局 Download.StrmGenerateBlacklist
		if len(life.StrmGenerateBlacklist) == 0 {
			life.StrmGenerateBlacklist = s.Download.StrmGenerateBlacklist
		}
		// STRM 扩展名：life 自身未设置时继承全局。
		// 修复历史问题：监控侧曾硬编码 model.DefaultStrmExtensions，
		// 导致用户在全局配置的自定义扩展名对生活事件完全无效。
		if len(life.StrmExtensions) == 0 {
			life.StrmExtensions = s.StrmExtensions
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
	mon.SetManagedRecorder(execDeps.ManagedStrm)
	// 辅助文件自动同步（字幕/nfo/海报）需要全局 DownloadExtensions
	mon.SetAppSettingsFn(func() *model.Settings {
		s, err := settingsStore.ReadSettings()
		if err != nil {
			return nil
		}
		return s
	})

	// ---------- MenuActions 适配器 ----------
	if cmdHandler != nil {
		adapter := &menuActionsAdapter{
			settingsStore:      settingsStore,
			tasksStore:         tasksStore,
			accountStore:       accountStore,
			monitor:            mon,
			embyRefresh:        embyRefresh,
			taskRuntime:        taskRuntime,
			execDeps:           execDeps,
			cmdHandler:         cmdHandler,
			cleanupInteraction: cleanupInteraction,
			cleanupDeps: handler.StrmCleanupDeps{
				SettingsStore: settingsStore,
				AccountStore:  accountStore,
				SQLiteDB:      execDeps.SQLiteDB,
				Interaction:   cleanupInteraction,
			},
		}
		cmdHandler.SetMenuActions(adapter)
	}

	lifeMonitorDeps := handler.LifeMonitorDeps{
		SettingsStore:    settingsStore,
		AccountStore:     accountStore,
		Monitor:          mon,
		LifeEventLogRepo: lifeEventLogRepo,
	}

	return notifyDeps, embyDeps, lifeMonitorDeps, mon
}
