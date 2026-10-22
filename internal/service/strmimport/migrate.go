package strmimport

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// Resolver 把 115 file_id 解析成 pickcode（LitePan URL 需要）。
type Resolver interface {
	ResolvePickCode(ctx context.Context, account, fileID string) (string, error)
}

// RegisterInput 迁移后的托管登记信息。
type RegisterInput struct {
	LocalPath string
	Account   string
	CloudPath string
	PickCode  string
	FileID    string
	Content   string
}

// Options 迁移参数。
type Options struct {
	Root               string
	From               Source // 空/auto 表示自动识别
	TargetAccount      string
	AccountMap         map[string]string // LitePan accountID / 来源账号名 → FastStrm 账号
	Prefix             string
	Template           string
	EnablePathEncoding bool
	Write              bool
	BackupDir          string
	CloudRoot          string // 本地根对应的云端路径；用于托管登记和 {file_path} 模板变量
	MaxFileSize        int64
	Resolver           Resolver
	Register           func(ctx context.Context, in RegisterInput) error
}

// FileResult 单个文件的迁移结果。
type FileResult struct {
	Path     string
	Source   Source
	Status   string // changed / written / unchanged / dry-run / skipped / failed
	Reason   string
	OldURL   string
	NewURL   string
	Account  string
	PickCode string
}

// Report 迁移汇总。
type Report struct {
	Total     int
	Changed   int
	Written   int
	Unchanged int
	Skipped   int
	Failed    int
	Results   []FileResult
}

// Run 遍历 Root 下的 .strm 并重写为标准 FastStrm URL。
// 默认是 dry-run；只有 Options.Write=true 才写盘。
func Run(ctx context.Context, opts Options) (Report, error) {
	var report Report
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return report, fmt.Errorf("migrate root is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return report, fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return report, fmt.Errorf("root is not a directory: %s", root)
	}
	maxSize := opts.MaxFileSize
	if maxSize <= 0 {
		maxSize = 32 << 10
	}
	backupDir := filepath.Clean(strings.TrimSpace(opts.BackupDir))

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			report.Failed++
			report.Results = append(report.Results, FileResult{Path: p, Status: "failed", Reason: walkErr.Error()})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(p, root, backupDir) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(p), ".strm") {
			return nil
		}
		report.Total++
		res := migrateOne(ctx, root, p, opts, maxSize)
		report.Results = append(report.Results, res)
		switch res.Status {
		case "changed", "written", "dry-run":
			report.Changed++
			if res.Status == "written" {
				report.Written++
			}
		case "unchanged":
			report.Unchanged++
		case "skipped":
			report.Skipped++
		case "failed":
			report.Failed++
		}
		return nil
	})
	if walkErr != nil {
		return report, walkErr
	}
	return report, nil
}

func shouldSkipDir(p, root, backupDir string) bool {
	if p == root {
		return false
	}
	if backupDir != "" && (p == backupDir || strings.HasPrefix(p, backupDir+string(os.PathSeparator))) {
		return true
	}
	switch d := filepath.Base(p); d {
	case ".git", ".faststrm-trash", "node_modules":
		return true
	}
	return false
}

func migrateOne(ctx context.Context, root, p string, opts Options, maxSize int64) FileResult {
	res := FileResult{Path: p}
	raw, ref, ok := readSTRM(p, maxSize, &res)
	if !ok {
		return res
	}
	res.Source, res.OldURL = ref.Source, ref.RawURL
	if opts.From != "" && opts.From != "auto" && !sourceMatches(opts.From, ref.Source) {
		res.Status, res.Reason = "skipped", "来源类型不匹配"
		return res
	}
	if !ref.Supported() {
		res.Status, res.Reason = "skipped", ref.UnsupportedReason()
		return res
	}

	account, reason := targetAccount(ref, opts)
	if reason != "" {
		res.Status, res.Reason = "failed", reason
		return res
	}
	res.Account = account
	pickcode, reason := targetPickCode(ctx, ref, account, opts)
	if reason != "" {
		res.Status, res.Reason = "failed", reason
		return res
	}
	res.PickCode = pickcode

	content, cloudPath, reason := buildMigratedContent(root, p, ref, opts, account, pickcode)
	if reason != "" {
		res.Status, res.Reason = "failed", reason
		return res
	}
	res.NewURL = strings.TrimSpace(content)
	if string(raw) == content {
		res.Status = "unchanged"
	} else if !opts.Write {
		res.Status = "dry-run"
		return res
	} else {
		if reason := writeMigratedFile(root, p, content, raw, opts); reason != "" {
			res.Status, res.Reason = "failed", reason
			return res
		}
		res.Status = "written"
	}

	if opts.Register != nil && opts.CloudRoot != "" {
		if rerr := opts.Register(ctx, RegisterInput{
			LocalPath: p,
			Account:   account,
			CloudPath: cloudPath,
			PickCode:  pickcode,
			FileID:    ref.FileID,
			Content:   content,
		}); rerr != nil {
			res.Status, res.Reason = "failed", "托管登记失败: "+rerr.Error()
			res.NewURL = strings.TrimSpace(content)
		}
	}
	return res
}

