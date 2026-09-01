import {
  CheckCircle,
  XCircle,
  AlertCircle,
} from "lucide-react";
import type { TaskSchedule } from "./components/TaskScheduleDialog";

// Task 类型
export type Task = {
  id: string;
  accountType: string;
  account: string;
  originPath: string;
  targetPath: string;
  strmType: string;
  strmPrefix: string;
  removeExtraFiles?: boolean;
  name: string;
  status: "pending" | "processing" | "success" | "failed" | "cancelled";
  error?: string | null;
  schedule?: TaskSchedule;
  _computedNextRunAt?: number | null;
  runtime?: {
    status?: string;
    startedAt?: number;
    endedAt?: number;
    error?: string;
    totalFiles?: number;
    downloadedFiles?: number;
    deletedFiles?: number;
    stage?: string;
    stageDetail?: string;
  };
};

// 阶段配置：中文标签、颜色、图标
export const STAGE_CONFIG: Record<string, { label: string; className: string; icon: string }> = {
  starting:    { label: "启动中",     className: "bg-slate-500/20 text-slate-300 border-slate-500/30",   icon: "⏳" },
  scanning:    { label: "扫描目录",   className: "bg-cyan-500/20 text-cyan-300 border-cyan-500/30",      icon: "🔍" },
  incremental: { label: "增量比对",   className: "bg-violet-500/20 text-violet-300 border-violet-500/30",  icon: "📊" },
  cleanup:     { label: "清理文件",   className: "bg-amber-500/20 text-amber-300 border-amber-500/30",    icon: "🧹" },
  writing_db:  { label: "写入数据库", className: "bg-indigo-500/20 text-indigo-300 border-indigo-500/30", icon: "💾" },
  generating:  { label: "生成STRM",   className: "bg-blue-500/20 text-blue-300 border-blue-500/30",      icon: "⚡" },
  finalizing:  { label: "收尾处理",   className: "bg-teal-500/20 text-teal-300 border-teal-500/30",      icon: "🔧" },
  completed:   { label: "已完成",     className: "bg-green-500/20 text-green-300 border-green-500/30",    icon: "✅" },
  failed:      { label: "失败",       className: "bg-red-500/20 text-red-300 border-red-500/30",        icon: "❌" },
};

export function getStageCfg(stage?: string) {
  if (!stage) return null;
  return STAGE_CONFIG[stage] || { label: stage, className: "bg-muted text-muted-foreground border-border", icon: "📌" };
}

// 格式化毫秒为人类可读的时间："12分34秒" / "1时02分30秒"
export function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "0秒";
  const totalSec = Math.floor(ms / 1000);
  const h = Math.floor(totalSec / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const s = totalSec % 60;
  const pad = (n: number) => n.toString().padStart(2, "0");
  if (h > 0) return `${h}时${pad(m)}分${pad(s)}秒`;
  if (m > 0) return `${m}分${pad(s)}秒`;
  return `${s}秒`;
}

// 计算任务已用时间（考虑 startedAt / endedAt）
export function computeElapsedMs(task: Task, nowTs: number): number {
  const start = task.runtime?.startedAt;
  if (!start) return 0;
  const end = task.status === "processing" ? nowTs : (task.runtime?.endedAt || nowTs);
  return end - start;
}

// 后端状态到前端状态的映射
export const STATUS_MAP: Record<string, Task["status"]> = {
  pending: "pending",
  running: "processing",
  completed: "success",
  failed: "failed",
  cancelled: "cancelled",
};

// 状态图标和颜色映射
export const getStatusConfig = (status: Task["status"]) => {
  const configs = {
    pending: { icon: AlertCircle, color: "bg-slate-500/20 text-slate-300 hover:bg-slate-500/30", label: "待处理" },
    processing: { icon: AlertCircle, color: "bg-blue-500/20 text-blue-300 hover:bg-blue-500/30", label: "处理中" },
    success: { icon: CheckCircle, color: "bg-green-500/20 text-green-300 hover:bg-green-500/30", label: "成功" },
    failed: { icon: XCircle, color: "bg-red-500/20 text-red-300 hover:bg-red-500/30", label: "失败" },
    cancelled: { icon: XCircle, color: "bg-gray-500/20 text-gray-300 hover:bg-gray-500/30", label: "已取消" }
  };
  return configs[status] || { icon: CheckCircle, color: "bg-muted text-muted-foreground border border-border hover:bg-muted/80", label: "空闲" };
};

// UI 样式常量
export const BUTTON_STYLES = {
  disabled: "opacity-30 cursor-not-allowed bg-muted hover:bg-muted",
  enabled: "hover:bg-green-500/10 hover:text-green-500",
  loading: "text-blue-500",
  icon: {
    disabled: "text-muted-foreground",
    enabled: "text-foreground"
  }
} as const;

export const ACCOUNT_STYLES = {
  busy: "border-orange-500/30 bg-orange-500/10 text-orange-500",
  normal: ""
} as const;

// 状态标签常量
export const STATUS_LABELS = {
  starting: "启动中",
  running: "运行中"
} as const;
