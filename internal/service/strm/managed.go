package strm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// ManagedInput 一次 STRM 写入的登记信息。
type ManagedInput struct {
	LocalPath string
	Account   string
	CloudPath string
	Pickcode  string
	FileID    string
	Content   string
	Source    string
	TaskID    string
}

// ManagedRecorder 负责登记本工具写出的 STRM，并在清理前复核文件指纹与归属。
// 实现必须对 nil DB 保持安全：未启用 SQLite 时返回 nil 实例，调用方据此降级/跳过自动删除。
type ManagedRecorder interface {
	Record(ctx context.Context, in ManagedInput) error
	VerifyFile(ctx context.Context, localPath, pickcode string) (bool, error)
	MarkDeleted(ctx context.Context, localPath string) error
	CloudRef(ctx context.Context, localPath string) (*ManagedCloudRef, error)
	Bootstrap(ctx context.Context, roots []string, accounts []string) (added, skipped int, err error)
}

// ManagedCloudRef 删除前复核云端所需的最小元数据。
type ManagedCloudRef struct {
	Account   string
	CloudPath string
	Pickcode  string
}

type managedRecorder struct {
	db *sql.DB
}

// NewManagedRecorder 创建托管登记器；db 为空时返回 nil（调用方据此跳过自动清理）。
func NewManagedRecorder(database *sql.DB) ManagedRecorder {
	if database == nil {
		return nil
	}
	return &managedRecorder{db: database}
}

