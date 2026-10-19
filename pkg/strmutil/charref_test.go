package strmutil

import "testing"

// TestUnescape115CharRef 115 私有字符引用解码："\x02<码点十进制>" → 对应字符。
// 对齐 p115client unescape_115_charref（"\x02128074]0号：优质资源" == "👊0号：优质资源"）。
func TestUnescape115CharRef(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"no marker", "普通电影名.2023.mkv", "普通电影名.2023.mkv"},
		{"emoji", "[\x02128074]0号：优质资源", "[👊]0号：优质资源"},
		{"multiple", "a\x02128102b\x02128074c", "a👦b👊c"},
		{"lone marker preserved", "bad\x02name", "bad\x02name"},
		{"invalid codepoint (surrogate)", "x\x0255296y", "x\x0255296y"},
		{"overflow codepoint", "x\x02999999999999y", "x\x02999999999999y"},
		{"marker followed by non-digit", "x\x02ay", "x\x02ay"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Unescape115CharRef(c.input)
			if got != c.want {
				t.Fatalf("Unescape115CharRef(%q) = %q, want %q", c.input, got, c.want)
			}
			// 幂等：解码产物是常规字符，重复调用不变
			if again := Unescape115CharRef(got); again != got {
				t.Fatalf("Unescape115CharRef not idempotent: %q -> %q", got, again)
			}
		})
	}
}
