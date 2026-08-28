package task

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// Scheduler 任务调度器（基于 cron 的最小实现，无第三方 cron 依赖）
// 对齐 TS taskScheduler.ts：registerTaskSchedule / unregisterTaskSchedule / refreshAllSchedules
//
// 简化设计：
// - 每分钟 tick 一次
// - 对每个注册任务用 mode + 规则判断 "now 是否应该触发"
// - 不依赖 cron 库，保持依赖干净
type Scheduler struct {
	mu          sync.Mutex
	initialized bool
	jobs        map[string]*registeredJob
	ticker      *time.Ticker
	stopCh      chan struct{}
	deps        *ExecutorDeps
	executing   map[string]bool
}

type registeredJob struct {
	taskID   string
	schedule TaskSchedule
	cronExpr string
}

var (
	schedInst *Scheduler
	schedOnce sync.Once
)

// GetScheduler 获取全局单例
func GetScheduler() *Scheduler {
	schedOnce.Do(func() {
		schedInst = &Scheduler{
			jobs:      make(map[string]*registeredJob),
			executing: make(map[string]bool),
		}
	})
	return schedInst
}

// Init 初始化调度器（惰性启动；传入 deps 用于触发 executeTask）
func (s *Scheduler) Init(deps ExecutorDeps, ts TasksReaderWriter, settings SettingsStore) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return nil
	}
	s.initialized = true
	s.deps = &deps
	s.mu.Unlock()

	// 启动 ticker：每分钟
	s.ticker = time.NewTicker(1 * time.Minute)
	s.stopCh = make(chan struct{})

	// 启动后 5s 内先 refreshAllSchedules 一次
	go func() {
		time.Sleep(5 * time.Second)
		s.RefreshAll(ts)
		s.loop(ts)
	}()
	logger.S().Info("[TaskScheduler] initialized")
	return nil
}

// SetNotifier 更新内部 deps 的 Notifier（用于 dispatcher 创建后延迟注入）
func (s *Scheduler) SetNotifier(n TaskNotifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deps != nil {
		s.deps.Notifier = n
	}
}

// Stop 停止调度器（服务关闭时调用）
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.initialized {
		s.mu.Unlock()
		return
	}
	s.initialized = false
	s.mu.Unlock()
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.stopCh != nil {
		close(s.stopCh)
	}
}

// Register 注册一个任务调度（新增/覆盖）；无 schedule 或未启用等价 unregister
func (s *Scheduler) Register(taskID string, schedule *TaskSchedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if schedule == nil || !schedule.Enabled || schedule.Mode == "" {
		delete(s.jobs, taskID)
		return
	}
	cron := scheduleToCron(*schedule)
	if cron == "" {
		delete(s.jobs, taskID)
		return
	}
	s.jobs[taskID] = &registeredJob{taskID: taskID, schedule: *schedule, cronExpr: cron}
	logger.S().Infof("[TaskScheduler] register %s (cron=%s)", taskID, cron)
}

// Unregister 取消注册
func (s *Scheduler) Unregister(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, taskID)
}

// RefreshAll 重读 tasks 全量刷新调度注册
func (s *Scheduler) RefreshAll(ts TasksReaderWriter) {
	tasks, err := ts.ReadTasks()
	if err != nil {
		logger.S().Warnf("[TaskScheduler] read tasks failed: %v", err)
		return
	}
	s.mu.Lock()
	want := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		want[t.ID] = struct{}{}
		if t.Schedule != nil && t.Schedule.Enabled && t.Schedule.Mode != "" {
			cron := scheduleToCron(*t.Schedule)
			if cron != "" {
				s.jobs[t.ID] = &registeredJob{taskID: t.ID, schedule: *t.Schedule, cronExpr: cron}
				continue
			}
		}
		delete(s.jobs, t.ID)
	}
	// 清理掉已经不存在的任务
	for id := range s.jobs {
		if _, ok := want[id]; !ok {
			delete(s.jobs, id)
		}
	}
	n := len(s.jobs)
	s.mu.Unlock()
	logger.S().Infof("[TaskScheduler] refreshed, %d scheduled jobs", n)
}

// Status 返回当前调度器状态
type SchedulerStatus struct {
	Initialized bool             `json:"initialized"`
	Registered  int              `json:"registeredCount"`
	Tasks       []map[string]any `json:"tasks"`
}

func (s *Scheduler) Status() SchedulerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := SchedulerStatus{Initialized: s.initialized, Registered: len(s.jobs)}
	for id, j := range s.jobs {
		next := computeNextRun(j.schedule, time.Now())
		status.Tasks = append(status.Tasks, map[string]any{
			"taskId":    id,
			"cron":      j.cronExpr,
			"nextRunAt": next,
		})
	}
	return status
}

// loop 主循环：每分钟 tick 判断是否触发
func (s *Scheduler) loop(ts TasksReaderWriter) {
	for {
		select {
		case <-s.stopCh:
			return
		case now, ok := <-s.ticker.C:
			if !ok {
				return
			}
			s.tick(now, ts)
		}
	}
}

