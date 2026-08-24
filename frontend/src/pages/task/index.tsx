import { DataTable } from "@/components/data-table";
import { AddTaskDialog } from "./components/AddTaskDialog";
import { TaskScheduleDialog, TaskSchedule } from "./components/TaskScheduleDialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
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
import { ColumnDef } from "@tanstack/react-table";
import { useNavigate } from "react-router-dom";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";
import { 
  Play, 
  Square, 
  FileText, 
  Edit, 
  Trash2, 
  Plus,
  CheckCircle,
  XCircle,
  AlertCircle,
  FolderX,
  Loader2,
  RefreshCw,
  Clock
} from "lucide-react";

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
  enable302?: boolean;
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
const STAGE_CONFIG: Record<string, { label: string; className: string; icon: string }> = {
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

function getStageCfg(stage?: string) {
  if (!stage) return null;
  return STAGE_CONFIG[stage] || { label: stage, className: "bg-muted text-muted-foreground border-border", icon: "📌" };
}

// 格式化毫秒为人类可读的时间："12分34秒" / "1时02分30秒"
function formatElapsed(ms: number): string {
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
function computeElapsedMs(task: Task, nowTs: number): number {
  const start = task.runtime?.startedAt;
  if (!start) return 0;
  const end = task.status === "processing" ? nowTs : (task.runtime?.endedAt || nowTs);
  return end - start;
}

// 后端状态到前端状态的映射
const STATUS_MAP: Record<string, Task["status"]> = {
  pending: "pending",
  running: "processing",
  completed: "success",
  failed: "failed",
  cancelled: "cancelled",
};

// 状态图标和颜色映射
const getStatusConfig = (status: Task["status"]) => {
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
const BUTTON_STYLES = {
  disabled: "opacity-30 cursor-not-allowed bg-muted hover:bg-muted",
  enabled: "hover:bg-green-500/10 hover:text-green-500",
  loading: "text-blue-500",
  icon: {
    disabled: "text-muted-foreground",
    enabled: "text-foreground"
  }
} as const;

const ACCOUNT_STYLES = {
  busy: "border-orange-500/30 bg-orange-500/10 text-orange-500",
  normal: ""
} as const;

// 状态标签常量
const STATUS_LABELS = {
  starting: "启动中",
  running: "运行中"
} as const;

export default function Home() {
  const [data, setData] = useState<Task[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState<string | null>(null);
  const [deleteCleanStrm, setDeleteCleanStrm] = useState(false);
  const [clearDialogOpen, setClearDialogOpen] = useState<string | null>(null);
  const [accounts, setAccounts] = useState<Array<{name: string, accountType: string}>>([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [startingTasks, setStartingTasks] = useState<Set<string>>(new Set());
  const [nowTs, setNowTs] = useState<number>(Date.now()); // 用于刷新已用时间计时器
  const navigate = useNavigate();

  // 每秒刷新一次 nowTs（驱动所有 running 任务的已用时间显示）
  useEffect(() => {
    const t = setInterval(() => setNowTs(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  // 检查账户是否有任务正在运行或启动
  const isAccountBusy = (accountName: string) => {
    return data.some(task => 
      task.account === accountName && 
      (task.status === "processing" || startingTasks.has(task.id))
    );
  };

  // 检查任务是否应该被禁用
  const isTaskDisabled = (task: Task) => {
    const isStarting = startingTasks.has(task.id);
    const isRunning = task.status === "processing";
    const hasSameAccountRunning = isAccountBusy(task.account);
    
    return isStarting || isRunning || hasSameAccountRunning;
  };

  // 获取任务显示状态
  const getTaskDisplayStatus = (task: Task) => {
    const isStarting = startingTasks.has(task.id);
    const isRunning = task.status === "processing";
    
    if (isStarting) {
      return { status: "processing" as const, label: STATUS_LABELS.starting };
    } else if (isRunning) {
      return { status: "processing" as const, label: STATUS_LABELS.running };
    } else {
      return { status: task.status, label: getStatusConfig(task.status).label };
    }
  };

  useEffect(() => {
    fetchTasks();
    fetchAccounts();
  }, []);

  // 获取任务列表
  const fetchTasks = async () => {
    try {
      setIsLoading(true);
      const res = await axiosInstance.get("/api/tasks");
      const payload = res.data;
      const tasks = Array.isArray(payload) ? payload : (payload.tasks || []);
      const mapped: Task[] = tasks.map((t: any) => ({
        id: t.id || '',
        name: t.name || '',
        account: t.account || '',
        accountType: t.accountType || '',
        originPath: t.originPath || '',
        targetPath: t.targetPath || '',
        strmType: t.strmType || '',
        strmPrefix: t.strmPrefix || '',
        removeExtraFiles: t.removeExtraFiles ?? false,
        enable302: t.enable302 ?? false,
        // 使用状态映射将后端状态转换为前端状态
        status: STATUS_MAP[t.runtime?.status || t.status] || "pending",
        error: t.runtime?.error ?? null,
        schedule: t.schedule ? {
          enabled: t.schedule.enabled ?? false,
          mode: t.schedule.mode || "interval",
          intervalMinutes: t.schedule.intervalMinutes,
          time: t.schedule.time,
          weekdays: t.schedule.weekdays,
          lastRunAt: t.schedule.lastRunAt,
          nextRunAt: t.scheduleNext?.nextRunAt,
        } : undefined,
        _computedNextRunAt: t.scheduleNext?.nextRunAt ?? null,
      }));
      setData(mapped);
    } catch {
      toast.error("获取任务列表失败");
    } finally {
      setIsLoading(false);
    }
  };

  // 获取账户列表
  const fetchAccounts = async () => {
    try {
      setAccountsLoading(true);
      const res = await axiosInstance.get("/api/account");
      setAccounts(res.data.map((a: { name: string, accountType: string }) => ({ name: a.name, accountType: a.accountType })));
    } catch {
      toast.error("获取账户列表失败");
    } finally {
      setAccountsLoading(false);
    }
  };



  // 删除任务
  const deleteTask = async (id: string, cleanStrm: boolean) => {
    try {
      await axiosInstance.delete(`/api/task?id=${id}${cleanStrm ? "&cleanStrm=true" : ""}`);
      toast.success(cleanStrm ? "任务删除成功，STRM 目录已清理" : "任务删除成功");
      fetchTasks();
    } catch {
      toast.error("删除失败");
    }
  };

  // 开始任务
  const startTask = async (id: string) => {
    // 添加到正在启动的任务集合
    setStartingTasks(prev => new Set(prev).add(id));
    
    try {
      const res = await axiosInstance.post("/api/startTask", { taskId: id }, {
        timeout: 180000 // 设置180秒超时
      });
      toast.success(`任务已开始: ${res.data.message}`);
      
      // 只有在API成功返回后才更新状态为processing
      setData(prevData => 
        prevData.map(task => 
          task.id === id ? { ...task, status: "processing" as const, error: null } : task
        )
      );
      
      // 启动后自动轮询刷新状态：3s、10s 各一次（快速失败能立即看到）
      setTimeout(() => {
        fetchTasks();
      }, 3000);
      setTimeout(() => {
        fetchTasks();
      }, 10000);
    } catch (error: unknown) {
      if (error && typeof error === 'object' && 'code' in error && error.code === 'ECONNABORTED') {
        toast.error("任务启动超时，请稍后检查任务状态");
      } else if (error && typeof error === 'object' && 'response' in error) {
        // 处理API错误响应
        const apiError = error as { response?: { data?: { message?: string; error?: string } } };
        const message = apiError.response?.data?.message || "任务开始失败";
        const detail = apiError.response?.data?.error;
        const errorText = detail ? `${message}: ${detail}` : message;
        toast.error(errorText);
      } else {
        toast.error("任务开始失败");
      }
    } finally {
      // 从正在启动的任务集合中移除
      setStartingTasks(prev => {
        const newSet = new Set(prev);
        newSet.delete(id);
        return newSet;
      });
    }
  };

  // 取消任务
  const cancelTask = async (id: string) => {
    try {
      await axiosInstance.post("/api/cancelTask", { taskId: id });
      toast.success("任务已取消");
    } catch {
      toast.error("任务取消失败");
    }
  };

  // 查看日志
  const goToLog = async (id: string) => {
    try {
      const logRes = await axiosInstance.get(`/api/taskLog/${id}`);
      const logText: string = logRes.data || "";
      if (logText.trim()) {
        navigate(`/log/${id}`);
      } else {
        toast.info("任务尚未执行，暂无日志");
      }
    } catch {
      toast.error("获取日志失败");
    }
  };

  // 清空目录
  const clearDirectory = async (targetPath: string) => {
    try {
      await axiosInstance.post("/api/directory/clear", { targetPath });
      toast.success(`目录 ${targetPath} 清空成功`);
    } catch (error: unknown) {
      const errorMessage = (error as { response?: { data?: { error?: string } } })?.response?.data?.error || "清空目录失败";
      toast.error(errorMessage);
    }
  };

  const columns: ColumnDef<Task>[] = [
    { 
      accessorKey: "id", 
      header: "任务ID",
      cell: ({ row }) => (
        <code className="text-xs bg-muted px-2 py-1 rounded">
          {row.original.id.slice(0, 8)}...
        </code>
      )
    },
    { 
      accessorKey: "account", 
      header: "账户",
      cell: ({ row }) => {
        const task = row.original;
        const isBusy = isAccountBusy(task.account);
        
        return (
          <div className="flex items-center gap-2">
            <Badge 
              variant="outline" 
              className={`text-xs ${
                isBusy ? ACCOUNT_STYLES.busy : ACCOUNT_STYLES.normal
              }`}
            >
              {task.accountType}
            </Badge>
            <span className={`font-medium ${
              isBusy ? "text-orange-700" : ""
            }`}>
              {task.account}
              {isBusy && (
                <span className="ml-1 text-xs text-orange-600">●</span>
              )}
            </span>
          </div>
        );
      }
    },
    { 
      accessorKey: "originPath", 
      header: "远程路径",
      cell: ({ row }) => {
        const task = row.original;
        return (
          <span className="text-sm text-muted-foreground max-w-xs truncate block">
            {row.original.originPath}
          </span>
        );
      }
    },
    { 
      accessorKey: "targetPath", 
      header: "本地路径",
      cell: ({ row }) => {
        const task = row.original;
        return (
          <div className="group flex items-center gap-2 max-w-xs">
            <span className="text-sm text-muted-foreground truncate flex-1">
              {task.targetPath}
            </span>
            <Dialog 
              open={clearDialogOpen === task.id} 
              onOpenChange={(open) => setClearDialogOpen(open ? task.id : null)}
            >
              <DialogTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 w-7 p-0 text-muted-foreground hover:text-red-500 hover:bg-red-500/10 opacity-0 group-hover:opacity-100 transition-all duration-200 flex-shrink-0"
                  title="清空目录"
                >
                  <FolderX className="w-4 h-4" />
                </Button>
              </DialogTrigger>
              <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
                <DialogHeader>
                  <DialogTitle>确认清空目录</DialogTitle>
                  <DialogDescription>
                    你确定要清空目标路径下的所有文件吗？此操作无法撤销。
                    <br />
                    <span className="text-sm text-gray-500 mt-2 block">
                      目标路径: {task.targetPath}
                    </span>
                    <br />
                    <span className="text-sm text-red-600 mt-2 block font-medium">
                      ⚠️ 这将删除该目录下的所有文件和子目录！
                    </span>
                  </DialogDescription>
                </DialogHeader>
                <DialogFooter className="gap-2">
                  <Button 
                    variant="outline"
                    onClick={() => setClearDialogOpen(null)}
                  >
                    取消
                  </Button>
                  <Button
                    variant="destructive"
                    onClick={() => {
                      clearDirectory(task.targetPath);
                      setClearDialogOpen(null);
                    }}
                  >
                    确认清空
                  </Button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
          </div>
        );
      }
    },
    { 
      accessorKey: "status", 
      header: "状态 / 进度",
      cell: ({ row }) => {
        const task = row.original;
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
                    toast.error(`任务 ${task.name || task.id.slice(0,8)} 失败`, {
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

            {/* 中部：阶段详情（扫描中显示 "已扫描 5 个目录..."） */}
            {task.runtime?.stageDetail && (
              <div className="text-[12px] text-muted-foreground leading-tight pl-0.5">
                {task.runtime.stageDetail}
              </div>
            )}

            {/* 底部：进度条 / 骨架条（running + scanning 或 0% 时显示骨架动画） */}
            {(isRunning || status === "success" || status === "failed") && (
              <div className="mt-0.5">
                {/* 骨架条：running 且 scanning 阶段显示脉动条纹 */}
                {isScanning ? (
                  <div className="relative w-full h-2 bg-muted rounded-md overflow-hidden">
                    {/* 条纹动画层 */}
                    <div 
                      className="absolute inset-y-0 left-0 w-full opacity-60"
                      style={{
                        backgroundImage:
                          "linear-gradient(90deg, rgba(6,182,212,0) 0%, rgba(6,182,212,0.4) 50%, rgba(6,182,212,0) 100%)",
                        backgroundSize: "200% 100%",
                        animation: "skeleton-shimmer 1.4s linear infinite",
                      }}
                    />
                    {/* 顶部高亮线 */}
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
                  // 运行中但还没有 totalFiles（短暂过渡）：简易骨架
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
    },
    {
      id: "schedule",
      header: "定时",
      cell: ({ row }) => {
        const task = row.original;
        const sched = task.schedule;
        const enabled = !!sched?.enabled;
        const nextRun =
          task._computedNextRunAt ?? sched?.nextRunAt ?? null;

        if (!enabled) {
          return (
            <span className="text-xs text-slate-400">未配置</span>
          );
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
      },
    },
    {
      id: "actions",
      header: "操作",
      cell: ({ row }) => {
        const task = row.original;
        const isStarting = startingTasks.has(task.id);
        const isDisabled = isTaskDisabled(task);
        
        return (
          <div className="flex gap-1">
            <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => startTask(task.id)}
                  disabled={isDisabled}
                  className={`h-8 w-8 p-0 ${
                    isDisabled 
                      ? BUTTON_STYLES.disabled 
                      : task.status === "processing"
                        ? "bg-blue-500/20 hover:bg-blue-500/30" 
                        : BUTTON_STYLES.enabled
                  }`}
              title={
                isStarting ? `${STATUS_LABELS.starting}...` :
                task.status === "processing" ? "任务运行中" :
                isAccountBusy(task.account) ? `账户 ${task.account} 有任务正在运行` :
                "开始任务"
              }
            >
              {isStarting ? (
                <Loader2 className={`w-4 h-4 animate-spin ${BUTTON_STYLES.loading}`} />
              ) : task.status === "processing" ? (
                <div className="w-4 h-4 flex items-center justify-center">
                  <div className="w-2 h-2 bg-blue-600 rounded-full animate-pulse"></div>
                </div>
              ) : (
                <Play className={`w-4 h-4 ${
                  isDisabled ? BUTTON_STYLES.icon.disabled : BUTTON_STYLES.icon.enabled
                }`} />
              )}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => cancelTask(task.id)}
              className="h-8 w-8 p-0"
              title="取消任务"
            >
              <Square className="w-4 h-4" />
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => goToLog(task.id)}
              className="h-8 w-8 p-0"
              title="查看日志"
            >
              <FileText className="w-4 h-4" />
            </Button>
            <TaskScheduleDialog
              task={task}
              onSuccess={fetchTasks}
              trigger={
                <Button
                  variant="ghost"
                  size="sm"
                  className={`h-8 w-8 p-0 ${
                    task.schedule?.enabled
                      ? "text-indigo-600 hover:text-indigo-700 hover:bg-indigo-50"
                      : ""
                  }`}
                  title={
                    task.schedule?.enabled
                      ? "定时任务已启用，点击编辑"
                      : "配置定时任务"
                  }
                >
                  <Clock className="w-4 h-4" />
                </Button>
              }
            />
            <AddTaskDialog
              task={task}
              accounts={accounts}
              accountsLoading={accountsLoading}
              trigger={
                <Button 
                  variant="ghost" 
                  size="sm"
                  className="h-8 w-8 p-0"
                  title="编辑任务"
                >
                  <Edit className="w-4 h-4" />
                </Button>
              }
              onSuccess={fetchTasks}
            />
            <Dialog 
              open={deleteDialogOpen === task.id} 
              onOpenChange={(open) => setDeleteDialogOpen(open ? task.id : null)}
            >
              <DialogTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-8 w-8 p-0 text-red-500 hover:text-red-600 hover:bg-red-500/10"
                  title="删除任务"
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </DialogTrigger>
              <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
                <DialogHeader>
                  <DialogTitle>确认删除</DialogTitle>
                  <DialogDescription>
                    你确定要删除这个任务吗？此操作无法撤销。
                    <br />
                    <span className="text-sm text-gray-500 mt-2 block">
                      任务ID: {task.id.slice(0, 8)}...
                    </span>
                    <span className="text-sm text-gray-500 mt-1 block">
                      目标路径: {task.targetPath}
                    </span>
                  </DialogDescription>
                </DialogHeader>
                <div className="flex items-center gap-2 py-2">
                  <Checkbox
                    id={`clean-strm-${task.id}`}
                    checked={deleteCleanStrm}
                    onCheckedChange={(checked) => setDeleteCleanStrm(checked === true)}
                  />
                  <label htmlFor={`clean-strm-${task.id}`} className="text-sm cursor-pointer">
                    同时清理 STRM 目录（{task.targetPath}）
                  </label>
                </div>
                {deleteCleanStrm && (
                  <p className="text-xs text-red-600 font-medium">
                    ⚠️ 将删除该目录下所有 STRM 文件和子目录！
                  </p>
                )}
                <DialogFooter className="gap-2">
                  <Button 
                    variant="outline"
                    onClick={() => {
                      setDeleteCleanStrm(false);
                      setDeleteDialogOpen(null);
                    }}
                  >
                    取消
                  </Button>
                  <Button
                    variant="destructive"
                    onClick={() => {
                      deleteTask(task.id, deleteCleanStrm);
                      setDeleteCleanStrm(false);
                      setDeleteDialogOpen(null);
                    }}
                  >
                    删除
                  </Button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
          </div>
        );
      },
    },
  ];

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-7xl space-y-6">
      {/* 动画 keyframes：扫描骨架闪烁 + 扫描扫光效果 */}
      <style>{`
        @keyframes skeleton-shimmer {
          0%   { background-position: -200% 0; }
          100% { background-position:  200% 0; }
        }
        @keyframes scanning-sweep {
          0%   { left: -20%; width: 15%; }
          50%  { left:  60%; width: 25%; }
          100% { left: 110%; width: 15%; }
        }
      `}</style>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold">任务管理</h1>
          <p className="text-muted-foreground mt-1">管理和监控你的下载任务</p>
        </div>
        <div className="flex gap-2">
          <Button 
            variant="outline" 
            onClick={fetchTasks}
            disabled={isLoading}
          >
            <RefreshCw className={`w-4 h-4 mr-2 ${isLoading ? 'animate-spin' : ''}`} />
            刷新状态
          </Button>
          <AddTaskDialog 
            onSuccess={fetchTasks}
            accounts={accounts}
            accountsLoading={accountsLoading}
            trigger={
              <Button>
                <Plus className="w-4 h-4 mr-2" />
                新建任务
              </Button>
            }
          />
        </div>
      </div>
      
      {data.length === 0 ? (
        <div className="text-center py-12 bg-muted/30 rounded-lg border border-border">
          <AlertCircle className="mx-auto h-12 w-12 text-muted-foreground" />
          <h3 className="mt-4 text-lg font-medium">暂无任务</h3>
          <p className="mt-2 text-muted-foreground">点击上方按钮创建你的第一个任务</p>
        </div>
      ) : (
        <DataTable columns={columns} data={data} />
      )}
    </div>
  );
}
