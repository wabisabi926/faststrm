package monitor

import (
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

// TestSingleFileParentDir 验证单文件事件的父目录回收逻辑（对齐参考项目取 file_path.parent）。
// 确保 电影/叶问.iso 生成 电影本地/叶问.iso.strm，而非多拼一层同名目录 电影本地/叶问.iso/叶问.iso.strm。
func TestSingleFileParentDir(t *testing.T) {
	tests := []struct {
		name       string
		m          *pathMapping
		fileName   string
		wantParent string
		wantStrm   string
	}{
		{
			name:       "nil 返回空",
			m:          nil,
			fileName:   "叶问.iso",
			wantParent: "",
		},
		{
			name:       "精确匹配映射根：不回收",
			m:          &pathMapping{cloudPath: "电影", localPath: "D:/Strm/电影", relativePath: ""},
			fileName:   "叶问.iso",
			wantParent: "D:/Strm/电影",
			wantStrm:   "D:/Strm/电影/叶问.iso.strm",
		},
		{
			name:       "前缀匹配-裸文件名：回收一级",
			m:          &pathMapping{cloudPath: "电影", localPath: "D:/Strm/电影/叶问.iso", relativePath: "叶问.iso"},
			fileName:   "叶问.iso",
			wantParent: "D:/Strm/电影",
			wantStrm:   "D:/Strm/电影/叶问.iso.strm",
		},
		{
			name:       "前缀匹配-多级父目录：只回收文件级",
			m:          &pathMapping{cloudPath: "电影", localPath: "D:/Strm/电影/动作/叶问.iso", relativePath: "动作/叶问.iso"},
			fileName:   "叶问.iso",
			wantParent: "D:/Strm/电影/动作",
			wantStrm:   "D:/Strm/电影/动作/叶问.iso.strm",
		},
		{
			name:       "多级父目录下的 mkv：只回收文件级",
			m:          &pathMapping{cloudPath: "电影", localPath: "D:/Strm/电影/动作/叶问.mkv", relativePath: "动作/叶问.mkv"},
			fileName:   "叶问.mkv",
			wantParent: "D:/Strm/电影/动作",
			wantStrm:   "D:/Strm/电影/动作/叶问.strm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := singleFileParentDir(tt.m)
			if filepath.FromSlash(parent) != filepath.FromSlash(tt.wantParent) {
				t.Fatalf("singleFileParentDir() = %q, want %q", filepath.FromSlash(parent), filepath.FromSlash(tt.wantParent))
			}
			if tt.wantStrm != "" {
				got := filepath.Join(parent, getStrmFileName(tt.fileName))
				if want := filepath.FromSlash(tt.wantStrm); got != want {
					t.Fatalf("strm path = %q, want %q（是否存在多拼一层同名目录）", got, want)
				}
			}
		})
	}
}

// TestMatchPathMappingPrefixSingleFile 验证单文件事件前缀匹配产生的 localPath 末段即文件名
// （即 bug 前提），再经 singleFileParentDir 回收后落在正确父目录。
func TestMatchPathMappingPrefixSingleFile(t *testing.T) {
	mappings := []model.MonitorPathMapping{
		{CloudPath: "电影", LocalPath: "D:/Strm/电影", Account: ""},
	}
	// 单文件电影/叶问.iso → 前缀匹配，localPath 末段是文件名
	mapping := matchPathMapping("电影/叶问.iso", mappings, "")
	if mapping == nil {
		t.Fatal("matchPathMapping returned nil")
	}
	if got := filepath.Join(singleFileParentDir(mapping), getStrmFileName("叶问.iso")); got != filepath.FromSlash("D:/Strm/电影/叶问.iso.strm") {
		t.Fatalf("strm path = %q, want %q", got, filepath.FromSlash("D:/Strm/电影/叶问.iso.strm"))
	}
}
