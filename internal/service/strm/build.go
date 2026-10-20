package strm

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// 本文件是 STRM 内容生成的唯一实现。
//
// 历史背景：全量任务（service/task）、生活事件（service/monitor）、清理补建
// （handler/strm_cleanup）此前各写一份「模板分支 + 默认分支」，三份逐渐漂移：
// pickcode 校验松紧不一、前缀兜底策略不同（cleanup 曾硬编码 127.0.0.1）、
// 结尾换行有无不一、file_name 空值处理不同。任何一次规则调整都要改三处，
// 漏改一处就会出现「同一文件两条链路生成不同内容」——这正是 URL 编码
// （空格 '+' vs '%20'）那类 bug 反复出现的根因。
//
// 现在三条链路都调用 BuildContent，规则只在这里定义一次。

var (
	// ErrInvalidPickcode pickcode 非 17 位字母数字。
	// 播放侧 handler/strm.go 同样以 17 位为硬校验（不通过直接 400），
	// 因此生成侧遇到非法 pickcode 必须拒绝，而不是写一个打不开的 STRM。
	ErrInvalidPickcode = errors.New("pickcode 无效(需17位字母数字)")
	// ErrUnusablePrefix 前缀为空 / 无法解析 / 为回环或 0.0.0.0。
	// 这类前缀写进 STRM 后除本机外都播不了，必须由调用方先解析出可用前缀。
	ErrUnusablePrefix = errors.New("STRM 前缀不可用")
)

// ContentInput 生成 STRM 内容的全部输入。
//
// 前缀解析（任务级 > 全局 > 请求 Host > 内网 IP）由调用方完成，因为各链路的
// 候选源不同；本函数只负责判定「是否可用」，不替调用方选。
type ContentInput struct {
	Template  string // 自定义 URL 模板；空则走标准格式
	Prefix    string // 已解析的 STRM 前缀（服务地址）
	Account   string // 115 账号名
	PickCode  string // 17 位 pickcode
	FileName  string // 文件名（含扩展名）；空则不携带 file_name 参数
	CloudPath string // 云端完整路径（相对映射根，'/' 分隔），供 {file_path} 等变量
	FileID    string // 云端 file_id；未知时为空串
	// DisableEncode 为 true 时参数值原样写入（对应设置页「URL 编码」关闭）。
	// 零值 false = 编码（安全默认）。
	DisableEncode bool
}

// BuildContent 生成 .strm 文件内容（含结尾换行）。
//
// 规则顺序（固定，勿调整）：
//  1. pickcode 必须 17 位字母数字
//  2. Template 非空 → 模板渲染（模板可能完全不用 {prefix}，例如指向外部网关，
//     因此模板分支不做前缀校验）
//  3. 默认分支要求前缀可用
//  4. 标准格式：{prefix}/api/strm?account=..&pickcode=..[&file_name=..]
//
// 返回内容始终以单个 "\n" 结尾（模板渲染由 model 层统一规范化）。
func BuildContent(in ContentInput) (string, error) {
	if !strmutil.IsValidPickcode(in.PickCode) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPickcode, in.PickCode)
	}

	// —— 自定义模板优先（不依赖前缀） ——
	if in.Template != "" {
		ext, stem := splitExtStem(in.FileName)
		if rendered := model.RenderStrmUrlTemplate(model.StrmURLTemplateVars{
			Template:      in.Template,
			Prefix:        in.Prefix,
			Account:       in.Account,
			PickCode:      in.PickCode,
			FileName:      in.FileName,
			Ext:           ext,
			Stem:          stem,
			CloudPath:     in.CloudPath,
			FileID:        in.FileID,
			DisableEncode: in.DisableEncode,
		}); rendered != "" {
			return rendered, nil
		}
	}

	prefix := strings.TrimRight(strings.TrimSpace(in.Prefix), "/")
	if IsUnusablePrefix(prefix) {
		return "", fmt.Errorf("%w: %q", ErrUnusablePrefix, in.Prefix)
	}

	account, fileName := in.Account, in.FileName
	if !in.DisableEncode {
		account = strmutil.URLPathEncode(account)
		fileName = strmutil.URLPathEncode(fileName)
	}

	var b strings.Builder
	b.Grow(len(prefix) + len(fileName) + 64)
	b.WriteString(prefix)
	b.WriteString("/api/strm?account=")
	b.WriteString(account)
	b.WriteString("&pickcode=")
	b.WriteString(in.PickCode)
	if fileName != "" {
		b.WriteString("&file_name=")
		b.WriteString(fileName)
	}
	b.WriteString("\n")
	return b.String(), nil
}

// splitExtStem 拆分扩展名与主名，供模板变量 {ext} / {stem} / {file_stem} 使用。
// .iso 的主名保留 ".iso"（原盘按双扩展名识别），与历史行为一致。
func splitExtStem(fileName string) (ext, stem string) {
	ext = strings.ToLower(filepath.Ext(fileName))
	stem = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if strings.EqualFold(ext, ".iso") {
		stem += ".iso"
	}
	return ext, stem
}
