package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== .tasks.json 持久化结构 ====================

// persistedTask 磁盘格式：保持字段兼容 TS
type persistedTask struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name,omitempty"`
	Account             string                 `json:"account"`
	AccountType         string                 `json:"accountType,omitempty"`
	OriginPath          string                 `json:"originPath"`
	TargetPath          string                 `json:"targetPath"`
	StrmPrefix          string                 `json:"strmPrefix,omitempty"`
	EnablePathEncoding  bool                   `json:"enablePathEncoding,omitempty"`
	Enable302           bool                   `json:"enable302,omitempty"`
	RemoveExtraFiles    bool                   `json:"removeExtraFiles,omitempty"`
	Schedule            *task.TaskSchedule     `json:"schedule,omitempty"`
	CreatedAt           int64                  `json:"createdAt,omitempty"`
	UpdatedAt           int64                  `json:"updatedAt,omitempty"`
	// 历史遗留字段（兼容 TS JSON）：允许以 Raw 存在
	Extra map[string]json.RawMessage `json:"-"`
}

// TasksStore 读写 tasks.json
type TasksStore struct {
	mu   sync.Mutex
	path string // tasks.json 绝对路径
	cfg  *configProvider
}

// NewTasksStore 基于配置目录创建
func NewTasksStore(configDir string) *TasksStore {
	return &TasksStore{
		path: filepath.Join(configDir, "tasks.json"),
		cfg:  &configProvider{dir: configDir},
	}
}

// ReadTasks 读取任务列表
func (s *TasksStore) ReadTasks() ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, statErr := os.Stat(s.path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			if werr := ensureDir(filepath.Dir(s.path)); werr != nil {
				logger.S().Warnf("[ReadTasks] ensureDir failed: %v", werr)
				return []task.Task{}, nil
			}
			if werr := os.WriteFile(s.path, []byte("[]\n"), 0o600); werr != nil {
				logger.S().Warnf("[ReadTasks] create default file failed: %v", werr)
				return []task.Task{}, nil
			}
			return []task.Task{}, nil
		}
		logger.S().Warnf("[ReadTasks] stat failed: %v", statErr)
		return []task.Task{}, nil
	}
	if info.IsDir() {
		logger.S().Warnf("[ReadTasks] path is a directory: %s", s.path)
		return []task.Task{}, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		logger.S().Warnf("[ReadTasks] read failed: %v", err)
		return []task.Task{}, nil
	}
	var persisted []persistedTask
	if len(bytesTrimSpace(raw)) == 0 {
		return []task.Task{}, nil
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		logger.S().Warnf("[ReadTasks] json unmarshal failed: %v, returning empty list", err)
		return []task.Task{}, nil
	}
	out := make([]task.Task, 0, len(persisted))
	for _, p := range persisted {
		out = append(out, task.Task{
			ID:                 p.ID,
			Name:               p.Name,
			Account:            p.Account,
			AccountType:        p.AccountType,
			OriginPath:         p.OriginPath,
			TargetPath:         p.TargetPath,
			StrmPrefix:         p.StrmPrefix,
			EnablePathEncoding: p.EnablePathEncoding,
			Enable302:          p.Enable302,
			RemoveExtraFiles:   p.RemoveExtraFiles,
			Schedule:           p.Schedule,
			CreatedAt:          p.CreatedAt,
			UpdatedAt:          p.UpdatedAt,
		})
	}
	return out, nil
}

// SaveTasks 覆盖写回 .tasks.json（原子写：tmp + rename）
func (s *TasksStore) SaveTasks(tasks []task.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ensureDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	out := make([]persistedTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, persistedTask{
			ID:                 t.ID,
			Name:               t.Name,
			Account:            t.Account,
			AccountType:        t.AccountType,
			OriginPath:         t.OriginPath,
			TargetPath:         t.TargetPath,
			StrmPrefix:         t.StrmPrefix,
			EnablePathEncoding: t.EnablePathEncoding,
			Enable302:          t.Enable302,
			RemoveExtraFiles:   t.RemoveExtraFiles,
			Schedule:           t.Schedule,
			CreatedAt:          t.CreatedAt,
			UpdatedAt:          t.UpdatedAt,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if werr := os.WriteFile(tmp, b, 0o600); werr != nil {
		return werr
	}
	if rerr := os.Rename(tmp, s.path); rerr != nil {
		_ = os.Remove(tmp)
		return rerr
	}
	return nil
}

// UpsertTask 创建或更新单个任务
func (s *TasksStore) UpsertTask(t task.Task) error {
	if t.ID == "" {
		return errors.New("task id required")
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().UnixMilli()
	}
	t.UpdatedAt = time.Now().UnixMilli()

	tasks, err := s.ReadTasks()
	if err != nil {
		return err
	}
	found := false
	for i := range tasks {
		if tasks[i].ID == t.ID {
			// 保留 createdAt，其他覆盖
			if tasks[i].CreatedAt > 0 {
				t.CreatedAt = tasks[i].CreatedAt
			}
			tasks[i] = t
			found = true
			break
		}
	}
	if !found {
		tasks = append(tasks, t)
	}
	return s.SaveTasks(tasks)
}

// DeleteTask 按 id 删除
func (s *TasksStore) DeleteTask(id string) (bool, error) {
	tasks, err := s.ReadTasks()
	if err != nil {
		return false, err
	}
	out := tasks[:0]
	deleted := false
	for _, t := range tasks {
		if t.ID == id {
			deleted = true
			continue
		}
		out = append(out, t)
	}
	if !deleted {
		return false, nil
	}
	return true, s.SaveTasks(out)
}

// ==================== 辅助 ====================

func bytesTrimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

// SettingsAdapter 实现 task.SettingsStore 接口，代理给已有的 store.SettingsStore
type SettingsAdapter struct {
	inner *SettingsStore
}

// NewSettingsAdapter 包装
func NewSettingsAdapter(inner *SettingsStore) *SettingsAdapter {
	return &SettingsAdapter{inner: inner}
}

// ReadSettings 读取 Settings
func (a *SettingsAdapter) ReadSettings() (*model.Settings, error) {
	return a.inner.ReadSettings()
}

// SaveSettings 保存
func (a *SettingsAdapter) SaveSettings(s *model.Settings) error {
	return a.inner.SaveSettings(s)
}

// configProvider 仅提供 ensureDir 所需的目录路径（简化依赖）
type configProvider struct{ dir string }

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	logger.S().Debugf("[store] ensure dir=%s", dir)
	return os.MkdirAll(dir, 0o755)
}
