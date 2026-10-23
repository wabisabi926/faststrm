package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ManagedStrm 记录本工具写出的本地 STRM 及其写入时的指纹。
// 只有 state=active 的记录才允许被自动清理；未登记的文件一律视为“未托管”。
type ManagedStrm struct {
	LocalPath     string
	Account       string
	CloudPath     string
	Pickcode      string
	FileID        string
	ContentSHA256 string
	Size          int64
	MtimeNS       int64
	Source        string
	TaskID        string
	State         string
	WrittenAt     int64
	LastSeenAt    int64
}

// NormalizeManagedLocalPath 统一为绝对、清理过的本地路径。
func NormalizeManagedLocalPath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

func scanManagedStrm(row interface{ Scan(...any) error }) (*ManagedStrm, error) {
	var m ManagedStrm
	err := row.Scan(
		&m.LocalPath, &m.Account, &m.CloudPath, &m.Pickcode, &m.FileID,
		&m.ContentSHA256, &m.Size, &m.MtimeNS, &m.Source, &m.TaskID,
		&m.State, &m.WrittenAt, &m.LastSeenAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

const managedStrmColumns = `local_path, account, cloud_path, pickcode, file_id,
	content_sha256, size, mtime_ns, source, task_id, state, written_at, last_seen_at`

// UpsertManagedStrm 登记或刷新一条托管记录（写入后调用）。
func UpsertManagedStrm(db *sql.DB, m ManagedStrm) error {
	if db == nil {
		return nil
	}
	m.LocalPath = NormalizeManagedLocalPath(m.LocalPath)
	if m.LocalPath == "" || m.Pickcode == "" {
		return errors.New("managed strm: local_path and pickcode required")
	}
	now := time.Now().UnixMilli()
	if m.WrittenAt == 0 {
		m.WrittenAt = now
	}
	if m.State == "" {
		m.State = "active"
	}
	_, err := db.Exec(`
		INSERT INTO managed_strms (`+managedStrmColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(local_path) DO UPDATE SET
			account=excluded.account,
			-- cloud_path 用 COALESCE 保护：调用方漏传（如回收站恢复后的重新登记）时
			-- 不能把已知的云端路径清空，否则该文件会因「缺少云端路径」被永久跳过清理。
			cloud_path=COALESCE(NULLIF(excluded.cloud_path, ''), managed_strms.cloud_path),
			pickcode=excluded.pickcode,
			file_id=excluded.file_id,
			content_sha256=excluded.content_sha256,
			size=excluded.size,
			mtime_ns=excluded.mtime_ns,
			source=excluded.source,
			task_id=excluded.task_id,
			state='active',
			last_seen_at=excluded.last_seen_at`,
		m.LocalPath, m.Account, m.CloudPath, m.Pickcode, m.FileID,
		m.ContentSHA256, m.Size, m.MtimeNS, m.Source, m.TaskID,
		m.State, m.WrittenAt, now,
	)
	return err
}

// GetManagedStrm 按本地路径查询托管记录；不存在返回 (nil, nil)。
func GetManagedStrm(db *sql.DB, localPath string) (*ManagedStrm, error) {
	if db == nil {
		return nil, nil
	}
	localPath = NormalizeManagedLocalPath(localPath)
	row := db.QueryRow(`SELECT `+managedStrmColumns+` FROM managed_strms WHERE local_path = ?`, localPath)
	m, err := scanManagedStrm(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// ListManagedStrmsByRoot 返回某个映射根下所有 active 托管记录（key=规范化绝对路径）。
func ListManagedStrmsByRoot(db *sql.DB, root string) (map[string]ManagedStrm, error) {
	out := make(map[string]ManagedStrm)
	if db == nil || root == "" {
		return out, nil
	}
	root = NormalizeManagedLocalPath(root)
	prefix := root + string(filepath.Separator)
	rows, err := db.Query(
		`SELECT `+managedStrmColumns+` FROM managed_strms
		 WHERE state='active' AND local_path LIKE ? ESCAPE '\'`,
		escapeLike(prefix)+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanManagedStrm(rows)
		if err != nil {
			return nil, err
		}
		out[m.LocalPath] = *m
	}
	return out, rows.Err()
}

// UpdateManagedPathPrefix 迁移托管记录的本地/云端路径前缀（目录或文件 rename/move）。
// 使用事务并逐条检查新路径冲突：任何冲突都会整体回滚，避免覆盖另一条托管记录。
func UpdateManagedPathPrefix(db *sql.DB, oldLocalRoot, newLocalRoot, oldCloudRoot, newCloudRoot string) (int64, error) {
	if db == nil {
		return 0, nil
	}
	oldRoot := NormalizeManagedLocalPath(oldLocalRoot)
	newRoot := NormalizeManagedLocalPath(newLocalRoot)
	if oldRoot == "" || newRoot == "" || oldRoot == newRoot {
		return 0, nil
	}
	if filepath.Dir(oldRoot) == oldRoot || filepath.Dir(newRoot) == newRoot {
		return 0, errors.New("managed path: filesystem root is not allowed")
	}
	sep := string(filepath.Separator)
	if strings.HasPrefix(newRoot, oldRoot+sep) || strings.HasPrefix(oldRoot, newRoot+sep) {
		return 0, errors.New("managed path: nested prefix moves are not supported")
	}
	// managed_strms.cloud_path 统一保存带前导 "/" 的云端路径，
	// normalizeDbPath 会去掉前导斜杠，这里补回后再做前缀匹配。
	oldCloud := cloudRootWithSlash(oldCloudRoot)
	newCloud := cloudRootWithSlash(newCloudRoot)

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	prefix := oldRoot + sep
	rows, err := tx.Query(
		`SELECT local_path, cloud_path FROM managed_strms
		 WHERE local_path = ? OR local_path LIKE ? ESCAPE '\'`,
		oldRoot, escapeLike(prefix)+"%")
	if err != nil {
		return 0, err
	}
	type pathRow struct{ localPath, cloudPath string }
	var pending []pathRow
	for rows.Next() {
		var row pathRow
		if err := rows.Scan(&row.localPath, &row.cloudPath); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	now := time.Now().UnixMilli()
	var updated int64
	for _, row := range pending {
		newLocal := newRoot + strings.TrimPrefix(row.localPath, oldRoot)
		newCloudPath := row.cloudPath
		if oldCloud != "" && newCloud != "" {
			if row.cloudPath == oldCloud {
				newCloudPath = newCloud
			} else if strings.HasPrefix(row.cloudPath, oldCloud+"/") {
				newCloudPath = newCloud + strings.TrimPrefix(row.cloudPath, oldCloud)
			}
		}

		var exists string
		err := tx.QueryRow(`SELECT local_path FROM managed_strms WHERE local_path = ?`, newLocal).Scan(&exists)
		switch {
		case err == nil:
			return 0, fmt.Errorf("managed path conflict: %s already exists", newLocal)
		case !errors.Is(err, sql.ErrNoRows):
			return 0, err
		}
		if _, err := tx.Exec(
			`UPDATE managed_strms SET local_path = ?, cloud_path = ?, last_seen_at = ? WHERE local_path = ?`,
			newLocal, newCloudPath, now, row.localPath,
		); err != nil {
			return 0, err
		}
		updated++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func cloudRootWithSlash(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return "/" + strings.Trim(normalizeDbPath(p), "/")
}

// MarkManagedStrmDeleted 标记托管记录已被清理（保留墓碑，便于诊断）。
func MarkManagedStrmDeleted(db *sql.DB, localPath string) error {
	if db == nil {
		return nil
	}
	localPath = NormalizeManagedLocalPath(localPath)
	_, err := db.Exec(
		`UPDATE managed_strms SET state='deleted', last_seen_at=? WHERE local_path=?`,
		time.Now().UnixMilli(), localPath,
	)
	return err
}

// ListPickcodePathsByAccount 一次加载某账号的 pickcode → 云端路径，供历史纳管批量匹配。
func ListPickcodePathsByAccount(database *sql.DB, account string) (map[string]string, error) {
	out := make(map[string]string)
	if database == nil || account == "" {
		return out, nil
	}
	rows, err := database.Query(
		`SELECT pickcode, path FROM files WHERE account = ? AND pickcode <> ''`, account,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pc, p string
		if err := rows.Scan(&pc, &p); err != nil {
			return nil, err
		}
		out[pc] = p
	}
	return out, rows.Err()
}

// PickcodeExists 判断某个账号的云端索引里是否存在该 pickcode。
// 仅用于历史文件纳管，避免把来路不明的 STRM 直接登记为托管。
func PickcodeExists(db *sql.DB, account, pickcode string) (bool, error) {
	if db == nil || account == "" || pickcode == "" {
		return false, nil
	}
	var one int
	err := db.QueryRow(
		`SELECT 1 FROM files WHERE account = ? AND pickcode = ? LIMIT 1`,
		account, pickcode,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
