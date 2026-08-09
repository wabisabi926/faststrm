"use client";

import * as React from "react";
import { Clock, Play, X, CalendarDays, Repeat, Info } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";

export type ScheduleMode = "interval" | "daily" | "weekly";

export interface TaskSchedule {
  enabled: boolean;
  mode: ScheduleMode;
  intervalMinutes?: number;
  time?: string;
  weekdays?: number[];
  lastRunAt?: number;
  nextRunAt?: number;
  lastRunStatus?: "success" | "failed" | "blocked" | "catchup";
  lastRunMessage?: string;
  lastRunDurationMs?: number;
  _computedNextRunAt?: number | null;
}

export interface TaskWithSchedule {
  id: string;
  name?: string;
  account: string;
  schedule?: TaskSchedule;
}

interface Props {
  task: TaskWithSchedule;
  trigger?: React.ReactNode;
  onSuccess?: () => void;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

const WEEKDAY_OPTIONS = [
  { value: 1, label: "一" },
  { value: 2, label: "二" },
  { value: 3, label: "三" },
  { value: 4, label: "四" },
  { value: 5, label: "五" },
  { value: 6, label: "六" },
  { value: 0, label: "日" },
];

function formatNextRun(ts?: number | null): string {
  if (!ts) return "—";
  const d = new Date(ts);
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  const tomorrow = new Date(now);
  tomorrow.setDate(now.getDate() + 1);
  const isTomorrow =
    d.getFullYear() === tomorrow.getFullYear() &&
    d.getMonth() === tomorrow.getMonth() &&
    d.getDate() === tomorrow.getDate();
  const pad = (n: number) => n.toString().padStart(2, "0");
  const timeStr = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (sameDay) return `今天 ${timeStr}`;
  if (isTomorrow) return `明天 ${timeStr}`;
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${timeStr}`;
}

function formatLastRun(ts?: number): string {
  if (!ts) return "从未";
  const d = new Date(ts);
  const pad = (n: number) => n.toString().padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const STATUS_LABEL: Record<string, { label: string; cls: string }> = {
  success: { label: "成功", cls: "text-green-600 bg-green-50" },
  failed: { label: "失败", cls: "text-red-600 bg-red-50" },
  blocked: { label: "跳过", cls: "text-amber-600 bg-amber-50" },
  catchup: { label: "补跑", cls: "text-blue-600 bg-blue-50" },
};

export function TaskScheduleDialog({
  task,
  trigger,
  onSuccess,
  open: controlledOpen,
  onOpenChange,
}: Props) {
  const internalOpen = React.useState(false);
  const open = controlledOpen !== undefined ? controlledOpen : internalOpen[0];
  const setOpen = controlledOpen !== undefined ? onOpenChange! : internalOpen[1];

  const schedule = task.schedule;

  const [enabled, setEnabled] = React.useState(schedule?.enabled ?? false);
  const [mode, setMode] = React.useState<ScheduleMode>(schedule?.mode ?? "interval");
  const [intervalMinutes, setIntervalMinutes] = React.useState<number>(
    schedule?.intervalMinutes ?? 30
  );
  const [time, setTime] = React.useState(schedule?.time ?? "03:00");
  const [weekdays, setWeekdays] = React.useState<number[]>(
    schedule?.weekdays ?? [1, 2, 3, 4, 5]
  );
  const [saving, setSaving] = React.useState(false);

  // Reset when dialog opens for a different task
  const taskKey = task.id;
  React.useEffect(() => {
    setEnabled(task.schedule?.enabled ?? false);
    setMode(task.schedule?.mode ?? "interval");
    setIntervalMinutes(task.schedule?.intervalMinutes ?? 30);
    setTime(task.schedule?.time ?? "03:00");
    setWeekdays(task.schedule?.weekdays ?? [1, 2, 3, 4, 5]);
  }, [taskKey]);

  const previewPayload = React.useMemo<TaskSchedule>(() => {
    return {
      enabled,
      mode,
      intervalMinutes: Math.max(5, intervalMinutes || 5),
      time,
      weekdays,
    };
  }, [enabled, mode, intervalMinutes, time, weekdays]);

  async function handleSave() {
    setSaving(true);
    try {
      await axiosInstance.put("/api/task", {
        id: task.id,
        schedule: previewPayload,
      });
      toast.success(enabled ? "定时任务已保存" : "定时任务已关闭");
      setOpen(false);
      onSuccess?.();
    } catch (err) {
      const msg = (err as { response?: { data?: { message?: string } } })?.response
        ?.data?.message || "保存失败";
      toast.error(msg);
    } finally {
      setSaving(false);
    }
  }

  async function handleFireNow() {
    try {
      const res = await axiosInstance.post("/api/startTask", { id: task.id });
      toast.success(`立即执行：${res.data.message}`);
      onSuccess?.();
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data
          ?.message || "执行失败";
      toast.error(msg);
    }
  }

  function toggleWeekday(v: number) {
    setWeekdays((prev) =>
      prev.includes(v) ? prev.filter((x) => x !== v) : [...prev, v].sort()
    );
  }

  const hasExisting = !!(
    schedule?.enabled ||
    schedule?.lastRunAt ||
    schedule?.nextRunAt
  );

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {trigger && <DialogTrigger asChild>{trigger}</DialogTrigger>}
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Clock className="w-5 h-5 text-indigo-600" />
            定时任务
            <span className="text-xs text-slate-500 font-normal ml-1">
              · {task.name || task.id.slice(0, 8)} · {task.account}
            </span>
          </DialogTitle>
          <DialogDescription>
            为该任务配置自动执行计划，服务端重启后会自动恢复调度。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-6 py-2">
          {/* Enable toggle */}
          <div className="flex items-center justify-between rounded-lg border border-slate-200 bg-slate-50/50 px-4 py-3">
            <div className="flex items-center gap-3">
              <div
                className={`h-5 w-5 rounded border flex items-center justify-center transition-colors ${
                  enabled
                    ? "bg-indigo-600 border-indigo-600"
                    : "bg-white border-slate-300"
                }`}
                onClick={() => setEnabled(!enabled)}
                role="checkbox"
                aria-checked={enabled}
              >
                {enabled && (
                  <svg viewBox="0 0 24 24" fill="none" className="w-3.5 h-3.5 text-white">
                    <path d="M5 12l5 5 9-11" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                )}
              </div>
              <Label className="text-sm font-medium text-slate-800 cursor-pointer" onClick={() => setEnabled(!enabled)}>
                启用定时执行
              </Label>
            </div>
            {enabled && (
              <span className="inline-flex items-center gap-1 text-xs font-medium text-indigo-700 bg-indigo-100 px-2 py-0.5 rounded-full">
                <span className="w-1.5 h-1.5 bg-indigo-600 rounded-full animate-pulse" />
                运行中
              </span>
            )}
          </div>

          {/* Mode selector */}
          <div className={enabled ? "" : "opacity-40 pointer-events-none"}>
            <Label className="text-sm font-medium text-slate-700 mb-2 block">
              执行方式
            </Label>
            <div className="grid grid-cols-3 gap-2">
              {(
                [
                  { v: "interval", label: "间隔执行", icon: Repeat },
                  { v: "daily", label: "每天执行", icon: CalendarDays },
                  { v: "weekly", label: "每周执行", icon: Clock },
                ] as { v: ScheduleMode; label: string; icon: typeof Clock }[]
              ).map((opt) => {
                const Icon = opt.icon;
                const active = mode === opt.v;
                return (
                  <button
                    key={opt.v}
                    type="button"
                    onClick={() => setMode(opt.v)}
                    className={`flex flex-col items-center justify-center gap-1.5 rounded-lg border px-3 py-3 text-sm transition-all ${
                      active
                        ? "border-indigo-500 bg-indigo-50 text-indigo-700 ring-2 ring-indigo-100"
                        : "border-slate-200 bg-white text-slate-600 hover:border-slate-300 hover:bg-slate-50"
                    }`}
                  >
                    <Icon className="w-4 h-4" />
                    {opt.label}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Mode-specific config */}
          <div className={enabled ? "" : "opacity-40 pointer-events-none"}>
            {mode === "interval" && (
              <div className="rounded-lg border border-slate-200 p-4 space-y-3">
                <Label className="text-sm font-medium text-slate-700">
                  执行间隔（分钟，最小 5）
                </Label>
                <div className="flex items-center gap-3">
                  <Input
                    type="number"
                    min={5}
                    value={intervalMinutes}
                    onChange={(e) =>
                      setIntervalMinutes(
                        Math.max(5, parseInt(e.target.value) || 5)
                      )
                    }
                    className="w-24"
                  />
                  <span className="text-sm text-slate-500">
                    每 {Math.max(5, intervalMinutes)} 分钟执行一次
                  </span>
                </div>
              </div>
            )}

            {mode === "daily" && (
              <div className="rounded-lg border border-slate-200 p-4 space-y-3">
                <Label className="text-sm font-medium text-slate-700">
                  每天执行时间
                </Label>
                <div className="flex items-center gap-3">
                  <Input
                    type="time"
                    value={time}
                    onChange={(e) => setTime(e.target.value)}
                    className="w-32"
                  />
                  <span className="text-sm text-slate-500">
                    每天 {time || "03:00"} 执行
                  </span>
                </div>
              </div>
            )}

            {mode === "weekly" && (
              <div className="rounded-lg border border-slate-200 p-4 space-y-3">
                <Label className="text-sm font-medium text-slate-700">
                  每周执行时间
                </Label>
                <Input
                  type="time"
                  value={time}
                  onChange={(e) => setTime(e.target.value)}
                  className="w-32"
                />
                <div>
                  <Label className="text-sm text-slate-600 mb-2 block">
                    选择星期（至少选一天）
                  </Label>
                  <div className="flex flex-wrap gap-2">
                    {WEEKDAY_OPTIONS.map((w) => {
                      const checked = weekdays.includes(w.value);
                      return (
                        <button
                          key={w.value}
                          type="button"
                          onClick={() => toggleWeekday(w.value)}
                          className={`h-8 w-8 rounded-md border text-sm font-medium transition-all ${
                            checked
                              ? "bg-indigo-600 border-indigo-600 text-white"
                              : "bg-white border-slate-200 text-slate-600 hover:border-slate-300"
                          }`}
                        >
                          {w.label}
                        </button>
                      );
                    })}
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Schedule stats card */}
          {hasExisting && (
            <div className="rounded-lg border border-slate-200 bg-white p-4">
              <div className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
                最近调度记录
              </div>
              <div className="grid grid-cols-3 gap-4 text-sm">
                <div>
                  <div className="text-slate-500 text-xs mb-0.5">上次执行</div>
                  <div className="font-medium text-slate-800">
                    {formatLastRun(schedule?.lastRunAt)}
                  </div>
                </div>
                <div>
                  <div className="text-slate-500 text-xs mb-0.5">状态</div>
                  {schedule?.lastRunStatus ? (
                    <span
                      className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${
                        STATUS_LABEL[schedule.lastRunStatus]?.cls ||
                        "text-slate-600 bg-slate-100"
                      }`}
                    >
                      {STATUS_LABEL[schedule.lastRunStatus]?.label ||
                        schedule.lastRunStatus}
                    </span>
                  ) : (
                    <span className="text-slate-400">—</span>
                  )}
                </div>
                <div>
                  <div className="text-slate-500 text-xs mb-0.5">耗时</div>
                  <div className="font-medium text-slate-800">
                    {schedule?.lastRunDurationMs
                      ? `${(schedule.lastRunDurationMs / 1000).toFixed(1)}s`
                      : "—"}
                  </div>
                </div>
              </div>
              {schedule?.lastRunMessage && (
                <div className="mt-3 text-xs text-slate-500 line-clamp-2">
                  <Info className="w-3 h-3 inline mr-1" />
                  {schedule.lastRunMessage}
                </div>
              )}
            </div>
          )}
        </div>

        <DialogFooter className="gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={handleFireNow}
            className="mr-auto"
          >
            <Play className="w-3.5 h-3.5 mr-1" />
            立即执行
          </Button>
          <Button
            variant="ghost"
            onClick={() => setOpen(false)}
          >
            取消
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? "保存中..." : "保存配置"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
