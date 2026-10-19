package strmutil

import "testing"

// TestURLPathEncode 保留 '/'，空格 %20（不用 '+'），中文百分号编码
func TestURLPathEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"movie.mkv", "movie.mkv"},
		{"电影 2024/x.mkv", "%E7%94%B5%E5%BD%B1%202024/x.mkv"},
		{"a+b.mkv", "a%2Bb.mkv"},
		{"a&b?c#d.mkv", "a%26b%3Fc%23d.mkv"},
	}
	for _, c := range cases {
		if got := URLPathEncode(c.in); got != c.want {
			t.Fatalf("URLPathEncode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestURLPathEncodeStrict 完整编码：'/' 也编码为 %2F，空格 %20
func TestURLPathEncodeStrict(t *testing.T) {
	cases := []struct{ in, want string }{
		{"movie.mkv", "movie.mkv"},
		{"电影/科幻/x.iso", "%E7%94%B5%E5%BD%B1%2F%E7%A7%91%E5%B9%BB%2Fx.iso"},
		{"东邪西毒：终极版 (2008)/a.mkv", "%E4%B8%9C%E9%82%AA%E8%A5%BF%E6%AF%92%EF%BC%9A%E7%BB%88%E6%9E%81%E7%89%88%20%282008%29%2Fa.mkv"},
		{"a+b@c.mkv", "a%2Bb%40c.mkv"},
	}
	for _, c := range cases {
		if got := URLPathEncodeStrict(c.in); got != c.want {
			t.Fatalf("URLPathEncodeStrict(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
