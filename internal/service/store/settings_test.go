package store

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// writeSettings 写入指定 JSON 内容到 config/settings.json
func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

// 合法 64 hex 字符 secret（32 字节），用于断言不被覆盖的场景
const validSecretForTest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestReadSettings_TokenSecret_Disabled_NoGeneration
// 开关关闭时即使 secret 为空也不生成，保证老部署零破坏。
func TestReadSettings_TokenSecret_Disabled_NoGeneration(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"enableTokenSigning":false,"tokenSecret":""}}`)
	ss := NewSettingsStore("test_salt", dir)
	s, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if s.Strm.TokenSecret != "" {
		t.Errorf("开关关闭时 TokenSecret 应保持空，got %q (len=%d)",
			s.Strm.TokenSecret, len(s.Strm.TokenSecret))
	}
}

// TestReadSettings_TokenSecret_Empty_AutoGenerate
// 开关打开且 secret 为空 → 自动生成 64 hex 字符。
func TestReadSettings_TokenSecret_Empty_AutoGenerate(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"enableTokenSigning":true,"tokenSecret":""}}`)
	ss := NewSettingsStore("test_salt", dir)
	s, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if len(s.Strm.TokenSecret) != 64 {
		t.Fatalf("自动生成的 TokenSecret 长度 want 64, got %d (%q)",
			len(s.Strm.TokenSecret), s.Strm.TokenSecret)
	}
	if _, err := hex.DecodeString(s.Strm.TokenSecret); err != nil {
		t.Fatalf("自动生成的 TokenSecret 不是合法 hex: %v", err)
	}
}

// TestReadSettings_TokenSecret_BadLength_Regenerate
// settings.json 损坏或被截断导致 secret 长度异常 → 自动重新生成。
func TestReadSettings_TokenSecret_BadLength_Regenerate(t *testing.T) {
	dir := t.TempDir()
	// 30 字符，明显不是合法 64 hex
	malformed := "bad_length_30_chars_xxxxxx"
	writeSettings(t, dir, `{"strm":{"enableTokenSigning":true,"tokenSecret":"`+malformed+`"}}`)
	ss := NewSettingsStore("test_salt", dir)
	s, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if len(s.Strm.TokenSecret) != 64 {
		t.Fatalf("损坏 secret 应被重新生成为 64 hex，got len=%d (%q)",
			len(s.Strm.TokenSecret), s.Strm.TokenSecret)
	}
	if s.Strm.TokenSecret == malformed {
		t.Fatalf("TokenSecret 不应保留损坏的原值")
	}
	if _, err := hex.DecodeString(s.Strm.TokenSecret); err != nil {
		t.Fatalf("重新生成的 TokenSecret 不是合法 hex: %v", err)
	}
}

// TestReadSettings_TokenSecret_Valid_Unchanged
// 开关打开且 secret 为合法 64 hex → 不重新生成，保持稳定。
func TestReadSettings_TokenSecret_Valid_Unchanged(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"enableTokenSigning":true,"tokenSecret":"`+validSecretForTest+`"}}`)
	ss := NewSettingsStore("test_salt", dir)
	s, err := ss.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if s.Strm.TokenSecret != validSecretForTest {
		t.Errorf("合法 secret 不应被重新生成，want %q, got %q",
			validSecretForTest, s.Strm.TokenSecret)
	}
}

// TestReadSettings_TokenSecret_PersistedAcrossRestart
// 首次自动生成 → 回写 settings.json → 重启后保持一致。
// 这是 T9 迁移幂等性的关键保证：不会每次启动重新生成导致旧 STRM 失效。
func TestReadSettings_TokenSecret_PersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"strm":{"enableTokenSigning":true,"tokenSecret":""}}`)

	// 首次启动：自动生成
	ss1 := NewSettingsStore("test_salt", dir)
	s1, err := ss1.ReadSettings()
	if err != nil {
		t.Fatalf("first ReadSettings: %v", err)
	}
	if len(s1.Strm.TokenSecret) != 64 {
		t.Fatalf("首次生成 len=%d", len(s1.Strm.TokenSecret))
	}
	firstSecret := s1.Strm.TokenSecret

	// 模拟重启：新实例读同一文件
	ss2 := NewSettingsStore("test_salt", dir)
	s2, err := ss2.ReadSettings()
	if err != nil {
		t.Fatalf("restart ReadSettings: %v", err)
	}
	if s2.Strm.TokenSecret != firstSecret {
		t.Errorf("重启后 TokenSecret 应保持一致，first=%q restart=%q",
			firstSecret, s2.Strm.TokenSecret)
	}

	// 第三次重启仍稳定（验证不会重复生成）
	ss3 := NewSettingsStore("test_salt", dir)
	s3, err := ss3.ReadSettings()
	if err != nil {
		t.Fatalf("third ReadSettings: %v", err)
	}
	if s3.Strm.TokenSecret != firstSecret {
		t.Errorf("第三次启动 TokenSecret 漂移，first=%q third=%q",
			firstSecret, s3.Strm.TokenSecret)
	}
}

// TestReadSettings_TokenSecret_BadThenValidStays
// 损坏 secret 被重新生成后，重启时新 secret 保持稳定（不会再次重新生成）。
func TestReadSettings_TokenSecret_BadThenValidStays(t *testing.T) {
	dir := t.TempDir()
	malformed := "truncated_secret_value"
	writeSettings(t, dir, `{"strm":{"enableTokenSigning":true,"tokenSecret":"`+malformed+`"}}`)

	// 首次：重新生成
	ss1 := NewSettingsStore("test_salt", dir)
	s1, err := ss1.ReadSettings()
	if err != nil {
		t.Fatalf("first ReadSettings: %v", err)
	}
	if len(s1.Strm.TokenSecret) != 64 {
		t.Fatalf("重新生成后 len=%d", len(s1.Strm.TokenSecret))
	}
	regenerated := s1.Strm.TokenSecret

	// 重启：新 secret 合法，应保持不变
	ss2 := NewSettingsStore("test_salt", dir)
	s2, err := ss2.ReadSettings()
	if err != nil {
		t.Fatalf("restart ReadSettings: %v", err)
	}
	if s2.Strm.TokenSecret != regenerated {
		t.Errorf("重新生成的 secret 应在重启后保持，regen=%q restart=%q",
			regenerated, s2.Strm.TokenSecret)
	}
}
