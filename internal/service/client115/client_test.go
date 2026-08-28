// Package client115 unit tests
// Uses http.RoundTripper mock to intercept hardcoded URLs, zero production code changes
package client115

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type mockTrip struct {
	Method     string
	Path       string
	Query      map[string]string
	Body       string
	Status     int
	BodyString string
	Headers    map[string]string
	called     *bool
}

type mockRoundTripper struct {
	trips []*mockTrip
	t     *testing.T
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))

	for _, trip := range m.trips {
		if trip.Method != "" && !strings.EqualFold(trip.Method, req.Method) {
			continue
		}
		if trip.Path != "" && !strings.HasPrefix(req.URL.Path, trip.Path) {
			continue
		}
		if trip.Query != nil {
			ok := true
			for k, v := range trip.Query {
				if req.URL.Query().Get(k) != v {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		if trip.Body != "" && !strings.Contains(string(bodyBytes), trip.Body) {
			continue
		}

		if trip.called != nil {
			*trip.called = true
		}

		status := trip.Status
		if status == 0 {
			status = http.StatusOK
		}
		header := make(http.Header)
		for k, v := range trip.Headers {
			header.Set(k, v)
		}
		if header.Get("Content-Type") == "" {
			header.Set("Content-Type", "application/json")
		}

		return &http.Response{
			StatusCode: status,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(trip.BodyString)),
			Request:    req,
		}, nil
	}

	m.t.Errorf("unexpected request: %s %s body=%q", req.Method, req.URL.String(), string(bodyBytes))
	return nil, fmt.Errorf("no mock trip matched")
}

func newMockClient(t *testing.T, trips []*mockTrip) *Client {
	rt := &mockRoundTripper{trips: trips, t: t}
	c := NewClient("test-ua")
	c.HTTP.Transport = rt
	c.HTTP.CheckRedirect = nil
	return c
}

// === utility tests ===

func TestNewClient_DefaultUA(t *testing.T) {
	c := NewClient("")
	if c.UserAgent != DefaultUA {
		t.Errorf("empty UA should fallback to DefaultUA, got %q", c.UserAgent)
	}
	if c.HTTP.Timeout != 15*time.Second {
		t.Errorf("default timeout should be 15s, got %v", c.HTTP.Timeout)
	}
}

func TestNormalizeAppType(t *testing.T) {
	cases := []struct {
		input   string
		wantApp string
		wantUA  string
	}{
		{"", "alipaymini", ""},
		{"unknown", "alipaymini", ""},
		{"desktop", "web", ""},
		{"windows", "os_windows", ""},
		{"ios", "ios", "UPhone/1.0.0"},
		{"qios", "ios", "OfficePhone/1.0.0"},
		{"ipad", "ios", "UPad/1.0.0"},
		{"android", "115android", ""},
		{"alipaymini", "alipaymini", ""},
		{"wechatmini", "wechatmini", ""},
		{"web", "web", ""},
	}
	for _, tc := range cases {
		app, ua := normalizeAppType(tc.input)
		if app != tc.wantApp || ua != tc.wantUA {
			t.Errorf("normalizeAppType(%q) = (%q,%q), want (%q,%q)",
				tc.input, app, ua, tc.wantApp, tc.wantUA)
		}
	}
}

func TestIsValidClientType(t *testing.T) {
	for _, v := range []string{"ios", "android", "web", "alipaymini", "os_windows"} {
		if !isValidClientType(v) {
			t.Errorf("isValidClientType(%q) should be true", v)
		}
	}
	for _, v := range []string{"", "unknown"} {
		if isValidClientType(v) {
			t.Errorf("isValidClientType(%q) should be false", v)
		}
	}
}

func TestTypeNumberToString(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{1, "create"}, {22, "delete"}, {5, "move"}, {20, "rename"}, {999, "folder-sync"},
	}
	for _, tc := range cases {
		if got := TypeNumberToString(tc.in); got != tc.want {
			t.Errorf("TypeNumberToString(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateCookie(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		r := ValidateCookie("UID=abc; CID=def; SEID=ghi; KID=jkl")
		if !r.Valid {
			t.Errorf("Valid=true, missing %v", r.Missing)
		}
	})
	t.Run("missing", func(t *testing.T) {
		r := ValidateCookie("UID=abc")
		if r.Valid || len(r.Missing) != 3 {
			t.Errorf("Valid=false, missing 3, got %v", r.Missing)
		}
	})
	t.Run("empty", func(t *testing.T) {
		r := ValidateCookie("")
		if r.Valid || len(r.Missing) != 4 {
			t.Errorf("empty cookie should have 4 missing")
		}
	})
	t.Run("ignore-blank", func(t *testing.T) {
		r := ValidateCookie("  UID=a ; ; CID=b ; SEID=c ; KID=d  ")
		if !r.Valid {
			t.Errorf("should ignore blank segments")
		}
	})
}

func TestAPIRateLimiter(t *testing.T) {
	t.Run("TryAcquire-immediate", func(t *testing.T) {
		rl := NewAPIRateLimiter(100 * time.Millisecond)
		if !rl.TryAcquire() {
			t.Error("first TryAcquire should return true")
		}
		callCount, blocked, _ := rl.Stats()
		if callCount != 1 || blocked != 0 {
			t.Errorf("callCount=1 blocked=0, got %d,%d", callCount, blocked)
		}
	})
	t.Run("TryAcquire-too-soon", func(t *testing.T) {
		rl := NewAPIRateLimiter(500 * time.Millisecond)
		rl.TryAcquire()
		if rl.TryAcquire() {
			t.Error("too-soon should return false")
		}
		_, blocked, _ := rl.Stats()
		if blocked != 1 {
			t.Errorf("blocked=1, got %d", blocked)
		}
	})
	t.Run("UpdateInterval", func(t *testing.T) {
		rl := NewAPIRateLimiter(100 * time.Millisecond)
		rl.UpdateInterval(200 * time.Millisecond)
		_, _, interval := rl.Stats()
		if interval != 200*time.Millisecond {
			t.Errorf("interval=200ms, got %v", interval)
		}
		rl.UpdateInterval(-1 * time.Second)
		rl.UpdateInterval(0)
		_, _, interval = rl.Stats()
		if interval != 200*time.Millisecond {
			t.Errorf("negative should be ignored, got %v", interval)
		}
	})
	t.Run("nil-safe", func(t *testing.T) {
		var rl *APIRateLimiter
		rl.Wait()
		_ = rl.TryAcquire()
		rl.UpdateInterval(1 * time.Second)
		callCount, blocked, interval := rl.Stats()
		if callCount != 0 || blocked != 0 || interval != 0 {
			t.Errorf("nil Stats should be all 0")
		}
	})
	t.Run("negative-fallback", func(t *testing.T) {
		rl := NewAPIRateLimiter(-10 * time.Second)
		_, _, interval := rl.Stats()
		if interval != 1*time.Second {
			t.Errorf("negative should fallback to 1s, got %v", interval)
		}
	})
}

// === qrcode stage 1: GetQrcodeToken ===

func TestGetQrcodeToken_Success(t *testing.T) {
	var called bool
	trips := []*mockTrip{{
		Path:       "/api/1.0/115android/1.0/token/",
		BodyString: `{"data":{"uid":"830c3cb15a0","time":1787051208,"sign":"abc123","qrcode":""}}`,
		called:     &called,
	}}
	c := newMockClient(t, trips)

	resp, err := c.GetQrcodeToken("115android")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !called {
		t.Fatal("request not sent")
	}
	if resp.UID != "830c3cb15a0" || resp.Time != "1787051208" || resp.Sign != "abc123" {
		t.Errorf("fields mismatch: UID=%q Time=%q Sign=%q", resp.UID, resp.Time, resp.Sign)
	}
	if !strings.HasPrefix(resp.QrcodeBase64, "data:image/png;base64,") {
		t.Error("QrcodeBase64 should start with data:image/png;base64,")
	}
	if resp.Tips == "" {
		t.Error("Tips should have default value")
	}
}

func TestGetQrcodeToken_MissingFields(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/api/1.0/alipaymini/1.0/token/",
		BodyString: `{"data":{"uid":"","time":"","sign":"","qrcode":""}}`,
	}}
	c := newMockClient(t, trips)

	_, err := c.GetQrcodeToken("unknown-client")
	if err == nil || !strings.Contains(err.Error(), "不完整") {
		t.Errorf("should report incomplete fields, got: %v", err)
	}
}

