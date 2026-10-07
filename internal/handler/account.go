package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/rest/httpx"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== Account CRUD ====================

// ListAccounts GET /api/account 获取所有账号（解密后）
func ListAccounts(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts := accountStore.List()
		result := make([]model.AccountInfo, 0, len(accounts))
		for _, a := range accounts {
			result = append(result, *a)
		}
		httpx.WriteJson(w, http.StatusOK, result)
	}
}

// CreateAccountRequest 创建账号请求
type CreateAccountRequest struct {
	AccountType string `json:"accountType"`
	Name        string `json:"name"`
	Cookie      string `json:"cookie"`
	Account     string `json:"account"`
	Password    string `json:"password"`
	URL         string `json:"url"`
}

// CreateAccount POST /api/account 新建账号
func CreateAccount(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateAccountRequest

		contentType := r.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
		} else {
			if err := r.ParseForm(); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
			req.AccountType = r.FormValue("accountType")
			req.Name = r.FormValue("name")
			req.Cookie = r.FormValue("cookie")
			req.Account = r.FormValue("account")
			req.Password = r.FormValue("password")
			req.URL = r.FormValue("url")
		}

		if req.AccountType == "" || req.Name == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "accountType and name are required"})
			return
		}

		if req.AccountType == "115" && req.Cookie == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "cookie is required for 115 accounts"})
			return
		}
		if req.AccountType == "openlist" {
			if req.Account == "" || req.Password == "" || req.URL == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "account, password, and url are required for openlist accounts"})
				return
			}
		}

		if accountStore.Has(req.Name) {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Account name already exists"})
			return
		}

		// 格式完整不代表 Cookie 存活：格式校验仅用于判定「缺字段=invalid」，
		// 其余一律先记 unknown，等权威探测/监控给出有效结论，避免假阳性。
		status := model.CookieStatusUnknown
		if req.AccountType == "115" && req.Cookie != "" {
			if result := client115.ValidateCookie(req.Cookie); !result.Valid {
				status = model.CookieStatusInvalid
				logger.S().Warnf("[CreateAccount] Cookie 格式无效 account=%s 缺少: %s", req.Name, strings.Join(result.Missing, ","))
			}
		}

		newAcc := &model.AccountInfo{
			Name:        req.Name,
			AccountType: req.AccountType,
			Cookie:      req.Cookie,
			Account:     req.Account,
			Password:    req.Password,
			URL:         req.URL,
		}

		if err := accountStore.Upsert(newAcc); err != nil {
			logger.S().Errorf("upsert account: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
			return
		}

		// 统一经 SetCookieStatus 写入三态（同时刷新 LastCookieCheck），仅 115 账号有意义。
		if req.AccountType == "115" {
			if err := accountStore.SetCookieStatus(req.Name, status, 0, model.CookieSourceFormat); err != nil {
				logger.S().Warnf("[CreateAccount] 写入 Cookie 状态失败 account=%s: %v", req.Name, err)
			}
		}

		if err := accountStore.Flush(); err != nil {
			logger.S().Warnf("flush account after create: %v", err)
		}

		w.Header().Set("HX-Trigger", "accounts-changed")
		httpx.WriteJson(w, http.StatusCreated, newAcc)
	}
}

// UpdateAccountRequest 更新账号请求
type UpdateAccountRequest struct {
	Name         string `json:"name"`
	OriginalName string `json:"originalName"`
	AccountType  string `json:"accountType"`
	Cookie       string `json:"cookie"`
	Account      string `json:"account"`
	Password     string `json:"password"`
	URL          string `json:"url"`
}

