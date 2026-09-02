package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// FilePathEntry 对应 TS FilePathEntry 接口
type FilePathEntry struct {
	FileID     string // 始终用 string 存储，避免 JS Number 精度丢失（SQLite INTEGER 亲和）
	Path       string
	FileName   string
	ParentID   string
	PickCode   string
	UpdateTime int64
}

// ==================== 单条 CRUD ====================

// GetFilePathEntry 按 (account, fileId) 查单条记录
func GetFilePathEntry(db *sql.DB, account string, fileID string) (*FilePathEntry, error) {
	row := db.QueryRow(`
		SELECT file_id, path, file_name, parent_id, pickcode, update_time
		FROM files WHERE account = ? AND file_id = ?`,
		account, fileID,
	)
	return scanEntry(row.Scan)
}

// GetFilePathEntryByPath 按路径查找（用于 302 模式从路径反查 pickcode）
func GetFilePathEntryByPath(db *sql.DB, account string, filePath string) (*FilePathEntry, error) {
	p := normalizeDbPath(filePath)
	row := db.QueryRow(`
		SELECT file_id, path, file_name, parent_id, pickcode, update_time
		FROM files WHERE account = ? AND path = ?`,
		account, p,
	)
	return scanEntry(row.Scan)
}

// normalizeEntry 修正 entry 中会导致 INTEGER 列写入异常的字段
//   - ParentID "" → "0"（TS 默认 parentId=0；INTEGER 列扫 string 会报错）
//   - FileID/UpdateTime/ParentID 以字符串形式读，转为 int64 再写入，确保 INTEGER 亲和生效
func normalizeEntry(e *FilePathEntry) (fileID int64, path string, fileName string, parentID int64, pickCode string, updateTime int64, err error) {
	if e.ParentID == "" {
		e.ParentID = "0"
	}
	fileID, err = strconv.ParseInt(e.FileID, 10, 64)
	if err != nil {
		return 0, "", "", 0, "", 0, fmt.Errorf("invalid fileId %q: %w", e.FileID, err)
	}
	parentID, err = strconv.ParseInt(e.ParentID, 10, 64)
	if err != nil {
		return 0, "", "", 0, "", 0, fmt.Errorf("invalid parentId %q: %w", e.ParentID, err)
	}
	return fileID, normalizeDbPath(e.Path), e.FileName, parentID, e.PickCode, e.UpdateTime, nil
}

// UpsertFilePathEntry 插入或更新单条记录
func UpsertFilePathEntry(db *sql.DB, account string, e FilePathEntry) error {
	fileID, path, fileName, parentID, pickCode, updateTime, err := normalizeEntry(&e)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO files (account, file_id, path, file_name, parent_id, pickcode, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account, file_id) DO UPDATE SET
			path        = excluded.path,
			file_name   = excluded.file_name,
			parent_id   = excluded.parent_id,
			pickcode    = excluded.pickcode,
			update_time = excluded.update_time`,
		account, fileID, path, fileName, parentID, pickCode, updateTime,
	)
	return err
}

// RemoveFilePathEntry 删除单条记录
func RemoveFilePathEntry(db *sql.DB, account string, fileID string) error {
	_, err := db.Exec("DELETE FROM files WHERE account = ? AND file_id = ?", account, fileID)
	return err
}

// ==================== 批量操作 ====================

// UpsertFilePathEntryBatch 批量 upsert（单次事务，减少 fsync）
// 性能：现代 SQLite 10000 条 < 100ms
func UpsertFilePathEntryBatch(db *sql.DB, account string, entries []FilePathEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			log.Printf("rollback error: %v", err)
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT INTO files (account, file_id, path, file_name, parent_id, pickcode, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account, file_id) DO UPDATE SET
			path        = excluded.path,
			file_name   = excluded.file_name,
			parent_id   = excluded.parent_id,
			pickcode    = excluded.pickcode,
			update_time = excluded.update_time`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		fileID, path, fileName, parentID, pickCode, updateTime, berr := normalizeEntry(&e)
		if berr != nil {
			return fmt.Errorf("batch entry %q: %w", e.FileID, berr)
		}
		if _, err := stmt.Exec(
			account, fileID, path, fileName, parentID, pickCode, updateTime,
		); err != nil {
			return fmt.Errorf("batch upsert: %w", err)
		}
	}
	return tx.Commit()
}

