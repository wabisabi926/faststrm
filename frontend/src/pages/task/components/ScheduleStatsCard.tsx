// 调度记录卡片：上次执行时间 / 状态 / 耗时 / 提示消息。
// 从 TaskScheduleDialog.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { Info } from "lucide-react";
import type { TaskSchedule } from "./TaskScheduleDialog";

const STATUS_LABEL: Record<string, { label: string; cls: string }> = {
  success: { label: "成功", cls: "text-green-600 bg-green-50" },
  failed: { label: "失败", cls: "text-red-600 bg-red-50" },
  blocked: { label: "跳过", cls: "text-amber-600 bg-amber-50" },
  catchup: { label: "补跑", cls: "text-blue-600 bg-blue-50" },
};

function formatLastRun(ts?: number): string {
  if (!ts) return "从未";
  const d = new Date(ts);
  const pad = (n: number) => n.toString().padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export interface ScheduleStatsCardProps {
  schedule: TaskSchedule;
}

export function ScheduleStatsCard({ schedule }: ScheduleStatsCardProps) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-4">
      <div className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
        最近调度记录
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 text-sm">
        <div>
          <div className="text-slate-500 text-xs mb-0.5">上次执行</div>
          <div className="font-medium text-slate-800">
            {formatLastRun(schedule.lastRunAt)}
          </div>
        </div>
        <div>
          <div className="text-slate-500 text-xs mb-0.5">状态</div>
          {schedule.lastRunStatus ? (
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
            {schedule.lastRunDurationMs
              ? `${(schedule.lastRunDurationMs / 1000).toFixed(1)}s`
              : "—"}
          </div>
        </div>
      </div>
      {schedule.lastRunMessage && (
        <div className="mt-3 text-xs text-slate-500 line-clamp-2">
          <Info className="w-3 h-3 inline mr-1" />
          {schedule.lastRunMessage}
        </div>
      )}
    </div>
  );
}