// UpdateAccount PUT /api/account 更新账号
func UpdateAccount(accountStore *store.AccountStore) http.HandlerFunc { //nolint:cyclop // complexity: 32
	return func(w http.ResponseWriter, r *http.Request) {
		var req UpdateAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		if req.Name == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "账户名称不能为空"})
			return
		}

		if req.AccountType == "115" && req.Cookie == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "115 账户必须提供 Cookie"})
			return
		}
		if req.AccountType == "openlist" {
			if req.Account == "" || req.Password == "" || req.URL == "" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "openlist 账户必须提供账号、密码和服务器地址"})
				return
			}
		}

		lookupName := req.OriginalName
		if lookupName == "" {
			lookupName = req.Name
		}

		acc := accountStore.Get(lookupName)
		if acc == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户不存在"})
			return
		}

		// 验证新 cookie 格式（仅记录警告，不阻止更新）
		cookieChanged := req.Cookie != "" && req.Cookie != acc.Cookie
		if cookieChanged {
			result := client115.ValidateCookie(req.Cookie)
			if !result.Valid {
				logger.S().Warnf("[UpdateAccount] Cookie 格式无效 account=%s 缺少: %s", req.Name, strings.Join(result.Missing, ","))
			}
		}

		// 与 CreateAccount 一致：格式校验仅用于判定「缺字段=invalid」，其余记 unknown；
		// 未改 Cookie 时记 unknown（unknown 不覆盖既有的 valid/invalid 明确状态）。
		status := model.CookieStatusUnknown
		if req.AccountType == "115" && cookieChanged {
			if result := client115.ValidateCookie(req.Cookie); !result.Valid {
				status = model.CookieStatusInvalid
			}
		}

		if lookupName != req.Name {
			// 改名：先把除 Name 外的其他字段一并同步（Cookie/AccountType/Account/Password/URL），
			// 避免「同时改名字 + 改 Cookie」场景下非 Name 字段更新被悄悄丢弃
			acc.Name = req.Name
			if req.Cookie != "" {
				acc.Cookie = req.Cookie
			}
			if req.AccountType != "" {
				acc.AccountType = req.AccountType
			}
			if req.Account != "" {
				acc.Account = req.Account
			}
			if req.Password != "" {
				acc.Password = req.Password
			}
			if req.URL != "" {
				acc.URL = req.URL
			}
			if cookieChanged {
				// Cookie 已变更：旧存活结论作废，交由后续 SetCookieStatus 重新判定
				acc.ResetCookieStatus()
			}
			if err := accountStore.Delete(lookupName); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "更新账号失败"})
				return
			}
			if err := accountStore.Upsert(acc); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "更新账号失败"})
				return
			}
		} else {
			err := accountStore.Update(lookupName, func(a *model.AccountInfo) {
				if req.Cookie != "" {
					a.Cookie = req.Cookie
				}
				if req.AccountType != "" {
					a.AccountType = req.AccountType
				}
				if req.Account != "" {
					a.Account = req.Account
				}
				if req.Password != "" {
					a.Password = req.Password
				}
				if req.URL != "" {
					a.URL = req.URL
				}
				if cookieChanged {
					// Cookie 已变更：旧存活结论作废，交由后续 SetCookieStatus 重新判定
					a.ResetCookieStatus()
				}
			})
			if err != nil {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户不存在"})
				return
			}
		}

		// 统一经 SetCookieStatus 写入三态（同时刷新 LastCookieCheck），仅 115 账号有意义。
		if req.AccountType == "115" {
			if err := accountStore.SetCookieStatus(req.Name, status, 0, model.CookieSourceFormat); err != nil {
				logger.S().Warnf("[UpdateAccount] 写入 Cookie 状态失败 account=%s: %v", req.Name, err)
			}
		}

		if err := accountStore.Flush(); err != nil {
			logger.S().Warnf("flush account after update: %v", err)
		}

		httpx.OkJson(w, acc)
	}
}

// DeleteAccount DELETE /api/account?name=xxx 删除账号
func DeleteAccount(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Missing name"})
			return
		}

		if !accountStore.Has(name) {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户不存在"})
			return
		}

		if err := accountStore.Delete(name); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "删除账号失败"})
			return
		}

		if err := accountStore.Flush(); err != nil {
			logger.S().Warnf("flush account after delete: %v", err)
		}

		httpx.OkJson(w, map[string]string{"message": "Account deleted"})
	}
}

// ==================== QR Code Login ====================

// GetQrcodeTokenHandler GET /api/account/qrcode/token?clientType=xxx
func GetQrcodeTokenHandler(c *client115.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientType := strings.TrimSpace(r.URL.Query().Get("clientType"))
		if clientType == "" {
			clientType = "alipaymini"
		}

		if _, ok := client115.APP_TO_SSOENT[clientType]; !ok {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]any{
				"error":         "无效的客户端类型",
				"clientDisplay": client115.CLIENT_DISPLAY,
			})
			return
		}

		result, err := c.GetQrcodeToken(clientType)
		if err != nil {
			logger.S().Errorf("[API/qrcode/token] %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{
				"error":   "获取二维码失败",
				"details": err.Error(),
			})
			return
		}

		httpx.OkJson(w, map[string]any{
			"success":       true,
			"uid":           result.UID,
			"time":          result.Time,
			"sign":          result.Sign,
			"qrcode":        result.Qrcode,
			"qrcodeBase64":  result.QrcodeBase64,
			"tips":          result.Tips,
			"clientType":    result.ClientType,
			"clientDisplay": client115.CLIENT_DISPLAY[clientType],
		})
	}
}

