// Package client115 实现 115 API 客户端
// 对齐 frontend/src/lib/115.ts 扫码登录三阶段
package client115

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// ==================== 常量 ====================

// APP_TO_SSOENT 设备类型 → ssoent 映射（对齐 p115client APP_TO_SSOENT）
var APP_TO_SSOENT = map[string]string{
	"web":        "A1",
	"desktop":    "A1",
	"ios":        "D1",
	"bios":       "D2",
	"115ios":     "D3",
	"android":    "F1",
	"bandroid":   "F2",
	"115android": "F3",
	"ipad":       "H1",
	"bipad":      "H2",
	"115ipad":    "H3",
	"tv":         "I1",
	"apple_tv":   "I2",
	"qandroid":   "M1",
	"qios":       "N1",
	"qipad":      "O1",
	"os_windows": "P1",
	"os_mac":     "P2",
	"os_linux":   "P3",
	"wechatmini": "R1",
	"alipaymini": "R2",
	"harmony":    "S1",
}

// CLIENT_DISPLAY 设备类型 → 中文显示名
var CLIENT_DISPLAY = map[string]string{
	"alipaymini": "支付宝小程序",
	"wechatmini": "微信小程序",
	"115android": "115 安卓",
	"android":    "安卓原生",
	"115ios":     "115 iOS",
	"ios":        "iOS 原生",
	"115ipad":    "115 iPad",
	"ipad":       "iPad 原生",
	"tv":         "115 TV",
	"web":        "115 网页",
	"qandroid":   "115 管理端",
	"qios":       "企业 iOS",
	"qipad":      "企业 iPad",
	"os_windows": "Windows 客户端",
	"os_mac":     "Mac 客户端",
	"os_linux":   "Linux 客户端",
	"harmony":    "鸿蒙",
}

// DefaultUA 默认 iOS 115 客户端 UA
const DefaultUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 115App/27.0.0"

// ==================== 类型 ====================

// QrCodeStatus 扫码状态
type QrCodeStatus string

const (
	QrCodeWaiting   QrCodeStatus = "waiting"
	QrCodeScanned   QrCodeStatus = "scanned"
	QrCodeSuccess   QrCodeStatus = "success"
	QrCodeExpired   QrCodeStatus = "expired"
	QrCodeCancelled QrCodeStatus = "cancelled"
)

// QrCodeTokenResp 获取二维码 token 响应
type QrCodeTokenResp struct {
	UID          string `json:"uid"`
	Time         string `json:"time"`
	Sign         string `json:"sign"`
	Qrcode       string `json:"qrcode"`       // 二维码内容（URL）
	QrcodeBase64 string `json:"qrcodeBase64"` // base64 PNG 图片（带 data: 前缀）
	Tips         string `json:"tips"`
	ClientType   string `json:"clientType"`
}

// QrCodeStatusResp 扫码状态响应
type QrCodeStatusResp struct {
	Status QrCodeStatus `json:"status"`
	Msg    string       `json:"msg"`
	Cookie string       `json:"cookie,omitempty"` // status=success 时返回
}

// ==================== Client ====================

// Client 115 API HTTP 客户端
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// NewClient 创建 115 API 客户端
func NewClient(userAgent string) *Client {
	if userAgent == "" {
		userAgent = DefaultUA
	}
	return &Client{
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
		},
		UserAgent: userAgent,
	}
}

// ==================== 扫码登录三阶段 ====================

