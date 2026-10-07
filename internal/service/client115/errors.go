// Copyright (c) 2026 wabisabi926
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client115

import (
	"errors"
	"fmt"
	"strings"
)

// 115 API 业务错误（HTTP 200 但 state=false）的类型化判定。
//
// 设计目标：判定只依赖结构化 errno（辅以文案兜底），不再让调用方用
// strings.Contains 去猜关键词——文案一变就失效。调用方统一用
// errors.Is(err, ErrCookieExpired) 之类精确判定账号状态。

var (
	// ErrCookieExpired Cookie 已失效/需要重新登录（认证类，确定）。
	ErrCookieExpired = errors.New("115 cookie expired")
	// ErrRateLimited 触发 115 风控/限流（非认证问题，不应标记账号失效）。
	ErrRateLimited = errors.New("115 rate limited")
	// ErrTransient 临时性错误（未知 errno / 服务端异常），不改变账号状态。
	ErrTransient = errors.New("115 transient error")
)

// authErrnos 认证类错误码：需重新登录。
// 99 = 请重新登录（download API 实测）。
var authErrnos = map[int]bool{
	99:  true,
	401: true,
	403: true,
}

// rateLimitErrnos 风控/限流类错误码。
// 宁可漏判也不误判：未收录的 errno 一律视为临时错误，不改写账号状态。
var rateLimitErrnos = map[int]bool{
	405:   true, // 部分接口以 405 表示频率限制
	10008: true, // 操作频率过快
	10019: true, // 请求过于频繁
}

// authMsgPatterns 文案兜底：仅在 errno 无法判定时使用。
var authMsgPatterns = []string{
	"请重新登录",
	"重新登录",
	"未登录",
	"登录过期",
	"cookie",
	"expired",
	"unauthorized",
	"invalid",
	"401",
	"403",
}

// rateLimitMsgPatterns 文案兜底：限流关键词。
var rateLimitMsgPatterns = []string{
	"访问频繁",
	"请求过于频繁",
	"操作过于频繁",
	"频率限制",
	"系统繁忙",
	"请稍后",
	"风控",
	"安全风险",
	"验证码",
	"too many requests",
	"rate limit",
	"429",
}

// APIError 115 API 业务错误。
//
// Kind 为 ErrCookieExpired / ErrRateLimited / ErrTransient 之一，
// 通过 Unwrap 暴露给 errors.Is 判定。
type APIError struct {
	Errno   int
	Message string
	Kind    error
}

func (e *APIError) Error() string {
	if e.Errno != 0 {
		return fmt.Sprintf("115 API error errno=%d: %s", e.Errno, e.Message)
	}
	return "115 API error: " + e.Message
}

// Unwrap 暴露 Kind，使 errors.Is(err, ErrCookieExpired) 可用。
func (e *APIError) Unwrap() error { return e.Kind }

// newAPIError 依据 errno 与文案构造类型化错误。
func newAPIError(errno int, msg string) error {
	if msg == "" {
		msg = "unknown error"
	}
	return &APIError{Errno: errno, Message: msg, Kind: classifyAPIError(errno, msg)}
}

// classifyAPIError 将 errno / 文案映射为类型化错误。
// errno 优先；errno 无法判定时再看文案（限流优先于认证，避免误判）。
func classifyAPIError(errno int, msg string) error {
	if errno != 0 {
		switch {
		case authErrnos[errno]:
			return ErrCookieExpired
		case rateLimitErrnos[errno]:
			return ErrRateLimited
		}
	}
	if containsAny(msg, rateLimitMsgPatterns) {
		return ErrRateLimited
	}
	if containsAny(msg, authMsgPatterns) {
		return ErrCookieExpired
	}
	return ErrTransient
}

// IsAuthError 判断错误是否为认证类（Cookie 失效）。
func IsAuthError(err error) bool {
	return errors.Is(err, ErrCookieExpired)
}

// IsRateLimitError 判断错误是否为风控/限流类。
func IsRateLimitError(err error) bool {
	return errors.Is(err, ErrRateLimited)
}

// ClassifyError 将任意错误归类为 ErrCookieExpired / ErrRateLimited / ErrTransient。
// 已是类型化错误则原样返回；否则按文案兜底归类（无法归类视为 ErrTransient）。
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCookieExpired) || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrTransient) {
		return err
	}
	return &APIError{Message: err.Error(), Kind: classifyAPIError(0, err.Error())}
}

// ErrnoOf 提取错误中的 115 errno（无则返回 0）。
func ErrnoOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Errno
	}
	return 0
}

func containsAny(s string, patterns []string) bool {
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}
