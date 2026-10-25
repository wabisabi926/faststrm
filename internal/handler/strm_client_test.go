package handler

import (
	"net/http"
	"testing"
)

// CDN 域名解析到黑洞 IP 时必须有连接阶段超时，不能挂到 OS TCP 超时。
func TestProxyHTTPClientHasConnectTimeouts(t *testing.T) {
	client := getProxyHTTPClient()
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if tr.DialContext == nil {
		t.Error("DialContext 未设置，连接阶段没有超时")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Error("TLSHandshakeTimeout 未设置")
	}
	if tr.MaxConnsPerHost <= 0 {
		t.Error("MaxConnsPerHost 未设置，单账号可能打满 CDN 连接")
	}
}
