package handler

import "testing"

func TestProxyClientAddr(t *testing.T) {
	tests := []struct {
		name       string
		embyURL    string
		listenAddr string
		want       string
	}{
		{name: "emby lan ip", embyURL: "http://192.168.1.10:8096", listenAddr: "0.0.0.0:8097", want: "http://192.168.1.10:8097"},
		{name: "emby hostname", embyURL: "http://nas.local:8096", listenAddr: "0.0.0.0:8097", want: "http://nas.local:8097"},
		{name: "specific listen host", embyURL: "", listenAddr: "192.168.1.10:8097", want: "http://192.168.1.10:8097"},
		{name: "empty listen", embyURL: "http://192.168.1.10:8096", listenAddr: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := proxyClientAddr(tt.embyURL, tt.listenAddr); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
