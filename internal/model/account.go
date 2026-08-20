package model

// AccountInfo 对应 frontend/src/lib/115.ts AccountInfo
// account.json 中每条记录的结构
type AccountInfo struct {
	Name           string `json:"name"`
	Cookie         string `json:"cookie,omitempty"`
	AccountType    string `json:"accountType,omitempty"`
	URL            string `json:"url,omitempty"`
	Token          string `json:"token,omitempty"`
	Account        string `json:"account,omitempty"`
	Password       string `json:"password,omitempty"`
	ExpiresAt      int64  `json:"expiresAt,omitempty"`
	LastCookieCheck int64 `json:"lastCookieCheck,omitempty"`
	CookieValid    *bool  `json:"cookieValid,omitempty"`
}

