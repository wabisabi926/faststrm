// Package db SQLite 文件路径数据库
// 对齐 frontend/src/lib/filePathDb.ts 的表结构与 PRAGMA 设置
package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ==================== 全局单例 ====================

var (
	instance *sql.DB
	once     sync.Once
)

// SQLite 单次绑定变量上限（TS 用 900，modernc 也兼容 999，保守设置）
const SQLITE_CHUNK_SIZE = 900

// OpenNew 总是打开新的 SQLite 连接（不使用全局单例，供测试或多租户场景）。
//
// PRAGMA 设置（严格对齐 filePathDb.ts）：
//   journal_mode = WAL          —— 并发读 + 单写
//   synchronous  = NORMAL       —— WAL 下性能/安全平衡点
//   busy_timeout = 5000         —— 锁等待超时 5s
func OpenNew(dbDir string) (*sql.DB, error) {
	dbFile := filepath.Join(dbDir, "filePathDb.sqlite")
	db, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbFile))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrateSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Open 打开/获取 SQLite 连接。线程安全，全局单例（生产环境使用）。
func Open(dbDir string) (*sql.DB, error) {
	var err error
	once.Do(func() {
		instance, err = OpenNew(dbDir)
	})
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	return instance, nil
}

// Close 关闭数据库（进程退出时调用）
func Close() {
	if instance != nil {
		_ = instance.Close()
		instance = nil
		once = sync.Once{} // 允许测试中重新初始化
	}
}

// migrateSchema 创建 files 表与索引（与 TS DDL 逐字一致）
func migrateSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files (
			account     TEXT    NOT NULL,
			file_id     INTEGER NOT NULL,
			path        TEXT    NOT NULL,
			file_name   TEXT    NOT NULL,
			parent_id   INTEGER NOT NULL DEFAULT 0,
			pickcode    TEXT    NOT NULL DEFAULT '',
			update_time INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (account, file_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_files_path ON files(account, path)`,
		`CREATE INDEX IF NOT EXISTS idx_files_parent ON files(account, parent_id)`,
		// P0-2 folders 表：对齐参考项目 FileDbHelper.process_life_dir_item
		// 文件夹路径持久化，move/rename 事件通过 file_id 查旧路径
		`CREATE TABLE IF NOT EXISTS folders (
			account     TEXT    NOT NULL,
			file_id     INTEGER NOT NULL,
			path        TEXT    NOT NULL,
			file_name   TEXT    NOT NULL,
			parent_id   INTEGER NOT NULL DEFAULT 0,
			update_time INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (account, file_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_folders_path ON folders(account, path)`,
		`CREATE INDEX IF NOT EXISTS idx_folders_parent ON folders(account, parent_id)`,
		// P0-4 STRM 执行历史回滚：记录每次 STRM 生成/删除的细粒度历史
		// 对齐参考项目 core/history/strm.py，限 500 条循环覆盖
		`CREATE TABLE IF NOT EXISTS strm_exec_history (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id       TEXT    NOT NULL DEFAULT '',
			kind          TEXT    NOT NULL DEFAULT '',
			account       TEXT    NOT NULL DEFAULT '',
			success       INTEGER NOT NULL DEFAULT 0,
			total_files   INTEGER NOT NULL DEFAULT 0,
			success_files INTEGER NOT NULL DEFAULT 0,
			failed_files  INTEGER NOT NULL DEFAULT 0,
			elapsed_ms    INTEGER NOT NULL DEFAULT 0,
			api_requests  INTEGER NOT NULL DEFAULT 0,
			error_msg     TEXT    NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_strm_history_created ON strm_exec_history(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_strm_history_kind ON strm_exec_history(kind, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_strm_history_task ON strm_exec_history(task_id, created_at DESC)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %s: %w", s, err)
		}
	}
	return nil
}

// StrmHistoryMaxRecords P0-4 保留最近 N 条 STRM 执行历史
// 对齐参考项目 core/history/strm.py 的 500 条上限
const StrmHistoryMaxRecords = 500

// ==================== 工具函数 ====================

// normalizeDbPath 对应 TS normalizeDbPath —— 去掉前导 '/'
// 统一 DB 层面的路径规范，避免 "电影/xxx" 和 "/电影/xxx" 不一致
func normalizeDbPath(p string) string {
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	return p
}

// NowMs 返回当前 unix ms 时间戳
func NowMs() int64 {
	return time.Now().UnixNano() / 1e6
}

// join 字符串切片连接（避免 fmt 之外的依赖）
func join(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	out := items[0]
	for i := 1; i < len(items); i++ {
		out += sep + items[i]
	}
	return out
}
