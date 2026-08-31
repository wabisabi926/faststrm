package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

		now := time.Now().UnixMilli()
		cookieValid := true
		if req.AccountType == "115" && req.Cookie != "" {
			result := client115.ValidateCookie(req.Cookie)
			cookieValid = result.Valid
			if !result.Valid {
				logger.S().Warnf("[CreateAccount] Cookie 格式无效 account=%s 缺少: %s", req.Name, strings.Join(result.Missing, ","))
			}
		}

		newAcc := &model.AccountInfo{
			Name:            req.Name,
			AccountType:     req.AccountType,
			Cookie:          req.Cookie,
			Account:         req.Account,
			Password:        req.Password,
			URL:             req.URL,
			LastCookieCheck: now,
			CookieValid:     &cookieValid,
		}

		if err := accountStore.Upsert(newAcc); err != nil {
			logger.S().Errorf("upsert account: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
			return
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

		now := time.Now().UnixMilli()
		cookieValid := true
		if req.AccountType == "115" {
			if cookieChanged {
				result := client115.ValidateCookie(req.Cookie)
				cookieValid = result.Valid
			} else if acc.CookieValid != nil {
				cookieValid = *acc.CookieValid
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
			acc.LastCookieCheck = now
			acc.CookieValid = &cookieValid
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
				a.LastCookieCheck = now
				a.CookieValid = &cookieValid
			})
			if err != nil {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户不存在"})
				return
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

			now := time.Now().UnixMilli()
			valid := true
			if err := accountStore.Update(req.AccountName, func(a *model.AccountInfo) {
				a.Cookie = cookie
				a.LastCookieCheck = now
				a.CookieValid = &valid
			}); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
				return
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
					pingOk, pingMsg := client115.PingCookie(acc.Cookie)
					// 根据真实结果更新 store 中的 CookieValid
					validBool := pingOk
					_ = accountStore.MarkCookieStatus(acc.Name, validBool)
					deepResults = append(deepResults, map[string]any{
						"account":   acc.Name,
						"type":      "115",
						"valid":     pingOk,
						"missing":   []string{},
						"error":     pingMsg,
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
		info.Status = "ok"
		info.Message = "Cookie 格式有效"
		if acc.LastCookieCheck > 0 {
			checkedAt := time.UnixMilli(acc.LastCookieCheck)
			info.Message += fmt.Sprintf(" (校验于 %s)", checkedAt.Format("2006-01-02 15:04:05"))
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

// ==================== Cookie 验证 API ====================

// VerifyAccountHandler POST /api/account/verify?name=xxx
// 对单个账号执行 cookie 格式校验并更新元数据
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

		valid, missing, err := accountStore.ValidateCookie(name)
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		accountStore.Flush()

		httpx.OkJson(w, map[string]any{
			"account":   name,
			"valid":     valid,
			"missing":   missing,
			"checkedAt": time.Now().UnixMilli(),
		})
	}
}

// VerifyAllAccountsHandler POST /api/account/verify-all
// 批量校验所有 115 账号的 cookie
func VerifyAllAccountsHandler(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		validCount, invalidCount, err := accountStore.ValidateAllCookies()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		accountStore.Flush()

		accounts := accountStore.List()
		results := make([]map[string]any, 0, len(accounts))
		for _, acc := range accounts {
			if acc.AccountType == "115" {
				results = append(results, map[string]any{
					"account":     acc.Name,
					"cookieValid": acc.CookieValid,
					"lastCheck":   acc.LastCookieCheck,
				})
			}
		}

		httpx.OkJson(w, map[string]any{
			"validCount":   validCount,
			"invalidCount": invalidCount,
			"total":        validCount + invalidCount,
			"results":      results,
			"checkedAt":    time.Now().UnixMilli(),
		})
	}
}