// GetQrcodeToken 阶段1：获取二维码 token
// GET https://qrcodeapi.115.com/api/1.0/{clientType}/1.0/token/
// clientType 决定了二维码的客户端类型（alipaymini, wechatmini, 115android 等）
func (c *Client) GetQrcodeToken(clientType string) (*QrCodeTokenResp, error) {
	normalizedApp, _ := normalizeAppType(clientType)
	urlStr := fmt.Sprintf("https://qrcodeapi.115.com/api/1.0/%s/1.0/token/", normalizedApp)
	body, err := c.httpGet(urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("get qrcode token: %w", err)
	}

	// 解析响应: { data: { uid, time, sign, qrcode } }
	// uid 可能是字符串（如 "830c3cb15a0..."），time/sign 可能是数字或字符串。
	// 使用 json.RawMessage 保留原始 JSON token，避免 float64 科学计数法问题。
	var resp struct {
		Data struct {
			UID    json.RawMessage `json:"uid"`
			Time   json.RawMessage `json:"time"`
			Sign   json.RawMessage `json:"sign"`
			Qrcode string          `json:"qrcode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse qrcode token: %w", err)
	}

	uid := rawToString(resp.Data.UID)
	timeStr := rawToString(resp.Data.Time)
	sign := rawToString(resp.Data.Sign)

	if uid == "" || timeStr == "" || sign == "" {
		return nil, fmt.Errorf("获取二维码失败：返回登录参数不完整")
	}

	qrcodeContent := resp.Data.Qrcode
	if qrcodeContent == "" {
		qrcodeContent = fmt.Sprintf("https://115.com/scan/dg-%s", uid)
	}

	// 生成 base64 PNG（避免前端跨域加载图片）
	pngBytes, err := qrcode.Encode(qrcodeContent, qrcode.Medium, 240)
	if err != nil {
		return nil, fmt.Errorf("generate qrcode image: %w", err)
	}
	qrcodeBase64 := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	return &QrCodeTokenResp{
		UID:          uid,
		Time:         timeStr,
		Sign:         sign,
		Qrcode:       qrcodeContent,
		QrcodeBase64: qrcodeBase64,
		Tips:         "请使用 115 客户端扫描二维码登录",
		ClientType:   clientType,
	}, nil
}

// GetQrcodeStatus 阶段2：查询扫码状态
// GET https://qrcodeapi.115.com/get/status/?uid=&time=&sign=
// 注意：URL 末尾必须带 /，否则 404
func (c *Client) GetQrcodeStatus(uid, timeStr, sign, clientType string) (*QrCodeStatusResp, error) {
	if !isValidClientType(clientType) {
		clientType = "alipaymini"
	}

	params := url.Values{}
	params.Set("uid", uid)
	params.Set("time", timeStr)
	params.Set("sign", sign)

	urlStr := "https://qrcodeapi.115.com/get/status/?" + params.Encode()
	body, err := c.httpGet(urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("get qrcode status: %w", err)
	}

	// 解析响应: { data: { status: int, msg?: string }, message?: string }
	var resp struct {
		Data struct {
			Status int    `json:"status"`
			Msg    string `json:"msg"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse qrcode status: %w", err)
	}

	switch resp.Data.Status {
	case 0:
		return &QrCodeStatusResp{Status: QrCodeWaiting, Msg: "等待扫码"}, nil
	case 1:
		return &QrCodeStatusResp{Status: QrCodeScanned, Msg: "已扫码，等待确认"}, nil
	case 2:
		// 登录成功，调第 3 步换 cookie
		cookie, err := c.GetQrcodeResult(uid, clientType)
		if err != nil {
			return nil, fmt.Errorf("获取登录结果失败: %w", err)
		}
		return &QrCodeStatusResp{Status: QrCodeSuccess, Msg: "登录成功", Cookie: cookie}, nil
	case -1:
		return &QrCodeStatusResp{Status: QrCodeExpired, Msg: "二维码已过期"}, nil
	case -2:
		return &QrCodeStatusResp{Status: QrCodeCancelled, Msg: "用户取消登录"}, nil
	default:
		// 兜底：key invalid 也视为过期
		if resp.Message == "key invalid" {
			return &QrCodeStatusResp{Status: QrCodeExpired, Msg: "二维码已过期"}, nil
		}
		return &QrCodeStatusResp{Status: QrCodeExpired, Msg: fmt.Sprintf("未知状态码: %d", resp.Data.Status)}, nil
	}
}

// GetQrcodeResult 阶段3：用 uid 换 cookie
// POST https://qrcodeapi.115.com/app/1.0/{clientType}/1.0/login/qrcode/
// body: app={clientType}&account={uid}
// 返回标准 cookie 字符串：key1=value1; key2=value2; ...
func (c *Client) GetQrcodeResult(uid, clientType string) (string, error) {
	normalizedApp, specialUA := normalizeAppType(clientType)

	urlStr := fmt.Sprintf("https://qrcodeapi.115.com/app/1.0/%s/1.0/login/qrcode/", normalizedApp)
	// POST body 必须同时传 app 和 account 参数（对齐 p115client）
	formData := url.Values{}
	formData.Set("account", uid)
	formData.Set("app", normalizedApp)

	req, err := http.NewRequest("POST", urlStr, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if specialUA != "" {
		req.Header.Set("User-Agent", specialUA)
	} else {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	// 不跟随重定向，保留 Set-Cookie
	c.HTTP.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() {
		c.HTTP.CheckRedirect = nil // 恢复默认
	}()

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("qrcode result request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// 解析响应: { data: { cookie: { key: val, ... } } }
	var result struct {
		Data struct {
			Cookie map[string]string `json:"cookie"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse qrcode result: %w", err)
	}

	if len(result.Data.Cookie) == 0 {
		return "", fmt.Errorf("登录响应中未包含 cookie 数据: %s", string(body))
	}

	// 拼接 cookie 字符串：key=value; key=value; ...
	var parts []string
	for k, v := range result.Data.Cookie {
		if k != "" && v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	cookieStr := strings.Join(parts, "; ")
	if cookieStr == "" {
		return "", fmt.Errorf("Cookie 字符串为空")
	}
	return cookieStr, nil
}

// ==================== 辅助 ====================

// rawToString 将 json.RawMessage 转为字符串，兼容 JSON 字符串和数字两种格式。
// 避免大数字被 float64 解析后输出科学计数法（如 1.78e+09），导致 115 API 无法识别。
// normalizeAppType 将客户端类型归一化为 115 API 认可的 app 参数
// 对齐 p115client login_qrcode_scan_result 中的归一化逻辑
func normalizeAppType(clientType string) (app string, specialUA string) {
	ct := strings.TrimSpace(clientType)

	switch ct {
	case "desktop":
		return "web", ""
	case "windows", "os_windows":
		return "os_windows", ""
	case "mac", "os_mac":
		return "os_mac", ""
	case "linux", "os_linux":
		return "os_linux", ""
	case "ios", "115ios":
		return "ios", "UPhone/1.0.0"
	case "qios":
		return "ios", "OfficePhone/1.0.0"
	case "ipad", "115ipad":
		return "ios", "UPad/1.0.0"
	case "qipad":
		return "ios", "OfficePad/1.0.0"
	case "android", "115android":
		return "115android", ""
	case "bandroid":
		return "bandroid", ""
	case "bios":
		return "bios", ""
	case "apple_tv":
		return "apple_tv", ""
	case "bipad":
		return "bipad", ""
	case "alipaymini", "wechatmini", "tv", "qandroid", "harmony", "web":
		return ct, ""
	default:
		if _, ok := APP_TO_SSOENT[ct]; ok {
			return ct, ""
		}
		return "alipaymini", ""
	}
}

func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// 如果是 JSON 字符串（带引号），去掉引号
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	// 否则原样返回字节内容（数字字面量如 1787051208）
	return strings.TrimSpace(string(raw))
}

func isValidClientType(ct string) bool {
	_, ok := APP_TO_SSOENT[ct]
	return ok
}

// httpGet 发送 GET 请求，返回 body
func (c *Client) httpGet(urlStr string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