// UpdatePathPrefixBatch 文件夹 rename/move → 批量更新路径前缀
// SQL: UPDATE SET path = ? || substr(path, oldP+1+1) WHERE account=? AND (path=oldP OR path LIKE oldP"/%")
// 对应 TS updatePathPrefixBatch
func UpdatePathPrefixBatch(db *sql.DB, account, oldPrefix, newPrefix string) (int64, error) {
	oldP := strings.TrimSuffix(normalizeDbPath(oldPrefix), "/")
	newP := strings.TrimSuffix(normalizeDbPath(newPrefix), "/")
	if oldP == "" || oldP == "/" {
		return 0, nil // 根目录短路，防全表误改
	}
	// 对齐 TS filePathDb.ts L253：oldPrefixLen = oldP.length + 1
	//   - +1：因为 SQLite substr(X, Y) 从 1 开始计数
	//   - oldP 是 字符长度（TS 同 SQLite：一个汉字/ASCII 都是 length=1）
	//   对 "老文件夹/a.mkv"（oldP="老文件夹" length=4），
	//     substr(path, 5) 从第 5 字符 '/' 开始 => "/a.mkv"
	//     拼接 newP || substr(path, Y) => "新文件夹/a.mkv" ✅
	oldPrefixLen := len([]rune(oldP)) + 1
	oldPLike := oldP + "/%"

	res, err := db.Exec(`
		UPDATE files
		SET path = ? || substr(path, ?)
		WHERE account = ? AND (path = ? OR path LIKE ?)`,
		newP, oldPrefixLen, account, oldP, oldPLike,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteByPath 按精确路径删除（Emby 删除同步用）
func DeleteByPath(db *sql.DB, account, filePath string) (int64, error) {
	res, err := db.Exec("DELETE FROM files WHERE account = ? AND path = ?",
		account, normalizeDbPath(filePath))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteByPathPrefix 按路径前缀批量删除 files + folders 两表（根目录保护）
// 修复 Bug：cleanupOldStrmAssets 只删 files 表，folders 残留导致下次 rename 拿旧路径
func DeleteByPathPrefix(db *sql.DB, account, pathPrefix string) (int64, error) {
	p := strings.TrimSuffix(normalizeDbPath(pathPrefix), "/")
	if p == "" || p == "/" {
		return 0, nil
	}
	res, err := db.Exec(`DELETE FROM files WHERE account = ? AND (path = ? OR path LIKE ?)`,
		account, p, p+"/%")
	if err != nil {
		return 0, err
	}
	_, ferr := db.Exec(`DELETE FROM folders WHERE account = ? AND (path = ? OR path LIKE ?)`,
		account, p, p+"/%")
	if ferr != nil {
		return res.RowsAffected()
	}
	return res.RowsAffected()
}

// GetEntryCount 获取账号或全局记录数
func GetEntryCount(db *sql.DB, account string) (int64, error) {
	var (
		row *sql.Row
		n   int64
	)
	if account != "" {
		row = db.QueryRow("SELECT COUNT(*) FROM files WHERE account = ?", account)
	} else {
		row = db.QueryRow("SELECT COUNT(*) FROM files")
	}
	err := row.Scan(&n)
	return n, err
}

// CountFilesByPrefix 统计指定 account + cloudPath 前缀下的 files 表记录数
// 用于 STRM 清理「全量对账」返回 dbRecordCount 字段
// cloudPathPrefix 为空时统计该 account 下全部记录
// 匹配规则与 ListSnapshotByAccount 一致：path = cloudPath OR path LIKE cloudPath/%
func CountFilesByPrefix(db *sql.DB, account, cloudPathPrefix string) (int64, error) {
	if db == nil || account == "" {
		return 0, nil
	}
	if cloudPathPrefix == "" {
		var n int64
		err := db.QueryRow("SELECT COUNT(*) FROM files WHERE account = ?", account).Scan(&n)
		return n, err
	}
	// 与存储时一致：去掉前导 "/"，避免 "/电影" 查不到 "电影/..."
	base := normalizeDbPath(cloudPathPrefix)
	prefixNorm := base
	if !strings.HasSuffix(prefixNorm, "/") {
		prefixNorm = prefixNorm + "/"
	}
	like := prefixNorm + "%"
	var n int64
	err := db.QueryRow(
		"SELECT COUNT(*) FROM files WHERE account = ? AND (path = ? OR path LIKE ?)",
		account, base, like,
	).Scan(&n)
	return n, err
}

// SnapshotEntry P2-3 增量同步 snapshot entry（比 FilePathEntry 精简，仅含无变化判断字段）
type SnapshotEntry struct {
	Path     string
	PickCode string
	FileName string
}

// ListSnapshotByAccount 读取指定 account 的全量 files 快照（用于 P2-3 增量同步比对）。
// 若 cloudPathPrefix 非空，仅返回 path 以该 prefix 开头的条目（限定单任务范围，避免跨任务误匹配）。
// 返回值：map[cloudPath]SnapshotEntry
func ListSnapshotByAccount(db *sql.DB, account string, cloudPathPrefix string) (map[string]SnapshotEntry, error) {
	snap := make(map[string]SnapshotEntry)
	if account == "" || db == nil {
		return snap, nil
	}
	var (
		rows *sql.Rows
		err  error
	)
	base := `SELECT path, pickcode, file_name FROM files WHERE account = ?`
	if cloudPathPrefix != "" {
		base += ` AND (path = ? OR path LIKE ?)`
		prefixNorm := cloudPathPrefix
		if !strings.HasSuffix(prefixNorm, "/") {
			prefixNorm = prefixNorm + "/"
		}
		like := prefixNorm + "%"
		rows, err = db.Query(base, account, cloudPathPrefix, like)
	} else {
		rows, err = db.Query(base, account)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var se SnapshotEntry
		var pc sql.NullString
		var fn sql.NullString
		if e := rows.Scan(&se.Path, &pc, &fn); e != nil {
			return nil, e
		}
		se.PickCode = pc.String
		se.FileName = fn.String
		snap[se.Path] = se
	}
	if e := rows.Err(); e != nil {
		return nil, e
	}
	return snap, nil
}

// RemoveFilePathEntryBatch 分块删除多个 file_id
func RemoveFilePathEntryBatch(db *sql.DB, account string, fileIDs []string) (int64, error) {
	if len(fileIDs) == 0 {
		return 0, nil
	}
	var deleted int64
	for i := 0; i < len(fileIDs); i += SQLITE_CHUNK_SIZE {
		end := i + SQLITE_CHUNK_SIZE
		if end > len(fileIDs) {
			end = len(fileIDs)
		}
		chunk := fileIDs[i:end]
		// 构造 IN (?, ?, ...)
		qs := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, account)
		for _, id := range chunk {
			args = append(args, id)
		}
		res, err := db.Exec(fmt.Sprintf("DELETE FROM files WHERE account = ? AND file_id IN (%s)", qs), args...)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += n
	}
	return deleted, nil
}

// ==================== P0-2 folders 表 CRUD ====================

// UpsertFolderEntry 插入或更新文件夹记录（对齐参考项目 process_life_dir_item）
func UpsertFolderEntry(db *sql.DB, account string, e FilePathEntry) error {
	fileID, path, fileName, parentID, _, updateTime, err := normalizeEntry(&e)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO folders (account, file_id, path, file_name, parent_id, update_time)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(account, file_id) DO UPDATE SET
			path        = excluded.path,
			file_name   = excluded.file_name,
			parent_id   = excluded.parent_id,
			update_time = excluded.update_time`,
		account, fileID, path, fileName, parentID, updateTime,
	)
	return err
}

// GetFolderEntry 按 (account, fileId) 查 folders 表单条记录
func GetFolderEntry(db *sql.DB, account string, fileID string) (*FilePathEntry, error) {
	row := db.QueryRow(`
		SELECT file_id, path, file_name, parent_id, '', update_time
		FROM folders WHERE account = ? AND file_id = ?`,
		account, fileID,
	)
	return scanEntry(row.Scan)
}

// GetFileOrFolderEntry 先查 files 表，未命中再查 folders 表
// 对齐参考项目 _databasehelper.get_by_id(file_id) 的统一查询语义
func GetFileOrFolderEntry(db *sql.DB, account string, fileID string) (*FilePathEntry, error) {
	// 先查 files 表
	entry, err := GetFilePathEntry(db, account, fileID)
	if err != nil {
		return nil, err
	}
	if entry != nil {
		return entry, nil
	}
	// files 表未命中，查 folders 表
	return GetFolderEntry(db, account, fileID)
}

// UpdateFolderPathPrefixBatch folders 表批量更新路径前缀（rename/move 文件夹时调用）
func UpdateFolderPathPrefixBatch(db *sql.DB, account, oldPrefix, newPrefix string) (int64, error) {
	oldP := strings.TrimSuffix(normalizeDbPath(oldPrefix), "/")
	newP := strings.TrimSuffix(normalizeDbPath(newPrefix), "/")
	if oldP == "" || oldP == "/" {
		return 0, nil
	}
	oldPrefixLen := len([]rune(oldP)) + 1
	oldPLike := oldP + "/%"
	res, err := db.Exec(`
		UPDATE folders
		SET path = ? || substr(path, ?)
		WHERE account = ? AND (path = ? OR path LIKE ?)`,
		newP, oldPrefixLen, account, oldP, oldPLike,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteFolderByPathPrefix 按路径前缀批量删除 folders 表记录
func DeleteFolderByPathPrefix(db *sql.DB, account, pathPrefix string) (int64, error) {
	p := strings.TrimSuffix(normalizeDbPath(pathPrefix), "/")
	if p == "" || p == "/" {
		return 0, nil
	}
	res, err := db.Exec(`DELETE FROM folders WHERE account = ? AND (path = ? OR path LIKE ?)`,
		account, p, p+"/%")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ==================== 辅助 ====================

func scanEntry(scan func(dest ...any) error) (*FilePathEntry, error) {
	var (
		fileID     int64 // SQLite INTEGER 读取
		path       string
		fileName   string
		parentID   int64
		pickCode   string
		updateTime int64
	)
	err := scan(&fileID, &path, &fileName, &parentID, &pickCode, &updateTime)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &FilePathEntry{
		FileID:     strconv.FormatInt(fileID, 10),
		Path:       path,
		FileName:   fileName,
		ParentID:   strconv.FormatInt(parentID, 10),
		PickCode:   pickCode,
		UpdateTime: updateTime,
	}, nil
}

// ==================== FilePathRepo（适配 emby.FilePathDb 接口） ====================

// FilePathRepo 包装 *sql.DB 以实现 emby.FilePathDb 接口
// 用于 Emby 删除同步时清理 files 表中的关联记录
type FilePathRepo struct {
	db *sql.DB
}

// NewFilePathRepo 创建 FilePathRepo
// db 为 nil 时返回零值实例，方法调用时通过 nil 守卫安全降级
func NewFilePathRepo(db *sql.DB) *FilePathRepo {
	if db == nil {
		return nil
	}
	return &FilePathRepo{db: db}
}

// DeleteByPath 按精确路径删除（实现 emby.FilePathDb）
func (r *FilePathRepo) DeleteByPath(account, filePath string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	return DeleteByPath(r.db, account, filePath)
}

// DeleteByPathPrefix 按路径前缀批量删除（实现 emby.FilePathDb）
func (r *FilePathRepo) DeleteByPathPrefix(account, pathPrefix string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	return DeleteByPathPrefix(r.db, account, pathPrefix)
}

// ==================== P1-3 幽灵记录清理 ====================

// PurgeOrphanEntries 扫描 files 表，对 resolveLocal 返回的本地 STRM 路径执行 os.Stat，
// 若文件缺失且 shouldDeleteIfMissing==true，则删除该 DB 条目。用于清理手动删文件/同步异常
// 后遗留的 DB 幽灵记录，避免 302 模式 pickcode 反查返回已失效文件指向。
//
// resolveLocal: (entry) -> (本地 STRM 绝对路径, 当该路径文件缺失时是否判定为幽灵)
//   - 返回 shouldDeleteIfMissing=false 的条目会被跳过（例如条目不在当前 task targetPath 范围内）
//
// maxCheck<=0 表示不限制检查数量（分页扫描，内存安全）
// 返回 (实际删除 DB 条数, error)
func PurgeOrphanEntries(
	db *sql.DB,
	account string,
	maxCheck int,
	resolveLocal func(entry FilePathEntry) (strmPath string, shouldDeleteIfMissing bool),
) (int64, error) {
	if db == nil || account == "" {
		return 0, nil
	}
	var (
		deleted      int64
		checked      int
		orphanIDs    []string
		orphanBufCap = 200
		pageSize     = 1000
		offset       = 0
	)
	flushOrphans := func() error {
		if len(orphanIDs) == 0 {
			return nil
		}
		n, ferr := RemoveFilePathEntryBatch(db, account, orphanIDs)
		if ferr == nil {
			deleted += n
		} else {
			return ferr
		}
		orphanIDs = orphanIDs[:0]
		return nil
	}
	for {
		rows, qerr := db.Query(`
			SELECT file_id, path, file_name, parent_id, pickcode, update_time
			FROM files WHERE account = ?
			ORDER BY file_id LIMIT ? OFFSET ?`,
			account, pageSize, offset,
		)
		if qerr != nil {
			return deleted, qerr
		}
		batchCount := 0
		for rows.Next() {
			batchCount++
			entry, serr := scanEntry(rows.Scan)
			if serr != nil || entry == nil {
				continue
			}
			checked++
			if maxCheck > 0 && checked > maxCheck {
				break
			}
			strmPath, shouldDelete := resolveLocal(*entry)
			if !shouldDelete || strmPath == "" {
				continue
			}
			if _, sterr := osStat(strmPath); sterr != nil {
				// 文件不存在或无权限，判定为幽灵
				orphanIDs = append(orphanIDs, entry.FileID)
				if len(orphanIDs) >= orphanBufCap {
					if ferr := flushOrphans(); ferr != nil {
						rows.Close()
						return deleted, ferr
					}
				}
			}
		}
		cerr := rows.Close()
		if cerr != nil {
			return deleted, cerr
		}
		if batchCount < pageSize || (maxCheck > 0 && checked >= maxCheck) {
			break
		}
		offset += pageSize
	}
	if ferr := flushOrphans(); ferr != nil {
		return deleted, ferr
	}
	return deleted, nil
}

// osStat 包内封装方便单测 mock（默认走 os.Stat）
var osStat = _defaultOSStat

func _defaultOSStat(name string) (any, error) {
	return os.Stat(name)
}