// GetQrcodeStatusHandler GET /api/account/qrcode/status?uid=&time=&sign=&clientType=
func GetQrcodeStatusHandler(c *client115.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		uid := q.Get("uid")
		timeStr := q.Get("time")
		sign := q.Get("sign")
		clientType := strings.TrimSpace(q.Get("clientType"))
		if clientType == "" {
			clientType = "alipaymini"
		}

		if uid == "" || timeStr == "" || sign == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "uid, time, sign 参数不能为空"})
			return
		}

		if _, ok := client115.APP_TO_SSOENT[clientType]; !ok {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "无效的客户端类型"})
			return
		}

		result, err := c.GetQrcodeStatus(uid, timeStr, sign, clientType)
		if err != nil {
			logger.S().Errorf("[API/qrcode/status] %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{
				"error":   "查询扫码状态失败",
				"details": err.Error(),
			})
			return
		}

		resp := map[string]any{
			"success": true,
			"status":  result.Status,
			"msg":     result.Msg,
		}
		if result.Cookie != "" {
			resp["cookie"] = result.Cookie
		}
		httpx.OkJson(w, resp)
	}
}

// GetQrcodeCookieRequest 换取 cookie 请求
type GetQrcodeCookieRequest struct {
	UID         string `json:"uid"`
	ClientType  string `json:"clientType"`
	AccountName string `json:"accountName"`
}

// GetQrcodeCookieHandler POST /api/account/qrcode/cookie
func GetQrcodeCookieHandler(c *client115.Client, accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req GetQrcodeCookieRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		if req.UID == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "uid 不能为空"})
			return
		}

		clientType := req.ClientType
		if _, ok := client115.APP_TO_SSOENT[clientType]; !ok {
			clientType = "alipaymini"
		}

		cookie, err := c.GetQrcodeResult(req.UID, clientType)
		if err != nil {
			logger.S().Errorf("[API/qrcode/cookie] %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{
				"error":   "换取 Cookie 失败",
				"details": err.Error(),
			})
			return
		}

		// 验证 cookie 格式
		validateResult := client115.ValidateCookie(cookie)
		if !validateResult.Valid {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]any{
				"error":         "Cookie 格式无效",
				"missingFields": validateResult.Missing,
				"message":       "缺少必需字段: " + strings.Join(validateResult.Missing, ", "),
			})
			return
		}

		if req.AccountName != "" {
			acc := accountStore.Get(req.AccountName)
			if acc == nil {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户 \"" + req.AccountName + "\" 不存在"})
				return
			}
			if acc.AccountType != "115" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "账户 \"" + req.AccountName + "\" 不是 115 类型，无法更新 Cookie"})
				return
			}

			if err := accountStore.Update(req.AccountName, func(a *model.AccountInfo) {
				a.Cookie = cookie
			}); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
				return
			}
			// 扫码登录成功即视为有效，来源标记 login（统一经三态通道写入）。
			if err := accountStore.SetCookieStatus(req.AccountName, model.CookieStatusValid, 0, model.CookieSourceLogin); err != nil {
				logger.S().Warnf("[QRCODE-LOGIN] 写入 Cookie 状态失败 account=%s: %v", req.AccountName, err)
			}
			if err := accountStore.Flush(); err != nil {
				logger.S().Warnf("flush account after cookie update: %v", err)
			}

			logger.S().Infof("[QRCODE-LOGIN] Cookie updated for account: %s", req.AccountName)
			httpx.OkJson(w, map[string]any{
				"success":      true,
				"message":      "Cookie 更新成功",
				"accountName":  req.AccountName,
				"cookieLength": len(cookie),
				"cookieValid":  true,
			})
			return
		}

		httpx.OkJson(w, map[string]any{
			"success":      true,
			"message":      "获取 Cookie 成功",
			"cookie":       cookie,
			"cookieLength": len(cookie),
			"cookieValid":  true,
		})
	}
}

// ==================== Account Status ====================

// AccountStatusInfo 账号状态（带 cookie 元数据）
type AccountStatusInfo struct {
	Name            string `json:"name"`
	Status          string `json:"status"`
	Message         string `json:"message,omitempty"`
	CookieValid     *bool  `json:"cookieValid,omitempty"`
	LastCookieCheck int64  `json:"lastCookieCheck,omitempty"`
	// CookieStatus 三态状态：valid / invalid / unknown。
	CookieStatus string `json:"cookieStatus,omitempty"`
	// CookieErrno 最近一次判定拿到的 115 错误码。
	CookieErrno int `json:"cookieErrno,omitempty"`
	// CookieSource 最近一次判定来源。
	CookieSource string `json:"cookieSource,omitempty"`
}

