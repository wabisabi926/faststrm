package client115

import (
	"context"
	"errors"
	"strings"
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

// ProbeAccount 通过权威探测接口验证 cookie 是否存活。
//
// 判定只看 HTTP 状态与结构化 state/errno，不再依赖错误文案关键词：
//   - 返回 nil            → 有效（valid）
//   - ErrCookieExpired    → 确定失效（invalid）
//   - ErrRateLimited/Transient → 临时不可用（unknown，调用方不得改写账号状态）
//
// 探测端点选用 /files?cid=0（必须登录才可用、返回结构化 errno），
// 后续如需更换端点，仅改本函数内部实现即可。
func ProbeAccount(ctx context.Context, cookie string) error {
	if cookie == "" {
		return &APIError{Message: "cookie is empty", Kind: ErrCookieExpired}
	}
	if r := ValidateCookie(cookie); !r.Valid {
		return &APIError{
			Message: "cookie 缺少字段: " + strings.Join(r.Missing, ", "),
			Kind:    ErrCookieExpired,
		}
	}

	c := NewClient(DefaultUA)
	if _, err := c.FsFiles(ctx, "0", 1, 0, cookie); err != nil {
		if errors.Is(err, ErrCookieExpired) || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrTransient) {
			return err
		}
		// 网络层/解析层错误：按文案兜底分类，无法归类时视为临时错误（不改写状态）。
		return &APIError{Message: err.Error(), Kind: classifyAPIError(0, err.Error())}
	}
	return nil
}