// === qrcode stage 2: GetQrcodeStatus ===

func TestGetQrcodeStatus_AllStatuses(t *testing.T) {
	cases := []struct {
		name       string
		apiStatus  int
		wantQr     QrCodeStatus
		wantCookie bool
	}{
		{"waiting", 0, QrCodeWaiting, false},
		{"scanned", 1, QrCodeScanned, false},
		{"expired", -1, QrCodeExpired, false},
		{"cancelled", -2, QrCodeCancelled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trips := []*mockTrip{{
				Path:       "/get/status/",
				BodyString: fmt.Sprintf(`{"data":{"status":%d,"msg":""}}`, tc.apiStatus),
			}}
			c := newMockClient(t, trips)
			resp, err := c.GetQrcodeStatus("u1", "t1", "s1", "alipaymini")
			if err != nil {
				t.Fatalf("fail: %v", err)
			}
			if resp.Status != tc.wantQr {
				t.Errorf("Status=%q, want %q", resp.Status, tc.wantQr)
			}
			if (resp.Cookie != "") != tc.wantCookie {
				t.Errorf("Cookie has value=%v", resp.Cookie != "")
			}
		})
	}
}

func TestGetQrcodeStatus_Success_CallsGetQrcodeResult(t *testing.T) {
	var statusCalled, resultCalled bool
	trips := []*mockTrip{
		{
			Path:       "/get/status/",
			BodyString: `{"data":{"status":2,"msg":""}}`,
			called:     &statusCalled,
		},
		{
			Path:       "/app/1.0/alipaymini/1.0/login/qrcode/",
			Status:     http.StatusFound,
			BodyString: `{"data":{"cookie":{"UID":"abc","CID":"def","SEID":"ghi","KID":"jkl"}}}`,
			called:     &resultCalled,
		},
	}
	c := newMockClient(t, trips)

	resp, err := c.GetQrcodeStatus("u1", "t1", "s1", "alipaymini")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !statusCalled || !resultCalled {
		t.Fatalf("both steps should be called")
	}
	if resp.Status != QrCodeSuccess || !strings.Contains(resp.Cookie, "UID=abc") {
		t.Errorf("status=%q cookie=%s", resp.Status, resp.Cookie)
	}
}

