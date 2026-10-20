package handler

import (
	"database/sql"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/monitor"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// AccountCascadeDeps 账号改名 / 删除时的引用联动依赖。
// 所有字段均可为 nil：缺省时跳过对应联动，便于测试与渐进接入。
type AccountCascadeDeps struct {
	// TasksStore 任务持久化存储（任务按账户名绑定）
	TasksStore *store.TasksStore
	// SettingsStore 全局设置（生活监控 accounts / pathMappings 按账户名绑定）
	SettingsStore *store.SettingsStore
	// SQLiteDB 文件索引库（files / folders / life_event_cursor 按账户名分区）
	SQLiteDB *sql.DB
	// Monitor 生活监控运行时（改名后刷新按新名重启）
	Monitor *monitor.Monitor
}

// AccountRefs 账号引用情况快照（删除前检查用）。
// 空值表示无引用；JSON 字段均 omitempty，前端可按存在性渲染。
type AccountRefs struct {
	Tasks           []string `json:"tasks,omitempty"`           // 引用该账号的任务（名称或 id）
	MonitorAccounts bool     `json:"monitorAccounts,omitempty"` // 生活监控 accounts 列表引用
	MonitorMappings int      `json:"monitorMappings,omitempty"` // 生活监控路径映射引用数
	DbFileRecords   int      `json:"dbFileRecords,omitempty"`   // SQLite files 表记录数
	DbFolderRecords int      `json:"dbFolderRecords,omitempty"` // SQLite folders 表记录数
}

// HasRefs 是否存在任何引用
func (r AccountRefs) HasRefs() bool {
	return len(r.Tasks) > 0 || r.MonitorAccounts || r.MonitorMappings > 0 ||
		r.DbFileRecords > 0 || r.DbFolderRecords > 0
}

// CollectAccountRefs 统计对指定账号的引用（删除前检查）。
// 任一依赖为 nil 时跳过对应统计；查询失败仅记日志不阻塞（宁漏勿拒）。
func CollectAccountRefs(name string, deps *AccountCascadeDeps) AccountRefs {
	var refs AccountRefs
	if deps == nil || name == "" {
		return refs
	}
	if deps.TasksStore != nil {
		tasks, err := deps.TasksStore.ReadTasks()
		if err != nil {
			logger.S().Warnf("[accountRefs] 读取任务失败: %v", err)
		} else {
			for _, t := range tasks {
				if t.Account == name {
					label := t.ID
					if t.Name != "" {
						label = t.Name + "（" + t.ID + "）"
					}
					refs.Tasks = append(refs.Tasks, label)
				}
			}
		}
	}
	if deps.SettingsStore != nil {
		if s, err := deps.SettingsStore.ReadSettings(); err != nil {
			logger.S().Warnf("[accountRefs] 读取设置失败: %v", err)
		} else {
			for _, a := range s.LifeMonitor.Accounts {
				if a == name {
					refs.MonitorAccounts = true
					break
				}
			}
			for _, pm := range s.LifeMonitor.PathMappings {
				if pm.Account == name {
					refs.MonitorMappings++
				}
			}
		}
	}
	if deps.SQLiteDB != nil {
		_ = deps.SQLiteDB.QueryRow(`SELECT COUNT(*) FROM files WHERE account = ?`, name).Scan(&refs.DbFileRecords)
		_ = deps.SQLiteDB.QueryRow(`SELECT COUNT(*) FROM folders WHERE account = ?`, name).Scan(&refs.DbFolderRecords)
	}
	return refs
}

// CascadeAccountRename 账号改名后的引用联动：
// 把任务 / 生活监控配置 / SQLite 分区中的旧账户名同步为新名，并刷新监控。
// 返回联动摘要（供 API 回显）；单项失败仅记日志，不回滚账号改名。
func CascadeAccountRename(oldName, newName string, deps *AccountCascadeDeps) map[string]any {
	summary := map[string]any{"oldName": oldName, "newName": newName}
	if deps == nil || oldName == "" || newName == "" || oldName == newName {
		return summary
	}

	// 1) 任务（.tasks.json）
	if deps.TasksStore != nil {
		tasks, err := deps.TasksStore.ReadTasks()
		if err != nil {
			logger.S().Warnf("[accountCascade] 读取任务失败: %v", err)
		} else {
			updated := 0
			now := time.Now().UnixMilli()
			for i := range tasks {
				if tasks[i].Account == oldName {
					tasks[i].Account = newName
					tasks[i].UpdatedAt = now
					updated++
				}
			}
			if updated > 0 {
				if err := deps.TasksStore.SaveTasks(tasks); err != nil {
					logger.S().Warnf("[accountCascade] 保存任务失败: %v", err)
				} else {
					summary["tasksUpdated"] = updated
				}
			}
		}
	}

	// 2) 生活监控配置（accounts + pathMappings）
	if deps.SettingsStore != nil {
		updated := false
		_, err := deps.SettingsStore.Update(func(s *model.Settings) error {
			changed := false
			for i, a := range s.LifeMonitor.Accounts {
				if a == oldName {
					s.LifeMonitor.Accounts[i] = newName
					changed = true
				}
			}
			for i := range s.LifeMonitor.PathMappings {
				if s.LifeMonitor.PathMappings[i].Account == oldName {
					s.LifeMonitor.PathMappings[i].Account = newName
					changed = true
				}
			}
			updated = changed
			return nil
		})
		if err != nil {
			logger.S().Warnf("[accountCascade] 保存设置失败: %v", err)
		} else if updated {
			summary["monitorConfigUpdated"] = true
		}
	}

	// 3) SQLite 账号分区（files / folders / life_event_cursor）
	if counts, err := db.RenameAccountData(deps.SQLiteDB, oldName, newName); err != nil {
		logger.S().Warnf("[accountCascade] 迁移 SQLite 账号数据出错: %v", err)
		if len(counts) > 0 {
			summary["dbRecordsRenamed"] = counts
		}
	} else if len(counts) > 0 {
		summary["dbRecordsRenamed"] = counts
	}

	// 4) 监控运行时：停掉旧名，Refresh 会按配置里的新名重新启动
	if deps.Monitor != nil {
		deps.Monitor.Stop(oldName)
		deps.Monitor.Refresh()
		summary["monitorRefreshed"] = true
	}

	return summary
}
