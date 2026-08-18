package db

import (
	"database/sql"
	"fmt"
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
	defer tx.Rollback()

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

// DeleteByPathPrefix 按路径前缀批量删除（整季整剧删除同步，根目录保护）
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
func NewFilePathRepo(db *sql.DB) *FilePathRepo {
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
