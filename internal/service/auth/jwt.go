// Package auth JWT 签发与校验（对应 lib/jwt.ts + lib/jwtSecret.ts）
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// Claims FastStrm JWT 自定义声明
type Claims struct {
	Username string `json:"username"`
	// 预留后续扩展：role / csrf 等
	jwt.RegisteredClaims
}

// TokenIssuer 负责签发 + 校验 JWT
type TokenIssuer struct {
	secret []byte
	// 默认有效期（可配置）
	Validity time.Duration
	Issuer   string
}

// DefaultValidity JWT 默认有效期（7 天，与前端登录态一致）
const DefaultValidity = 7 * 24 * time.Hour

// NewTokenIssuer 创建 Issuer。secret 为空时自动生成随机密钥（但会导致重启失效）。
// 生产环境必须从 /app/config/.jwt_secret 或 settings.json internalToken 派生持久化密钥。
func NewTokenIssuer(secret []byte) *TokenIssuer {
	return &TokenIssuer{
		secret:   secret,
		Validity: DefaultValidity,
		Issuer:   "faststrm-go",
	}
}

// GenerateSecret 生成 32 字节 hex 编码（64 字符）随机密钥，供持久化用
func GenerateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Sign 签发 token
func (t *TokenIssuer) Sign(username string) (string, error) {
	if len(t.secret) == 0 {
		return "", errors.New("jwt secret not set")
	}
	now := time.Now()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.Issuer,
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.Validity)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(t.secret)
}

// Parse 解析并校验 token；返回 *Claims 或 错误
func (t *TokenIssuer) Parse(tokenString string) (*Claims, error) {
	if len(t.secret) == 0 {
		return nil, errors.New("jwt secret not set")
	}
	tok, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := tok.Claims.(*Claims); ok && tok.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}
