// 定时任务单元格：模式标签 + 下次执行时间。
// 从 TaskColumns.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { Clock } from "lucide-react";
import type { Task } from "./types";

export interface ScheduleCellProps {
  task: Task;
}

export function ScheduleCell({ task }: ScheduleCellProps) {
  const sched = task.schedule;
  const enabled = !!sched?.enabled;
  const nextRun = task._computedNextRunAt ?? sched?.nextRunAt ?? null;

  if (!enabled) {
    return <span className="text-xs text-slate-400">未配置</span>;
  }

  const fmtNext = (() => {
    if (!nextRun) return "计算中";
    const d = new Date(nextRun);
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
    const t = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
    if (sameDay) return `今天 ${t}`;
    if (isTomorrow) return `明天 ${t}`;
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${t}`;
  })();

  const modeLabel =
    sched.mode === "interval"
      ? `每 ${sched.intervalMinutes} 分钟`
      : sched.mode === "daily"
        ? `每天 ${sched.time}`
        : `每周 ${sched.time}`;

  return (
    <div className="flex flex-col gap-0.5">
      <span className="inline-flex items-center gap-1 text-xs font-medium text-indigo-700 bg-indigo-50 rounded-full px-2 py-0.5 w-fit">
        <Clock className="w-3 h-3" />
        {modeLabel}
      </span>
      <span className="text-xs text-slate-500">下次: {fmtNext}</span>
    </div>
  );
}
