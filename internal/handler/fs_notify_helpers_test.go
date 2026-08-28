package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// === fs.go: encodeRedirectURL, hexUpper, itoa ===

func TestEncodeRedirectURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ascii", "https://example.com/path", "https://example.com/path"},
		{"with space", "https://example.com/a b", "https://example.com/a%20b"},
		{"with chinese", "https://example.com/电影", "https://example.com/%E7%94%B5%E5%BD%B1"},
		{"with percent", "https://example.com/%2F", "https://example.com/%2F"},
		{"empty", "", ""},
		{"with special chars", "https://example.com/?q=a@b.com#frag", "https://example.com/?q=a@b.com#frag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := encodeRedirectURL(c.input)
			if got != c.want {
				t.Fatalf("encodeRedirectURL(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestHexUpper(t *testing.T) {
	cases := []struct {
		input byte
		want  byte
	}{
		{0, '0'}, {9, '9'}, {10, 'A'}, {15, 'F'},
	}
	for _, c := range cases {
		got := hexUpper(c.input)
		if got != c.want {
			t.Fatalf("hexUpper(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestItoa(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{0, "0"}, {42, "42"}, {-5, "-5"}, {9999999999, "9999999999"},
	}
	for _, c := range cases {
		got := itoa(c.input)
		if got != c.want {
			t.Fatalf("itoa(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

// === notify.go: maskBotToken, parseUserID, firstNonEmpty ===

func TestMaskBotToken(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"empty", "", ""},
		{"short", "abc", "***"},
		{"no separator", "abcdefgh", "***"},
		{"normal", "123456789:ABCdefGHIjklMNOpqrsTUV", "123456789:******************sTUV"},
		{"short token (<=8)", "12:ab", "***"},
		{"long token", "9876543210:abcdefghijklmnopqrstuvwxyz123456", "9876543210:****************************3456"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskBotToken(c.token)
			if got != c.want {
				t.Fatalf("maskBotToken(%q) = %q, want %q", c.token, got, c.want)
			}
		})
	}
}

func TestParseUserID(t *testing.T) {
	t.Run("nil returns error", func(t *testing.T) {
		_, err := parseUserID(nil)
		if err == nil {
			t.Fatal("expected error for nil")
		}
	})

	t.Run("float64", func(t *testing.T) {
		v, err := parseUserID(float64(12345))
		if err != nil || v != 12345 {
			t.Fatalf("got %d, err=%v", v, err)
		}
	})

	t.Run("string valid", func(t *testing.T) {
		v, err := parseUserID("67890")
		if err != nil || v != 67890 {
			t.Fatalf("got %d, err=%v", v, err)
		}
	})

	t.Run("string with spaces", func(t *testing.T) {
		v, err := parseUserID("  111  ")
		if err != nil || v != 111 {
			t.Fatalf("got %d, err=%v", v, err)
		}
	})

	t.Run("string invalid", func(t *testing.T) {
		_, err := parseUserID("abc")
		if err == nil {
			t.Fatal("expected error for invalid string")
		}
	})

	t.Run("json.Number", func(t *testing.T) {
		n := json.Number("99999")
		v, err := parseUserID(n)
		if err != nil || v != 99999 {
			t.Fatalf("got %d, err=%v", v, err)
		}
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := parseUserID([]int{1, 2})
		if err == nil {
			t.Fatal("expected error for unsupported type")
		}
	})
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		vals []string
		want string
	}{
		{"all empty", []string{"", "", ""}, ""},
		{"first non-empty", []string{"a", "b", "c"}, "a"},
		{"middle non-empty", []string{"", "x", ""}, "x"},
		{"last non-empty", []string{"", "", "z"}, "z"},
		{"no args", []string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstNonEmpty(c.vals...)
			if got != c.want {
				t.Fatalf("firstNonEmpty(%v) = %q, want %q", c.vals, got, c.want)
			}
		})
	}
}

// === task.go: lastSeg ===

func TestLastSeg(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"simple", "/api/task/abc-123", "abc-123"},
		{"trailing slash", "/api/task/abc-123/", "abc-123"},
		{"multiple trailing slashes", "/api/task/abc///", "abc"},
		{"no slash", "abc", "abc"},
		{"root", "/", ""},
		{"empty", "", ""},
		{"only slashes", "///", ""},
		{"two segments", "/foo/bar", "bar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := lastSeg(c.path)
			if got != c.want {
				t.Fatalf("lastSeg(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

// === task.go: fillUpsertFromBody ===

func TestFillUpsertFromBody(t *testing.T) {
	t.Run("form values", func(t *testing.T) {
		body := "name=test&account=myacc&sourcePath=/src&targetPath=/dst&strmPrefix=/prefix&removeExtraFiles=on&enablePathEncoding=true&enable302=yes&scheduleMode=daily&scheduleValue=09:00&enabled=on"
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		var u UpsertTaskRequest
		fillUpsertFromBody(req, &u)

		if u.Name != "test" {
			t.Fatalf("Name: got %q, want %q", u.Name, "test")
		}
		if u.Account != "myacc" {
			t.Fatalf("Account: got %q, want %q", u.Account, "myacc")
		}
		if u.SourcePath != "/src" {
			t.Fatalf("SourcePath: got %q, want %q", u.SourcePath, "/src")
		}
		if u.TargetPath != "/dst" {
			t.Fatalf("TargetPath: got %q, want %q", u.TargetPath, "/dst")
		}
		// StrmType is not read from form data (only from JSON)
		if u.StrmPrefix != "/prefix" {
			t.Fatalf("StrmPrefix: got %q, want %q", u.StrmPrefix, "/prefix")
		}
		if u.RemoveExtra != "on" {
			t.Fatalf("RemoveExtra: got %q, want %q", u.RemoveExtra, "on")
		}
		if u.EnableEnc != "true" {
			t.Fatalf("EnableEnc: got %q, want %q", u.EnableEnc, "true")
		}
		if u.Enable302 != "yes" {
			t.Fatalf("Enable302: got %q, want %q", u.Enable302, "yes")
		}
		if u.ScheduleMode != "daily" {
			t.Fatalf("ScheduleMode: got %q, want %q", u.ScheduleMode, "daily")
		}
		if u.ScheduleValue != "09:00" {
			t.Fatalf("ScheduleValue: got %q, want %q", u.ScheduleValue, "09:00")
		}
		if u.Enabled != "on" {
			t.Fatalf("Enabled: got %q, want %q", u.Enabled, "on")
		}
	})

	t.Run("JSON body", func(t *testing.T) {
		jsonBody := `{"name":"jsontask","account":"jsonacc","targetPath":"/json","strmType":"302","scheduleMode":"interval","scheduleValue":"60"}`
		req, _ := http.NewRequest("POST", "/", strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")

		var u UpsertTaskRequest
		fillUpsertFromBody(req, &u)

		if u.Name != "jsontask" {
			t.Fatalf("Name: got %q, want %q", u.Name, "jsontask")
		}
		if u.Account != "jsonacc" {
			t.Fatalf("Account: got %q, want %q", u.Account, "jsonacc")
		}
	})

	t.Run("empty body", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "/", strings.NewReader(""))
		var u UpsertTaskRequest
		fillUpsertFromBody(req, &u)
		if u.Name != "" {
			t.Fatalf("Name should be empty, got %q", u.Name)
		}
	})
}
