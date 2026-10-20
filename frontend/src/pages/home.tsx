import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  Activity,
  ArrowRight,
  BellRing,
  CheckCircle2,
  HeartPulse,
  ListChecks,
  Loader2,
  RefreshCw,
  Server,
  Trash2,
  Users,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import axiosInstance from "@/lib/axios";

interface AccountInfo {
  name: string;
  accountType: string;
  cookieStatus?: string;
  cookieValid?: boolean;
  url?: string;
}

interface TaskInfo {
  id: string;
  name?: string;
  account: string;
  runtime?: {
    status?: string;
    totalFiles?: number;
    downloadedFiles?: number;
    stage?: string;
    stageDetail?: string;
  };
  scheduleNext?: { nextRunAt?: number };
}

interface PendingBatch {
  pathCount?: number;
}

interface TrashEntry {
  trashPath: string;
}

interface PollingInfo {
  polling?: boolean;
  webhook?: { url?: string };
}

interface EmbySettingsInfo {
  url?: string;
  apiKey?: string;
}

interface MonitorState {
  account: string;
  running: boolean;
}

interface LifeMonitorInfo {
  config?: { enabled?: boolean; accounts?: string[] };
  states?: MonitorState[];
}

interface StrmHistoryItem {
  id: number;
  taskId?: string;
  kind?: string;
  errorMsg?: string;
  createdAt?: number;
}

interface TaskHistoryItem {
  id: number;
  taskId?: string;
  error?: string;
  status?: string;
}

interface AlertItem {
  title: string;
  detail: string;
  to: string;
  tone: "danger" | "warning" | "info";
}