func readSTRM(p string, maxSize int64, res *FileResult) ([]byte, Ref, bool) {
	info, err := os.Stat(p)
	if err != nil {
		res.Status, res.Reason = "failed", err.Error()
		return nil, Ref{}, false
	}
	if info.Size() > maxSize {
		res.Status, res.Reason = "skipped", fmt.Sprintf("文件超过 %d 字节，疑似非单行 STRM", maxSize)
		return nil, Ref{}, false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		res.Status, res.Reason = "failed", err.Error()
		return nil, Ref{}, false
	}
	ref, err := ParseContent(string(raw))
	if err != nil {
		res.Status, res.Reason = "failed", err.Error()
		return nil, Ref{}, false
	}
	return raw, ref, true
}

func targetAccount(ref Ref, opts Options) (string, string) {
	account := strings.TrimSpace(opts.TargetAccount)
	if account == "" && opts.AccountMap != nil {
		account = strings.TrimSpace(opts.AccountMap[ref.Account])
	}
	if account == "" {
		account = strings.TrimSpace(ref.Account)
	}
	if account == "" {
		return "", "无法确定目标账号；请使用 -account 或 -account-map"
	}
	return account, ""
}

func targetPickCode(ctx context.Context, ref Ref, account string, opts Options) (string, string) {
	pickcode := ref.PickCode
	if pickcode == "" && ref.FileID != "" {
		if opts.Resolver == nil {
			return "", "缺少 file_id 到 pickcode 的解析器"
		}
		pc, err := opts.Resolver.ResolvePickCode(ctx, account, ref.FileID)
		if err != nil {
			return "", "file_id 反查 pickcode 失败: " + err.Error()
		}
		pickcode = pc
	}
	if !pickcodeRE.MatchString(pickcode) {
		return "", fmt.Sprintf("解析出的 pickcode 非法: %q", pickcode)
	}
	return pickcode, ""
}

func buildMigratedContent(root, p string, ref Ref, opts Options, account, pickcode string) (string, string, string) {
	name := strings.TrimSpace(ref.FileName)
	if name == "" {
		name = fileNameFromLocal(p)
	}
	cloudName := name
	if cloudName == "" {
		cloudName = localStem(p)
	}
	rel, _ := filepath.Rel(root, p)
	cloudPath := cloudPathFor(opts.CloudRoot, rel, cloudName)
	content, err := strm.BuildContent(strm.ContentInput{
		Template:      opts.Template,
		Prefix:        opts.Prefix,
		Account:       account,
		PickCode:      pickcode,
		FileName:      name,
		CloudPath:     cloudPath,
		FileID:        ref.FileID,
		DisableEncode: !opts.EnablePathEncoding,
	})
	if err != nil {
		return "", "", "生成标准 URL 失败: " + err.Error()
	}
	return content, cloudPath, ""
}

func writeMigratedFile(root, p, content string, raw []byte, opts Options) string {
	if opts.BackupDir != "" {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "计算备份相对路径失败: " + err.Error()
		}
		if err := backupFile(opts.BackupDir, rel, raw); err != nil {
			return "备份旧内容失败: " + err.Error()
		}
	}
	if _, err := strmutil.WriteStrmFile(p, content); err != nil {
		return "写入 STRM 失败: " + err.Error()
	}
	return ""
}

func sourceMatches(from, got Source) bool {
	if from == SourceFastStrm {
		return got.IsFastStrm()
	}
	return from == got
}

// fileNameFromLocal 只从本地文件名恢复“可信的媒体名”：.strm 本身不携带
// 原始容器扩展名，只有 .iso.strm 这类双扩展名能安全还原；普通 movie.strm
// 无法判断是 mkv 还是 mp4，宁可不写 file_name，也不要写一个错误扩展名。
func fileNameFromLocal(p string) string {
	base := localStem(p)
	if base == "" || strm.ContentTypeForName(base) == "" {
		return ""
	}
	return base
}

func localStem(p string) string {
	base := filepath.Base(p)
	if strings.EqualFold(filepath.Ext(base), ".strm") {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if base == "" || base == "." {
		return "media"
	}
	return base
}

func cloudPathFor(root, rel, name string) string {
	root = strings.TrimRight(strings.TrimSpace(strings.ReplaceAll(root, "\\", "/")), "/")
	if root == "" {
		return ""
	}
	dir := path.Dir(strings.ReplaceAll(rel, "\\", "/"))
	if dir == "." {
		dir = ""
	}
	return path.Join(root, dir, name)
}

func backupFile(backupDir, rel string, content []byte) error {
	target := filepath.Join(backupDir, rel)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o600)
}