func (r *managedRecorder) Record(ctx context.Context, in ManagedInput) error {
	if r == nil || r.db == nil {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	in.LocalPath = db.NormalizeManagedLocalPath(in.LocalPath)
	if in.LocalPath == "" || in.Pickcode == "" {
		return errors.New("managed record: local_path and pickcode required")
	}
	var (
		size    int64
		mtimeNS int64
	)
	if fi, err := os.Stat(in.LocalPath); err == nil {
		size = fi.Size()
		mtimeNS = fi.ModTime().UnixNano()
	}
	sum := ""
	if in.Content != "" {
		h := sha256.Sum256([]byte(in.Content))
		sum = hex.EncodeToString(h[:])
	}
	return db.UpsertManagedStrm(r.db, db.ManagedStrm{
		LocalPath:     in.LocalPath,
		Account:       in.Account,
		CloudPath:     in.CloudPath,
		Pickcode:      in.Pickcode,
		FileID:        in.FileID,
		ContentSHA256: sum,
		Size:          size,
		MtimeNS:       mtimeNS,
		Source:        in.Source,
		TaskID:        in.TaskID,
		State:         "active",
		WrittenAt:     time.Now().UnixMilli(),
	})
}

// VerifyFile 在删除前复核：托管记录必须存在、pickcode 一致、文件大小/mtime/SHA256 未变。
func (r *managedRecorder) VerifyFile(ctx context.Context, localPath, pickcode string) (bool, error) {
	if r == nil || r.db == nil {
		return false, nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	localPath = db.NormalizeManagedLocalPath(localPath)
	record, err := db.GetManagedStrm(r.db, localPath)
	if err != nil {
		return false, err
	}
	if record == nil || record.State != "active" {
		return false, nil
	}
	if pickcode != "" && record.Pickcode != "" && record.Pickcode != pickcode {
		return false, nil
	}
	fi, err := os.Lstat(localPath)
	if err != nil {
		return false, nil
	}
	if fi.Mode()&os.ModeSymlink != 0 || fi.IsDir() {
		return false, nil
	}
	if record.Size > 0 && fi.Size() != record.Size {
		return false, nil
	}
	if record.MtimeNS > 0 && fi.ModTime().UnixNano() != record.MtimeNS {
		return false, nil
	}
	if record.ContentSHA256 != "" {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return false, nil
		}
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != record.ContentSHA256 {
			return false, nil
		}
	}
	return true, nil
}

// MarkDeleted 记录文件已被清理（保留墓碑；文件从回收站恢复后由下一次写入重新登记）。
func (r *managedRecorder) MarkDeleted(ctx context.Context, localPath string) error {
	if r == nil || r.db == nil {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return db.MarkManagedStrmDeleted(r.db, localPath)
}

// CloudRef 返回托管记录的云端元数据；文件未托管/已删除时返回 (nil, nil)。
func (r *managedRecorder) CloudRef(ctx context.Context, localPath string) (*ManagedCloudRef, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	record, err := db.GetManagedStrm(r.db, localPath)
	if err != nil {
		return nil, err
	}
	if record == nil || record.State != "active" {
		return nil, nil
	}
	return &ManagedCloudRef{
		Account: record.Account, CloudPath: record.CloudPath, Pickcode: record.Pickcode,
	}, nil
}

// ParseManagedContent 从默认 STRM URL 解析 account/pickcode。
// 自定义模板若不携带这两个查询参数，返回空串（该文件不会被纳管，自动清理会跳过）。
func ParseManagedContent(content string) (account, pickcode string) {
	s := strings.TrimSpace(content)
	if s == "" {
		return "", ""
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return "", ""
	}
	q := u.Query()
	return q.Get("account"), q.Get("pickcode")
}

// isStandardStrmURL 判断历史文件是否为本工具生成形态。
// /api/fs/get 路由已在 v1.5.4 删除，这里仍识别它，仅用于历史纳管/清理归属，
// 避免旧 STRM 被当成第三方文件；播放端应使用 /api/strm 或先运行迁移工具。
func isStandardStrmURL(content string) bool {
	s := strings.TrimSpace(content)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	p := strings.TrimSuffix(u.Path, "/")
	return p == "/api/strm" || p == "/api/fs/get"
}

// Bootstrap 一次性纳管历史 STRM：仅登记「内容含合法 pickcode + 账号存在 + 云端索引里有该 pickcode」的文件。
// 手写/第三方 STRM 不会被登记，因此后续自动清理不会碰它们。
func (r *managedRecorder) Bootstrap(ctx context.Context, roots []string, accounts []string) (added, skipped int, err error) {
	if r == nil || r.db == nil {
		return 0, 0, nil
	}
	accountSet := make(map[string]struct{}, len(accounts))
	for _, a := range accounts {
		if a != "" {
			accountSet[a] = struct{}{}
		}
	}
	if len(accountSet) == 0 {
		return 0, 0, nil
	}
	// 一次加载各账号 pickcode 集合，避免每个历史文件都查一次 SQLite。
	pickcodeSets := make(map[string]map[string]string, len(accountSet))
	for account := range accountSet {
		set, lerr := db.ListPickcodePathsByAccount(r.db, account)
		if lerr != nil {
			logger.S().Warnf("[ManagedStrm] 加载账号 %s 的 pickcode 索引失败: %v", account, lerr)
			continue
		}
		pickcodeSets[account] = set
	}
	state := &bootstrapState{recorder: r, accountSet: accountSet, pickcodes: pickcodeSets}
	for _, root := range roots {
		root = db.NormalizeManagedLocalPath(root)
		if root == "" {
			continue
		}
		if werr := state.walkRoot(ctx, root); werr != nil {
			return state.added, state.skipped, werr
		}
	}
	logger.S().Infof("[ManagedStrm] 历史纳管完成：新增 %d，跳过 %d（手写/非本工具/云端索引缺失）", state.added, state.skipped)
	return state.added, state.skipped, nil
}

type bootstrapState struct {
	recorder   *managedRecorder
	accountSet map[string]struct{}
	pickcodes  map[string]map[string]string
	added      int
	skipped    int
}

func (s *bootstrapState) walkRoot(ctx context.Context, root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if d.IsDir() {
			if d.Name() == strmutil.TrashDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".strm") {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			s.skipped++
			return nil
		}
		if s.recordFile(ctx, p) {
			s.added++
			if s.added%1000 == 0 {
				logger.S().Infof("[ManagedStrm] 已纳管历史 STRM %d 个...", s.added)
			}
		} else {
			s.skipped++
		}
		return nil
	})
}

func (s *bootstrapState) recordFile(ctx context.Context, p string) bool {
	if existing, _ := db.GetManagedStrm(s.recorder.db, p); existing != nil {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) > 8192 {
		return false
	}
	account, pickcode := ParseManagedContent(string(data))
	if account == "" || !strmutil.IsValidPickcode(pickcode) || !isStandardStrmURL(string(data)) {
		return false
	}
	if _, ok := s.accountSet[account]; !ok {
		return false
	}
	set, ok := s.pickcodes[account]
	if !ok {
		return false
	}
	cloudPath, exists := set[pickcode]
	if !exists {
		return false
	}
	err = s.recorder.Record(ctx, ManagedInput{
		LocalPath: p,
		Account:   account,
		CloudPath: cloudPath,
		Pickcode:  pickcode,
		Content:   string(data),
		Source:    "bootstrap",
	})
	return err == nil
}
