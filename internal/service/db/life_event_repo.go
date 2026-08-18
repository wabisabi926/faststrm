package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LifeEvent 增量监控事件（对应 TS life_event_repo / folder_changed_events）
type LifeEvent struct {
	ID          int64  `json:"id"`
	Account     string `json:"account"`
	EventType   string `json:"eventType"` // folderChanged / fileRenamed / fileRemoved ...
	FolderPath  string `json:"folderPath,omitempty"`
	PayloadJSON string `json:"payload,omitempty"`
	Processed   int    `json:"processed"` // 0 / 1
	CreatedAt   int64  `json:"createdAt"`
}

const sqlLifeEventSchema = `
CREATE TABLE IF NOT EXISTS life_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account TEXT NOT NULL,
  event_type TEXT NOT NULL,
  folder_path TEXT,
  payload_json TEXT,
  processed INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_life_event_account_processed ON life_events(account, processed);
CREATE INDEX IF NOT EXISTS idx_life_event_created ON life_events(created_at);
`

// LifeEventRepo 生命周期事件库（增量监控的落盘/重放）
type LifeEventRepo struct {
	db *sql.DB
}

// NewLifeEventRepo 初始化
func NewLifeEventRepo(db *sql.DB) (*LifeEventRepo, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if _, err := db.ExecContext(context.Background(), sqlLifeEventSchema); err != nil {
		return nil, fmt.Errorf("init life_events: %w", err)
	}
	return &LifeEventRepo{db: db}, nil
}

// Insert 写入事件
func (r *LifeEventRepo) Insert(ctx context.Context, e LifeEvent) (int64, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO life_events(account, event_type, folder_path, payload_json, processed, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		e.Account, e.EventType, e.FolderPath, e.PayloadJSON, e.Processed, e.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListPending 取未处理的事件（FIFO）
func (r *LifeEventRepo) ListPending(ctx context.Context, account string, limit int) ([]LifeEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	var (
		rows any
		err  error
	)
	if account == "" {
		rows, err = r.db.QueryContext(ctx, `SELECT id, account, event_type, folder_path, payload_json, processed, created_at
FROM life_events WHERE processed = 0 ORDER BY created_at ASC, id ASC LIMIT ?`, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `SELECT id, account, event_type, folder_path, payload_json, processed, created_at
FROM life_events WHERE account = ? AND processed = 0 ORDER BY created_at ASC, id ASC LIMIT ?`, account, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanLifeEvents(rows)
}

// MarkProcessed 批量标记
func (r *LifeEventRepo) MarkProcessed(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	// 简单循环：id 数量不大（< 200），避免写复杂 IN 拼接
	for _, id := range ids {
		if _, err := r.db.ExecContext(ctx, `UPDATE life_events SET processed = 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// PurgeOld 删除 created_at < beforeMs 的 processed 事件
func (r *LifeEventRepo) PurgeOld(ctx context.Context, beforeMs int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM life_events WHERE processed = 1 AND created_at < ?`, beforeMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanLifeEvents(rows any) ([]LifeEvent, error) {
	// 简化：利用 SQLite DB 统一 QueryContext 返回的是 *sql.Rows
	type rowScanner interface {
		Next() bool
		Scan(dest ...any) error
		Close() error
		Err() error
	}
	rs, ok := rows.(rowScanner)
	if !ok {
		return nil, errors.New("invalid rows type")
	}
	defer rs.Close()
	var out []LifeEvent
	for rs.Next() {
		var e LifeEvent
		if err := rs.Scan(&e.ID, &e.Account, &e.EventType, &e.FolderPath, &e.PayloadJSON, &e.Processed, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rs.Err()
}
