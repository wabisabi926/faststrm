package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// ==================== Account CRUD ====================

// ListAccounts GET /api/account 获取所有账号（解密后）
func ListAccounts(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := accountStore.ReadAccounts()
		if err != nil {
			logger.S().Errorf("read accounts: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取账号失败"})
			return
		}
	// 列表接口统一直接返回 JSON 数组，与 E2E/回归测试契约一致：空为 [] 而非 null
	if accounts == nil {
		accounts = []model.AccountInfo{}
	}
	httpx.WriteJson(w, http.StatusOK, accounts)
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
// 支持 form-urlencoded（HTMX 默认）和 JSON 两种格式
func CreateAccount(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateAccountRequest

		contentType := r.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/json") {
			// JSON 格式
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
		} else {
			// form-urlencoded 格式（HTMX 默认）
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

		// 根据账户类型验证必需字段
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

		accounts, err := accountStore.ReadAccounts()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取账号失败"})
			return
		}

		// 检查名称唯一性
		for _, a := range accounts {
			if a.Name == req.Name {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "Account name already exists"})
				return
			}
		}

		newAcc := model.AccountInfo{
			Name:        req.Name,
			AccountType: req.AccountType,
			Cookie:      req.Cookie,
			Account:     req.Account,
			Password:    req.Password,
			URL:         req.URL,
		}

		accounts = append(accounts, newAcc)
		if err := accountStore.WriteAccounts(accounts); err != nil {
			logger.S().Errorf("write accounts: %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
			return
		}

		// 触发前端刷新账号列表
		w.Header().Set("HX-Trigger", "accounts-changed")
		httpx.WriteJson(w, http.StatusCreated, newAcc)
	}
}

// UpdateAccountRequest 更新账号请求
type UpdateAccountRequest struct {
	Name        string `json:"name"`
	AccountType string `json:"accountType"`
	Cookie      string `json:"cookie"`
	Account     string `json:"account"`
	Password    string `json:"password"`
	URL         string `json:"url"`
}

// UpdateAccount PUT /api/account 更新账号
func UpdateAccount(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req UpdateAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}

		if req.Name == "" {
			httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}

		// 根据账户类型验证（如果提供了 accountType）
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

		accounts, err := accountStore.ReadAccounts()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取账号失败"})
			return
		}

		idx := -1
		for i, a := range accounts {
			if a.Name == req.Name {
				idx = i
				break
			}
		}
		if idx == -1 {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "Account not found"})
			return
		}

		// 合并更新（非空字段覆盖）
		if req.Cookie != "" {
			accounts[idx].Cookie = req.Cookie
		}
		if req.AccountType != "" {
			accounts[idx].AccountType = req.AccountType
		}
		if req.Account != "" {
			accounts[idx].Account = req.Account
		}
		if req.Password != "" {
			accounts[idx].Password = req.Password
		}
		if req.URL != "" {
			accounts[idx].URL = req.URL
		}

		if err := accountStore.WriteAccounts(accounts); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
			return
		}

		httpx.OkJson(w, accounts[idx])
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

		accounts, err := accountStore.ReadAccounts()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取账号失败"})
			return
		}

		newAccounts := make([]model.AccountInfo, 0, len(accounts))
		found := false
		for _, a := range accounts {
			if a.Name == name {
				found = true
				continue
			}
			newAccounts = append(newAccounts, a)
		}

		if !found {
			httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "Account not found"})
			return
		}

		if err := accountStore.WriteAccounts(newAccounts); err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
			return
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
				"error":    "获取二维码失败",
				"details":  err.Error(),
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
	AccountName string `json:"accountName"` // 可选：更新已有账户的 cookie
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

		// 换取 cookie
		cookie, err := c.GetQrcodeResult(req.UID, clientType)
		if err != nil {
			logger.S().Errorf("[API/qrcode/cookie] %v", err)
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{
				"error":   "换取 Cookie 失败",
				"details": err.Error(),
			})
			return
		}

		// 如果提供了 accountName，则更新已有账户
		if req.AccountName != "" {
			accounts, err := accountStore.ReadAccounts()
			if err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取账号失败"})
				return
			}

			idx := -1
			for i, a := range accounts {
				if a.Name == req.AccountName {
					idx = i
					break
				}
			}
			if idx == -1 {
				httpx.WriteJson(w, http.StatusNotFound, map[string]string{"error": "账户 \"" + req.AccountName + "\" 不存在"})
				return
			}
			if accounts[idx].AccountType != "115" {
				httpx.WriteJson(w, http.StatusBadRequest, map[string]string{"error": "账户 \"" + req.AccountName + "\" 不是 115 类型，无法更新 Cookie"})
				return
			}

			accounts[idx].Cookie = cookie
			if err := accountStore.WriteAccounts(accounts); err != nil {
				httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "保存账号失败"})
				return
			}

			logger.S().Infof("[QRCODE-LOGIN] Cookie updated for account: %s", req.AccountName)
			httpx.OkJson(w, map[string]any{
				"success":      true,
				"message":      "Cookie 更新成功",
				"accountName":  req.AccountName,
				"cookieLength": len(cookie),
			})
			return
		}

		// 未提供 accountName：仅返回 cookie
		httpx.OkJson(w, map[string]any{
			"success":      true,
			"message":      "获取 Cookie 成功",
			"cookie":       cookie,
			"cookieLength": len(cookie),
		})
	}
}

// ==================== Account Status ====================

// AccountStatus 账号状态
type AccountStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok / error / unknown
	Message string `json:"message,omitempty"`
}

// GetAccountStatus GET /api/account/status?names=xxx,yyy
func GetAccountStatus(accountStore *store.AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := accountStore.ReadAccounts()
		if err != nil {
			httpx.WriteJson(w, http.StatusInternalServerError, map[string]string{"error": "读取账号失败"})
			return
		}

		namesParam := r.URL.Query().Get("names")
		var targets []model.AccountInfo
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

		results := make([]AccountStatus, 0, len(targets))
		for _, acc := range targets {
			results = append(results, checkAccountStatus(acc))
		}

		httpx.OkJson(w, map[string]any{
			"results":   results,
			"checkedAt": 0, // 前端会自己处理时间戳
		})
	}
}

// checkAccountStatus 检查单个账号状态
// 当前阶段仅做 cookie 格式校验，后续阶段接入 115 API 做在线检测
func checkAccountStatus(acc model.AccountInfo) AccountStatus {
	switch acc.AccountType {
	case "115":
		if acc.Cookie == "" {
			return AccountStatus{Name: acc.Name, Status: "error", Message: "Cookie 为空"}
		}
		result := client115.ValidateCookie(acc.Cookie)
		if !result.Valid {
			return AccountStatus{Name: acc.Name, Status: "error", Message: "Cookie 缺少字段: " + strings.Join(result.Missing, ", ")}
		}
		return AccountStatus{Name: acc.Name, Status: "ok", Message: "Cookie 格式有效"}
	case "openlist":
		if acc.URL == "" {
			return AccountStatus{Name: acc.Name, Status: "error", Message: "URL 为空"}
		}
		return AccountStatus{Name: acc.Name, Status: "ok", Message: "配置完整"}
	default:
		return AccountStatus{Name: acc.Name, Status: "unknown", Message: "未知账户类型"}
	}
}
