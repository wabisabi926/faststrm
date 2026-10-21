package db

import "database/sql"

// RenameAccountData 账号改名后，把按账户名分区的本地数据整体迁移到新账号名。
// 覆盖：files / folders（115 文件索引，PRIMARY KEY (account, file_id)）
// 与 life_event_cursor（生活事件游标，account 为主键）。
// 事件与执行历史（life_events / strm_exec_history）保留原账号名，作为历史事实不回溯。
//
// 使用 UPDATE OR REPLACE：目标名下若已有同键记录（例如"删除后又建同名"的残留），
// 冲突行先删后写，保证迁移不因唯一键冲突失败。
// 返回各表实际迁移行数；某表不存在或失败时跳过并记录首个错误（不影响其余表）。
func RenameAccountData(dbConn *sql.DB, oldName, newName string) (map[string]int64, error) {
	counts := map[string]int64{}
	if dbConn == nil || oldName == "" || newName == "" || oldName == newName {
		return counts, nil
	}
	tables := []struct {
		name  string
		query string
	}{
		{"files", `UPDATE OR REPLACE files SET account = ? WHERE account = ?`},
		{"folders", `UPDATE OR REPLACE folders SET account = ? WHERE account = ?`},
		{"life_event_cursor", `UPDATE OR REPLACE life_event_cursor SET account = ? WHERE account = ?`},
		// 托管记录是实时所有权状态，不是历史事实；不改会继续用旧账号做云端复核。
		{"managed_strms", `UPDATE OR REPLACE managed_strms SET account = ? WHERE account = ?`},
	}
	var firstErr error
	for _, t := range tables {
		res, err := dbConn.Exec(t.query, newName, oldName)
		if err != nil {
			// 老库可能尚未创建该表（如从未启用生活监控）→ 跳过但保留首个错误供日志排查
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n, _ := res.RowsAffected()
		counts[t.name] = n
	}
	return counts, firstErr
}
