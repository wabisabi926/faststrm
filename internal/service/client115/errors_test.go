package client115

import (
	"context"
	"errors"
	"testing"
)

// TestGetDownloadUrlWebFull_StateFalseArrayData 回归：
// 115 错误响应形如 {"state":false,"errno":99,"error":"请重新登录","data":[]}，
// 其中 data 是数组。旧实现用 Data string 解析会先报
// "cannot unmarshal array into ... field .data of type string"，
// 把真正的 errno=99 盖掉。这里必须拿到认证类错误且 errno=99。
func TestGetDownloadUrlWebFull_StateFalseArrayData(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/android/2.0/ufile/download",
		BodyString: `{"state":false,"errno":99,"error":"请重新登录","data":[]}`,
	}}
	c := newMockClient(t, trips)

	_, err := c.GetDownloadUrlWebFull(context.Background(), "X", "UID=x;CID=y", "")
	if err == nil {
		t.Fatal("state=false 应返回错误")
	}
	if !IsAuthError(err) {
		t.Errorf("应判定为认证类错误，got %v", err)
	}
	if got := ErrnoOf(err); got != 99 {
		t.Errorf("errno 应为 99，got %d (err=%v)", got, err)
	}
}

// TestFsFiles_StateFalseAuthError FsFiles 在 state=false 且带 errno/文案时
// 必须返回类型化错误，而不是被当成「空目录」继续往下扫（漏扫根源之一）。
func TestFsFiles_StateFalseAuthError(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/files",
		BodyString: `{"state":false,"errno":99,"errmsg":"请重新登录","data":[]}`,
	}}
	c := newMockClient(t, trips)

	_, err := c.FsFiles(context.Background(), "0", 10, 0, "UID=x;CID=y")
	if err == nil {
		t.Fatal("state=false 应返回错误")
	}
	if !IsAuthError(err) {
		t.Errorf("应判定为认证类错误，got %v", err)
	}
}

// state=false 但没有 errno/文案时也必须是错误，不能当成空目录。
func TestFsFiles_StateFalseWithoutMessageFails(t *testing.T) {
	trips := []*mockTrip{{
		Path:       "/files",
		BodyString: `{"state":false,"data":[]}`,
	}}
	c := newMockClient(t, trips)

	if _, err := c.FsFiles(context.Background(), "0", 10, 0, "UID=x;CID=y"); err == nil {
		t.Fatal("state=false 无错误文案也必须返回错误")
	}
	if _, err := c.FsFilesAll(context.Background(), "0", 10, 0, "UID=x;CID=y"); err == nil {
		t.Fatal("FsFilesAll state=false 无错误文案也必须返回错误")
	}
}

func TestClassifyAPIError(t *testing.T) {
	cases := []struct {
		name  string
		errno int
		msg   string
		want  error
	}{
		{"errno99登录失效", 99, "", ErrCookieExpired},
		{"errno401", 401, "unauthorized", ErrCookieExpired},
		{"errno10008限流", 10008, "", ErrRateLimited},
		{"errno405限流", 405, "", ErrRateLimited},
		{"文案请重新登录", 0, "请重新登录", ErrCookieExpired},
		{"文案访问频繁", 0, "访问频繁，请稍后", ErrRateLimited},
		{"文案430限流", 0, "HTTP 430", ErrRateLimited},
		{"文案key invalid", 0, "key invalid", ErrCookieExpired},
		// 泛化的 invalid / 403 不能误判为认证失败
		{"文案invalid request", 0, "invalid request", ErrTransient},
		{"文案HTTP403", 0, "HTTP 403: forbidden", ErrTransient},
		{"未知错误", 0, "something weird", ErrTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyAPIError(tc.errno, tc.msg)
			if !errors.Is(got, tc.want) {
				t.Errorf("classifyAPIError(%d,%q) = %v, want %v", tc.errno, tc.msg, got, tc.want)
			}
		})
	}
}

func TestErrnoOfAndPredicates(t *testing.T) {
	auth := newAPIError(99, "请重新登录")
	if ErrnoOf(auth) != 99 {
		t.Errorf("ErrnoOf = %d, want 99", ErrnoOf(auth))
	}
	if !IsAuthError(auth) || IsRateLimitError(auth) {
		t.Error("errno=99 应为认证类且非限流类")
	}

	rl := newAPIError(10008, "")
	if IsAuthError(rl) || !IsRateLimitError(rl) {
		t.Error("errno=10008 应为限流类且非认证类")
	}

	if ErrnoOf(errors.New("plain")) != 0 {
		t.Error("普通错误 ErrnoOf 应为 0")
	}
	if got := ClassifyError(nil); got != nil {
		t.Errorf("ClassifyError(nil) 应为 nil，got %v", got)
	}
	// 已类型化错误原样返回
	if !errors.Is(ClassifyError(auth), ErrCookieExpired) {
		t.Error("已类型化错误应保持分类")
	}
}

func TestProbeAccount_InvalidCookie(t *testing.T) {
	// 空 cookie 与缺字段 cookie 均为确定性认证失败，无需网络
	if err := ProbeAccount(context.Background(), ""); !IsAuthError(err) {
		t.Errorf("空 cookie 应判定失效，got %v", err)
	}
	if err := ProbeAccount(context.Background(), "UID=x"); !IsAuthError(err) {
		t.Errorf("缺字段 cookie 应判定失效，got %v", err)
	}
}

func TestClassifyError_TLSCertExpiredIsTransient(t *testing.T) {
	err := errors.New("x509: certificate has expired or is not yet valid")
	if IsAuthError(ClassifyError(err)) {
		t.Fatal("TLS certificate expiry must not be classified as cookie expired")
	}
	if !errors.Is(ClassifyError(err), ErrTransient) {
		t.Fatal("TLS error should be transient")
	}
}

func TestNewHTTPStatusError_ClassifiesRateLimitAndAuth(t *testing.T) {
	for _, status := range []int{429, 430, 599} {
		e := newHTTPStatusError(status, "blocked")
		if !IsRateLimitError(e) {
			t.Fatalf("HTTP %d should be rate limited", status)
		}
	}
	if !IsAuthError(newHTTPStatusError(401, "unauthorized")) {
		t.Fatal("HTTP 401 should be auth error")
	}
}
