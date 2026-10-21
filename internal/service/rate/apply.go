package rate

import (
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ApplySettings 把 settings 中的限流参数装配到全局 Registry。
//
// 规则：未配置（0）的项回落到 Registry 内置默认值，
// 这样老版本 settings.json（没有 rateLimit 段）升级后行为等价于新默认值。
// GlobalCapQpm 传负数表示显式关闭全进程兜底。
//
// 调用时机：
//  1. 服务启动时（server.go）
//  2. 保存设置成功后（handler.HandleSettingsPOST）——使限流修改无需重启即可生效
func ApplySettings(s *model.Settings) {
	lim := Limits{
		API115QPM:    RegistryDefaultAPI115QPM,
		PlaybackQPM:  RegistryDefaultPlaybackQPM,
		LifeQPM:      RegistryDefaultLifeQPM,
		GlobalCapQPM: RegistryDefaultGlobalCapQPM,
	}
	if s != nil {
		if v := s.RateLimit.API115Qpm; v > 0 {
			lim.API115QPM = v
		}
		if v := s.RateLimit.PlaybackQpm; v > 0 {
			lim.PlaybackQPM = v
		}
		if v := s.RateLimit.LifeQpm; v > 0 {
			lim.LifeQPM = v
		}
		if v := s.RateLimit.GlobalCapQpm; v < 0 {
			lim.GlobalCapQPM = 0 // 负数 = 关闭兜底
		} else if v > 0 {
			lim.GlobalCapQPM = v
		}
	}
	GetRegistry().Configure(lim)
	logger.S().Infof("[RateLimit] 分桶限流已装配: api115=%d/min playback=%d/min life=%d/min globalCap=%d/min",
		lim.API115QPM, lim.PlaybackQPM, lim.LifeQPM, lim.GlobalCapQPM)
}
