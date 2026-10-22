// Package strmimport 把外部 STRM 方言解析并重写为 FastStrm 标准 URL。
//
// 迁移只读本地 .strm 文件，不访问来源 URL；LitePan 的 file_id 需要由调用方
// 通过 115 API 反查 pickcode，p115/FastStrm 的 pickcode 可直接复用。
package strmimport

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/strm"
)

// Source STRM URL 方言。
type Source string

const (
	SourceFastStrm       Source = "faststrm"
	SourceFastStrmLegacy Source = "faststrm_legacy"
	SourceP115           Source = "p115"
	SourceP115Path       Source = "p115_path"
	SourceP115Share      Source = "p115_share"
	SourceLitePan        Source = "litepan"
	SourceLitePanPath    Source = "litepan_path"
	SourceUnknown        Source = "unknown"
)

// Ref 从 STRM 内容解析出的引用信息。
type Ref struct {
	Source       Source
	RawURL       string
	Account      string // FastStrm 账号名，或 LitePan 的数字 accountID
	PickCode     string
	FileID       string
	FileName     string
	RootID       string
	RelativePath string
	ShareCode    string
	ReceiveCode  string
}

// IsFastStrm 判断是否为 FastStrm 自己的两种端点。
func (s Source) IsFastStrm() bool {
	return s == SourceFastStrm || s == SourceFastStrmLegacy
}

// Supported 当前能否原地重写。
func (r Ref) Supported() bool {
	switch r.Source {
	case SourceFastStrm, SourceFastStrmLegacy, SourceP115:
		return r.PickCode != ""
	case SourceLitePan:
		return r.FileID != ""
	default:
		return false
	}
}

// UnsupportedReason 返回不可迁移原因。
func (r Ref) UnsupportedReason() string {
	switch r.Source {
	case SourceP115Share:
		return "p115 分享链接需要先转存，无法直接迁移"
	case SourceP115Path:
		return "p115 路径路由暂不支持，请先改为 pickcode 形式"
	case SourceLitePanPath:
		return "LitePan 路径路由暂不支持，请先改为 file_id 形式"
	case SourceUnknown:
		return "未识别的 STRM URL"
	}
	if !r.Supported() {
		return "缺少 pickcode/file_id"
	}
	return ""
}

var pickcodeRE = regexp.MustCompile(`^[A-Za-z0-9]{17}$`)

// ErrEmptyContent STRM 文件为空。
var ErrEmptyContent = errors.New("strm content is empty")

// ParseContent 解析 STRM 文件内容（只取第一条非空行）。
func ParseContent(content string) (Ref, error) {
	raw := firstNonEmptyLine(content)
	if raw == "" {
		return Ref{}, ErrEmptyContent
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Ref{Source: SourceUnknown, RawURL: raw}, nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Ref{Source: SourceUnknown, RawURL: raw}, nil
	}

	if ref, ok, err := parseFastStrm(u, raw); ok || err != nil {
		return ref, err
	}
	if ref, ok, err := parseP115(u, raw); ok || err != nil {
		return ref, err
	}
	if ref, ok, err := parseLitePan(u, raw); ok || err != nil {
		return ref, err
	}
	return Ref{Source: SourceUnknown, RawURL: raw}, nil
}

func parseFastStrm(u *url.URL, raw string) (Ref, bool, error) {
	p := strings.TrimRight(u.Path, "/")
	if p != "/api/strm" && p != "/api/fs/get" {
		return Ref{}, false, nil
	}
	q := u.Query()
	pickcode := strings.TrimSpace(q.Get("pickcode"))
	if pickcode == "" {
		return Ref{}, true, fmt.Errorf("FastStrm URL 缺少 pickcode: %s", raw)
	}
	if !pickcodeRE.MatchString(pickcode) {
		return Ref{}, true, fmt.Errorf("FastStrm pickcode 非法: %q", pickcode)
	}
	source := SourceFastStrm
	if p == "/api/fs/get" {
		source = SourceFastStrmLegacy
	}
	return Ref{
		Source:   source,
		RawURL:   raw,
		Account:  strings.TrimSpace(q.Get("account")),
		PickCode: pickcode,
		FileName: strm.ResolveFileName(q.Get("file_name")),
	}, true, nil
}

func parseP115(u *url.URL, raw string) (Ref, bool, error) {
	lower := strings.ToLower(u.Path)
	idx := strings.Index(lower, "/redirect_url")
	if idx < 0 {
		return Ref{}, false, nil
	}
	tail := strings.Trim(lower[idx+len("/redirect_url"):], "/")
	q := u.Query()
	if tail != "" {
		return Ref{Source: SourceP115Path, RawURL: raw, RelativePath: strings.TrimPrefix(u.Path[idx+len("/redirect_url"):], "/")}, true, nil
	}
	if q.Get("share_code") != "" {
		return Ref{
			Source:      SourceP115Share,
			RawURL:      raw,
			ShareCode:   q.Get("share_code"),
			ReceiveCode: q.Get("receive_code"),
			FileID:      strings.TrimSpace(q.Get("id")),
			FileName:    strm.ResolveFileName(q.Get("file_name")),
		}, true, nil
	}
	pickcode := strings.TrimSpace(q.Get("pickcode"))
	if pickcode == "" {
		return Ref{}, true, fmt.Errorf("p115 URL 缺少 pickcode: %s", raw)
	}
	if !pickcodeRE.MatchString(pickcode) {
		return Ref{}, true, fmt.Errorf("p115 pickcode 非法: %q", pickcode)
	}
	return Ref{
		Source:   SourceP115,
		RawURL:   raw,
		PickCode: pickcode,
		FileName: strm.ResolveFileName(q.Get("file_name")),
	}, true, nil
}

var (
	litePanPlayRE = regexp.MustCompile(`^/api/strm/play/([0-9]+)/([^/]+)/t/[^/]+/n/([^/]+)(?:/s/[^/]+)?/?$`)
	litePanPathRE = regexp.MustCompile(`^/api/strm/path/([0-9]+)/([^/]+)/([^/]+)/t/[^/]+/n/([^/]+)(?:/s/[^/]+)?/?$`)
)

func parseLitePan(u *url.URL, raw string) (Ref, bool, error) {
	if m := litePanPathRE.FindStringSubmatch(u.Path); m != nil {
		relativePath, _ := decodePathKey(m[3])
		return Ref{
			Source:       SourceLitePanPath,
			RawURL:       raw,
			Account:      m[1],
			RootID:       m[2],
			RelativePath: relativePath,
			FileName:     unescapeName(m[4]),
		}, true, nil
	}
	m := litePanPlayRE.FindStringSubmatch(u.Path)
	if m == nil {
		return Ref{}, false, nil
	}
	fileID, err := decodePathKey(m[2])
	if err != nil {
		return Ref{}, true, fmt.Errorf("LitePan file_key 解码失败: %w", err)
	}
	return Ref{
		Source:   SourceLitePan,
		RawURL:   raw,
		Account:  m[1],
		FileID:   fileID,
		FileName: unescapeName(m[3]),
	}, true, nil
}

func decodePathKey(key string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unescapeName(name string) string {
	if decoded, err := url.PathUnescape(name); err == nil {
		return decoded
	}
	return name
}

func firstNonEmptyLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if s := strings.TrimSpace(strings.TrimSuffix(line, "\r")); s != "" {
			return s
		}
	}
	return ""
}
