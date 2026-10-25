package embyproxy

import (
	"net/http"
	"testing"
)

func TestStreamClientHasConnectTimeouts(t *testing.T) {
	p, err := New("http://emby.invalid")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr, ok := p.streamClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", p.streamClient.Transport)
	}
	if tr.DialContext == nil {
		t.Error("DialContext 未设置，连接阶段没有超时")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Error("TLSHandshakeTimeout 未设置")
	}
	if tr.MaxConnsPerHost <= 0 {
		t.Error("MaxConnsPerHost 未设置")
	}
}
