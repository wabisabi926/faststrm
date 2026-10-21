package handler

import (
	"path/filepath"
	"strings"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// strmGenFilter 复刻「生成侧」的媒体筛选规则，供清理模块在扫描/补生成时复用。
//
// 背景：清理扫描此前只跳过关联文件（图片/字幕/nfo），没有套用 STRM 扩展名列表、
// 文件名黑名单、最小文件大小与 BDMV/STREAM 判定。后果是云端任何 .txt/.zip/.torrent、
// trailer/sample、以及蓝光原盘里的 m2ts 都会被列为「缺失 STRM」，用户点一次
// 「补生成」就会在 Emby 里多出一堆无法识别的条目。
//
// 注意：本过滤器**只作用于「缺失 STRM」方向**（决定该不该补建），不参与
// cloudStrmSet 的构造——否则用户把某个关键词加进黑名单后，已有的 STRM 会立刻
// 被判为「残留」并被清理，属于意料之外的破坏性行为。
type strmGenFilter struct {
	exts        map[string]struct{}
	blacklist   []string
	minFileSize int64
}

// newStrmGenFilter 从设置构造过滤器；settings 为 nil 时退化为内置默认扩展名。
func newStrmGenFilter(settings *model.Settings) strmGenFilter {
	f := strmGenFilter{exts: make(map[string]struct{})}
	if settings != nil {
		for _, ext := range settings.StrmExtensions {
			if e := strmutil.NormalizeExt(strings.ToLower(strings.TrimSpace(ext))); e != "" && e != "." {
				f.exts[e] = struct{}{}
			}
		}
		f.blacklist = settings.Download.StrmGenerateBlacklist
		f.minFileSize = int64(settings.Download.MinFileSize)
	}
	if len(f.exts) == 0 {
		for _, ext := range model.DefaultStrmExtensions {
			f.exts[strmutil.NormalizeExt(ext)] = struct{}{}
		}
	}
	return f
}

// allow 判断某个云端文件是否应该生成 STRM，返回 (拒绝原因, 是否允许)。
// 判定顺序与生成侧一致：BDMV 结构 → 扩展名列表 → 黑名单/最小体积。
func (f strmGenFilter) allow(cloudRelPath string, size int64) (string, bool) {
	if model.IsBdmvStreamPath(cloudRelPath) {
		return "BDMV/STREAM 原盘视频流", false
	}
	ext := strmutil.NormalizeExt(strings.ToLower(filepath.Ext(cloudRelPath)))
	if _, ok := f.exts[ext]; !ok {
		return "扩展名不在 STRM 生成列表", false
	}
	if reason, pass := strmutil.ShouldGenerateStrm(filepath.Base(cloudRelPath), size, f.minFileSize, f.blacklist); !pass {
		return reason, false
	}
	return "", true
}

// strmScanRules 扫描阶段需要的全部规则：媒体筛选 + STRM 文件名模板。
// 打包成一个结构体传给 scanMapping，避免为「模板」再单独加一个参数。
type strmScanRules struct {
	gen              strmGenFilter
	filenameTemplate string
}

func newStrmScanRules(settings *model.Settings) strmScanRules {
	r := strmScanRules{gen: newStrmGenFilter(settings)}
	if settings != nil {
		r.filenameTemplate = settings.Strm.StrmFilenameTemplate
	}
	return r
}
