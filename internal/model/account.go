package model

// Cookie 状态三态常量。
//
// 只有 invalid 才代表「确定的失效」；unknown 表示临时不可用或尚未做存活校验，
// 不得据此把账号判为失效，也不应显示为「有效」。
const (
	CookieStatusValid   = "valid"
	CookieStatusInvalid = "invalid"
	CookieStatusUnknown = "unknown"
)

// 状态判定来源，便于排查。
const (
	CookieSourceProbe   = "probe"   // 权威探测接口
	CookieSourceMonitor = "monitor" // 生活事件监控被动判定
	CookieSourceStrm    = "strm"    // STRM 直链获取失败联动
	CookieSourceFormat  = "format"  // 仅格式校验
)

// AccountInfo 对应 frontend/src/lib/115.ts AccountInfo
// account.json 中每条记录的结构
type AccountInfo struct {
	Name            string `json:"name"`
	Cookie          string `json:"cookie,omitempty"`
	AccountType     string `json:"accountType,omitempty"`
	URL             string `json:"url,omitempty"`
	Token           string `json:"token,omitempty"`
	Account         string `json:"account,omitempty"`
	Password        string `json:"password,omitempty"`
	ExpiresAt       int64  `json:"expiresAt,omitempty"`
	LastCookieCheck int64  `json:"lastCookieCheck,omitempty"`
	// CookieValid 兼容旧数据/旧前端：由 CookieStatus 派生并保持同步。
	CookieValid *bool `json:"cookieValid,omitempty"`
	// CookieStatus 三态状态：valid / invalid / unknown。
	CookieStatus string `json:"cookieStatus,omitempty"`
	// CookieErrno 最近一次判定拿到的 115 错误码（0 表示无）。
	CookieErrno int `json:"cookieErrno,omitempty"`
	// CookieSource 最近一次判定来源。
	CookieSource string `json:"cookieSource,omitempty"`
}

// EffectiveCookieStatus 返回三态状态，兼容尚未迁移的旧数据（仅有 cookieValid 字段）。
func (a *AccountInfo) EffectiveCookieStatus() string {
	if a.CookieStatus != "" {
		return a.CookieStatus
	}
	if a.CookieValid == nil {
		return CookieStatusUnknown
	}
	if *a.CookieValid {
		return CookieStatusValid
	}
	return CookieStatusInvalid
}

// SetCookieStatus 写入三态状态，并同步兼容字段 CookieValid。
func (a *AccountInfo) SetCookieStatus(status string, errno int, source string, checkedAt int64) {
	a.CookieStatus = status
	a.CookieErrno = errno
	a.CookieSource = source
	a.LastCookieCheck = checkedAt
	switch status {
	case CookieStatusValid:
		v := true
		a.CookieValid = &v
	case CookieStatusInvalid:
		v := false
		a.CookieValid = &v
	}
}
