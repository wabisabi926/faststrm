// Package db P0-4 STRM 执行历史回滚
// 对齐参考项目 core/history/strm.py：记录每次 STRM 生成/删除的细粒度历史，
// 用于失败定位、API 配额监控、局部重试依据。限 500 条循环覆盖。
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// StrmHistoryKind STRM 执行历史操作类型
type StrmHistoryKind string

const (
	StrmHistoryKindFull     StrmHistoryKind = "full"     // 全量同步
	StrmHistoryKindIncrement StrmHistoryKind = "increment" // 增量同步
	StrmHistoryKindMonitor  StrmHistoryKind = "monitor"  // 生活事件 STRM 生成
	StrmHistoryKindRename   StrmHistoryKind = "rename"   // 重命名 STRM
	StrmHistoryKindMove     StrmHistoryKind = "move"     // 移动 STRM
	StrmHistoryKindDelete   StrmHistoryKind = "delete"   // 删除 STRM
)

// StrmHistoryEntry 单条 STRM 执行历史记录
type StrmHistoryEntry struct {
	ID           int64           `json:"id"`
	TaskID       string          `json:"taskId"`
	Kind         StrmHistoryKind `json:"kind"`
	Account      string          `json:"account"`
	Success      bool            `json:"success"`
	TotalFiles   int             `json:"totalFiles"`
	SuccessFiles int             `json:"successFiles"`
	FailedFiles  int             `json:"failedFiles"`
	ElapsedMs    int64           `json:"elapsedMs"`
	APIRequests  int             `json:"apiRequests"`
	ErrorMsg     string          `json:"errorMsg,omitempty"`
	CreatedAt    int64           `json:"createdAt"` // unix ms
}