func TestGetQrcodeStatus_UnknownKeyInvalid(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/get/status/",
		BodyString: `{"data":{"status":99},"message":"key invalid"}`,
	}}
	c := newMockClient(t, trips)

	resp, err := c.GetQrcodeStatus("u", "t", "s", "alipaymini")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if resp.Status != QrCodeExpired {
		t.Errorf("key invalid should map to expired, got %q", resp.Status)
	}
}

// === qrcode stage 3: GetQrcodeResult ===

func TestGetQrcodeResult_Success(t *testing.T) {
	var called bool
	trips := []*mockTrip{{
		Path:       "/app/1.0/ios/1.0/login/qrcode/",
		Status:     http.StatusFound,
		BodyString: `{"data":{"cookie":{"UID":"abc","CID":"def","SEID":"ghi","KID":"jkl"}}}`,
		called:     &called,
	}}
	c := newMockClient(t, trips)

	cookie, err := c.GetQrcodeResult("u1", "ios")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !called {
		t.Fatal("request not sent")
	}
	if !strings.Contains(cookie, "UID=abc") {
		t.Errorf("cookie concat error, got: %s", cookie)
	}
}

func TestGetQrcodeResult_EmptyCookie(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/app/1.0/alipaymini/1.0/login/qrcode/",
		Status:     http.StatusFound,
		BodyString: `{"data":{"cookie":{}}}`,
	}}
	c := newMockClient(t, trips)

	if _, err := c.GetQrcodeResult("u1", "alipaymini"); err == nil {
		t.Error("empty cookie should error")
	}
}

// === FsFiles ===

func TestFsFiles_DirectoryDetection(t *testing.T) {
	trips := []*mockTrip{{
		Path: "/files",
		BodyString: `{"state":true,"count":2,"data":[
			{"pc":"p1","fid":12345,"cid":"0","n":"movie.mp4","s":1048576,"fc":0},
			{"pc":"p2","fid":"","cid":"3491751436709005103","n":"movie folder","s":0,"fc":5}
		]}`,
	}}
	c := newMockClient(t, trips)

	resp, err := c.FsFiles(context.Background(), "0", 1000, 0, "UID=x;CID=y")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if resp.Data[0].IsDir {
		t.Error("movie.mp4 has fid -> file (IsDir=false)")
	}
	if !resp.Data[1].IsDir {
		t.Error("movie folder no fid but has valid cid -> dir (IsDir=true)")
	}
}

func TestFsFiles_EmptyCookie(t *testing.T) {
	c := NewClient("test-ua")
	if _, err := c.FsFiles(context.Background(), "0", 100, 0, ""); err == nil {
		t.Error("empty cookie should error")
	}
}

