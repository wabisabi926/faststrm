package strm

import "testing"

func TestIsUnusablePrefix(t *testing.T) {
	unusable := []string{
		"",
		"   ",
		"http://0.0.0.0:8090",
		"http://127.0.0.1:8090",
		"http://127.0.1.1:8090",
		"http://localhost:8090",
		"http://LOCALHOST:8090",
		"http://[::]:8090",
		"not-a-url",
		"/api/strm",
	}
	for _, p := range unusable {
		if !IsUnusablePrefix(p) {
			t.Fatalf("IsUnusablePrefix(%q) = false, want true", p)
		}
	}

	usable := []string{
		"http://192.168.1.10:8090",
		"http://10.0.0.5:8090",
		"https://media.example.com",
		"https://media.example.com:8443/faststrm",
		"http://172.16.3.4",
	}
	for _, p := range usable {
		if IsUnusablePrefix(p) {
			t.Fatalf("IsUnusablePrefix(%q) = true, want false", p)
		}
	}
}

func TestResolveStrmPrefix_PrefersFirstUsable(t *testing.T) {
	got, source := ResolveStrmPrefix([]string{
		"",
		"http://0.0.0.0:8090",
		"https://media.example.com/",
		"http://192.168.1.10:8090",
	}, 8090)
	if source != "configured" {
		t.Fatalf("source = %q, want configured", source)
	}
	if got != "https://media.example.com" {
		t.Fatalf("prefix = %q, want https://media.example.com (trailing slash trimmed)", got)
	}
}

func TestResolveStrmPrefix_FallsBackToLANIP(t *testing.T) {
	got, source := ResolveStrmPrefix([]string{"http://0.0.0.0:8090"}, 9000)
	switch source {
	case "auto_lan_ip":
		if got == "" {
			t.Fatal("auto_lan_ip source must yield non-empty prefix")
		}
		if IsUnusablePrefix(got) {
			t.Fatalf("auto LAN prefix must be usable, got %q", got)
		}
	case "unresolved":
		if got != "" {
			t.Fatalf("unresolved must return empty prefix, got %q", got)
		}
	default:
		t.Fatalf("unexpected source %q", source)
	}
}

func TestPortFromBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"http://0.0.0.0:9000", 9000},
		{"http://192.168.1.10:18080", 18080},
		{"https://media.example.com", defaultServicePort},
		{"", defaultServicePort},
		{"garbage", defaultServicePort},
	}
	for _, tc := range cases {
		if got := PortFromBaseURL(tc.in); got != tc.want {
			t.Fatalf("PortFromBaseURL(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
