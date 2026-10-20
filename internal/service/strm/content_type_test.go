package strm

import "testing"

func TestContentTypeForName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"movie.iso", "application/octet-stream"},
		{"movie.ISO", "application/octet-stream"},
		{"movie.mkv", "video/x-matroska"},
		{"movie.mp4", "video/mp4"},
		{"movie.ts", "video/mp2t"},
		{"movie.m2ts", "video/mp2t"},
		{"movie.unknown", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := ContentTypeForName(c.name); got != c.want {
			t.Fatalf("ContentTypeForName(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
