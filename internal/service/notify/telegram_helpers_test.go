package notify

import (
	"errors"
	"testing"
)

func TestStringToInt64(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int64
	}{
		{"pure digits", "123", 123},
		{"digits with prefix", "abc123", 123},
		{"digits with suffix", "123xyz", 123},
		{"digits in middle", "abc789xyz", 789},
		{"no digits", "no digits here", 0},
		{"empty string", "", 0},
		{"single digit", "5", 5},
		{"multiple digit groups", "a1b2c3", 123}, // 连续拼接: 1,2,3 -> 123
		{"zero", "0", 0},
		{"long digits", "9999999999", 9999999999},
		{"with minus", "-5", 5},   // 跳过 '-' 只取数字
		{"with dot", "3.14", 314}, // 跳过 '.' 只取数字
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stringToInt64(c.input)
			if got != c.want {
				t.Fatalf("stringToInt64(%q) = %d, want %d", c.input, got, c.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"ascii shorter", "hello", 10, "hello"},
		{"ascii longer", "hello world", 5, "hello"},
		{"ascii equal", "hello", 5, "hello"},
		{"chinese shorter", "你好世界", 10, "你好世界"},
		{"chinese longer", "你好世界测试", 4, "你好世界"},
		{"chinese exact", "你好世界", 4, "你好世界"},
		{"mixed shorter", "a你b好c世d界", 10, "a你b好c世d界"},
		{"mixed longer", "a你b好c世d界e测f试", 6, "a你b好c世"},
		{"empty string", "", 5, ""},
		{"max zero", "test", 0, ""}, // 切片 [:0] 得到空
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateRunes(c.input, c.max)
			if got != c.want {
				t.Fatalf("truncateRunes(%q, %d) = %q, want %q", c.input, c.max, got, c.want)
			}
		})
	}
}

func TestSplitTextByRunes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		max   int
		want  []string
	}{
		{"max<=0 returns whole", "abcdefg", 0, []string{"abcdefg"}},
		{"max negative returns whole", "abcdefg", -1, []string{"abcdefg"}},
		{"shorter than max", "hello", 100, []string{"hello"}},
		{"empty string", "", 10, []string{""}},
		{"exactly max", "abcdef", 6, []string{"abcdef"}},
		{"hard cut no newlines", "abcdefghij", 3, []string{"abc", "def", "ghi", "j"}},
		{"split on newline boundary", "abc\ndef\nghi\njkl\nmno", 5, []string{"abc\n", "def\n", "ghi\n", "jkl\n", "mno"}},
		{"chinese split", "你好世界测试abcd", 4, []string{"你好世界", "测试ab", "cd"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitTextByRunes(c.input, c.max)
			if len(got) != len(c.want) {
				t.Fatalf("len mismatch: got %v (len=%d), want %v (len=%d)", got, len(got), c.want, len(c.want))
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("index %d: got %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestIsTimeoutError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil -> false", nil, false},
		{"plain error", errors.New("something went wrong"), false},
		{"timeout keyword", errors.New("connection timeout"), true},
		{"deadline exceeded", errors.New("context deadline exceeded"), true},
		{"i/o timeout", errors.New("i/o timeout"), true},
		{"dial timeout", errors.New("dial tcp timeout"), true},
		{"tls handshake timeout", errors.New("tls handshake timeout"), true},
		{"connection timeout", errors.New("connection timeout"), true},
		{"uppercase TIMEOUT", errors.New("CONNECTION TIMEOUT"), true},
		{"deadline in different case", errors.New("Deadline Exceeded"), true},
		{"contains timeout", errors.New("request has timeout limit"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isTimeoutError(c.err)
			if got != c.want {
				t.Fatalf("isTimeoutError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
