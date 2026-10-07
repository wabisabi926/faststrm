package model

import "testing"

func TestAccountInfo_EffectiveCookieStatus(t *testing.T) {
	// 无任何状态信息 → unknown
	if got := (&AccountInfo{}).EffectiveCookieStatus(); got != CookieStatusUnknown {
		t.Errorf("空账号应为 unknown，got %q", got)
	}

	// 兼容字段派生
	tv, fv := true, false
	if got := (&AccountInfo{CookieValid: &tv}).EffectiveCookieStatus(); got != CookieStatusValid {
		t.Errorf("CookieValid=true 应派生 valid，got %q", got)
	}
	if got := (&AccountInfo{CookieValid: &fv}).EffectiveCookieStatus(); got != CookieStatusInvalid {
		t.Errorf("CookieValid=false 应派生 invalid，got %q", got)
	}

	// 三态字段优先于兼容字段
	if got := (&AccountInfo{CookieValid: &tv, CookieStatus: CookieStatusInvalid}).EffectiveCookieStatus(); got != CookieStatusInvalid {
		t.Errorf("CookieStatus 应优先，got %q", got)
	}
}

func TestAccountInfo_SetCookieStatus(t *testing.T) {
	a := &AccountInfo{}
	a.SetCookieStatus(CookieStatusInvalid, 99, CookieSourceProbe, 12345)
	if a.CookieStatus != CookieStatusInvalid || a.CookieErrno != 99 || a.CookieSource != CookieSourceProbe || a.LastCookieCheck != 12345 {
		t.Errorf("SetCookieStatus 字段写入不正确: %+v", a)
	}
	if a.CookieValid == nil || *a.CookieValid {
		t.Error("invalid 应同步 CookieValid=false")
	}

	a.SetCookieStatus(CookieStatusValid, 0, CookieSourceMonitor, 67890)
	if a.CookieValid == nil || !*a.CookieValid {
		t.Error("valid 应同步 CookieValid=true")
	}
}

func TestCookieInspectSettings(t *testing.T) {
	// 未配置 → 默认开启、默认 6 小时
	var c CookieInspectSettings
	if !c.IsEnabled() {
		t.Error("未配置时应默认开启巡检")
	}
	if c.EffectiveIntervalHours() != DefaultCookieInspectIntervalHours {
		t.Errorf("默认间隔应为 %d，got %d", DefaultCookieInspectIntervalHours, c.EffectiveIntervalHours())
	}

	// 显式关闭
	off := false
	c = CookieInspectSettings{Enabled: &off, IntervalHours: 12}
	if c.IsEnabled() {
		t.Error("显式 Enabled=false 应关闭巡检")
	}
	if c.EffectiveIntervalHours() != 12 {
		t.Errorf("间隔应为 12，got %d", c.EffectiveIntervalHours())
	}

	// 非法间隔回退默认
	c = CookieInspectSettings{IntervalHours: -1}
	if c.EffectiveIntervalHours() != DefaultCookieInspectIntervalHours {
		t.Errorf("非法间隔应回退默认，got %d", c.EffectiveIntervalHours())
	}
}

func TestDefaultSettings_CookieInspectEnabled(t *testing.T) {
	s := DefaultSettings()
	if !s.CookieInspect.IsEnabled() {
		t.Error("默认配置应开启 Cookie 主动巡检")
	}
	if s.CookieInspect.EffectiveIntervalHours() != DefaultCookieInspectIntervalHours {
		t.Error("默认配置巡检间隔应为 6 小时")
	}
}