const TASK_STATUS_LABELS: Record<string, { label: string; className: string }> = {
  running: { label: "运行中", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" },
  completed: { label: "已完成", className: "bg-green-500/15 text-green-600 dark:text-green-400" },
  failed: { label: "失败", className: "bg-red-500/15 text-red-600 dark:text-red-400" },
  cancelled: { label: "已取消", className: "bg-muted text-muted-foreground" },
  pending: { label: "等待", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" },
};

function formatNextRun(ts?: number): string {
  if (!ts) return "未配置定时";
  return new Date(ts).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function ProgressBar({ value, tone = "default" }: { value: number; tone?: "default" | "success" | "danger" }) {
  const color =
    tone === "success" ? "bg-green-500" : tone === "danger" ? "bg-red-500" : "bg-primary";
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
      <div className={`h-full rounded-full ${color}`} style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  );
}

export default function OverviewPage() {
  const [accounts, setAccounts] = useState<AccountInfo[]>([]);
  const [tasks, setTasks] = useState<TaskInfo[]>([]);
  const [pendingBatches, setPendingBatches] = useState<PendingBatch[]>([]);
  const [trash, setTrash] = useState<TrashEntry[]>([]);
  const [polling, setPolling] = useState<PollingInfo | null>(null);
  const [emby, setEmby] = useState<EmbySettingsInfo | null>(null);
  const [life, setLife] = useState<LifeMonitorInfo | null>(null);
  const [failedStrm, setFailedStrm] = useState<StrmHistoryItem[]>([]);
  const [failedTasks, setFailedTasks] = useState<TaskHistoryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  const load = async (isRefresh = false) => {
    if (isRefresh) setRefreshing(true);
    else setLoading(true);

    const results = await Promise.allSettled([
      axiosInstance.get("/api/account"),
      axiosInstance.get("/api/tasks"),
      axiosInstance.get("/api/strmCleanup/pending"),
      axiosInstance.get("/api/strmCleanup/trash"),
      axiosInstance.get("/api/notify/polling"),
      axiosInstance.get("/api/emby/settings"),
      axiosInstance.get("/api/lifeMonitor"),
      axiosInstance.get("/api/history/strm?limit=20"),
      axiosInstance.get("/api/taskHistory?status=failed&limit=5"),
    ]);

    if (results[0].status === "fulfilled") setAccounts(Array.isArray(results[0].value.data) ? results[0].value.data : []);
    if (results[1].status === "fulfilled") setTasks(Array.isArray(results[1].value.data?.tasks) ? results[1].value.data.tasks : []);
    if (results[2].status === "fulfilled") setPendingBatches(Array.isArray(results[2].value.data?.batches) ? results[2].value.data.batches : []);
    if (results[3].status === "fulfilled") setTrash(Array.isArray(results[3].value.data?.entries) ? results[3].value.data.entries : []);
    setPolling(results[4].status === "fulfilled" ? results[4].value.data : null);
    setEmby(results[5].status === "fulfilled" ? results[5].value.data : null);
    setLife(results[6].status === "fulfilled" ? results[6].value.data : null);
    if (results[7].status === "fulfilled") {
      const items = Array.isArray(results[7].value.data?.items) ? results[7].value.data.items : [];
      setFailedStrm(items.filter((item: StrmHistoryItem) => item.errorMsg).slice(0, 2));
    }
    if (results[8].status === "fulfilled") {
      const items = Array.isArray(results[8].value.data?.items) ? results[8].value.data.items : [];
      setFailedTasks(items.filter((item: TaskHistoryItem) => item.status === "failed").slice(0, 2));
    }

    if (isRefresh) setRefreshing(false);
    else setLoading(false);
  };

  useEffect(() => {
    void load();
    // 概览只在进入时聚合一次；后续用刷新按钮
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const accountStats = useMemo(() => {
    let valid = 0;
    let invalid = 0;
    let unknown = 0;
    for (const account of accounts) {
      if (account.accountType === "openlist") {
        if (account.url) valid += 1;
        else invalid += 1;
        continue;
      }
      const status = account.cookieStatus || (account.cookieValid === true ? "valid" : account.cookieValid === false ? "invalid" : "unknown");
      if (status === "valid") valid += 1;
      else if (status === "invalid") invalid += 1;
      else unknown += 1;
    }
    return { valid, invalid, unknown, total: accounts.length };
  }, [accounts]);

  const runningTasks = useMemo(
    () => tasks.filter((task) => task.runtime?.status === "running" || task.runtime?.status === "pending"),
    [tasks]
  );
  const nextScheduled = useMemo(() => {
    const times = tasks
      .map((task) => task.scheduleNext?.nextRunAt)
      .filter((ts): ts is number => typeof ts === "number" && ts > Date.now());
    return times.length > 0 ? Math.min(...times) : undefined;
  }, [tasks]);
  const pendingFiles = pendingBatches.reduce((sum, batch) => sum + (batch.pathCount || 0), 0);
  const telegramOnline = Boolean(polling?.polling || polling?.webhook?.url);
  const embyConfigured = Boolean(emby?.url && emby?.apiKey);
  const monitorStates = life?.states || [];
  const monitorRunning = monitorStates.filter((state) => state.running).length;
  const monitorTotal = life?.config?.accounts?.length || monitorStates.length;
  const monitorEnabled = Boolean(life?.config?.enabled);
  const serviceCount = [embyConfigured, telegramOnline, monitorEnabled].filter(Boolean).length;

  const alerts = useMemo<AlertItem[]>(() => {
    const list: AlertItem[] = [];
    const invalid115 = accounts.filter(
      (account) =>
        account.accountType !== "openlist" &&
        (account.cookieStatus === "invalid" || account.cookieValid === false)
    );
    if (invalid115.length > 0) {
      const names = invalid115.slice(0, 2).map((account) => account.name);
      list.push({
        title: "账号 Cookie 失效",
        detail: `${names.join("、")}${invalid115.length > names.length ? " 等" : ""} 需要重新登录`,
        to: "/account",
        tone: "danger",
      });
    }
    if (pendingBatches.length > 0) {
      list.push({
        title: `${pendingBatches.length} 个清理批次待确认`,
        detail: `共 ${pendingFiles} 个文件，超过阈值后已进入待确认队列`,
        to: "/cleanup",
        tone: "warning",
      });
    }
    if (failedTasks.length > 0) {
      list.push({
        title: "最近有任务执行失败",
        detail: failedTasks[0]?.error || `任务 ${failedTasks[0]?.taskId || "-"} 执行失败`,
        to: "/history",
        tone: "warning",
      });
    }
    if (failedStrm.length > 0) {
      list.push({
        title: "最近有 STRM 操作失败",
        detail: failedStrm[0]?.errorMsg || `任务 ${failedStrm[0]?.taskId || "-"} 的 STRM 操作失败`,
        to: "/history?tab=strm",
        tone: "warning",
      });
    }
    return list.slice(0, 3);
  }, [accounts, accountStats.invalid, failedStrm, failedTasks, pendingBatches.length, pendingFiles]);

  const taskRows = useMemo(() => {
    const priority: Record<string, number> = { running: 0, pending: 1, failed: 2, completed: 3, cancelled: 4 };
    return [...tasks]
      .sort((a, b) => (priority[a.runtime?.status || "completed"] ?? 5) - (priority[b.runtime?.status || "completed"] ?? 5))
      .slice(0, 3);
  }, [tasks]);

  return (
    <div className="mx-auto max-w-5xl space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">概览</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            账号、任务、通知和清理状态一屏掌握。
          </p>
        </div>
        <div className="flex gap-2 shrink-0">
          <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={refreshing}>
            <RefreshCw className={`mr-1 h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
            刷新
          </Button>
          <Button size="sm" asChild>
            <Link to="/task">
              <ListChecks className="mr-1 h-4 w-4" />
              管理任务
            </Link>
          </Button>
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center h-64 text-sm text-muted-foreground">
          <Loader2 className="mr-2 h-4 w-4 animate-spin" />
          正在汇总状态...
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
            <Card>
              <CardContent className="p-4 space-y-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">账户</span>
                  <Users className="h-4 w-4 text-muted-foreground" />
                </div>
                <div>
                  <span className="text-2xl font-semibold">{accountStats.valid}</span>
                  <span className="text-sm text-muted-foreground"> / {accountStats.total}</span>
                </div>
                <div className="flex flex-wrap gap-x-2 gap-y-1 text-xs text-muted-foreground">
                  <span className="text-green-600 dark:text-green-400">{accountStats.valid} 正常</span>
                  <span className="text-red-600 dark:text-red-400">{accountStats.invalid} 异常</span>
                  <span>{accountStats.unknown} 待验证</span>
                </div>
                <ProgressBar value={accountStats.total > 0 ? (accountStats.valid / accountStats.total) * 100 : 0} tone="success" />
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-4 space-y-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">任务</span>
                  <ListChecks className="h-4 w-4 text-muted-foreground" />
                </div>
                <div>
                  <span className="text-2xl font-semibold">{runningTasks.length}</span>
                  <span className="text-sm text-muted-foreground"> 进行中</span>
                </div>
                <div className="text-xs text-muted-foreground">
                  下次定时：{formatNextRun(nextScheduled)}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-4 space-y-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">STRM 清理</span>
                  <Trash2 className="h-4 w-4 text-muted-foreground" />
                </div>
                <div>
                  <span className="text-2xl font-semibold">{pendingFiles}</span>
                  <span className="text-sm text-muted-foreground"> 待确认文件</span>
                </div>
                <div className="text-xs text-muted-foreground">
                  {pendingBatches.length} 个批次 · 回收站 {trash.length} 项
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-4 space-y-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">服务状态</span>
                  <Server className="h-4 w-4 text-muted-foreground" />
                </div>
                <div>
                  <span className="text-2xl font-semibold">{serviceCount}</span>
                  <span className="text-sm text-muted-foreground"> / 3 已配置</span>
                </div>
                <div className="flex flex-wrap gap-x-2 gap-y-1 text-xs text-muted-foreground">
                  <span className={embyConfigured ? "text-green-600 dark:text-green-400" : ""}>Emby</span>
                  <span className={telegramOnline ? "text-green-600 dark:text-green-400" : ""}>Telegram</span>
                  <span className={monitorEnabled ? "text-green-600 dark:text-green-400" : ""}>生活监控</span>
                </div>
                <ProgressBar value={(serviceCount / 3) * 100} tone={serviceCount === 3 ? "success" : "default"} />
              </CardContent>
            </Card>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            <Card className="lg:col-span-2">
              <CardHeader className="pb-3">
                <div className="flex items-center justify-between gap-2">
                  <div>
                    <CardTitle className="text-base">任务运行</CardTitle>
                    <CardDescription className="mt-1">当前执行中的任务与最近状态</CardDescription>
                  </div>
                  <Button variant="ghost" size="sm" asChild>
                    <Link to="/task">
                      管理任务 <ArrowRight className="ml-1 h-3.5 w-3.5" />
                    </Link>
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="pt-0">
                {taskRows.length === 0 ? (
                  <p className="py-8 text-center text-sm text-muted-foreground">暂无任务，先去创建一个。</p>
                ) : (
                  <div className="space-y-1">
                    {taskRows.map((task) => {
                      const status = task.runtime?.status || "pending";
                      const statusCfg = TASK_STATUS_LABELS[status] || TASK_STATUS_LABELS.pending;
                      const total = task.runtime?.totalFiles || 0;
                      const downloaded = task.runtime?.downloadedFiles || 0;
                      const percent = total > 0 ? Math.round((downloaded / total) * 100) : 0;
                      const isRunning = status === "running";
                      return (
                        <Link
                          key={task.id}
                          to={isRunning ? `/log/${encodeURIComponent(task.id)}` : "/task"}
                          className="flex items-start gap-3 rounded-md p-2.5 hover:bg-muted/50 transition-colors"
                        >
                          <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-muted">
                            {isRunning ? (
                              <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
                            ) : (
                              <ListChecks className="h-4 w-4 text-muted-foreground" />
                            )}
                          </div>
                          <div className="min-w-0 flex-1 space-y-1.5">
                            <div className="flex flex-wrap items-center gap-2">
                              <span className="truncate text-sm font-medium">{task.name || task.id}</span>
                              <Badge className={`text-[10px] ${statusCfg.className}`}>{statusCfg.label}</Badge>
                              {task.runtime?.stage && (
                                <span className="text-xs text-muted-foreground">{task.runtime.stage}</span>
                              )}
                            </div>
                            <div className="truncate text-xs text-muted-foreground">
                              {task.account} · {task.id}
                            </div>
                            {isRunning && (
                              <div className="space-y-1">
                                <ProgressBar value={percent} />
                                <div className="flex justify-between text-[10px] text-muted-foreground">
                                  <span>{downloaded}/{total || "?"} 文件</span>
                                  <span>{percent}%</span>
                                </div>
                              </div>
                            )}
                          </div>
                        </Link>
                      );
                    })}
                  </div>
                )}
              </CardContent>
            </Card>

            <div className="space-y-4">
              <Card>
                <CardHeader className="pb-2">
                  <div className="flex items-center justify-between gap-2">
                    <CardTitle className="text-base">需要关注</CardTitle>
                    <Badge variant="outline" className="text-[10px]">
                      {alerts.length} 条
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="pt-0">
                  {alerts.length === 0 ? (
                    <div className="flex items-center gap-2 py-4 text-sm text-green-600 dark:text-green-400">
                      <CheckCircle2 className="h-4 w-4" />
                      目前没有需要处理的告警
                    </div>
                  ) : (
                    <div className="space-y-1">
                      {alerts.map((alert) => (
                        <Link
                          key={`${alert.title}-${alert.detail}`}
                          to={alert.to}
                          className="flex items-start gap-2 rounded-md p-2 hover:bg-muted/50 transition-colors"
                        >
                          <span
                            className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${
                              alert.tone === "danger"
                                ? "bg-red-500"
                                : alert.tone === "warning"
                                  ? "bg-amber-500"
                                  : "bg-blue-500"
                            }`}
                          />
                          <div className="min-w-0 flex-1">
                            <div className="text-xs font-medium">{alert.title}</div>
                            <div className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{alert.detail}</div>
                          </div>
                          <ArrowRight className="mt-0.5 h-3 w-3 shrink-0 text-muted-foreground" />
                        </Link>
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-base">服务</CardTitle>
                </CardHeader>
                <CardContent className="space-y-2.5 pt-0 text-xs">
                  <div className="flex items-center gap-2">
                    <Server className="h-3.5 w-3.5 text-muted-foreground" />
                    <span className="flex-1 text-muted-foreground">Emby</span>
                    <Badge variant={embyConfigured ? "default" : "outline"} className="text-[10px]">
                      {embyConfigured ? "已配置" : "未配置"}
                    </Badge>
                  </div>
                  <div className="flex items-center gap-2">
                    <BellRing className="h-3.5 w-3.5 text-muted-foreground" />
                    <span className="flex-1 text-muted-foreground">Telegram</span>
                    <Badge variant={telegramOnline ? "default" : "outline"} className="text-[10px]">
                      {polling?.polling ? "轮询中" : polling?.webhook?.url ? "Webhook" : "未配置"}
                    </Badge>
                  </div>
                  <div className="flex items-center gap-2">
                    <Activity className="h-3.5 w-3.5 text-muted-foreground" />
                    <span className="flex-1 text-muted-foreground">生活监控</span>
                    <Badge variant={monitorEnabled ? "default" : "outline"} className="text-[10px]">
                      {monitorEnabled ? `${monitorRunning}/${monitorTotal} 运行` : "未启用"}
                    </Badge>
                  </div>
                  <div className="flex items-center gap-2">
                    <HeartPulse className="h-3.5 w-3.5 text-muted-foreground" />
                    <span className="flex-1 text-muted-foreground">回收站</span>
                    <span className="text-muted-foreground">{trash.length} 项</span>
                  </div>
                </CardContent>
              </Card>
            </div>
          </div>

        </>
      )}
    </div>
  );
}
