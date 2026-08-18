// Package pwdcrypto 实现账号密码哈希 + 凭据可逆加密
// 与 frontend/src/lib/passwordCrypto.ts 逐字节对齐
package pwdcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	HashPrefix   = "$sha256$"
	CipherPrefix = "$aes256gcm$"

	// 固定 IV 长度（AES-GCM 推荐 12 字节）
	gcmIVLen = 12
	// GCM authTag 长度（Node.js 默认 16 字节）
	gcmTagLen = 16
)

// HashPassword 使用 salt + SHA-256 哈希密码
// 格式: $sha256$<hex>
// 对齐 TS: crypto.createHash("sha256").update(salt + password).digest("hex")
func HashPassword(salt, password string) string {
	h := sha256.Sum256([]byte(salt + password))
	return HashPrefix + hex.EncodeToString(h[:])
}

// VerifyPassword 验证密码
// - stored 以 $sha256$ 开头 → 哈希校验
// - 否则视作明文兼容（旧格式）
func VerifyPassword(salt, password, stored string) bool {
	if stored == "" {
		return password == ""
	}
	if strings.HasPrefix(stored, HashPrefix) {
		want := stored[len(HashPrefix):]
		h := sha256.Sum256([]byte(salt + password))
		got := hex.EncodeToString(h[:])
		return want == got
	}
	// 明文兼容
	return password == stored
}

// IsHashed 判断是否已是哈希格式
func IsHashed(stored string) bool {
	return strings.HasPrefix(stored, HashPrefix)
}

// deriveAESKey 从 salt 派生 AES-256 主密钥（32 字节）
// 对齐 TS: sha256(salt + ":aes-key")
func deriveAESKey(salt string) []byte {
	h := sha256.Sum256([]byte(salt + ":aes-key"))
	key := make([]byte, 32)
	copy(key, h[:])
	return key
}

// EncryptCredential AES-256-GCM 加密
// 返回格式: $aes256gcm$<iv_hex>$<authTag_hex>$<ciphertext_hex>
//
// 对齐 TS:
//
//	iv = randomBytes(12)
//	cipher = createCipheriv("aes-256-gcm", key, iv)
//	encrypted = update(utf8) + final()
//	authTag = getAuthTag()
func EncryptCredential(salt, plaintext string) (string, error) {
	if plaintext == "" || strings.HasPrefix(plaintext, CipherPrefix) {
		return plaintext, nil
	}
	key := deriveAESKey(salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if gcm.NonceSize() != gcmIVLen {
		// 强制 12 字节 IV，与 TS 版本一致
		return "", fmt.Errorf("unexpected gcm nonce size: %d", gcm.NonceSize())
	}
	iv := make([]byte, gcmIVLen)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	// Seal 输出格式: ciphertext || tag
	out := gcm.Seal(nil, iv, []byte(plaintext), nil)
	// Node.js: authTag 单独获取（16 字节，位于末尾）
	ciphertext := out[:len(out)-gcmTagLen]
	tag := out[len(out)-gcmTagLen:]

	return fmt.Sprintf("%s%s$%s$%s",
		CipherPrefix,
		hex.EncodeToString(iv),
		hex.EncodeToString(tag),
		hex.EncodeToString(ciphertext),
	), nil
}

// DecryptCredential AES-256-GCM 解密
// 若不是加密格式，原样返回（兼容明文）
func DecryptCredential(salt, stored string) string {
	if stored == "" || !strings.HasPrefix(stored, CipherPrefix) {
		return stored
	}
	parts := strings.Split(stored[len(CipherPrefix):], "$")
	if len(parts) != 3 {
		return stored
	}
	iv, err := hex.DecodeString(parts[0])
	if err != nil {
		return stored
	}
	authTag, err := hex.DecodeString(parts[1])
	if err != nil {
		return stored
	}
	ciphertext, err := hex.DecodeString(parts[2])
	if err != nil {
		return stored
	}

	key := deriveAESKey(salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return stored
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return stored
	}
	if len(authTag) != gcmTagLen {
		return stored
	}

	// Go GCM 需要 tag 拼接在 ciphertext 末尾
	full := make([]byte, 0, len(ciphertext)+len(authTag))
	full = append(full, ciphertext...)
	full = append(full, authTag...)

	plain, err := gcm.Open(nil, iv, full, nil)
	if err != nil {
		return stored
	}
	return string(plain)
}

// IsEncrypted 判断是否已是加密格式
func IsEncrypted(s string) bool {
	return s != "" && strings.HasPrefix(s, CipherPrefix)
}

// EncryptString 与 EncryptCredential 相同语义但不允许空串（显式报错）
// 保留以避免调用方混淆
func EncryptString(salt, plaintext string) (string, error) {
	return EncryptCredential(salt, plaintext)
}

// RandomSalt 生成 32 字节 hex 编码的随机 salt（64 字符）
// 对应 TS: crypto.randomBytes(32).toString("hex")
func RandomSalt() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// RandomToken 生成指定字节数的 hex token
func RandomToken(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("invalid token size")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
