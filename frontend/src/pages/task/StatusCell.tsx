// 状态 / 进度单元格：状态徽章 + 阶段徽章 + 已用时间 + 进度条 / 骨架条。
// 从 TaskColumns.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { Badge } from "@/components/ui/badge";
import { Clock } from "lucide-react";
import { toast } from "sonner";
import {
  type Task,
  getStatusConfig,
  getStageCfg,
  computeElapsedMs,
  formatElapsed,
} from "./types";
import type { TaskDisplayStatus } from "./useTasks";

export interface StatusCellProps {
  task: Task;
  nowTs: number;
  getTaskDisplayStatus: (task: Task) => TaskDisplayStatus;
}

export function StatusCell({
  task,
  nowTs,
  getTaskDisplayStatus,
}: StatusCellProps) {
  const { status, label } = getTaskDisplayStatus(task);
  const config = getStatusConfig(status);
  const StatusIcon = config.icon;
  const hasError = status === "failed" && task.error;
  const stageCfg = getStageCfg(task.runtime?.stage);
  const isRunning = status === "processing";
  const isScanning = task.runtime?.stage === "scanning" && isRunning;
  const elapsedMs = computeElapsedMs(task, nowTs);
  const elapsedStr = task.runtime?.startedAt ? formatElapsed(elapsedMs) : "";
  const totalFiles = task.runtime?.totalFiles;
  const downloadedFiles = task.runtime?.downloadedFiles;

  return (
    <div className="flex flex-col gap-1.5 min-w-[240px]">
      {/* 顶部：状态徽章 + 阶段徽章 + 已用时间 */}
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge
          className={`${config.color} border-0 ${hasError ? "cursor-help" : ""}`}
          title={hasError ? `失败原因: ${task.error}` : undefined}
          onClick={() => {
            if (hasError && task.error) {
              toast.error(`任务 ${task.name || task.id.slice(0, 8)} 失败`, {
                description: task.error,
                duration: 10000,
                closeButton: true,
              });
            }
          }}
        >
          <StatusIcon className={`w-3 h-3 mr-1 ${hasError ? "animate-pulse" : ""}`} />
          {label}
        </Badge>

        {stageCfg && (
          <Badge variant="outline" className={`${stageCfg.className} border`}>
            <span className="mr-1 text-xs leading-none">{stageCfg.icon}</span>
            {stageCfg.label}
          </Badge>
        )}

        {elapsedStr && (
          <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground font-mono bg-muted/50 rounded px-1.5 py-0.5">
            <Clock className="w-3 h-3" />
            {elapsedStr}
          </span>
        )}
      </div>

      {/* 中部：阶段详情 */}
      {task.runtime?.stageDetail && (
        <div className="text-[12px] text-muted-foreground leading-tight pl-0.5">
          {task.runtime.stageDetail}
        </div>
      )}

      {/* 底部：进度条 / 骨架条 */}
      {(isRunning || status === "success" || status === "failed") && (
        <div className="mt-0.5">
          {isScanning ? (
            <div className="relative w-full h-2 bg-muted rounded-md overflow-hidden">
              <div
                className="absolute inset-y-0 left-0 w-full opacity-60"
                style={{
                  backgroundImage:
                    "linear-gradient(90deg, rgba(6,182,212,0) 0%, rgba(6,182,212,0.4) 50%, rgba(6,182,212,0) 100%)",
                  backgroundSize: "200% 100%",
                  animation: "skeleton-shimmer 1.4s linear infinite",
                }}
              />
              <div
                className="absolute inset-y-0 left-0 bg-cyan-500/70 rounded-md"
                style={{
                  width: "18%",
                  animation: "scanning-sweep 2.4s ease-in-out infinite",
                }}
              />
            </div>
          ) : totalFiles && totalFiles > 0 && downloadedFiles !== undefined ? (
            <>
              <div className="w-full h-2 bg-muted rounded-md overflow-hidden">
                <div
                  className={`h-full rounded-md transition-all duration-500 ${
                    status === "failed"
                      ? "bg-red-500/80"
                      : status === "success"
                        ? "bg-green-500/80"
                        : "bg-blue-500/80"
                  }`}
                  style={{
                    width: `${Math.min(100, Math.round((downloadedFiles / totalFiles) * 100))}%`,
                  }}
                />
              </div>
              <div className="text-[11px] text-muted-foreground mt-0.5 pl-0.5 font-mono">
                {downloadedFiles} / {totalFiles} 个文件
                <span className="ml-2 text-muted-foreground/70">
                  ({Math.round((downloadedFiles / totalFiles) * 100)}%)
                </span>
              </div>
            </>
          ) : isRunning ? (
            <div className="w-full h-2 bg-muted rounded-md overflow-hidden relative">
              <div
                className="absolute inset-y-0 left-0 bg-blue-400/60 rounded-md"
                style={{
                  width: "25%",
                  animation: "scanning-sweep 2s ease-in-out infinite",
                }}
              />
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
