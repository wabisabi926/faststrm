package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalNameMatches 本地落盘名（sanitize 后）与云端原始名的比较规则
func TestLocalNameMatches(t *testing.T) {
	cases := []struct {
		name      string
		localName string
		cloudName string
		want      bool
	}{
		{"普通名相等", "电影.mkv", "电影.mkv", true},
		{"斜杠云端名匹配下划线本地名", "国语次时代_台_粤音轨", "国语次时代/台/粤音轨", true},
		{"冒号云端名匹配全角本地名", "电影：合集", "电影:合集", true},
		{"不相等", "电影A", "电影B", false},
		{"空云端名不匹配", "_", "", false},
		{"点不匹配", "_", ".", false},
		{"斜杠不匹配", "_", "/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := localNameMatches(c.localName, c.cloudName); got != c.want {
				t.Fatalf("localNameMatches(%q, %q) = %v, want %v", c.localName, c.cloudName, got, c.want)
			}
		})
	}
}

// TestFindLocalStrmByFileName_SlashInName 扩展目录 fallback 查找：
// 云端文件夹名含 '/'，本地落盘为 '_'，必须能命中（回归：原实现直接 Join 原始名导致路径错位）
func TestFindLocalStrmByFileName_SlashInName(t *testing.T) {
	root := t.TempDir()
	// 本地落盘形态：'/' 已被替换为 '_'
	localDir := filepath.Join(root, "国语次时代_台_粤音轨")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Monitor{}
	got := m.findLocalStrmByFileName("acc", "国语次时代/台/粤音轨", 0, nil, root)
	if got != localDir {
		t.Fatalf("findLocalStrmByFileName = %q, want %q", got, localDir)
	}

	// 不存在的名字仍返回空
	if got := m.findLocalStrmByFileName("acc", "不存在/的目录", 0, nil, root); got != "" {
		t.Fatalf("不存在时应返回空, got %q", got)
	}
}