// InsertStrmHistory 插入一条 STRM 执行历史，并清理超过上限的旧记录。
// 失败不阻断主流程，仅返回 error 供调用方日志。
func InsertStrmHistory(db *sql.DB, entry StrmHistoryEntry) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("db is nil")
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = time.Now().UnixMilli()
	}
	res, err := db.Exec(`
		INSERT INTO strm_exec_history (
			task_id, kind, account, success,
			total_files, success_files, failed_files,
			elapsed_ms, api_requests, error_msg, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.TaskID, string(entry.Kind), entry.Account, boolToInt(entry.Success),
		entry.TotalFiles, entry.SuccessFiles, entry.FailedFiles,
		entry.ElapsedMs, entry.APIRequests, entry.ErrorMsg, entry.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert strm_exec_history: %w", err)
	}
	id, _ := res.LastInsertId()

	// 清理超过上限的旧记录：删除最早 (count - max) 条
	if _, derr := db.Exec(`
		DELETE FROM strm_exec_history
		WHERE id IN (
			SELECT id FROM strm_exec_history
			ORDER BY created_at DESC
			LIMIT -1 OFFSET ?
		)`, StrmHistoryMaxRecords); derr != nil {
		// 清理失败不影响插入结果，仅日志
		return id, fmt.Errorf("trim strm_exec_history: %w", derr)
	}
	return id, nil
}

// ListStrmHistory 查询 STRM 执行历史
//   - kind 为空时查所有类型
//   - taskID 为空时不按任务过滤
//   - limit <= 0 时默认 100，上限 500
// 按 created_at DESC 排序
func ListStrmHistory(db *sql.DB, kind, taskID string, limit int) ([]StrmHistoryEntry, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if limit <= 0 || limit > StrmHistoryMaxRecords {
		limit = 100
	}

	var (
		q    strings.Builder
		args []any
	)
	q.WriteString(`
		SELECT id, task_id, kind, account, success,
		       total_files, success_files, failed_files,
		       elapsed_ms, api_requests, error_msg, created_at
		FROM strm_exec_history WHERE 1=1`)
	if kind != "" {
		q.WriteString(" AND kind = ?")
		args = append(args, kind)
	}
	if taskID != "" {
		q.WriteString(" AND task_id = ?")
		args = append(args, taskID)
	}
	q.WriteString(" ORDER BY created_at DESC LIMIT ?")
	args = append(args, limit)

	rows, err := db.Query(q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query strm_exec_history: %w", err)
	}
	defer rows.Close()

	var out []StrmHistoryEntry
	for rows.Next() {
		var (
			e            StrmHistoryEntry
			successInt   int
			kindStr      string
			errMsg       sql.NullString
		)
		if err := rows.Scan(
			&e.ID, &e.TaskID, &kindStr, &e.Account, &successInt,
			&e.TotalFiles, &e.SuccessFiles, &e.FailedFiles,
			&e.ElapsedMs, &e.APIRequests, &errMsg, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan strm_exec_history: %w", err)
		}
		e.Kind = StrmHistoryKind(kindStr)
		e.Success = successInt != 0
		e.ErrorMsg = errMsg.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetStrmHistoryByID 按 ID 查单条 STRM 执行历史
func GetStrmHistoryByID(db *sql.DB, id int64) (*StrmHistoryEntry, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	var (
		e          StrmHistoryEntry
		successInt int
		kindStr    string
		errMsg     sql.NullString
	)
	err := db.QueryRow(`
		SELECT id, task_id, kind, account, success,
		       total_files, success_files, failed_files,
		       elapsed_ms, api_requests, error_msg, created_at
		FROM strm_exec_history WHERE id = ?`, id,
	).Scan(
		&e.ID, &e.TaskID, &kindStr, &e.Account, &successInt,
		&e.TotalFiles, &e.SuccessFiles, &e.FailedFiles,
		&e.ElapsedMs, &e.APIRequests, &errMsg, &e.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query strm_exec_history by id: %w", err)
	}
	e.Kind = StrmHistoryKind(kindStr)
	e.Success = successInt != 0
	e.ErrorMsg = errMsg.String
	return &e, nil
}

// StrmHistoryStats 统计聚合
type StrmHistoryStats struct {
	TotalCount   int64 `json:"totalCount"`
	SuccessCount int64 `json:"successCount"`
	FailedCount  int64 `json:"failedCount"`
	TotalFiles   int64 `json:"totalFiles"`
	SuccessFiles int64 `json:"successFiles"`
	FailedFiles  int64 `json:"failedFiles"`
	TotalAPIReqs int64 `json:"totalApiRequests"`
	TotalElapsedMs int64 `json:"totalElapsedMs"`
}

// GetStrmHistoryStats 按 kind 聚合统计（kind 为空时聚合全部）
func GetStrmHistoryStats(db *sql.DB, kind string) (*StrmHistoryStats, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	q := `
		SELECT COUNT(*) AS total,
		       SUM(CASE WHEN success=1 THEN 1 ELSE 0 END) AS ok,
		       SUM(CASE WHEN success=0 THEN 1 ELSE 0 END) AS fail,
		       COALESCE(SUM(total_files), 0),
		       COALESCE(SUM(success_files), 0),
		       COALESCE(SUM(failed_files), 0),
		       COALESCE(SUM(api_requests), 0),
		       COALESCE(SUM(elapsed_ms), 0)
		FROM strm_exec_history`
	var args []any
	if kind != "" {
		q += " WHERE kind = ?"
		args = append(args, kind)
	}
	var s StrmHistoryStats
	var ok, fail sql.NullInt64
	err := db.QueryRow(q, args...).Scan(
		&s.TotalCount, &ok, &fail,
		&s.TotalFiles, &s.SuccessFiles, &s.FailedFiles,
		&s.TotalAPIReqs, &s.TotalElapsedMs,
	)
	if err != nil {
		return nil, fmt.Errorf("query strm_exec_history stats: %w", err)
	}
	s.SuccessCount = ok.Int64
	s.FailedCount = fail.Int64
	return &s, nil
}

// boolToInt SQLite 无布尔类型，用 INTEGER 0/1 存储
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
