import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Play,
  Square,
  FileText,
  Edit,
  Trash2,
  Loader2,
  Clock,
  User,
  FolderOpen,
} from "lucide-react";
import { AddTaskDialog } from "./components/AddTaskDialog";
import { TaskScheduleDialog } from "./components/TaskScheduleDialog";
import {
  type Task,
  getStatusConfig,
  getStageCfg,
  computeElapsedMs,
  formatElapsed,
  BUTTON_STYLES,
  ACCOUNT_STYLES,
} from "./types";

export interface MobileTaskCardProps {
  task: Task;
  nowTs: number;
  startingTasks: Set<string>;
  accounts: Array<{ name: string; accountType: string }>;
  accountsLoading: boolean;
  isAccountBusy: (accountName: string) => boolean;
  isTaskDisabled: (task: Task) => boolean;
  getTaskDisplayStatus: (task: Task) => { status: Task["status"]; label: string };
  startTask: (id: string) => void;
  cancelTask: (id: string) => void;
  goToLog: (id: string) => void;
  fetchTasks: () => void;
  setDeleteDialogOpen: (id: string | null) => void;
}

export function MobileTaskCard(props: MobileTaskCardProps) {
  const {
    task,
    nowTs,
    startingTasks,
    accounts,
    accountsLoading,
    isAccountBusy,
    isTaskDisabled,
    getTaskDisplayStatus,
    startTask,
    cancelTask,
    goToLog,
    fetchTasks,
    setDeleteDialogOpen,
  } = props;

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
  const isStarting = startingTasks.has(task.id);
  const isDisabled = isTaskDisabled(task);
  const isBusy = isAccountBusy(task.account);

  // Schedule info
  const sched = task.schedule;
  const enabled = !!sched?.enabled;
  const nextRun = task._computedNextRunAt ?? sched?.nextRunAt ?? null;
  let schedText = "";
  if (enabled && nextRun) {
    const d = new Date(nextRun);
    const now = new Date();
    const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
    const pad = (n: number) => n.toString().padStart(2, "0");
    const t = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
    const dateStr = sameDay ? `今天 ${t}` : `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${t}`;
    const modeLabel = sched.mode === "interval" ? `每${sched.intervalMinutes}分` : sched.mode === "daily" ? `每天${sched.time}` : `每周${sched.time}`;
    schedText = `${modeLabel} · ${dateStr}`;
  }

  return (
    <Card key={task.id} className="py-0 shadow-sm">
      <CardContent className="p-3 space-y-3">
        {/* Header: task id + status */}
        <div className="flex items-start justify-between gap-2">
          <div className="flex items-center gap-1.5 min-w-0">
            <code className="text-xs bg-muted px-1.5 py-0.5 rounded font-mono">
              {task.id.slice(0, 8)}
            </code>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <Badge
              className={`${config.color} border-0 ${hasError ? "cursor-help" : ""} text-xs`}
              title={hasError ? `失败原因: ${task.error}` : undefined}
            >
              <StatusIcon className={`w-3 h-3 mr-1 ${hasError ? "animate-pulse" : ""}`} />
              {label}
            </Badge>
          </div>
        </div>

        {/* Account info */}
        <div className="flex items-center gap-2 min-w-0">
          <User className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
          <Badge variant="outline" className={`text-xs shrink-0 ${isBusy ? ACCOUNT_STYLES.busy : ""}`}>
            {task.accountType}
          </Badge>
          <span className={`text-sm truncate ${isBusy ? "text-orange-700 font-medium" : ""}`}>
            {task.account}
          </span>
          {isBusy && <span className="text-orange-600 text-xs">●</span>}
        </div>

        {/* Paths */}
        <div className="space-y-1.5 text-xs">
          <div className="flex items-center gap-1.5 min-w-0">
            <FolderOpen className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
            <span className="text-muted-foreground">远程：</span>
            <span className="truncate font-mono" title={task.originPath}>{task.originPath}</span>
          </div>
          <div className="flex items-center gap-1.5 min-w-0">
            <FolderOpen className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
            <span className="text-muted-foreground">本地：</span>
            <span className="truncate font-mono" title={task.targetPath}>{task.targetPath}</span>
          </div>
        </div>

        {/* Stage + progress */}
        {(isRunning || status === "success" || status === "failed") && (
          <div className="space-y-1.5">
            <div className="flex items-center gap-1.5 flex-wrap">
              {stageCfg && (
                <Badge variant="outline" className={`${stageCfg.className} border text-xs`}>
                  <span className="mr-1 text-xs">{stageCfg.icon}</span>
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
            {task.runtime?.stageDetail && (
              <div className="text-[11px] text-muted-foreground leading-tight">
                {task.runtime.stageDetail}
              </div>
            )}
            {/* Progress bar */}
            {isScanning ? (
              <div className="relative w-full h-2 bg-muted rounded-md overflow-hidden">
                <div
                  className="absolute inset-y-0 left-0 w-full opacity-60"
                  style={{
                    backgroundImage: "linear-gradient(90deg, rgba(6,182,212,0) 0%, rgba(6,182,212,0.4) 50%, rgba(6,182,212,0) 100%)",
                    backgroundSize: "200% 100%",
                    animation: "skeleton-shimmer 1.4s linear infinite",
                  }}
                />
                <div
                  className="absolute inset-y-0 left-0 bg-cyan-500/70 rounded-md"
                  style={{ width: "18%", animation: "scanning-sweep 2.4s ease-in-out infinite" }}
                />
              </div>
            ) : totalFiles && totalFiles > 0 && downloadedFiles !== undefined ? (
              <>
                <div className="w-full h-2 bg-muted rounded-md overflow-hidden">
                  <div
                    className={`h-full rounded-md transition-all duration-500 ${
                      status === "failed" ? "bg-red-500/80" :
                      status === "success" ? "bg-green-500/80" :
                      "bg-blue-500/80"
                    }`}
                    style={{ width: `${Math.min(100, Math.round((downloadedFiles / totalFiles) * 100))}%` }}
                  />
                </div>
                <div className="text-[11px] text-muted-foreground font-mono">
                  {downloadedFiles} / {totalFiles} 个文件 ({Math.round((downloadedFiles / totalFiles) * 100)}%)
                </div>
              </>
            ) : isRunning ? (
              <div className="w-full h-2 bg-muted rounded-md overflow-hidden relative">
                <div
                  className="absolute inset-y-0 left-0 bg-blue-400/60 rounded-md"
                  style={{ width: "25%", animation: "scanning-sweep 2s ease-in-out infinite" }}
                />
              </div>
            ) : null}
          </div>
        )}

        {/* Schedule */}
        {enabled && (
          <div className="flex items-center gap-1.5 text-xs">
            <Clock className="w-3 h-3 text-indigo-500" />
            <span className="text-indigo-700 font-medium">定时：</span>
            <span className="text-muted-foreground">{schedText}</span>
          </div>
        )}

        {/* Actions */}
        <div className="flex items-center justify-between gap-2 pt-2 border-t border-border/50">
          <div className="flex gap-1">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => startTask(task.id)}
              disabled={isDisabled}
              className={`h-7 w-7 p-0 ${isDisabled ? BUTTON_STYLES.disabled : task.status === "processing" ? "bg-blue-500/20" : BUTTON_STYLES.enabled}`}
              title="开始任务"
            >
              {isStarting ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin text-blue-500" />
              ) : task.status === "processing" ? (
                <div className="w-2.5 h-2.5 bg-blue-600 rounded-full animate-pulse"></div>
              ) : (
                <Play className={`w-3.5 h-3.5 ${isDisabled ? "text-muted-foreground" : "text-foreground"}`} />
              )}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => cancelTask(task.id)} className="h-7 w-7 p-0" title="取消任务">
              <Square className="w-3.5 h-3.5" />
            </Button>
            <Button variant="ghost" size="sm" onClick={() => goToLog(task.id)} className="h-7 w-7 p-0" title="查看日志">
              <FileText className="w-3.5 h-3.5" />
            </Button>
            <TaskScheduleDialog
              task={task}
              onSuccess={fetchTasks}
              trigger={
                <Button
                  variant="ghost"
                  size="sm"
                  className={`h-7 w-7 p-0 ${task.schedule?.enabled ? "text-indigo-600" : ""}`}
                  title="配置定时任务"
                >
                  <Clock className="w-3.5 h-3.5" />
                </Button>
              }
            />
            <AddTaskDialog
              task={task}
              accounts={accounts}
              accountsLoading={accountsLoading}
              onSuccess={fetchTasks}
              trigger={
                <Button variant="ghost" size="sm" className="h-7 w-7 p-0" title="编辑任务">
                  <Edit className="w-3.5 h-3.5" />
                </Button>
              }
            />
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0 text-red-500 hover:text-red-600 hover:bg-red-500/10 shrink-0"
            onClick={() => setDeleteDialogOpen(task.id)}
            title="删除任务"
          >
            <Trash2 className="w-3.5 h-3.5" />
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
