package handler

import (
	"strings"
	"testing"
)

func TestBoolToInt(t *testing.T) {
	cases := []struct {
		name  string
		input bool
		want  int
	}{
		{"true -> 1", true, 1},
		{"false -> 0", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := boolToInt(c.input)
			if got != c.want {
				t.Fatalf("boolToInt(%v) = %d, want %d", c.input, got, c.want)
			}
		})
	}
}

func TestIntToBool(t *testing.T) {
	cases := []struct {
		name  string
		input int
		want  bool
	}{
		{"0 -> false", 0, false},
		{"1 -> true", 1, true},
		{"-1 -> true", -1, true},
		{"large pos -> true", 999, true},
		{"large neg -> true", -999, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := intToBool(c.input)
			if got != c.want {
				t.Fatalf("intToBool(%d) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestParseCleanupCallback(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantRequestID string
		wantApprove   bool
		wantOK        bool
	}{
		{
			name:          "approve valid",
			input:         CleanupCallbackPrefix + "|abc|y",
			wantRequestID: "abc",
			wantApprove:   true,
			wantOK:        true,
		},
		{
			name:          "reject valid",
			input:         CleanupCallbackPrefix + "|xyz|n",
			wantRequestID: "xyz",
			wantApprove:   false,
			wantOK:        true,
		},
		{
			name:          "wrong prefix",
			input:         "other|abc|y",
			wantRequestID: "",
			wantApprove:   false,
			wantOK:        false,
		},
		{
			name:          "too few parts",
			input:         CleanupCallbackPrefix + "|abc",
			wantRequestID: "",
			wantApprove:   false,
			wantOK:        false,
		},
		{
			name:          "too many parts",
			input:         CleanupCallbackPrefix + "|abc|y|extra",
			wantRequestID: "",
			wantApprove:   false,
			wantOK:        false,
		},
		{
			name:          "empty string",
			input:         "",
			wantRequestID: "",
			wantApprove:   false,
			wantOK:        false,
		},
		{
			name:          "invalid decision char",
			input:         CleanupCallbackPrefix + "|abc|z",
			wantRequestID: "",
			wantApprove:   false,
			wantOK:        false,
		},
		{
			name:          "decision is maybe",
			input:         CleanupCallbackPrefix + "|abc|maybe",
			wantRequestID: "",
			wantApprove:   false,
			wantOK:        false,
		},
		{
			name:          "realistic long requestID",
			input:         CleanupCallbackPrefix + "|1700000000000000000|y",
			wantRequestID: "1700000000000000000",
			wantApprove:   true,
			wantOK:        true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotID, gotApprove, gotOK := ParseCleanupCallback(c.input)
			if gotOK != c.wantOK {
				t.Fatalf("ok mismatch: got %v, want %v", gotOK, c.wantOK)
			}
			if gotOK {
				if gotID != c.wantRequestID {
					t.Fatalf("requestID mismatch: got %q, want %q", gotID, c.wantRequestID)
				}
				if gotApprove != c.wantApprove {
					t.Fatalf("approve mismatch: got %v, want %v", gotApprove, c.wantApprove)
				}
			}
		})
	}
}

func TestIsCleanupCallback(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid prefix y", CleanupCallbackPrefix + "|abc|y", true},
		{"valid prefix n", CleanupCallbackPrefix + "|abc|n", true},
		{"prefix alone", CleanupCallbackPrefix, false},
		{"prefix pipe only", CleanupCallbackPrefix + "|", true},
		{"different prefix", "other|abc|y", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsCleanupCallback(c.input)
			if got != c.want {
				t.Fatalf("IsCleanupCallback(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestBuildSamplePaths(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{"empty -> nil", nil, nil},
		{"empty slice -> nil", []string{}, nil},
		{"one item", []string{"a"}, []string{"a"}},
		{"exactly MaxSamplePaths", []string{"a", "b", "c", "d", "e"}, []string{"a", "b", "c", "d", "e"}},
		{"less than MaxSamplePaths", []string{"x", "y"}, []string{"x", "y"}},
		{"more than MaxSamplePaths - take first 5",
			[]string{"a", "b", "c", "d", "e", "f", "g"},
			[]string{"a", "b", "c", "d", "e"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildSamplePaths(c.input)
			if c.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %v, got nil", c.want)
			}
			if len(got) != len(c.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("index %d: got %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestGenerateRequestID(t *testing.T) {
	// 1. 非空
	id := GenerateRequestID()
	if id == "" {
		t.Fatal("GenerateRequestID returned empty string")
	}
	// 2. 全部都是数字（time.Now().UnixNano() 为 int64）
	for j, r := range id {
		if r < '0' || r > '9' {
			t.Fatalf("GenerateRequestID has non-digit char %q at position %d", r, j)
		}
	}

	// 3. 再调用一次应也合法（允许在 Windows 上相同，因 nano 精度有限）
	id2 := GenerateRequestID()
	if id2 == "" {
		t.Fatal("second GenerateRequestID returned empty string")
	}
	for j, r := range id2 {
		if r < '0' || r > '9' {
			t.Fatalf("second GenerateRequestID has non-digit char %q at position %d", r, j)
		}
	}
}

func TestCleanupConstants(t *testing.T) {
	// 确保常量定义正确
	if CleanupCallbackPrefix != "cleanup_confirm" {
		t.Fatalf("CleanupCallbackPrefix = %q, want %q", CleanupCallbackPrefix, "cleanup_confirm")
	}
	if MaxSamplePaths != 5 {
		t.Fatalf("MaxSamplePaths = %d, want 5", MaxSamplePaths)
	}
}

func TestParseCleanupCallbackPrefixUsesConstant(t *testing.T) {
	// 确认前缀匹配确实使用了 CleanupCallbackPrefix（不是硬编码字符串）
	data := strings.Join([]string{CleanupCallbackPrefix, "test123", "y"}, "|")
	id, approve, ok := ParseCleanupCallback(data)
	if !ok || id != "test123" || !approve {
		t.Fatalf("ParseCleanupCallback with constant prefix failed: got (%q,%v,%v)", id, approve, ok)
	}
}
