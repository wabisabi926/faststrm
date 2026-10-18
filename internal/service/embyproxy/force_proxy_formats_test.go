package embyproxy

import (
	"testing"
)

// resetForceProxyFormats 恢复默认格式列表，避免用例间相互污染
func resetForceProxyFormats(t *testing.T) {
	t.Helper()
	SetForceProxyFormats(nil)
	t.Cleanup(func() { SetForceProxyFormats(nil) })
}

// TestIsSeekRequiredFormat_Default 默认列表行为保持不变（本轮只做「可配置化」）
func TestIsSeekRequiredFormat_Default(t *testing.T) {
	resetForceProxyFormats(t)

	// 容器名匹配
	for _, c := range []string{"iso", "ISO", "bdmv", "m2ts", "ts", "vob"} {
		if !isSeekRequiredFormat(c, "") {
			t.Fatalf("container %q should require proxy by default", c)
		}
	}
	// 扩展名匹配
	for _, n := range []string{"movie.iso", "00000.m2ts", "record.ts", "VTS_01_1.VOB"} {
		if !isSeekRequiredFormat("", n) {
			t.Fatalf("name %q should require proxy by default", n)
		}
	}
	// 普通格式不应命中
	for _, n := range []string{"movie.mkv", "movie.mp4", "movie.avi"} {
		if isSeekRequiredFormat("", n) {
			t.Fatalf("name %q should NOT require proxy", n)
		}
	}
	if isSeekRequiredFormat("mkv", "movie.mkv") {
		t.Fatal("container mkv should not require proxy")
	}
}

// TestSetForceProxyFormats_RemoveTS 把 .ts 移除后，TS 文件应走 302 而非代理
func TestSetForceProxyFormats_RemoveTS(t *testing.T) {
	resetForceProxyFormats(t)

	// 只保留原盘类，去掉 ts
	SetForceProxyFormats([]string{"iso", "bdmv", "m2ts"})

	if isSeekRequiredFormat("", "record.ts") {
		t.Fatal("ts should NOT require proxy after being removed from config")
	}
	if isSeekRequiredFormat("ts", "") {
		t.Fatal("container ts should NOT require proxy after being removed")
	}
	// 原盘仍应命中
	if !isSeekRequiredFormat("", "movie.iso") {
		t.Fatal("iso should still require proxy")
	}
	if !isSeekRequiredFormat("", "00000.m2ts") {
		t.Fatal("m2ts should still require proxy")
	}
}

// TestSetForceProxyFormats_Normalization 大小写、点号、空白都应被规范化
func TestSetForceProxyFormats_Normalization(t *testing.T) {
	resetForceProxyFormats(t)

	SetForceProxyFormats([]string{" .ISO ", "Ts", ""})

	if !isSeekRequiredFormat("iso", "") {
		t.Fatal("container iso should match after normalization")
	}
	if !isSeekRequiredFormat("", "a.iso") {
		t.Fatal("ext .iso should match after normalization")
	}
	if !isSeekRequiredFormat("", "b.TS") {
		t.Fatal("ext .TS should match case-insensitively")
	}
	// 空字符串条目不应把空扩展名放进来
	if isSeekRequiredFormat("", "noext") {
		t.Fatal("empty entry should not match extension-less name")
	}
}

// TestSetForceProxyFormats_EmptyFallsBackToDefault 空列表回落默认
func TestSetForceProxyFormats_EmptyFallsBackToDefault(t *testing.T) {
	resetForceProxyFormats(t)

	SetForceProxyFormats([]string{})
	if !isSeekRequiredFormat("", "movie.iso") {
		t.Fatal("empty config should fall back to default list")
	}
}
