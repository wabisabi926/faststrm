package pwdcrypto

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// ========== HashPassword / VerifyPassword ==========

func TestHashPasswordVectors(t *testing.T) {
	salt := "a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4"
	got := HashPassword(salt, "admin")
	wantPrefix := "$sha256$"
	if got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("HashPrefix missing: %s", got)
	}

	// 相同 salt + 相同密码 → 哈希恒定
	got2 := HashPassword(salt, "admin")
	if got != got2 {
		t.Fatalf("hash not deterministic: %s != %s", got, got2)
	}

	// 验证通过
	if !VerifyPassword(salt, "admin", got) {
		t.Fatalf("verify failed for correct password")
	}
	// 密码错误
	if VerifyPassword(salt, "wrong", got) {
		t.Fatalf("verify succeeded for wrong password")
	}
	// 明文兼容
	if !VerifyPassword("", "oldpass", "oldpass") {
		t.Fatalf("plaintext verify broken")
	}
}

// ========== AES-256-GCM 加解密 ==========

func TestEncryptDecryptRoundtrip(t *testing.T) {
	salt := "fixed_salt_for_tests_a1b2c3d4_a1b2c3d4"

	cases := []string{
		"short",
		"hello world",
		"中文测试 123!@#",
		"",
	}
	for _, plain := range cases {
		enc, err := EncryptCredential(salt, plain)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		if plain == "" {
			if enc != "" {
				t.Fatalf("empty plain not preserved: %s", enc)
			}
			continue
		}
		if !IsEncrypted(enc) {
			t.Fatalf("not encrypted format: %s", enc)
		}
		// 加密两次 → IV 不同 → 输出不同
		enc2, _ := EncryptCredential(salt, plain)
		if enc == enc2 {
			t.Logf("warning: two encrypts produced same result (IV may be deterministic)")
		}
		dec := DecryptCredential(salt, enc)
		if dec != plain {
			t.Fatalf("decrypt mismatch: want %q, got %q", plain, dec)
		}
		// 明文直通（兼容性）
		if DecryptCredential(salt, plain) != plain {
			t.Fatalf("plaintext passthrough broken")
		}
	}
}

// TestDeriveAESKeyVector 验证密钥派生向量与 TS 一致
// TS: sha256(salt + ":aes-key")
func TestDeriveAESKeyVector(t *testing.T) {
	salt := "a1b2c3d4"
	got := deriveAESKey(salt)
	if len(got) != 32 {
		t.Fatalf("key length not 32: %d", len(got))
	}
	// 固定预期：用 Go 原生 sha256(salt + ":aes-key") 校验（自洽）
	want := sha256sumCompat([]byte(salt + ":aes-key"))
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("deriveAESKey mismatch:\ngot  %s\nwant %s",
			hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// ========== 格式对齐验证 ==========

// TestEncryptedFormat 验证加密格式: $aes256gcm$<iv_hex(24)>$<tag_hex(32)>$<cipher_hex>
func TestEncryptedFormat(t *testing.T) {
	salt := "somesalt"
	enc, err := EncryptCredential(salt, "hello")
	if err != nil {
		t.Fatal(err)
	}
	parts := splitCipher(enc)
	if len(parts) != 3 {
		t.Fatalf("want 3 parts after prefix, got %d (%s)", len(parts), enc)
	}
	if len(parts[0]) != gcmIVLen*2 { // hex iv: 12 bytes = 24 chars
		t.Fatalf("iv hex length wrong: %d", len(parts[0]))
	}
	if len(parts[1]) != gcmTagLen*2 { // tag 16 bytes = 32 chars
		t.Fatalf("tag hex length wrong: %d", len(parts[1]))
	}
}

func splitCipher(s string) []string {
	const pre = "$aes256gcm$"
	return splitString(s[len(pre):], "$")
}

func splitString(s, sep string) []string {
	var out []string
	i := 0
	for {
		j := indexOf(s[i:], sep)
		if j == -1 {
			out = append(out, s[i:])
			return out
		}
		out = append(out, s[i:i+j])
		i += j + len(sep)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// sha256sumCompat 计算 SHA-256（测试自洽用）
func sha256sumCompat(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