// GetAccountStatus GET /api/account/status?names=xxx,yyy&deep=true
func GetAccountStatus(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts := accountStore.List()

		namesParam := r.URL.Query().Get("names")
		deepCheck := r.URL.Query().Get("deep") == "true"

		var targets []*model.AccountInfo
		if namesParam != "" {
			names := strings.Split(namesParam, ",")
			nameSet := make(map[string]bool)
			for _, n := range names {
				n = strings.TrimSpace(n)
				if n != "" {
					nameSet[n] = true
				}
			}
			for _, a := range accounts {
				if nameSet[a.Name] {
					targets = append(targets, a)
				}
			}
		} else {
			targets = accounts
		}

		results := make([]AccountStatusInfo, 0, len(targets))
		now := time.Now().UnixMilli()
		for _, acc := range targets {
			results = append(results, checkAccountStatusInfo(*acc))
		}

		// Deep check: 同步执行账号验证
		var deepResults []map[string]any
		if deepCheck {
			deepResults = make([]map[string]any, 0, len(targets))
			for _, acc := range targets {
				if acc.AccountType == "115" && acc.Cookie != "" {
					ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
					probeErr := client115.ProbeAccount(ctx, acc.Cookie)
					cancel()
					status := classifyProbeStatus(probeErr)
					_ = accountStore.SetCookieStatus(acc.Name, status, client115.ErrnoOf(probeErr), model.CookieSourceProbe)
					deepResults = append(deepResults, map[string]any{
						"account":   acc.Name,
						"type":      "115",
						"valid":     status == model.CookieStatusValid,
						"status":    status,
						"missing":   []string{},
						"error":     probeMessage(probeErr),
						"checkedAt": now,
					})
				} else if acc.AccountType == "115" && acc.Cookie == "" {
					deepResults = append(deepResults, map[string]any{
						"account":   acc.Name,
						"type":      "115",
						"valid":     false,
						"missing":   []string{"UID", "CID", "SEID", "KID"},
						"error":     "Cookie 为空",
						"checkedAt": now,
					})
				} else {
					// openlist 等非 115 账号，配置有效即视为 ok
					deepResults = append(deepResults, map[string]any{
						"account":   acc.Name,
						"type":      acc.AccountType,
						"valid":     acc.URL != "",
						"checkedAt": now,
					})
				}
			}
			accountStore.Flush()
		}

		resp := map[string]any{
			"results": results,
		}
		if deepCheck && deepResults != nil {
			resp["deepResults"] = deepResults
		}
		httpx.OkJson(w, resp)
	}
}

// checkAccountStatusInfo 检查单个账号状态（带元数据）
func checkAccountStatusInfo(acc model.AccountInfo) AccountStatusInfo {
	info := AccountStatusInfo{
		Name:            acc.Name,
		CookieValid:     acc.CookieValid,
		LastCookieCheck: acc.LastCookieCheck,
		CookieStatus:    acc.EffectiveCookieStatus(),
		CookieErrno:     acc.CookieErrno,
		CookieSource:    acc.CookieSource,
	}

	switch acc.AccountType {
	case "115":
		if acc.Cookie == "" {
			info.Status = "error"
			info.Message = "Cookie 为空"
			return info
		}
		result := client115.ValidateCookie(acc.Cookie)
		if !result.Valid {
			info.Status = "error"
			info.Message = "Cookie 缺少字段: " + strings.Join(result.Missing, ", ")
			return info
		}
		// 格式完整不代表存活：状态由三态判定（权威探测/监控）驱动。
		timeStr := ""
		if acc.LastCookieCheck > 0 {
			timeStr = fmt.Sprintf(" (校验于 %s)", time.UnixMilli(acc.LastCookieCheck).Format("2006-01-02 15:04:05"))
		}
		switch acc.EffectiveCookieStatus() {
		case model.CookieStatusInvalid:
			info.Status = "error"
			info.Message = "Cookie 已失效，请重新登录" + errnoSuffix(acc.CookieErrno) + timeStr
		case model.CookieStatusValid:
			info.Status = "ok"
			info.Message = "Cookie 有效" + timeStr
		default:
			info.Status = "ok"
			info.Message = "Cookie 格式有效，待存活校验" + timeStr
		}
		return info
	case "openlist":
		if acc.URL == "" {
			info.Status = "error"
			info.Message = "URL 为空"
			return info
		}
		info.Status = "ok"
		info.Message = "配置完整"
		return info
	default:
		info.Status = "unknown"
		info.Message = "未知账户类型"
		return info
	}
}

