package client115

import (
	"context"
	"strings"
	"time"
)

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

// PingCookie 真实请求 115 API 验证 cookie 是否存活
// 比 ValidateCookie 只验格式多了一步网络验证，能检测 cookie 过期/被踢
func PingCookie(cookie string) (ok bool, message string) {
	if cookie == "" {
		return false, "Cookie 为空"
	}
	formatResult := ValidateCookie(cookie)
	if !formatResult.Valid {
		return false, "Cookie 缺少字段: " + strings.Join(formatResult.Missing, ", ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := NewClient(DefaultUA)
	_, err := c.FsFiles(ctx, "0", 1, 0, cookie)
	if err != nil {
		errMsg := err.Error()
		// 识别常见过期关键词
		if strings.Contains(errMsg, "未登录") || strings.Contains(errMsg, "cookie") ||
			strings.Contains(errMsg, "登录过期") || strings.Contains(errMsg, "401") ||
			strings.Contains(errMsg, "403") {
			return false, "Cookie 可能已失效: " + errMsg
		}
		return false, "115 API 请求失败: " + errMsg
	}
	return true, "Cookie 有效"
}