func (s *Scheduler) tick(now time.Time, ts TasksReaderWriter) {
	s.mu.Lock()
	jobs := make([]*registeredJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	deps := s.deps
	s.mu.Unlock()
	if deps == nil {
		return
	}

	for _, j := range jobs {
		if !shouldFire(j.schedule, now) {
			continue
		}
		s.mu.Lock()
		if s.executing[j.taskID] {
			s.mu.Unlock()
			continue
		}
		s.executing[j.taskID] = true
		s.mu.Unlock()

		go func(taskID string) {
			defer func() {
				s.mu.Lock()
				delete(s.executing, taskID)
				s.mu.Unlock()
			}()
			logger.S().Infof("[TaskScheduler] fire task=%s", taskID)
			res := ExecuteTask(context.Background(), taskID, *deps)
			if !res.Success {
				logger.S().Warnf("[TaskScheduler] fire %s failed: %+v", taskID, res)
			}
			// 更新 lastRunAt
			updateLastRunAt(ts, taskID, time.Now().UnixMilli())
		}(j.taskID)
	}
}

// ==================== cron 规则判定 ====================

// MIN_INTERVAL_MINUTES 最小 interval 模式间隔
const MIN_INTERVAL_MINUTES = 10

// scheduleToCron 转换 TaskSchedule → "m h dom mon dow" 5 段 cron（仅调试展示 + 判定）
func scheduleToCron(sc TaskSchedule) string {
	if !sc.Enabled {
		return ""
	}
	switch sc.Mode {
	case "interval":
		m := sc.IntervalMinutes
		if m < MIN_INTERVAL_MINUTES {
			m = MIN_INTERVAL_MINUTES
		}
		return fmt.Sprintf("*/%d * * * *", m)
	case "daily":
		hh, mm := parseHM(sc.Time)
		return fmt.Sprintf("%d %d * * *", mm, hh)
	case "weekly":
		hh, mm := parseHM(sc.Time)
		dow := "1"
		if len(sc.Weekdays) > 0 {
			parts := make([]string, 0, len(sc.Weekdays))
			for _, w := range sc.Weekdays {
				parts = append(parts, strconv.Itoa(w))
			}
			dow = strings.Join(parts, ",")
		}
		return fmt.Sprintf("%d %d * * %s", mm, hh, dow)
	}
	return ""
}

// shouldFire 根据 schedule 判断 now 是否匹配
// - interval：now-unix % (interval*60) < 60 且距离 lastRunAt 至少 interval 分钟
// - daily/weekly：时间精确匹配 minute+hour (+ weekday)
func shouldFire(sc TaskSchedule, now time.Time) bool {
	switch sc.Mode {
	case "interval":
		m := sc.IntervalMinutes
		if m < MIN_INTERVAL_MINUTES {
			m = MIN_INTERVAL_MINUTES
		}
		if sc.LastRunAt > 0 {
			if now.UnixMilli()-sc.LastRunAt < int64(m)*60_000-30_000 {
				return false
			}
		}
		// 取当前分钟的分钟数是否为 interval 的倍数（容忍±1）
		min := now.Minute()
		return min%m == 0
	case "daily":
		hh, mm := parseHM(sc.Time)
		return now.Hour() == hh && now.Minute() == mm
	case "weekly":
		hh, mm := parseHM(sc.Time)
		if !(now.Hour() == hh && now.Minute() == mm) {
			return false
		}
		if len(sc.Weekdays) == 0 {
			return int(now.Weekday()) == 1 // 默认周一
		}
		curDow := int(now.Weekday())
		for _, w := range sc.Weekdays {
			if w == curDow {
				return true
			}
		}
		return false
	}
	return false
}

// computeNextRun 返回下次运行 unix ms；未启用返回 0
func computeNextRun(sc TaskSchedule, from time.Time) int64 {
	if !sc.Enabled {
		return 0
	}
	// 简化：最多往后找 7 天 * 1440 分钟
	limit := 7 * 1440
	probe := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < limit; i++ {
		if shouldFire(sc, probe) {
			return probe.UnixMilli()
		}
		probe = probe.Add(time.Minute)
	}
	return 0
}

func parseHM(t string) (hh, mm int) {
	if t == "" {
		return 3, 0
	}
	parts := strings.Split(t, ":")
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			hh = clamp023(n)
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			mm = clamp059(n)
		}
		return
	}
	if len(parts) == 1 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			hh = clamp023(n)
		}
	}
	return
}
func clamp023(x int) int {
	if x < 0 {
		return 0
	}
	if x > 23 {
		return 23
	}
	return x
}
func clamp059(x int) int {
	if x < 0 {
		return 0
	}
	if x > 59 {
		return 59
	}
	return x
}

// updateLastRunAt 更新 tasks 中的 schedule.lastRunAt
func updateLastRunAt(ts TasksReaderWriter, taskID string, t int64) {
	tasks, err := ts.ReadTasks()
	if err != nil {
		return
	}
	updated := false
	for i := range tasks {
		if tasks[i].ID == taskID && tasks[i].Schedule != nil {
			tasks[i].Schedule.LastRunAt = t
			updated = true
			break
		}
	}
	if updated {
		_ = ts.SaveTasks(tasks)
	}
}