// classifyProbeStatus 将探测错误映射为三态状态。
func classifyProbeStatus(err error) string {
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

// probeMessage 生成探测结果的可读文案。
func probeMessage(err error) string {
	if err == nil {
		return "Cookie 有效"
	}
	if client115.IsAuthError(err) {
		return "Cookie 已失效，请重新登录: " + err.Error()
	}
	if client115.IsRateLimitError(err) {
		return "疑似风控/限流，暂不判定: " + err.Error()
	}
	return "暂时无法验证: " + err.Error()
}

// errnoSuffix 拼接错误码后缀（0 时不显示）。
func errnoSuffix(errno int) string {
	if errno == 0 {
		return ""
	}
	return fmt.Sprintf("（errno=%d）", errno)
}

// ==================== Cookie 验证 API ====================

// VerifyAccountHandler POST /api/account/verify?name=xxx
// 对单个 115 账号执行权威存活探测（ProbeAccount），返回三态状态并回写账号。
func VerifyAccountHandler(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			// Try from body
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Name != "" {
				name = body.Name
			}
		}
		if name == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "name 参数不能为空"})
			return
		}

		acc := accountStore.Get(name)
		if acc == nil {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户不存在"})
			return
		}
		if acc.AccountType != "115" || acc.Cookie == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "仅 115 账号支持存活验证"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		probeErr := client115.ProbeAccount(ctx, acc.Cookie)
		status := classifyProbeStatus(probeErr)
		if err := accountStore.SetCookieStatus(name, status, client115.ErrnoOf(probeErr), model.CookieSourceProbe); err != nil {
			logger.S().Warnf("[VerifyAccount] 写入 Cookie 状态失败 account=%s: %v", name, err)
		}
		accountStore.Flush()

		missing := client115.ValidateCookie(acc.Cookie).Missing
		if missing == nil {
			missing = []string{}
		}
		httpx.OkJson(w, map[string]any{
			"account":   name,
			"status":    status,
			"valid":     status == model.CookieStatusValid, // 兼容旧前端
			"errno":     client115.ErrnoOf(probeErr),
			"source":    model.CookieSourceProbe,
			"message":   probeMessage(probeErr),
			"missing":   missing,
			"checkedAt": time.Now().UnixMilli(),
		})
	}
}

// VerifyAllAccountsHandler POST /api/account/verify-all
// 并发对所有 115 账号执行权威存活探测，逐个回写三态状态。
func VerifyAllAccountsHandler(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxConcurrency = 4
		sem := make(chan struct{}, maxConcurrency)
		var wg sync.WaitGroup
		var mu sync.Mutex

		targets := make([]*model.AccountInfo, 0)
		for _, acc := range accountStore.List() {
			if acc.AccountType == "115" && acc.Cookie != "" {
				targets = append(targets, acc)
			}
		}

		results := make([]map[string]any, 0, len(targets))
		validCount, invalidCount, unknownCount := 0, 0, 0
		for _, acc := range targets {
			wg.Add(1)
			sem <- struct{}{}
			go func(a *model.AccountInfo) {
				defer wg.Done()
				defer func() { <-sem }()

				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()
				probeErr := client115.ProbeAccount(ctx, a.Cookie)
				status := classifyProbeStatus(probeErr)
				errno := client115.ErrnoOf(probeErr)
				if err := accountStore.SetCookieStatus(a.Name, status, errno, model.CookieSourceProbe); err != nil {
					logger.S().Warnf("[VerifyAll] 写入 Cookie 状态失败 account=%s: %v", a.Name, err)
				}

				mu.Lock()
				switch status {
				case model.CookieStatusValid:
					validCount++
				case model.CookieStatusInvalid:
					invalidCount++
				default:
					unknownCount++
				}
				results = append(results, map[string]any{
					"account": a.Name,
					"status":  status,
					"valid":   status == model.CookieStatusValid,
					"errno":   errno,
					"message": probeMessage(probeErr),
				})
				mu.Unlock()
			}(acc)
		}
		wg.Wait()
		accountStore.Flush()

		httpx.OkJson(w, map[string]any{
			"validCount":   validCount,
			"invalidCount": invalidCount,
			"unknownCount": unknownCount,
			"total":        len(targets),
			"results":      results,
			"checkedAt":    time.Now().UnixMilli(),
		})
	}
}
