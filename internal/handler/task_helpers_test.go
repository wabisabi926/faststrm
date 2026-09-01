package handler

import (
	"strings"
	"testing"

	"github.com/wabisabi926/faststrm/internal/service/task"
)

func TestParseBoolAny(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		// truthy  cases
		{"on lower", "on", true},
		{"on upper", "ON", true},
		{"true mixed case", "TRUE", true},
		{"true mixed", "TrUe", true},
		{"1 digit", "1", true},
		{"yes lower", "yes", true},
		{"yes upper", "YES", true},
		{"checked", "checked", true},
		{"checked mixed", "CHECKED", true},
		{"trimmed whitespace", " yes ", true},
		{"tab and newline", "\ton\n", true},
		{"true with spaces", " true ", true},

		// falsy cases
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"false", "false", false},
		{"FALSE", "FALSE", false},
		{"0 digit", "0", false},
		{"off", "off", false},
		{"no", "no", false},
		{"abc random", "abc", false},
		{"2", "2", false},
		{"nil-like", "nil", false},
		{"something else", "enabled", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseBoolAny(c.input)
			if got != c.want {
				t.Fatalf("parseBoolAny(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestParseScheduleNilCases(t *testing.T) {
	// mode 为空或 manual → nil
	cases := []struct {
		name  string
		mode  string
		value string
	}{
		{"both empty", "", ""},
		{"empty mode with value", "", "10:00"},
		{"manual lowercase", "manual", "whatever"},
		{"Manual capitalized", "Manual", "10:00"},
		{"MANUAL uppercase", "MANUAL", "30"},
		{"manual with spaces", "  manual  ", "anything"},
		{"whitespace only mode", "   ", "something"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseSchedule(c.mode, c.value)
			if got != nil {
				t.Fatalf("parseSchedule(%q, %q) should be nil, got %+v", c.mode, c.value, got)
			}
		})
	}
}

func TestParseScheduleInterval(t *testing.T) {
	t.Run("valid positive integer", func(t *testing.T) {
		got := parseSchedule("interval", "45")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "interval" {
			t.Fatalf("Mode = %q, want interval", got.Mode)
		}
		if !got.Enabled {
			t.Fatalf("Enabled should be true")
		}
		if got.IntervalMinutes != 45 {
			t.Fatalf("IntervalMinutes = %d, want 45", got.IntervalMinutes)
		}
	})

	t.Run("valid interval with spaces", func(t *testing.T) {
		got := parseSchedule(" interval ", "  60  ")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "interval" {
			t.Fatalf("Mode = %q, want interval", got.Mode)
		}
		if got.IntervalMinutes != 60 {
			t.Fatalf("IntervalMinutes = %d, want 60", got.IntervalMinutes)
		}
	})

	t.Run("default 30 for invalid value", func(t *testing.T) {
		cases := []string{"", "abc", "0", "-5", "3.14"}
		for _, v := range cases {
			got := parseSchedule("interval", v)
			if got == nil {
				t.Fatalf("expected non-nil for value %q", v)
			}
			if got.IntervalMinutes != 30 {
				t.Fatalf("IntervalMinutes = %d, want default 30 for value %q", got.IntervalMinutes, v)
			}
		}
	})

	t.Run("interval uppercase", func(t *testing.T) {
		got := parseSchedule("INTERVAL", "15")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "interval" {
			t.Fatalf("Mode = %q, want interval", got.Mode)
		}
		if got.IntervalMinutes != 15 {
			t.Fatalf("IntervalMinutes = %d, want 15", got.IntervalMinutes)
		}
	})
}

func TestParseScheduleDaily(t *testing.T) {
	t.Run("valid daily", func(t *testing.T) {
		got := parseSchedule("daily", "02:30")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "daily" {
			t.Fatalf("Mode = %q, want daily", got.Mode)
		}
		if got.Time != "02:30" {
			t.Fatalf("Time = %q, want 02:30", got.Time)
		}
	})

	t.Run("daily with trimmed value", func(t *testing.T) {
		got := parseSchedule("  Daily  ", " 08:00 ")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "daily" {
			t.Fatalf("Mode = %q, want daily", got.Mode)
		}
		if got.Time != "08:00" {
			t.Fatalf("Time = %q, want 08:00", got.Time)
		}
	})

	t.Run("daily empty time", func(t *testing.T) {
		got := parseSchedule("daily", "")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Time != "" {
			t.Fatalf("Time = %q, want empty", got.Time)
		}
	})
}

func TestParseScheduleWeekly(t *testing.T) {
	t.Run("valid weekly Mon-02:00", func(t *testing.T) {
		got := parseSchedule("weekly", "Mon-02:00")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "weekly" {
			t.Fatalf("Mode = %q, want weekly", got.Mode)
		}
		if got.Time != "02:00" {
			t.Fatalf("Time = %q, want 02:00", got.Time)
		}
		if len(got.Weekdays) != 1 || got.Weekdays[0] != 1 {
			t.Fatalf("Weekdays = %v, want [1]", got.Weekdays)
		}
	})

	t.Run("weekly with all days", func(t *testing.T) {
		days := []struct {
			abbr string
			want int
		}{
			{"Sun", 0}, {"sun", 0}, {"SUN", 0},
			{"Mon", 1}, {"mon", 1}, {"MON", 1},
			{"Tue", 2}, {"tue", 2},
			{"Wed", 3}, {"wed", 3},
			{"Thu", 4}, {"thu", 4},
			{"Fri", 5}, {"fri", 5},
			{"Sat", 6}, {"sat", 6},
		}
		for _, d := range days {
			val := d.abbr + "-10:00"
			got := parseSchedule("weekly", val)
			if got == nil {
				t.Fatalf("expected non-nil for %s", val)
			}
			if got.Time != "10:00" {
				t.Fatalf("Time = %q, want 10:00 for %s", got.Time, val)
			}
			if len(got.Weekdays) != 1 || got.Weekdays[0] != d.want {
				t.Fatalf("Weekdays = %v, want [%d] for %s", got.Weekdays, d.want, val)
			}
		}
	})

	t.Run("weekly invalid day falls back to Monday(1)", func(t *testing.T) {
		got := parseSchedule("weekly", "Xxx-03:00")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Time != "03:00" {
			t.Fatalf("Time = %q, want 03:00", got.Time)
		}
		if len(got.Weekdays) != 1 || got.Weekdays[0] != 1 {
			t.Fatalf("Weekdays = %v, want [1] (Monday fallback)", got.Weekdays)
		}
	})

	t.Run("weekly no hyphen falls back to Monday(1)", func(t *testing.T) {
		got := parseSchedule("weekly", "05:00")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Time != "05:00" {
			t.Fatalf("Time = %q, want 05:00", got.Time)
		}
		if len(got.Weekdays) != 1 || got.Weekdays[0] != 1 {
			t.Fatalf("Weekdays = %v, want [1] (Monday fallback)", got.Weekdays)
		}
	})
}

func TestParseScheduleDefaultUnknownMode(t *testing.T) {
	// 不在 switch 里的模式（如 cron 或未来扩展），s 仍会被创建
	// 不会设置 IntervalMinutes / Time / Weekdays，但 Mode 会保留
	t.Run("unknown cron mode creates schedule", func(t *testing.T) {
		got := parseSchedule("cron", "0 2 * * *")
		if got == nil {
			t.Fatalf("expected non-nil for unknown mode 'cron'")
		}
		if got.Mode != "cron" {
			t.Fatalf("Mode = %q, want 'cron'", got.Mode)
		}
		if !got.Enabled {
			t.Fatalf("Enabled should be true by default")
		}
		// Time/IntervalMinutes/Weekdays 未在 default 分支设置
	})

	t.Run("future mode 'monthly' creates schedule", func(t *testing.T) {
		got := parseSchedule("monthly", "01-00:00")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "monthly" {
			t.Fatalf("Mode = %q, want 'monthly'", got.Mode)
		}
	})

	t.Run("unknown mode with uppercase and spaces", func(t *testing.T) {
		got := parseSchedule("  MY-MODE  ", "val")
		if got == nil {
			t.Fatalf("expected non-nil")
		}
		if got.Mode != "my-mode" {
			t.Fatalf("Mode = %q, want 'my-mode' (lowercased+trimmed)", got.Mode)
		}
	})
}

// ===================== toTask 测试 =====================

func TestUpsertTaskRequest_ToTask_EmptyRequestNilExisting(t *testing.T) {
	req := &UpsertTaskRequest{}
	got := req.toTask(nil)

	// ID 应自动生成，且以 "task-" 开头
	if got.ID == "" {
		t.Fatalf("ID should be auto-generated")
	}
	if !strings.HasPrefix(got.ID, "task-") {
		t.Fatalf("ID = %q, want prefix 'task-'", got.ID)
	}

	// 其他字段应为零值
	if got.Name != "" || got.Account != "" || got.TargetPath != "" {
		t.Fatalf("expected zero fields, got %+v", got)
	}
	if got.Schedule != nil {
		t.Fatalf("Schedule should be nil for empty request")
	}
	if got.RemoveExtraFiles || got.EnablePathEncoding {
		t.Fatalf("bool flags should all be false")
	}
}

func TestUpsertTaskRequest_ToTask_FullRequestNilExisting(t *testing.T) {
	req := &UpsertTaskRequest{
		ID:            "custom-id-001",
		Name:          "my-task",
		Account:       "acc1",
		AccountType:   "115",
		OriginPath:    "/data/origin",
		TargetPath:    "/data/target",
		StrmType:      "remote",
		StrmPrefix:    "PREFIX_",
		RemoveExtra:   "on",
		EnableEnc:     "TRUE",
		ScheduleMode:  "interval",
		ScheduleValue: "120",
		Enabled:       "yes",
	}
	got := req.toTask(nil)

	if got.ID != "custom-id-001" {
		t.Fatalf("ID = %q, want 'custom-id-001'", got.ID)
	}
	if got.Name != "my-task" {
		t.Fatalf("Name = %q, want 'my-task'", got.Name)
	}
	if got.Account != "acc1" {
		t.Fatalf("Account = %q, want 'acc1'", got.Account)
	}
	if got.AccountType != "115" {
		t.Fatalf("AccountType = %q, want '115'", got.AccountType)
	}
	if got.OriginPath != "/data/origin" {
		t.Fatalf("OriginPath = %q, want '/data/origin'", got.OriginPath)
	}
	if got.TargetPath != "/data/target" {
		t.Fatalf("TargetPath = %q, want '/data/target'", got.TargetPath)
	}
	if got.StrmType != "remote" {
		t.Fatalf("StrmType = %q, want 'remote'", got.StrmType)
	}
	if got.StrmPrefix != "PREFIX_" {
		t.Fatalf("StrmPrefix = %q, want 'PREFIX_'", got.StrmPrefix)
	}
	if !got.RemoveExtraFiles {
		t.Fatalf("RemoveExtraFiles should be true")
	}
	if !got.EnablePathEncoding {
		t.Fatalf("EnablePathEncoding should be true")
	}
	if got.Schedule == nil {
		t.Fatalf("Schedule should not be nil")
	} else {
		if got.Schedule.Mode != "interval" {
			t.Fatalf("Schedule.Mode = %q, want 'interval'", got.Schedule.Mode)
		}
		if got.Schedule.IntervalMinutes != 120 {
			t.Fatalf("Schedule.IntervalMinutes = %d, want 120", got.Schedule.IntervalMinutes)
		}
		if !got.Schedule.Enabled {
			t.Fatalf("Schedule.Enabled should be true")
		}
	}
}

func TestUpsertTaskRequest_ToTask_SourcePathFallback(t *testing.T) {
	// req.OriginPath 为空，SourcePath 有值 → OriginPath 应该用 SourcePath
	req := &UpsertTaskRequest{
		Account:    "acc2",
		SourcePath: "/fallback/origin",
		TargetPath: "/data/target",
	}
	got := req.toTask(nil)
	if got.OriginPath != "/fallback/origin" {
		t.Fatalf("OriginPath = %q, want '/fallback/origin' (SourcePath fallback)", got.OriginPath)
	}
}

func TestUpsertTaskRequest_ToTask_PartialOverridesExisting(t *testing.T) {
	existing := &task.Task{
		ID:                 "existing-001",
		Name:               "old-name",
		Account:            "old-acc",
		AccountType:        "115",
		OriginPath:         "/old/origin",
		TargetPath:         "/old/target",
		StrmType:           "local",
		StrmPrefix:         "OLD_",
		RemoveExtraFiles:   true,
		EnablePathEncoding: false,
		Schedule: &task.TaskSchedule{
			Enabled:         true,
			Mode:            "daily",
			Time:            "08:00",
			IntervalMinutes: 0,
		},
	}

	// req 只覆盖 Name 和 TargetPath，其他字段保持 existing 的值
	req := &UpsertTaskRequest{
		Name:       "new-name",
		TargetPath: "/new/target",
		// 空 ScheduleMode/ScheduleValue → 不会覆盖 Schedule
	}
	got := req.toTask(existing)

	// 被覆盖的字段
	if got.Name != "new-name" {
		t.Fatalf("Name = %q, want 'new-name'", got.Name)
	}
	if got.TargetPath != "/new/target" {
		t.Fatalf("TargetPath = %q, want '/new/target'", got.TargetPath)
	}

	// 保持 existing 值的字段
	if got.ID != "existing-001" {
		t.Fatalf("ID = %q, want 'existing-001'", got.ID)
	}
	if got.Account != "old-acc" {
		t.Fatalf("Account = %q, want 'old-acc'", got.Account)
	}
	if got.AccountType != "115" {
		t.Fatalf("AccountType = %q, want '115'", got.AccountType)
	}
	if got.OriginPath != "/old/origin" {
		t.Fatalf("OriginPath = %q, want '/old/origin'", got.OriginPath)
	}
	if got.StrmType != "local" {
		t.Fatalf("StrmType = %q, want 'local'", got.StrmType)
	}
	if got.StrmPrefix != "OLD_" {
		t.Fatalf("StrmPrefix = %q, want 'OLD_'", got.StrmPrefix)
	}

	// bool 标志——req 里是假值（空字符串 parseBoolAny→false），会覆盖 existing 的值
	// RemoveExtraFiles: existing=true → req.RemoveExtra="" → parseBoolAny("")=false → 覆盖！
	if got.RemoveExtraFiles {
		t.Fatalf("RemoveExtraFiles should be false (req empty overrides existing true)")
	}
	if got.EnablePathEncoding {
		t.Fatalf("EnablePathEncoding should be false")
	}
	// Schedule——req.ScheduleMode 为空 → parseSchedule 返回 nil
	// req.ScheduleMode != "manual" → 保留 existing 的 Schedule
	if got.Schedule == nil {
		t.Fatalf("Schedule should keep existing when req.ScheduleMode is empty")
	} else if got.Schedule.Mode != "daily" {
		t.Fatalf("Schedule.Mode = %q, want 'daily'", got.Schedule.Mode)
	}
}

func TestUpsertTaskRequest_ToTask_ManualScheduleNil(t *testing.T) {
	// req.ScheduleMode="manual" → Schedule 应为 nil
	existing := &task.Task{
		ID:         "has-sched-001",
		Schedule:   &task.TaskSchedule{Enabled: true, Mode: "daily", Time: "10:00"},
		Account:    "a",
		TargetPath: "/t",
		OriginPath: "/o",
	}
	req := &UpsertTaskRequest{
		ScheduleMode:  "manual",
		ScheduleValue: "ignored",
	}
	got := req.toTask(existing)
	if got.Schedule != nil {
		t.Fatalf("Schedule should be nil after 'manual' mode, got %+v", got.Schedule)
	}
}

func TestUpsertTaskRequest_ToTask_ScheduleOverride(t *testing.T) {
	// req.ScheduleMode="interval" + 有效值 → 覆盖 existing Schedule
	existing := &task.Task{
		ID:         "has-sched-002",
		Schedule:   &task.TaskSchedule{Enabled: true, Mode: "daily", Time: "10:00"},
		Account:    "a",
		TargetPath: "/t",
		OriginPath: "/o",
	}
	req := &UpsertTaskRequest{
		ScheduleMode:  "interval",
		ScheduleValue: "15",
		Enabled:       "on",
	}
	got := req.toTask(existing)
	if got.Schedule == nil {
		t.Fatalf("Schedule should not be nil")
	}
	if got.Schedule.Mode != "interval" {
		t.Fatalf("Schedule.Mode = %q, want 'interval'", got.Schedule.Mode)
	}
	if got.Schedule.IntervalMinutes != 15 {
		t.Fatalf("Schedule.IntervalMinutes = %d, want 15", got.Schedule.IntervalMinutes)
	}
	if !got.Schedule.Enabled {
		t.Fatalf("Schedule.Enabled should be true (from req.Enabled='on')")
	}
}

func TestUpsertTaskRequest_ToTask_EmptyEnabledKeepsDefault(t *testing.T) {
	// req.ScheduleMode="interval" + req.Enabled="" (空) → parseBoolAny("")=false → Schedule.Enabled=false
	req := &UpsertTaskRequest{
		Account:       "acc1",
		TargetPath:    "/t",
		SourcePath:    "/o",
		ScheduleMode:  "interval",
		ScheduleValue: "60",
		Enabled:       "",
	}
	got := req.toTask(nil)
	if got.Schedule == nil {
		t.Fatalf("Schedule should not be nil")
	}
	if got.Schedule.Enabled {
		t.Fatalf("Schedule.Enabled should be false (req.Enabled empty → parseBoolAny returns false)")
	}
}

func TestUpsertTaskRequest_ToTask_ExistingIDOverridesAutoGen(t *testing.T) {
	// existing 有 ID，但 req 也给了 ID → 用 req 的 ID
	existing := &task.Task{
		ID:         "old-id",
		Name:       "old-name",
		Account:    "a",
		TargetPath: "/t",
		OriginPath: "/o",
	}
	req := &UpsertTaskRequest{
		ID:   "new-id",
		Name: "new-name",
	}
	got := req.toTask(existing)
	if got.ID != "new-id" {
		t.Fatalf("ID = %q, want 'new-id' (req.ID overrides existing.ID)", got.ID)
	}
}

func TestUpsertTaskRequest_ToTask_EmptyReqIDKeepsExisting(t *testing.T) {
	// req.ID 为空，existing 有 ID → 保留 existing.ID（不会触发 auto-generate）
	existing := &task.Task{
		ID:         "keep-me",
		Name:       "n",
		Account:    "a",
		TargetPath: "/t",
		OriginPath: "/o",
	}
	req := &UpsertTaskRequest{
		Name: "updated",
	}
	got := req.toTask(existing)
	if got.ID != "keep-me" {
		t.Fatalf("ID = %q, want 'keep-me' (existing.ID preserved when req.ID empty)", got.ID)
	}
}
