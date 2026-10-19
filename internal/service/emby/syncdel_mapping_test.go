package emby

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// MapEmbyPathToCloud 必须把本地落盘名还原成 DB 里的云端形态：
// 本地 '：'（全角）← 云端 ':'，不还原则含冒号的片名删不掉 DB 记录。
func TestMapEmbyPathToCloud_RestoresFullWidthColon(t *testing.T) {
	mapping := model.SyncDeletePathMapping{
		EmbyPath:  "/media/movies",
		CloudPath: "/电影",
	}
	cases := map[string]string{
		"/media/movies/复仇者联盟：终局之战 (2019)/复仇者联盟：终局之战.mkv": "/电影/复仇者联盟:终局之战 (2019)/复仇者联盟:终局之战.mkv",
		"/media/movies/普通电影/普通电影.mkv":                    "/电影/普通电影/普通电影.mkv",
		"/media/movies":  "/电影",
		"/other/xxx.mkv": "",
	}
	for in, want := range cases {
		if got := MapEmbyPathToCloud(in, mapping); got != want {
			t.Errorf("MapEmbyPathToCloud(%q) = %q, want %q", in, got, want)
		}
	}
}
