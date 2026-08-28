package strm

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// TokenDefaultTTL 默认 token 有效期：24 小时
	TokenDefaultTTL = 24 * time.Hour
)

// GenerateTokenSecret 生成一个 32 字节随机 secret（首次启动调用一次，持久化到 settings.json）
func GenerateTokenSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		b = []byte(fmt.Sprintf("fallback-%d", time.Now().UnixNano()))
	}
	return hex.EncodeToString(b)
}

// SignStrmToken 为 STRM 请求生成签名 token。
//
// 签名格式：expire_hex|hmac_hex
//
//	expire_hex = 过期时间 unix 秒（16 位 hex）
//	hmac_hex   = HMAC-SHA256(secret, "account|pickcode|expire_hex")
//
// secret 为空时返回空字符串（表示禁用签名）。
func SignStrmToken(secret, account, pickcode string, ttl time.Duration) string {
	if secret == "" {
		return ""
	}
	if ttl <= 0 {
		ttl = TokenDefaultTTL
	}
	expire := time.Now().Add(ttl).Unix()
	expireHex := fmt.Sprintf("%x", expire)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(account))
	mac.Write([]byte("|"))
	mac.Write([]byte(strings.ToLower(pickcode)))
	mac.Write([]byte("|"))
	mac.Write([]byte(expireHex))
	sig := hex.EncodeToString(mac.Sum(nil))

	return expireHex + "|" + sig
}

// VerifyStrmToken 校验签名 token。
// 返回 (ok, reason)：
//
//	ok=true  签名有效且未过期
//	ok=false reason: missing_secret / bad_format / bad_signature / expired
func VerifyStrmToken(secret, token, account, pickcode string) (bool, string) {
	if secret == "" {
		return true, ""
	}
	if token == "" {
		return false, "missing_token"
	}

	parts := strings.SplitN(token, "|", 2)
	if len(parts) != 2 {
		return false, "bad_format"
	}
	expireHex, sigHex := parts[0], parts[1]

	var expire int64
	if _, err := fmt.Sscanf(expireHex, "%x", &expire); err != nil {
		return false, "bad_format"
	}
	if time.Now().Unix() > expire {
		return false, "expired"
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(account))
	mac.Write([]byte("|"))
	mac.Write([]byte(strings.ToLower(pickcode)))
	mac.Write([]byte("|"))
	mac.Write([]byte(expireHex))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(sigHex)) {
		return false, "bad_signature"
	}
	return true, ""
}

// AppendSignedToken 给 URL 追加 token 参数（secret 空则不变）。
func AppendSignedToken(rawURL, secret, account, pickcode string, ttl time.Duration) string {
	tok := SignStrmToken(secret, account, pickcode, ttl)
	if tok == "" {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "token=" + tok
}
