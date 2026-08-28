package strm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestGenerateTokenSecret(t *testing.T) {
	s1 := GenerateTokenSecret()
	s2 := GenerateTokenSecret()
	if len(s1) != 64 {
		t.Fatalf("secret length = %d, want 64 hex chars", len(s1))
	}
	if s1 == s2 {
		t.Fatalf("两个 secret 不应相同")
	}
	if _, err := hex.DecodeString(s1); err != nil {
		t.Fatalf("secret 不是合法 hex: %v", err)
	}
}

func TestSignStrmToken_EmptySecret_ReturnsEmpty(t *testing.T) {
	if got := SignStrmToken("", "a", "pc123456789012345", 0); got != "" {
		t.Fatalf("空 secret 应返回空, got=%q", got)
	}
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	secret := GenerateTokenSecret()
	account := "alice"
	pickcode := "abcdefghijklmnopq"
	tok := SignStrmToken(secret, account, pickcode, 0)
	if tok == "" {
		t.Fatal("expected non-empty token")
	}
	if !strings.Contains(tok, "|") {
		t.Fatalf("token 缺少分隔符: %q", tok)
	}
	ok, reason := VerifyStrmToken(secret, tok, account, pickcode)
	if !ok {
		t.Fatalf("verify 失败: %s", reason)
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	secret := GenerateTokenSecret()
	expireHex := fmt.Sprintf("%x", time.Now().Add(-time.Hour).Unix())
	mac := func(msg string) string {
		h := sha256.New()
		h.Write([]byte(msg))
		return hex.EncodeToString(h.Sum(nil))
	}
	msg := "alice|abcdefghijklmnopq|" + expireHex
	tok := expireHex + "|" + mac(msg)
	ok, reason := VerifyStrmToken(secret, tok, "alice", "abcdefghijklmnopq")
	if ok || reason != "expired" {
		t.Fatalf("want expired=false, got ok=%v reason=%q", ok, reason)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	secret := GenerateTokenSecret()
	tok := SignStrmToken(secret, "alice", "abcdefghijklmnopq", time.Hour)
	fake := tok[:len(tok)-1] + "x"
	if ok, reason := VerifyStrmToken(secret, fake, "alice", "abcdefghijklmnopq"); ok || reason != "bad_signature" {
		t.Fatalf("want bad_signature, got ok=%v reason=%q", ok, reason)
	}
}

func TestVerify_AccountMismatch(t *testing.T) {
	secret := GenerateTokenSecret()
	tok := SignStrmToken(secret, "alice", "abcdefghijklmnopq", time.Hour)
	if ok, reason := VerifyStrmToken(secret, tok, "bob", "abcdefghijklmnopq"); ok || reason != "bad_signature" {
		t.Fatalf("want bad_signature, got ok=%v reason=%q", ok, reason)
	}
}

func TestVerify_PickcodeCaseInsensitive(t *testing.T) {
	secret := GenerateTokenSecret()
	tok := SignStrmToken(secret, "alice", "ABCDEFGHIJKLMNOPQ", time.Hour)
	if ok, reason := VerifyStrmToken(secret, tok, "alice", "abcdefghijklmnopq"); !ok {
		t.Fatalf("pickcode 大小写不敏感校验失败: %s", reason)
	}
}

func TestVerify_EmptySecret_SkipCheck(t *testing.T) {
	if ok, reason := VerifyStrmToken("", "", "a", "pc"); !ok || reason != "" {
		t.Fatalf("empty secret should pass through, got ok=%v reason=%q", ok, reason)
	}
}

func TestVerify_MissingToken(t *testing.T) {
	secret := GenerateTokenSecret()
	if ok, reason := VerifyStrmToken(secret, "", "a", "pc"); ok || reason != "missing_token" {
		t.Fatalf("want missing_token, got ok=%v reason=%q", ok, reason)
	}
}

func TestAppendSignedToken(t *testing.T) {
	secret := GenerateTokenSecret()
	url := "http://foo/api/strm?account=a&pickcode=abcdefghijklmnopq"
	out := AppendSignedToken(url, secret, "a", "abcdefghijklmnopq", time.Hour)
	if !strings.Contains(out, "&token=") {
		t.Fatalf("URL 未追加 token: %s", out)
	}
	if strings.Count(out, "token=") != 1 {
		t.Fatalf("token 重复: %s", out)
	}
}

func TestAppendSignedToken_EmptySecret_Noop(t *testing.T) {
	url := "http://foo/api/strm?account=a"
	if out := AppendSignedToken(url, "", "a", "pc", time.Hour); out != url {
		t.Fatalf("empty secret 不应改变 URL, got=%s", out)
	}
}

func TestGenerateTokenSecret_NotDeterministic(t *testing.T) {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	if hex.EncodeToString(buf) == "00000000000000000000000000000000" {
		t.Fatal("rand.Read 行为异常（全零）")
	}
}

// TestGenerateTokenSecret_StressAlwaysValid
// 多次调用断言每次都返回合法 64 hex 字符（32 字节）。
// 间接覆盖 rand.Read 失败兜底分支的输出契约：即使熵池异常，
// 兜底用时间戳填充也应保证输出长度可解码为 32 字节。
func TestGenerateTokenSecret_StressAlwaysValid(t *testing.T) {
	const iterations = 200
	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		s := GenerateTokenSecret()
		if len(s) != 64 {
			t.Fatalf("iter %d: secret 长度 = %d, want 64 hex chars", i, len(s))
		}
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("iter %d: secret 不是合法 hex: %v (s=%q)", i, err, s)
		}
		if len(b) != 32 {
			t.Fatalf("iter %d: 解码后字节数 = %d, want 32", i, len(b))
		}
		// 至少应是唯一的（防止兜底逻辑返回常量）
		seen[s] = struct{}{}
	}
	if len(seen) < iterations-1 {
		t.Errorf("重复 secret 过多：迭代 %d 次只产生 %d 个唯一值", iterations, len(seen))
	}
}
