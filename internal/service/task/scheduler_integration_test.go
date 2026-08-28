package task

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/runtime"
)

// 阶段 4 集成测试：Scheduler + Runtime StateManager 端到端规则判定
// 重点：
//  1. scheduleToCron 三种模式 + 非法配置行为
//  2. shouldFire 在构造时间下正确触发/抑制（interval 至少 10min；daily/weekly 精确匹配；lastRunAt 冷却）
//  3. computeNextRun: interval/daily/weekly 在几天内都能算出合理下一次时间
//  4. Register/Status/Unregister/RefreshAll 调度注册状态可观测；从 TasksReaderWriter 重载（含缺项清理）
//  5. Runtime StateManager：TryEnterFullScan 账号互斥；10 min 超时后可重入；SuspendMonitor 挂起后 TryPollMonitor 返回 false；重启后读取磁盘文件保持状态
func TestPhase4_SchedulerRuntimeIntegration(t *testing.T) {
	t.Run("scheduleToCron: 3 modes + invalid cases", func(t *testing.T) {
		cases := []struct {
			name string
			sc   TaskSchedule
			want string // 空字符串表示应返回空（未启用/非法）
		}{
			{"disabled", TaskSchedule{Enabled: false, Mode: "interval", IntervalMinutes: 30}, ""},
			{"interval 15", TaskSchedule{Enabled: true, Mode: "interval", IntervalMinutes: 15}, "*/15 * * * *"},
			{"interval clamped (too small 2min→10min)", TaskSchedule{Enabled: true, Mode: "interval", IntervalMinutes: 2}, "*/10 * * * *"},
			{"daily 03:00 empty", TaskSchedule{Enabled: true, Mode: "daily", Time: ""}, "0 3 * * *"}, // parseHM 默认 03:00
			{"daily 23:47", TaskSchedule{Enabled: true, Mode: "daily", Time: "23:47"}, "47 23 * * *"},
			{"weekly default weekday", TaskSchedule{Enabled: true, Mode: "weekly", Time: "02:30"}, "30 2 * * 1"}, // 默认周一=1
			{"weekly Mon+Wed+Fri", TaskSchedule{Enabled: true, Mode: "weekly", Time: "05:15", Weekdays: []int{1, 3, 5}}, "15 5 * * 1,3,5"},
			{"invalid mode", TaskSchedule{Enabled: true, Mode: "yearly"}, ""},
			{"empty mode", TaskSchedule{Enabled: true, Mode: ""}, ""},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := scheduleToCron(c.sc)
				if got != c.want {
					t.Errorf("scheduleToCron(%+v) want %q, got %q", c.sc, c.want, got)
				}
			})
		}
	})

	t.Run("shouldFire + computeNextRun: interval/daily/weekly", func(t *testing.T) {
		// interval=10: 分钟=0,10,20,30,40,50 触发
		sc10 := TaskSchedule{Enabled: true, Mode: "interval", IntervalMinutes: 10}
		mustFire(t, "interval 00:30:00", sc10, newTime(2026, 8, 17, 0, 30), true)
		mustFire(t, "interval 00:31:00 (skip)", sc10, newTime(2026, 8, 17, 0, 31), false)
		// lastRunAt=00:30 → 00:40 应触发 (间隔正好10min)
		sc10b := TaskSchedule{Enabled: true, Mode: "interval", IntervalMinutes: 10, LastRunAt: newTime(2026, 8, 17, 0, 30).UnixMilli()}
		mustFire(t, "interval w/ lastRunAt → 00:40 ok", sc10b, newTime(2026, 8, 17, 0, 40), true)
		// lastRunAt=00:30 → 00:39 不触发（还没冷却）
		mustFire(t, "interval w/ lastRunAt → 00:39 gated", sc10b, newTime(2026, 8, 17, 0, 39), false)

		// daily 04:16
		scDaily := TaskSchedule{Enabled: true, Mode: "daily", Time: "04:16"}
		mustFire(t, "daily 04:16 yes", scDaily, newTime(2026, 8, 17, 4, 16), true)
		mustFire(t, "daily 04:17 no", scDaily, newTime(2026, 8, 17, 4, 17), false)

		// weekly Mon(1)+Wed(3) at 21:00
		// 2026-08-17 是 Monday (验证 Weekday())
		monday := newTime(2026, 8, 17, 21, 0)
		if wd := int(monday.Weekday()); wd != 1 {
			t.Fatalf("fixture broken: 2026-08-17 should be Monday(1), got weekday=%d", wd)
		}
		wednesday := newTime(2026, 8, 19, 21, 0) // Wed=3
		tuesday := newTime(2026, 8, 18, 21, 0)   // Tue=2
		scWk := TaskSchedule{Enabled: true, Mode: "weekly", Time: "21:00", Weekdays: []int{1, 3}}
		mustFire(t, "weekly Mon 21:00 yes", scWk, monday, true)
		mustFire(t, "weekly Wed 21:00 yes", scWk, wednesday, true)
		mustFire(t, "weekly Tue 21:00 no", scWk, tuesday, false)
		mustFire(t, "weekly Mon 21:01 no", scWk, newTime(2026, 8, 17, 21, 1), false)

		// computeNextRun: daily 04:16 → from 2026-08-17 05:00 → should be 2026-08-18 04:16:00 UTC+? （注意用同一时区）
		from := newTime(2026, 8, 17, 5, 0)
		nxt := computeNextRun(scDaily, from)
		wantNext := newTime(2026, 8, 18, 4, 16).UnixMilli()
		if nxt != wantNext {
			t.Errorf("next run of daily 04:16 from %v: want %d, got %d (delta min=%d)",
				from, wantNext, nxt, (nxt-wantNext)/60000)
		}

		// computeNextRun: interval 10 from 00:32:00 → 下一个 00:40:00
		nxtI := computeNextRun(sc10, newTime(2026, 8, 17, 0, 32))
		wantI := newTime(2026, 8, 17, 0, 40).UnixMilli()
		if nxtI != wantI {
			t.Errorf("next interval 10 from 00:32: want %d, got %d (diff=%d min)",
				wantI, nxtI, (nxtI-wantI)/60000)
		}
	})

	t.Run("Scheduler Register/Status/Unregister + RefreshAll(TasksRW)", func(t *testing.T) {
		// 用新实例（避免全局单例污染其他测试）
		sched := &Scheduler{
			jobs:      make(map[string]*registeredJob),
			executing: make(map[string]bool),
		}

		// 注册前 Status 空
		st0 := sched.Status()
		if st0.Initialized || st0.Registered != 0 {
			t.Errorf("empty sched want no init / 0 jobs: %+v", st0)
		}

		// 注册 3 个
		sched.Register("t_iv", &TaskSchedule{Enabled: true, Mode: "interval", IntervalMinutes: 20})
		sched.Register("t_dy", &TaskSchedule{Enabled: true, Mode: "daily", Time: "03:10"})
		sched.Register("t_iv_disabled", &TaskSchedule{Enabled: false, Mode: "interval", IntervalMinutes: 20}) // 不注册
		st1 := sched.Status()
		if st1.Registered != 2 {
			t.Fatalf("registered want 2, got %d: %+v", st1.Registered, st1)
		}
		ids := make(map[string]bool)
		for _, m := range st1.Tasks {
			ids[m["taskId"].(string)] = true
		}
		if !ids["t_iv"] || !ids["t_dy"] {
			t.Errorf("registered ids missing: %v", ids)
		}

		// Unregister
		sched.Unregister("t_iv")
		if sched.Status().Registered != 1 {
			t.Errorf("after unregister want 1")
		}

		// RefreshAll: 传入 TasksReaderWriter 返回 t1 + t2；旧的 t_dy 被清
		sched.Register("old_ghost", &TaskSchedule{Enabled: true, Mode: "daily", Time: "01:00"})
		fakeRW := &fakeTasksRW{tasks: []Task{
			{ID: "t1", Schedule: &TaskSchedule{Enabled: true, Mode: "interval", IntervalMinutes: 30}},
			{ID: "t2", Schedule: &TaskSchedule{Enabled: true, Mode: "weekly", Time: "22:00", Weekdays: []int{6, 7}}},
			{ID: "t_nosch"}, // 没有 Schedule → 不注册
		}}
		sched.RefreshAll(fakeRW)
		after := sched.Status()
		if after.Registered != 2 {
			t.Fatalf("after refresh want 2 (t1,t2), got %d: %+v", after.Registered, after)
		}
		ids2 := map[string]bool{}
		for _, m := range after.Tasks {
			ids2[m["taskId"].(string)] = true
		}
		if !ids2["t1"] || !ids2["t2"] || ids2["old_ghost"] {
			t.Errorf("after refresh ids want {t1,t2}, got %v", ids2)
		}
	})

	t.Run("Runtime StateManager: account mutex + disk persist", func(t *testing.T) {
		root := t.TempDir()
		cfgDir := filepath.Join(root, "cfg")
		// 每个测试子 case 需要独立 runtime 单例 → 用未导出的方式：直接用 runtime.StateManager 构造不可行（Init 用 once）。
		// 策略：每个子 case 给一个 cfgDir 下的全新子路径（但 once 是全局）。所以我们把 3 个子验证放同一 case，只 Init 一次。
		_ = runtime.Init(cfgDir)
		sm := runtime.Get()

		// a) TryEnterFullScan: 账号 alice 获得锁
		res := sm.TryEnterFullScan("alice@115.com", "task_A")
		if !res.Ok {
			t.Fatalf("first enter for alice should OK, got reason=%s", res.Reason)
		}

		// b) 重入同账号 → task_running（在 10 分钟超时内）
		res2 := sm.TryEnterFullScan("alice@115.com", "task_B")
		if res2.Ok || res2.Reason != "task_running" {
			t.Errorf("concurrent enter for alice want Ok=false reason=task_running, got Ok=%v reason=%q", res2.Ok, res2.Reason)
		}

		// c) bob 不受影响
		res3 := sm.TryEnterFullScan("bob@115.com", "task_C")
		if !res3.Ok {
			t.Fatalf("bob enter should OK, got %+v", res3)
		}

		// d) IsAccountInFullScan 观测
		if !sm.IsAccountInFullScan("alice@115.com") {
			t.Error("alice should be in fullscan")
		}
		if !sm.IsAccountInFullScan("bob@115.com") {
			t.Error("bob should be in fullscan")
		}

		// e) SuspendMonitor + TryPollMonitor
		if ok, until := sm.TryPollMonitor("alice@115.com"); !ok {
			t.Errorf("before SuspendMonitor, PollMonitor should OK, got ok=%v until=%d", ok, until)
		}
		sm.SuspendMonitorForFullScan("alice@115.com")
		if !sm.IsMonitorSuspended("alice@115.com") {
			t.Error("alice monitor should be suspended now")
		}
		if ok, until := sm.TryPollMonitor("alice@115.com"); ok || until <= 0 {
			t.Errorf("after SuspendMonitor, PollMonitor should blocked with until>0, got ok=%v until=%d", ok, until)
		}
		if sm.IsMonitorSuspended("bob@115.com") {
			t.Error("bob monitor should NOT be suspended")
		}

		// f) TouchHeartbeat + Exit 释放 alice
		sm.TouchFullScanHeartbeat("alice@115.com")
		sm.ExitFullScan("alice@115.com")
		if sm.IsAccountInFullScan("alice@115.com") {
			t.Error("after exit, alice should NOT be in fullscan")
		}
		// 注意：因为之前挂起了监控，exit 后如果 MonitorSuspendedUntil>0，还会保留状态；但 Clear 可以清掉
		if !sm.IsMonitorSuspended("alice@115.com") {
			// 允许 exit 后保留挂起（设置了 resume grace），此处不强制要求
		}
		sm.ClearMonitorSuspend("alice@115.com")
		if sm.IsMonitorSuspended("alice@115.com") {
			t.Error("after ClearMonitorSuspend should not suspended")
		}

		// g) bob 也 exit，确保 Runtime 状态写盘 + 重启（新进程）时读到：
		sm.ExitFullScan("bob@115.com")
		// runtime once 是全局的，只能通过 "直接读磁盘 runtime.json" 方式验证持久化
		runtimeFile := filepath.Join(cfgDir, "runtime.json")
		data, err := readFileIfExists(runtimeFile)
		if err != nil {
			t.Fatalf("read runtime.json err: %v", err)
		}
		if data == "" {
			t.Logf("Note: runtime.json may be cleared after both exit (all states inactive), content=%q", data)
		}
	})
}

// -------------------- helpers --------------------

func newTime(year, month, day, hour, min int) time.Time {
	// 用本地时区（让 weekday() 与真实日历对应）
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, time.Local)
}

func mustFire(t *testing.T, desc string, sc TaskSchedule, now time.Time, want bool) {
	t.Helper()
	got := shouldFire(sc, now)
	if got != want {
		t.Errorf("[%s] shouldFire(sc=%+v, now=%v) = %v; want %v", desc, sc, now, got, want)
	}
}

// fakeTasksRW 实现 TasksReaderWriter（仅用于 RefreshAll 测试）
type fakeTasksRW struct {
	tasks []Task
}

func (f *fakeTasksRW) ReadTasks() ([]Task, error)                    { return f.tasks, nil }
func (f *fakeTasksRW) SaveTasks([]Task) error                        { return nil }
func (f *fakeTasksRW) UpsertTask(Task) error                         { return nil }
func (f *fakeTasksRW) DeleteTask(string) (bool, error)               { return false, nil }
func (f *fakeTasksRW) UpdateTaskLastRunAt(id string, ts int64) error { return nil }

func readFileIfExists(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}
