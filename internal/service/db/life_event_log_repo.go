package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ==================== LifeEventLog（事件处理日志，区别于 life_events 待处理队列） ====================

const sqlLifeEventLogSchema = `
CREATE TABLE IF NOT EXISTS life_event_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp INTEGER NOT NULL,
  account TEXT NOT NULL,
  event_type TEXT NOT NULL,             -- create / delete / move / rename / folder-sync
  success INTEGER NOT NULL DEFAULT 0,   -- 0=fail, 1=success
  file_path TEXT,
  local_path TEXT,
  message TEXT,
  file_id TEXT,
  pick_code TEXT,
  strm_content TEXT,
  old_local_full_path TEXT,
  new_local_full_path TEXT,
  extra_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_lel_account ON life_event_logs(account);
CREATE INDEX IF NOT EXISTS idx_lel_event_type ON life_event_logs(event_type);
CREATE INDEX IF NOT EXISTS idx_lel_success ON life_event_logs(success);
CREATE INDEX IF NOT EXISTS idx_lel_timestamp ON life_event_logs(timestamp);
`

// LifeEventLog 事件处理日志记录（对齐 TS lifeEventLogManager.ts LifeEventLog）
type LifeEventLog struct {
	ID               int64  `json:"id"`
	Timestamp        int64  `json:"timestamp"`
	Account          string `json:"account"`
	EventType        string `json:"eventType"`
	Success          bool   `json:"success"`
	FilePath         string `json:"filePath,omitempty"`
	LocalPath        string `json:"localPath,omitempty"`
	Message          string `json:"message,omitempty"`
	FileID           string `json:"fileId,omitempty"`
	PickCode         string `json:"pickCode,omitempty"`
	StrmContent      string `json:"strmContent,omitempty"`
	OldLocalFullPath string `json:"oldLocalFullPath,omitempty"`
	NewLocalFullPath string `json:"newLocalFullPath,omitempty"`
}

// LifeEventLogRepo 事件日志仓库（非队列，是处理结果的历史记录）
type LifeEventLogRepo struct {
	db *sql.DB
}

// NewLifeEventLogRepo 创建日志仓库（同时初始化表）
func NewLifeEventLogRepo(db *sql.DB) (*LifeEventLogRepo, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if _, err := db.ExecContext(context.Background(), sqlLifeEventLogSchema); err != nil {
		return nil, fmt.Errorf("init life_event_logs: %w", err)
	}
	return &LifeEventLogRepo{db: db}, nil
}

// AppendLog 追加一条事件处理日志
func (r *LifeEventLogRepo) AppendLog(ctx context.Context, log LifeEventLog) (int64, error) {
	if log.Timestamp == 0 {
		log.Timestamp = NowMs()
	}
	successInt := 0
	if log.Success {
		successInt = 1
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO life_event_logs
(timestamp, account, event_type, success, file_path, local_path, message, file_id, pick_code, strm_content, old_local_full_path, new_local_full_path)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.Timestamp, log.Account, log.EventType, successInt,
		log.FilePath, log.LocalPath, log.Message, log.FileID, log.PickCode,
		log.StrmContent, log.OldLocalFullPath, log.NewLocalFullPath)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LifeEventLogQuery 查询条件
type LifeEventLogQuery struct {
	Account   string
	EventType string // create/delete/move/rename/folder-sync
	Success   *bool  // nil=不过滤; true/false=精确过滤
	SinceMs   int64  // >= SinceMs
	UntilMs   int64  // < UntilMs
	Limit     int
	Offset    int
}

// Query 查询事件日志（按时间倒序）
func (r *LifeEventLogRepo) Query(ctx context.Context, q LifeEventLogQuery) ([]LifeEventLog, error) {
	where := []string{"1=1"}
	args := []any{}
	if q.Account != "" {
		where = append(where, "account = ?")
		args = append(args, q.Account)
	}
	if q.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, q.EventType)
	}
	if q.Success != nil {
		si := 0
		if *q.Success {
			si = 1
		}
		where = append(where, "success = ?")
		args = append(args, si)
	}
	if q.SinceMs > 0 {
		where = append(where, "timestamp >= ?")
		args = append(args, q.SinceMs)
	}
	if q.UntilMs > 0 {
		where = append(where, "timestamp < ?")
		args = append(args, q.UntilMs)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	sql := fmt.Sprintf(`SELECT id, timestamp, account, event_type, success, file_path, local_path, message, file_id, pick_code, strm_content, old_local_full_path, new_local_full_path
FROM life_event_logs WHERE %s ORDER BY timestamp DESC, id DESC LIMIT ? OFFSET ?`, join(where, " AND "))
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LifeEventLog
	for rows.Next() {
		var (
			log         LifeEventLog
			successInt  int
			filePath    *string
			localPath   *string
			message     *string
			fileID      *string
			pickCode    *string
			strmContent *string
			oldPath     *string
			newPath     *string
		)
		if err := rows.Scan(&log.ID, &log.Timestamp, &log.Account, &log.EventType, &successInt,
			&filePath, &localPath, &message, &fileID, &pickCode, &strmContent, &oldPath, &newPath); err != nil {
			return nil, err
		}
		log.Success = successInt == 1
		if filePath != nil {
			log.FilePath = *filePath
		}
		if localPath != nil {
			log.LocalPath = *localPath
		}
		if message != nil {
			log.Message = *message
		}
		if fileID != nil {
			log.FileID = *fileID
		}
		if pickCode != nil {
			log.PickCode = *pickCode
		}
		if strmContent != nil {
			log.StrmContent = *strmContent
		}
		if oldPath != nil {
			log.OldLocalFullPath = *oldPath
		}
		if newPath != nil {
			log.NewLocalFullPath = *newPath
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

// DeleteByID 按 ID 删除单条日志
func (r *LifeEventLogRepo) DeleteByID(ctx context.Context, id int64) (bool, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM life_event_logs WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CleanupOld 清理指定时间之前的日志
func (r *LifeEventLogRepo) CleanupOld(ctx context.Context, beforeMs int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM life_event_logs WHERE timestamp < ?`, beforeMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearAll 清空所有日志
func (r *LifeEventLogRepo) ClearAll(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM life_event_logs`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Stats 返回统计信息（总数 + 成功数 + 失败数）
func (r *LifeEventLogRepo) Stats(ctx context.Context) (total, success, failed int64, err error) {
	row := r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN success=1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END), 0) FROM life_event_logs`)
	if err = row.Scan(&total, &success, &failed); err != nil {
		return 0, 0, 0, err
	}
	return
}

// MarshalJSON 自定义序列化：将 Success bool 序列化为 JSON bool
func (l LifeEventLog) MarshalJSON() ([]byte, error) {
	type Alias LifeEventLog
	return json.Marshal(&struct {
		Alias
		Success bool `json:"success"`
	}{
		Alias:  Alias(l),
		Success: l.Success,
	})
}
