package server

import (
	"context"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// 账号 Cookie 主动巡检：
// 生活事件监控只覆盖「已开启监控」的账号，未开监控的账号 Cookie 过期后无人发现，
// 直到用户点播失败才暴露。这里用权威探测接口（client115.ProbeAccount）定时巡检，
// 补齐状态盲区。默认每 6 小时一次，可通过 settings.cookieInspect 关闭或调整。

// accountInspectInitialDelay 首次巡检启动延迟：避开启动时的 115 接口争抢
const accountInspectInitialDelay = 90 * time.Second

// accountInspectPerAccountTimeout 单账号探测超时
const accountInspectPerAccountTimeout = 15 * time.Second

// startAccountInspector 启动主动巡检后台循环（阻塞，调用方需 go 起）。
func startAccountInspector(ctx context.Context, settingsStore *store.SettingsStore, accountStore *store.AccountStore) {
	timer := time.NewTimer(accountInspectInitialDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		cfg := loadCookieInspectSettings(settingsStore)
		if cfg.IsEnabled() {
			runAccountInspection(ctx, accountStore)
		} else {
			logger.S().Infof("[CookieInspect] 主动巡检已关闭，跳过本轮")
		}

		timer.Reset(time.Duration(cfg.EffectiveIntervalHours()) * time.Hour)
	}
}

// loadCookieInspectSettings 读取巡检配置；读取失败时回退默认配置（默认开启）。
func loadCookieInspectSettings(settingsStore *store.SettingsStore) model.CookieInspectSettings {
	if s, err := settingsStore.ReadSettings(); err == nil && s != nil {
		return s.CookieInspect
	}
	def := model.DefaultSettings()
	return def.CookieInspect
}

// runAccountInspection 对所有 115 账号执行一次权威探测并回写状态。
func runAccountInspection(ctx context.Context, accountStore *store.AccountStore) {
	accounts := accountStore.List()
	checked, invalid := 0, 0

	for _, acc := range accounts {
		if acc == nil || acc.AccountType != "115" || acc.Cookie == "" {
			continue
		}

		pctx, cancel := context.WithTimeout(ctx, accountInspectPerAccountTimeout)
		err := client115.ProbeAccount(pctx, acc.Cookie)
		cancel()

		status := classifyInspectStatus(err)
		if status == model.CookieStatusUnknown {
			// 风控/网络等临时错误：保留既有明确状态，不误伤
			logger.S().Infof("[CookieInspect] account=%s 暂不可判定（风控/网络），保留原状态: %v", acc.Name, err)
			continue
		}
		if serr := accountStore.SetCookieStatus(acc.Name, status, client115.ErrnoOf(err), model.CookieSourceProbe); serr != nil {
			logger.S().Warnf("[CookieInspect] account=%s 写入状态失败: %v", acc.Name, serr)
			continue
		}
		checked++
		if status == model.CookieStatusInvalid {
			invalid++
			logger.S().Warnf("[CookieInspect] account=%s Cookie 已失效，请重新登录: %v", acc.Name, err)
		} else {
			logger.S().Infof("[CookieInspect] account=%s Cookie 有效", acc.Name)
		}
	}

	if checked > 0 {
		logger.S().Infof("[CookieInspect] 本轮完成：检查 %d 个 115 账号，其中 %d 个失效", checked, invalid)
	}
}

// classifyInspectStatus 将探测结果映射为三态状态。
func classifyInspectStatus(err error) string {
	switch {
	case err == nil:
		return model.CookieStatusValid
	case client115.IsAuthError(err):
		return model.CookieStatusInvalid
	default:
		// 风控/临时错误：不改写账号状态
		return model.CookieStatusUnknown
	}
}
