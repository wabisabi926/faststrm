package client115

import "strings"

// ValidateCookieResult cookie 校验结果
type ValidateCookieResult struct {
	Valid   bool     `json:"valid"`
	Missing []string `json:"missing"`
	Keys    []string `json:"keys"`
}

// requiredCookieFields 115 cookie 必需字段
var requiredCookieFields = []string{"UID", "CID", "SEID", "KID"}

// ValidateCookie 校验 cookie 是否包含必需字段
// 对齐 frontend/src/lib/115Life.ts validate115Cookie
func ValidateCookie(cookie string) ValidateCookieResult {
	parts := strings.Split(cookie, ";")
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eqIdx := strings.Index(p, "=")
		if eqIdx > 0 {
			keys = append(keys, p[:eqIdx])
		}
	}

	missing := make([]string, 0)
	for _, r := range requiredCookieFields {
		found := false
		for _, k := range keys {
			if k == r {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, r)
		}
	}

	return ValidateCookieResult{
		Valid:   len(missing) == 0,
		Missing: missing,
		Keys:    keys,
	}
}
