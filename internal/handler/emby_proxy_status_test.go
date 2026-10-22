package handler

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/embyproxy"
	"github.com/wabisabi926/faststrm/internal/service/store"
)

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

func TestSameStringSet(t *testing.T) {
	if !sameStringSet([]string{"iso", "bdmv"}, []string{"bdmv", "iso"}) {
		t.Fatal("order-insensitive sets should match")
	}
	if sameStringSet([]string{"iso", "m2ts"}, []string{"iso", "bdmv"}) {
		t.Fatal("different sets must not match")
	}
}

// TestBuildProxyStatus_FormatSource 锁定「按格式代理」的三态语义：
// 空列表 = 明确关闭（source=off / disableSeekFormatProxy=true），
// 与内置默认一致时标 default，其余标 custom。
// 前端 ConnectionSection 依赖 disableSeekFormatProxy 显示「按格式代理已关闭」。
func TestBuildProxyStatus_FormatSource(t *testing.T) {
	cases := []struct {
		name       string
		formats    []string
		wantSource string
		wantOff    bool
	}{
		{name: "空列表即关闭", formats: []string{}, wantSource: "off", wantOff: true},
		{name: "自定义列表", formats: []string{"iso", "mkv"}, wantSource: "custom", wantOff: false},
		{name: "等于内置默认", formats: append([]string{}, model.DefaultForceProxyFormats...), wantSource: "default", wantOff: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			settingsStore := store.NewSettingsStore("proxy_status_salt_32b_padding_____", t.TempDir())
			s := newContractSettings()
			// 已完成迁移：此后空列表表示用户主动关闭，读配置不得回填默认值
			s.FormatProxyDefaultsV1 = true
			s.Strm.ForceProxyFormats = c.formats
			if err := settingsStore.SaveSettings(s); err != nil {
				t.Fatal(err)
			}

			got := buildProxyStatus(EmbyDeps{
				SettingsStore:    settingsStore,
				EmbyProxyManager: embyproxy.NewManager(),
			})
			if got["forceProxyFormatsSource"] != c.wantSource {
				t.Errorf("source = %v, want %v", got["forceProxyFormatsSource"], c.wantSource)
			}
			if got["disableSeekFormatProxy"] != c.wantOff {
				t.Errorf("disableSeekFormatProxy = %v, want %v", got["disableSeekFormatProxy"], c.wantOff)
			}
			formats, _ := got["forceProxyFormats"].([]string)
			if len(formats) != len(c.formats) {
				t.Errorf("formats = %v, want %v", formats, c.formats)
			}
		})
	}
}