// === FsDirGetID ===

func TestFsDirGetID_NewFormat(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/files/getid",
		BodyString: `{"state":true,"id":"3491751436709005103"}`,
	}}
	c := newMockClient(t, trips)

	id, err := c.FsDirGetID(context.Background(), "/films", "UID=x;CID=y")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if id != 3491751436709005103 {
		t.Errorf("id=3491751436709005103, got %d", id)
	}
}

func TestFsDirGetID_LegacyFormat(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/files/getid",
		BodyString: `{"state":true,"data":{"id":"12345"}}`,
	}}
	c := newMockClient(t, trips)

	id, err := c.FsDirGetID(context.Background(), "/test", "UID=x;CID=y")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if id != 12345 {
		t.Errorf("id=12345, got %d", id)
	}
}

func TestFsDirGetID_StateFalse(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/files/getid",
		BodyString: `{"state":false,"errmsg":"dir not found"}`,
	}}
	c := newMockClient(t, trips)

	if _, err := c.FsDirGetID(context.Background(), "/nope", "UID=x;CID=y"); err == nil {
		t.Error("state=false should error")
	}
}

// === GetDownloadUrlWebFull ===

func TestGetDownloadUrlWebFull_StateFalse(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/android/2.0/ufile/download",
		BodyString: `{"state":false,"error":"pickcode expired"}`,
	}}
	c := newMockClient(t, trips)

	if _, err := c.GetDownloadUrlWebFull(context.Background(), "X", "UID=x;CID=y", ""); err == nil {
		t.Error("state=false should error")
	}
}

func TestGetDownloadUrlWebFull_EmptyParams(t *testing.T) {
	c := NewClient("test-ua")
	ctx := context.Background()
	if _, err := c.GetDownloadUrlWebFull(ctx, "", "cookie", ""); err == nil {
		t.Error("empty pickcode should error")
	}
	if _, err := c.GetDownloadUrlWebFull(ctx, "pc", "", ""); err == nil {
		t.Error("empty cookie should error")
	}
}

// === LifeClient ===

func TestNewLifeClient(t *testing.T) {
	lc := NewLifeClient("UID=abc;CID=def;SEID=ghi;KID=jkl")
	if lc == nil {
		t.Fatal("not nil")
	}
	if lc.Cookie() != "UID=abc;CID=def;SEID=ghi;KID=jkl" {
		t.Errorf("cookie access error")
	}
	if lc.FsClient() == nil {
		t.Error("FsClient not nil")
	}
}

func TestLifeClient_FsFiles(t *testing.T) {
	trips := []*mockTrip{{
		Path: "/files",
		BodyString: `{"state":true,"count":1,"data":[
			{"pc":"p1","fid":999,"cid":"0","n":"movie.mkv","s":1234567,"fc":0}
		]}`,
	}}
	lc := NewLifeClient("UID=x;CID=y;SEID=z;KID=w")
	lc.FsClient().HTTP.Transport = &mockRoundTripper{trips: trips, t: t}

	resp, err := lc.FsFiles(context.Background(), "0", 100, 0)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if resp.Data[0].Name != "movie.mkv" {
		t.Errorf("Name=movie.mkv, got %s", resp.Data[0].Name)
	}
}

func TestLifeEventRaw_ToItem(t *testing.T) {
	raw := lifeEventRaw{
		ID: "100", Type: 2, FileName: "test.mp4", FileID: "999", ParentID: "888",
		PickCode: "abc", FileSize: float64(1024), UpdateTime: float64(1700000000),
	}
	item := raw.toLifeEventItem()
	if item.ID != "100" {
		t.Errorf("ID=100, got %q", item.ID)
	}
	if item.BehaviorType != "upload_file" {
		t.Errorf("type=2 should infer create, got %q", item.BehaviorType)
	}
}

// === httptest.Server e2e ===

func TestRoundTripper_ForwardingToHttptestServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"uid":"test123","time":"1000","sign":"sign1","qrcode":""}}`))
	}))
	defer server.Close()

	rt := &forwardingRoundTripper{target: server.URL}
	c := NewClient("test-ua")
	c.HTTP.Transport = rt

	resp, err := c.GetQrcodeToken("ios")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if resp.UID != "test123" {
		t.Errorf("UID=test123, got %q", resp.UID)
	}
}

type forwardingRoundTripper struct {
	target string
}

func (f *forwardingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := url.Parse(f.target)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.Host = targetURL.Host
	return http.DefaultTransport.RoundTrip(req)
}
