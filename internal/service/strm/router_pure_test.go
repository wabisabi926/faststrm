package strm

import (
	"net/http/httptest"
	"testing"
)

// === IsValidPickcode ===

func TestIsValidPickcode(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"valid 17 alnum", "ABCDEFGHIJKLMNOPQ", true},
		{"valid 17 lowercase", "abcdefghijklmnopq", true},
		{"valid 17 mixed", "Ab3CdEfGhIjKlMnOp", true},
		{"valid 17 digits", "12345678901234567", true},
		{"too short", "ABC", false},
		{"too long", "ABCDEFGHIJKLMNOPQRSTUVWXYZ1", false},
		{"empty", "", false},
		{"with special char", "ABCDEFGHIJKLMNOPQ!", false},
		{"with space", "ABCDEFGHIJKLMNOPQ ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsValidPickcode(c.code)
			if got != c.want {
				t.Fatalf("IsValidPickcode(%q) = %v, want %v", c.code, got, c.want)
			}
		})
	}
}

// === IsPrivateNetworkIp ===

func TestIsPrivateNetworkIp(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"empty", "", false},
		{"192.168", "192.168.1.1", true},
		{"10.x", "10.0.0.1", true},
		{"127.0.0.1", "127.0.0.1", true},
		{"::1", "::1", true},
		{"localhost", "localhost", true},
		{"172.16-31", "172.16.0.1", true},
		{"172.31", "172.31.255.255", true},
		{"172.32 (not private)", "172.32.0.1", false},
		{"172.15 (not private)", "172.15.0.1", false},
		{"public", "8.8.8.8", false},
		{"with spaces", "  192.168.1.1  ", true},
		{"ipv6 bracket", "[::1]", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsPrivateNetworkIp(c.ip)
			if got != c.want {
				t.Fatalf("IsPrivateNetworkIp(%q) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// === GetClientIp ===

func TestGetClientIp(t *testing.T) {
	t.Run("x-forwarded-for", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("x-forwarded-for", "1.2.3.4, 5.6.7.8")
		got := GetClientIp(req)
		if got != "1.2.3.4" {
			t.Fatalf("got %q, want %q", got, "1.2.3.4")
		}
	})

	t.Run("x-real-ip", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("x-real-ip", "9.9.9.9")
		got := GetClientIp(req)
		if got != "9.9.9.9" {
			t.Fatalf("got %q, want %q", got, "9.9.9.9")
		}
	})

	t.Run("remote addr", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		got := GetClientIp(req)
		if got != "192.168.1.100" {
			t.Fatalf("got %q, want %q", got, "192.168.1.100")
		}
	})

	t.Run("empty remote addr", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = ""
		got := GetClientIp(req)
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

// === BuildContentDisposition ===

func TestBuildContentDisposition(t *testing.T) {
	t.Run("ascii filename", func(t *testing.T) {
		got := BuildContentDisposition("movie.mkv")
		want := `attachment; filename="movie.mkv"`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("chinese filename", func(t *testing.T) {
		got := BuildContentDisposition("电影.mkv")
		if got == "" {
			t.Fatal("should not be empty")
		}
		// Should contain UTF-8'' prefix
		if !contains(got, "UTF-8''") {
			t.Fatalf("should contain UTF-8'' prefix, got %q", got)
		}
	})

	t.Run("empty filename", func(t *testing.T) {
		got := BuildContentDisposition("")
		want := `attachment; filename=""`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

// === percentEncodePath, isUnreserved, upperHex ===

func TestPercentEncodePath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ascii", "abc123", "abc123"},
		{"with slash", "a/b", "a/b"},
		{"with space", "a b", "a%20b"},
		{"with chinese", "电影", "%E7%94%B5%E5%BD%B1"},
		{"unreserved", "a-_.~", "a-_.~"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := percentEncodePath(c.input)
			if got != c.want {
				t.Fatalf("percentEncodePath(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestIsUnreserved(t *testing.T) {
	unreserved := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.~"
	for _, c := range unreserved {
		if !isUnreserved(byte(c)) {
			t.Fatalf("isUnreserved(%q) should be true", string(c))
		}
	}
	// space, !, / should not be unreserved
	for _, c := range " !/" {
		if isUnreserved(byte(c)) {
			t.Fatalf("isUnreserved(%q) should be false", string(c))
		}
	}
}

func TestUpperHex(t *testing.T) {
	cases := []struct {
		input byte
		want  string
	}{
		{0x00, "%00"}, {0x0F, "%0F"}, {0x10, "%10"},
		{0x20, "%20"}, {0xFF, "%FF"}, {0xAB, "%AB"},
	}
	for _, c := range cases {
		got := upperHex(c.input)
		if got != c.want {
			t.Fatalf("upperHex(0x%02X) = %q, want %q", c.input, got, c.want)
		}
	}
}

// === ResolveFileName, urlQueryUnescape, parseHexByte, hexDigit ===

func TestResolveFileName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"plain", "movie.mkv", "movie.mkv"},
		{"percent encoded", "movie%20file.mkv", "movie file.mkv"},
		{"chinese encoded", "%E7%94%B5%E5%BD%B1.mkv", "电影.mkv"},
		{"no percent", "file_name.txt", "file_name.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveFileName(c.raw)
			if got != c.want {
				t.Fatalf("ResolveFileName(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestUrlQueryUnescape(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got, err := urlQueryUnescape("a%20b%2Fc")
		if err != nil || got != "a b/c" {
			t.Fatalf("got %q, err=%v", got, err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		_, err := urlQueryUnescape("ab%2")
		if err == nil {
			t.Fatal("expected error for truncated percent encoding")
		}
	})

	t.Run("invalid hex", func(t *testing.T) {
		_, err := urlQueryUnescape("ab%ZZ")
		if err == nil {
			t.Fatal("expected error for invalid hex digits")
		}
	})

	t.Run("no percent", func(t *testing.T) {
		got, err := urlQueryUnescape("hello")
		if err != nil || got != "hello" {
			t.Fatalf("got %q, err=%v", got, err)
		}
	})
}

func TestParseHexByte(t *testing.T) {
	cases := []struct {
		hi, lo byte
		want   byte
		ok     bool
	}{
		{'0', '0', 0x00, true},
		{'F', 'F', 0xFF, true},
		{'a', 'b', 0xAB, true},
		{'1', 'A', 0x1A, true},
		{'G', '0', 0, false},
		{'0', 'G', 0, false},
	}
	for _, c := range cases {
		got, ok := parseHexByte(c.hi, c.lo)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("parseHexByte(%q,%q) = (0x%02X, %v), want (0x%02X, %v)",
				c.hi, c.lo, got, ok, c.want, c.ok)
		}
	}
}

func TestHexDigit(t *testing.T) {
	cases := []struct {
		b    byte
		want int
		ok   bool
	}{
		{'0', 0, true}, {'9', 9, true},
		{'A', 10, true}, {'F', 15, true},
		{'a', 10, true}, {'f', 15, true},
		{'G', 0, false}, {' ', 0, false},
	}
	for _, c := range cases {
		got, ok := hexDigit(c.b)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("hexDigit(%q) = (%d, %v), want (%d, %v)", c.b, got, ok, c.want, c.ok)
		}
	}
}

// === simpleLRU ===

func TestSimpleLRU(t *testing.T) {
	t.Run("get missing", func(t *testing.T) {
		c := newSimpleLRU[string](3, 60_000_000_000)
		_, ok := c.Get("missing")
		if ok {
			t.Fatal("should not find missing key")
		}
	})

	t.Run("set and get", func(t *testing.T) {
		c := newSimpleLRU[string](3, 60_000_000_000)
		c.Set("k1", "v1")
		v, ok := c.Get("k1")
		if !ok || v != "v1" {
			t.Fatalf("got %q, ok=%v", v, ok)
		}
	})

	t.Run("eviction", func(t *testing.T) {
		c := newSimpleLRU[string](2, 60_000_000_000)
		c.Set("k1", "v1")
		c.Set("k2", "v2")
		c.Set("k3", "v3") // should evict k1
		_, ok := c.Get("k1")
		if ok {
			t.Fatal("k1 should have been evicted")
		}
		_, ok = c.Get("k2")
		if !ok {
			t.Fatal("k2 should still exist")
		}
		v, ok := c.Get("k3")
		if !ok || v != "v3" {
			t.Fatalf("k3 should exist: got %q, ok=%v", v, ok)
		}
	})

	t.Run("overwrite existing", func(t *testing.T) {
		c := newSimpleLRU[string](3, 60_000_000_000)
		c.Set("k1", "v1")
		c.Set("k1", "v2")
		v, ok := c.Get("k1")
		if !ok || v != "v2" {
			t.Fatalf("got %q, ok=%v", v, ok)
		}
	})
}

// === helper ===

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
