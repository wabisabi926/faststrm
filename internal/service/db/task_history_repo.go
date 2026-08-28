package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"modernc.org/sqlite"
)

// ==================== 表结构（首次打开自动建） ====================

const sqlTaskExecSchema = `
CREATE TABLE IF NOT EXISTS task_executions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  account TEXT,
  origin_path TEXT,
  target_path TEXT,
  status TEXT NOT NULL,             -- pending / running / completed / failed / cancelled
  started_at INTEGER,
  ended_at INTEGER,
  duration_ms INTEGER,
  summary_json TEXT,                -- {totalFiles, downloadedFiles, deletedFiles}
  error TEXT,
  extra_json TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_exec_task_id ON task_executions(task_id);
CREATE INDEX IF NOT EXISTS idx_task_exec_status ON task_executions(status);
CREATE INDEX IF NOT EXISTS idx_task_exec_created ON task_executions(created_at);
`

const sqlTaskLogsSchema = `
CREATE TABLE IF NOT EXISTS task_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_id INTEGER NOT NULL,
  seq INTEGER NOT NULL DEFAULT 0,
  line TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  FOREIGN KEY (execution_id) REFERENCES task_executions(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_task_logs_exec ON task_logs(execution_id);
CREATE INDEX IF NOT EXISTS idx_task_logs_created ON task_logs(created_at);
`

// TaskExecutionSummary 摘要 JSON
type TaskExecutionSummary struct {
	TotalFiles      int `json:"totalFiles,omitempty"`
	DownloadedFiles int `json:"downloadedFiles,omitempty"`
	DeletedFiles    int `json:"deletedFiles,omitempty"`
}

// TaskExecution 对应 task_executions
type TaskExecution struct {
	ID         int64                `json:"id"`
	TaskID     string               `json:"taskId"`
	Account    string               `json:"account,omitempty"`
	OriginPath string               `json:"originPath,omitempty"`
	TargetPath string               `json:"targetPath,omitempty"`
	Status     string               `json:"status"`
	StartedAt  int64                `json:"startedAt,omitempty"`
	EndedAt    int64                `json:"endedAt,omitempty"`
	DurationMs int64                `json:"durationMs,omitempty"`
	Summary    TaskExecutionSummary `json:"summary,omitempty"`
	Error      string               `json:"error,omitempty"`
	CreatedAt  int64                `json:"createdAt"`
}

// ==================== Repo ====================

// TaskHistoryRepo 任务历史 + 日志仓库
type TaskHistoryRepo struct {
	db *sql.DB
}

// NewTaskHistoryRepo 创建（同时初始化表）
func NewTaskHistoryRepo(db *sql.DB) (*TaskHistoryRepo, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if _, err := db.ExecContext(context.Background(), sqlTaskExecSchema); err != nil {
		return nil, fmt.Errorf("init task_executions: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), sqlTaskLogsSchema); err != nil {
		return nil, fmt.Errorf("init task_logs: %w", err)
	}
	return &TaskHistoryRepo{db: db}, nil
}

// CreateExecution 创建一条执行记录（返回新 id）
func (r *TaskHistoryRepo) CreateExecution(ctx context.Context, e TaskExecution) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	summaryB, _ := json.Marshal(e.Summary)
	res, err := r.db.ExecContext(ctx, `INSERT INTO task_executions
(task_id, account, origin_path, target_path, status, started_at, ended_at, duration_ms, summary_json, error, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TaskID, e.Account, e.OriginPath, e.TargetPath, e.Status,
		e.StartedAt, e.EndedAt, e.DurationMs, string(summaryB), e.Error, e.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateExecution 局部更新
func (r *TaskHistoryRepo) UpdateExecution(ctx context.Context, id int64, patch map[string]any) error {
	if r == nil || r.db == nil {
		return nil
	}
	if len(patch) == 0 {
		return nil
	}
	cols := make([]string, 0, len(patch))
	args := make([]any, 0, len(patch)+1)
	for k, v := range patch {
		cols = append(cols, k+" = ?")
		if k == "summary_json" {
			if b, err := json.Marshal(v); err == nil {
				v = string(b)
			}
		}
		args = append(args, v)
	}
	args = append(args, id)
	//nolint:gosec // G201 — 列名由代码内置集合构建，值通过 ExecContext 参数化
	q := fmt.Sprintf(`UPDATE task_executions SET %s WHERE id = ?`, join(cols, ", "))
	_, err := r.db.ExecContext(ctx, q, args...)
	return err
}

// CompleteExecution 快速标记完成（对应 TS completeTaskExecution）
func (r *TaskHistoryRepo) CompleteExecution(ctx context.Context, id int64, status string, summary TaskExecutionSummary, errStr string, durationMs int64) error {
	if r == nil || r.db == nil {
		return nil
	}
	patch := map[string]any{
		"status":       status,
		"summary_json": summary,
		"duration_ms":  durationMs,
	}
	if errStr != "" {
		patch["error"] = errStr
	}
	if summary.TotalFiles > 0 || summary.DownloadedFiles > 0 || summary.DeletedFiles > 0 {
		// 同时结束时间
		patch["ended_at"] = NowMs()
	}
	return r.UpdateExecution(ctx, id, patch)
}

// AddLog 追加一条日志
func (r *TaskHistoryRepo) AddLog(ctx context.Context, executionID int64, seq int, line string) error {
	if r == nil || r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO task_logs(execution_id, seq, line, created_at) VALUES (?, ?, ?, ?)`,
		executionID, seq, line, NowMs())
	return err
}

// List 按 taskId / account / status / time 分页查询
type TaskHistoryQuery struct {
	TaskID   string
	Account  string
	Status   string
	BeforeMs int64 // < BeforeMs
	Limit    int
}

// Query 查询执行历史
func (r *TaskHistoryRepo) Query(ctx context.Context, q TaskHistoryQuery) ([]TaskExecution, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	where := []string{"1=1"}
	args := []any{}
	if q.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, q.TaskID)
	}
	if q.Account != "" {
		where = append(where, "account = ?")
		args = append(args, q.Account)
	}
	if q.Status != "" {
		where = append(where, "status = ?")
		args = append(args, q.Status)
	}
	if q.BeforeMs > 0 {
		where = append(where, "created_at < ?")
		args = append(args, q.BeforeMs)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	//nolint:gosec // G201 — WHERE 子句由代码内置条件构建，值通过 QueryContext 参数化
	sql := fmt.Sprintf(`SELECT id, task_id, account, origin_path, target_path, status, started_at, ended_at, duration_ms, summary_json, error, created_at
FROM task_executions WHERE %s ORDER BY created_at DESC, id DESC LIMIT ?`, join(where, " AND "))
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskExecution
	for rows.Next() {
		var (
			e      TaskExecution
			sumStr *string
		)
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Account, &e.OriginPath, &e.TargetPath,
			&e.Status, &e.StartedAt, &e.EndedAt, &e.DurationMs, &sumStr, &e.Error, &e.CreatedAt); err != nil {
			return nil, err
		}
		if sumStr != nil && *sumStr != "" {
			_ = json.Unmarshal([]byte(*sumStr), &e.Summary)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetLogs 读取某执行的日志（按 seq asc，可 limit）
func (r *TaskHistoryRepo) GetLogs(ctx context.Context, executionID int64, limit int) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20000
	}
	rows, err := r.db.QueryContext(ctx, `SELECT line FROM task_logs WHERE execution_id = ? ORDER BY seq ASC, id ASC LIMIT ?`,
		executionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// IsErrConstraint 判重/约束冲突
func IsErrConstraint(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == 19 // SQLITE_CONSTRAINT
}
