package handler

import (
	"testing"
)

// ==================== maskAPIKey ====================

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"empty", "", ""},
		{"len 1 short", "a", "***"},
		{"len 4 short", "abcd", "***"},
		{"len 7 short", "abcdefg", "***"},
		{"exactly 8 -> short path", "abcdefgh", "***"},
		{"len 9 long", "123456789", "1234*6789"},            // 9 bytes, stars=1
		{"len 10 typical", "1234567890", "1234**7890"},      // 10 bytes, stars=2
		{"len 14 long", "abcdefghijklmn", "abcd******klmn"}, // 14 bytes, stars=6
		{"len 32 typical api key", "0123456789abcdef0123456789abcdef", "0123************************cdef"},
		{"special chars ASCII 12B", "!@#$%^&*abcd", "!@#$****abcd"}, // 12B, stars=4, tail=4 "abcd"
		{"unicode short (<=8 bytes)", "中文a", "***"},                 // 3+3+1=7B, short path
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskAPIKey(c.key)
			if got != c.want {
				t.Fatalf("maskAPIKey(%q) = %q, want %q", c.key, got, c.want)
			}
		})
	}
}

func TestMaskAPIKeyLength(t *testing.T) {
	// 脱敏后长度应与原 key 一致（按字节）
	longKey := "abcdefghijklmnopqrstuvwxyz012345" // 32 bytes
	masked := maskAPIKey(longKey)
	if len(masked) != len(longKey) {
		t.Fatalf("maskAPIKey length mismatch: got %d, want %d (masked=%q)", len(masked), len(longKey), masked)
	}

	// 边界：len 9 -> 9 bytes
	k9 := "123456789"
	m9 := maskAPIKey(k9)
	if len(m9) != 9 {
		t.Fatalf("len 9 key -> masked len %d, want 9", len(m9))
	}
	if m9 != "1234*6789" {
		t.Fatalf("len 9 key -> got %q, want 1234*6789", m9)
	}

	// 非 ASCII key：按字节脱敏后长度应等于原 key 的字节长度
	unicodeKey := "中文abcdefghijklmn" // 20 bytes
	mu := maskAPIKey(unicodeKey)
	if len(mu) != len(unicodeKey) {
		t.Fatalf("unicode key len %d -> masked len %d", len(unicodeKey), len(mu))
	}
}

// ==================== containsAsterisk ====================

func TestContainsAsterisk(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want bool
	}{
		{"triple star", "***", true},
		{"star in middle", "a*b", true},
		{"star at start", "*abc", true},
		{"star at end", "abc*", true},
		{"no star", "no star", false},
		{"empty", "", false},
		{"only letters", "abcdef", false},
		{"special but no star", "!@#$%", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := containsAsterisk(c.s)
			if got != c.want {
				t.Fatalf("containsAsterisk(%q) = %v, want %v", c.s, got, c.want)
			}
		})
	}
}
