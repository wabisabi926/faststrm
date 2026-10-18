package rate

import "testing"

func TestAccountKeyFromCookie(t *testing.T) {
	cases := []struct {
		name   string
		cookie string
		want   string
	}{
		{"标准 cookie", "UID=12345; CID=abc; SEID=def; KID=ghi", "uid:12345"},
		{"UID 在中间", "CID=abc;UID=999;SEID=def", "uid:999"},
		{"小写 uid", "uid=abc123;CID=x", "uid:abc123"},
		{"末尾无分号", "CID=x;UID=777", "uid:777"},
		{"空 cookie", "", GlobalKey},
		{"无 UID", "CID=abc;SEID=def", GlobalKey},
		{"UID 空值", "UID=;CID=x", GlobalKey},
		{"UID 前后有空格", "  UID=555  ;CID=x", "uid:555"},
		{"多个 UID 取首个", "UID=111;UID=222", "uid:111"},
		{"UID 带等号的值", "UID=a=b;CID=x", "uid:a=b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AccountKeyFromCookie(tc.cookie); got != tc.want {
				t.Fatalf("AccountKeyFromCookie(%q) = %q, want %q", tc.cookie, got, tc.want)
			}
		})
	}
}
