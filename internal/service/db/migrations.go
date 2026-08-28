package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// ==================== SQLite 版本化迁移框架 ====================
// 设计原则（对齐项目已有范式 + 避免 ExperienceRecall 里的坑）：
//   1. 每张迁移都在事务里执行 —— 失败自动回滚，避免半迁移状态
//   2. 幂等 —— 已应用的迁移不重复跑（靠 schema_migrations 表记录）
//   3. 简单 SQLite DDL —— ALTER TABLE ADD COLUMN 在 modernc.org/sqlite 下可用，
//      但复杂变更（删列/改列类型）用 RENAME→CREATE→INSERT 保守方案
//   4. 版本号单调递增，只升不降 —— 不支持回滚（同 goose down 语义，但我们没 CLI）

// MigrationFunc 单次迁移函数（在事务中调用）
type MigrationFunc func(tx *sql.Tx) error

// migration 内部注册表
type migration struct {
	version int
	name    string
	up      MigrationFunc
}

var registry = []migration{}

// officialRegistry 是 migrations.go init() 里注册的正式迁移列表（供测试隔离恢复使用）
var officialRegistry = []migration{}

// resetForTest 把注册表恢复为官方正式迁移列表
func resetForTest() {
	registry = make([]migration, len(officialRegistry))
	copy(registry, officialRegistry)
}

// RegisterMigration 注册迁移（用 init() 自动执行，版本号必须单调递增）
func RegisterMigration(version int, name string, up MigrationFunc) {
	if len(registry) > 0 && version <= registry[len(registry)-1].version {
		panic(fmt.Sprintf("migrations must be registered in strictly increasing order: got version=%d, last=%d",
			version, registry[len(registry)-1].version))
	}
	registry = append(registry, migration{version: version, name: name, up: up})
}

// RunMigrations 执行所有未应用的迁移。
// 内部保证原子性：每个迁移在独立事务里跑；schema_migrations 表记录成功版本。
// 首次启动自动创建 schema_migrations 表。
func RunMigrations(db *sql.DB) error {
	// 1) 确保 schema_migrations 表存在
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL DEFAULT '',
		applied_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// 2) 读当前已应用的最高版本
	var currentVersion int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("query current version: %w", err)
	}

	// 3) 逐个跑未应用的迁移
	for _, m := range registry {
		if m.version <= currentVersion {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin v%d: %w", m.version, err)
		}
		if err := m.up(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration v%d %q failed: %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
			m.version, m.name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration v%d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v%d: %w", m.version, err)
		}
	}
	return nil
}

// CurrentMigrationVersion 返回已应用的最高迁移版本（测试/诊断用）。0 表示没有任何迁移跑过。
func CurrentMigrationVersion(db *sql.DB) int {
	var v int
	_ = db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v
}

// ==================== 具体迁移版本 ====================

func init() {
	// ---------- v001: 初始 schema（files + folders + strm_exec_history + 索引） ----------
	RegisterMigration(1, "initial_schema", func(tx *sql.Tx) error {
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
			if _, err := tx.Exec(s); err != nil {
				return fmt.Errorf("exec %s: %w", s, err)
			}
		}
		return nil
	})
	// 保存官方注册快照给测试隔离恢复用
	officialRegistry = make([]migration, len(registry))
	copy(officialRegistry, registry)

	// ---------- v002+: 未来迁移在这里追加，版本号必须 > 1 ----------
	// 示例（注释掉，等真正需要时启用）：
	// RegisterMigration(2, "add_source_path_to_strm_history", func(tx *sql.Tx) error {
	//     // SQLite ALTER TABLE ADD COLUMN 支持新列，但默认值必须是常量
	//     // 如果需要非平凡默认值或改列类型 → 用 RENAME→CREATE→INSERT 保守方案
	//     _, err := tx.Exec(`ALTER TABLE strm_exec_history ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`)
	//     return err
	// })
}
