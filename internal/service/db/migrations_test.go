package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// 每个测试开头都 resetForTest()，结尾不保留 —— Go 测试顺序导致后续测试也能隔离

func newTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := OpenNew(dir)
	if err != nil {
		t.Fatalf("OpenNew err: %v", err)
	}
	return db, dir
}

// --- T10.1 新库首次启动：v1 自动应用，5 张表都存在 ---
func TestMigrate_FreshDB_Version1Applied(t *testing.T) {
	// 保留 migrations.go init() 注册的官方 v1
	db, _ := newTestDB(t)
	defer db.Close()

	var cnt int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&cnt); err != nil {
		t.Fatalf("schema_migrations missing: %v", err)
	}
	if cnt != 1 {
		t.Errorf("want 1, got %d", cnt)
	}
	if CurrentMigrationVersion(db) != 1 {
		t.Errorf("want v1, got v%d", CurrentMigrationVersion(db))
	}

	for _, tbl := range []string{"files", "folders", "strm_exec_history", "schema_migrations"} {
		var c int
		_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&c)
		if c != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}

	_, err := db.Exec(
		`INSERT INTO files (account, file_id, path, file_name, pickcode, update_time) VALUES (?, ?, ?, ?, ?, ?)`,
		"alice", 12345, "/电影/漫威", "复联4.mkv", "AbCdEf12345678901", 1700000000,
	)
	if err != nil {
		t.Fatalf("insert files: %v", err)
	}
}

// --- T10.2 幂等：重启同一个库不重复跑迁移 ---
func TestMigrate_Idempotent_NoDupExec(t *testing.T) {
	db, dir := newTestDB(t)
	v1 := CurrentMigrationVersion(db)
	_, _ = db.Exec(`INSERT INTO files (account, file_id, path, file_name) VALUES ('a',1,'/x','f')`)
	db.Close()

	Close() // 清空单例
	db2, err := OpenNew(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	if CurrentMigrationVersion(db2) != v1 {
		t.Errorf("version changed? %d -> %d", v1, CurrentMigrationVersion(db2))
	}
	var cnt int
	_ = db2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("want still 1 record, got %d", cnt)
	}
	_ = db2.QueryRow(`SELECT COUNT(*) FROM files WHERE file_id=1`).Scan(&cnt)
	if cnt != 1 {
		t.Error("data lost after reopen")
	}
}

// --- T10.3 RegisterMigration 单调递增保护：重复版本 panic ---
func TestMigrate_RejectsNonMonotonic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		} else {
			t.Logf("correctly panicked: %v", r)
		}
	}()
	// migrations.go init() 已注册 v1，再注册 v0 应 panic
	RegisterMigration(0, "dup", func(tx *sql.Tx) error { return nil })
}

// --- T10.4 事务回滚：迁移函数失败 → schema_migrations 不记录 + 副作用表不泄漏 ---
func TestMigrate_FailedTransactionRollback(t *testing.T) {
	resetForTest()
	defer resetForTest() // 恢复官方注册表给后续测试

	RegisterMigration(100, "fake_initial", func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE fake_tbl (x INTEGER)`)
		return err
	})
	RegisterMigration(999, "failing", func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE should_not_exist (y INTEGER)`); err != nil {
			return err
		}
		return sql.ErrNoRows // 故意失败
	})

	dir := t.TempDir()
	// 直接用 sql.Open，不走 OpenNew（它会跑 migrations.go init() 注册的 v1）
	db, err := sql.Open("sqlite", filepath.Join(dir, "filePathDb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = RunMigrations(db)
	if err == nil {
		t.Fatal("expected error")
	}
	t.Logf("failed as expected: %v", err)

	if CurrentMigrationVersion(db) != 100 {
		t.Errorf("v100 should be recorded, v999 should NOT. current=%d", CurrentMigrationVersion(db))
	}
	var c int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='should_not_exist'`).Scan(&c)
	if c != 0 {
		t.Error("transaction NOT rolled back — side-effect table leaked!")
	}
}

// --- T10.5 未来升级：老库 v1 → 新代码自动跑 v2 ---
func TestMigrate_FutureUpgrade_VersionAdvances(t *testing.T) {
	resetForTest()
	defer resetForTest()

	// 官方 v1 已在 registry（resetForTest 恢复），files 表会由它创建
	// 我们只加 v101 做 ALTER TABLE 模拟未来升级
	RegisterMigration(101, "add_sha1", func(tx *sql.Tx) error {
		_, err := tx.Exec(`ALTER TABLE files ADD COLUMN sha1 TEXT NOT NULL DEFAULT ''`)
		return err
	})

	dir := t.TempDir()
	db, err := OpenNew(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// v1 官方 + v101 自定义
	if CurrentMigrationVersion(db) != 101 {
		t.Fatalf("want v101, got v%d", CurrentMigrationVersion(db))
	}

	_, err = db.Exec(`INSERT INTO files (account, file_id, path, file_name, sha1) VALUES ('a', 999, '/x', 'f.mkv', 'abc123')`)
	if err != nil {
		t.Errorf("insert with sha1 failed — v101 not applied: %v", err)
	}
}
