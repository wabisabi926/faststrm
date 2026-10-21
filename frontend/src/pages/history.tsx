import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { PageTabs, type PageTabItem } from "@/components/page-tabs";
import { Card, CardContent } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";
import {
  Clock,
  CheckCircle,
  XCircle,
  AlertCircle,
  Trash2,
  Eye,
  FileText,
  Activity,
} from "lucide-react";
import { logger } from "@/lib/logger";

interface TaskExecutionHistory {
  id: number;
  taskId: string;
  account?: string;
  originPath?: string;
  targetPath?: string;
  status: "running" | "completed" | "failed" | "cancelled";
  startedAt: number;
  endedAt?: number;
  durationMs?: number;
  summary: {
    totalFiles?: number;
    downloadedFiles?: number;
    deletedFiles?: number;
  };
  error?: string;
  logs?: string[];
}

interface StrmHistoryEntry {
  id: number;
  taskId: string;
  kind: string;
  account: string;
  success: boolean;
  totalFiles: number;
  successFiles: number;
  failedFiles: number;
  elapsedMs: number;
  apiRequests: number;
  errorMsg?: string;
  createdAt: number;
}

interface StrmHistoryStats {
  totalCount: number;
  successCount: number;
  failedCount: number;
  totalFiles: number;
  successFiles: number;
  failedFiles: number;
  totalApiRequests: number;
  totalElapsedMs: number;
}

type HistoryTab = "tasks" | "strm";

const HISTORY_TABS: readonly PageTabItem<HistoryTab>[] = [
  { id: "tasks", label: "任务运行", icon: FileText },
  { id: "strm", label: "事件处理", icon: Activity },
];

// 任务运行 = 一次全量/定时任务运行；事件处理 = 生活事件逐条处理结果。
// 「增量任务」标签已随增量同步下线（v1.4.3），这里不再保留。
const STRM_KIND_LABELS: Record<string, string> = {
  full: "全量任务（旧记录）",
  monitor: "生活事件",
  rename: "重命名",
  move: "移动",
  delete: "删除",
};

// 状态图标和颜色映射
const getStatusConfig = (status: TaskExecutionHistory["status"]) => {
  const configs = {
    running: { icon: Clock, color: "bg-blue-500/20 text-blue-700 dark:text-blue-400", label: "运行中" },
    completed: { icon: CheckCircle, color: "bg-green-500/20 text-green-700 dark:text-green-400", label: "已完成" },
    failed: { icon: XCircle, color: "bg-red-500/20 text-red-700 dark:text-red-400", label: "失败" },
    cancelled: { icon: AlertCircle, color: "bg-yellow-500/20 text-yellow-700 dark:text-yellow-400", label: "已取消" },
  };
  return configs[status];
};

// 格式化时间
const formatTime = (timestamp: number) => {
  return new Date(timestamp).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
};

// 计算执行时长
function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "-";
  return `${(ms / 1000).toFixed(1)}s`;
}

function HistoryStatCard({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Card>
      <CardContent className="p-3">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="text-2xl font-semibold">{value}</p>
      </CardContent>
    </Card>
  );
}

export default function TaskHistoryPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab: HistoryTab = searchParams.get("tab") === "strm" ? "strm" : "tasks";
  const setActiveTab = (tab: HistoryTab) => {
    const next = new URLSearchParams(searchParams);
    if (tab === "tasks") {
      next.delete("tab");
    } else {
      next.set("tab", tab);
    }
    setSearchParams(next, { replace: true });
  };
  const [history, setHistory] = useState<TaskExecutionHistory[]>([]);
  const [loading, setLoading] = useState(true);
  const [strmHistory, setStrmHistory] = useState<StrmHistoryEntry[]>([]);
  const [strmStats, setStrmStats] = useState<StrmHistoryStats | null>(null);
  const [strmLoading, setStrmLoading] = useState(false);
  const [strmError, setStrmError] = useState<string | null>(null);

  const taskStats = useMemo(() => {
    let success = 0;
    let failed = 0;
    let filesOk = 0;
    let filesTotal = 0;
    let totalMs = 0;
    let durationCount = 0;

    for (const item of history) {
      if (item.status === "completed") success++;
      if (item.status === "failed") failed++;
      filesOk += item.summary?.downloadedFiles ?? 0;
      filesTotal += item.summary?.totalFiles ?? 0;
      const ms = item.durationMs ?? (item.endedAt ? item.endedAt - item.startedAt : 0);
      if (ms > 0) {
        totalMs += ms;
        durationCount++;
      }
    }

    return {
      total: history.length,
      success,
      failed,
      filesOk,
      filesTotal,
      avgDurationMs: durationCount > 0 ? totalMs / durationCount : 0,
    };
  }, [history]);

  useEffect(() => {
    void fetchHistory();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (activeTab === "strm") {
      void fetchStrmHistory();
    }
    // 切到 STRM 标签时刷新一次，避免看到过期数据
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab]);

  const fetchHistory = async () => {
    try {
      setLoading(true);
      const response = await axiosInstance.get("/api/taskHistory");
      const data = response.data;
      setHistory(Array.isArray(data) ? data : (data?.items || []));
    } catch (error) {
      logger.error("Failed to fetch task history:", error);
      toast.error("获取任务历史失败");
    } finally {
      setLoading(false);
    }
  };

  const fetchStrmHistory = async () => {
    try {
      setStrmLoading(true);
      setStrmError(null);
      const [listResp, statsResp] = await Promise.all([
        axiosInstance.get("/api/history/strm?limit=200"),
        axiosInstance.get("/api/history/strm/stats"),
      ]);
      const items = Array.isArray(listResp.data?.items) ? listResp.data.items : [];
      setStrmHistory(items as StrmHistoryEntry[]);
      setStrmStats((statsResp.data || null) as StrmHistoryStats | null);
    } catch (error) {
      logger.error("Failed to fetch STRM history:", error);
      setStrmError("获取事件处理记录失败");
      toast.error("获取事件处理记录失败");
    } finally {
      setStrmLoading(false);
    }
  };

  const deleteHistory = async (executionId: number) => {
    try {
      await axiosInstance.delete(`/api/taskHistory?executionId=${executionId}`);
      setHistory(history.filter(h => h.id !== executionId));
      toast.success("删除成功");
    } catch (error) {
      logger.error("Failed to delete history:", error);
      toast.error("删除失败");
    }
  };

  const deleteAllHistory = async () => {
    try {
      await axiosInstance.delete("/api/taskHistory?action=cleanup");
      toast.success("所有历史记录已删除");
      void fetchHistory();
    } catch (error) {
      logger.error("Failed to delete history:", error);
      toast.error("删除失败");
    }
  };

  const viewLogs = (execution: TaskExecutionHistory) => {
    window.open(`/log/${execution.taskId}?executionId=${String(execution.id)}`, "_blank");
  };

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 min-w-0">
        <div className="min-w-0 break-words">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">运行记录</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            任务运行看每次全量/定时任务的结果；事件处理看网盘改名/移动/删除的逐条结果
          </p>
        </div>
        <div className="flex flex-wrap gap-2 shrink-0">
          {activeTab === "tasks" ? (
            <>
              <Button onClick={fetchHistory} variant="outline">
                刷新
              </Button>
              <Button onClick={deleteAllHistory} variant="outline">
                删除所有历史
              </Button>
            </>
          ) : (
            <Button onClick={fetchStrmHistory} variant="outline">
              刷新
            </Button>
          )}
        </div>
      </div>

      <PageTabs
        tabs={HISTORY_TABS}
        value={activeTab}
        onChange={setActiveTab}
        ariaLabel="运行记录类型"
      />

      {activeTab === "tasks" ? (
        loading ? (
          <div className="flex items-center justify-center h-64">
            <p className="text-sm text-muted-foreground">加载中...</p>
          </div>
        ) : history.length === 0 ? (
          <Card>
            <CardContent className="flex items-center justify-center h-32">
              <div className="text-center">
                <FileText className="h-8 w-8 text-muted-foreground mx-auto mb-2" />
                <p className="text-sm text-muted-foreground">暂无任务运行记录</p>
              </div>
            </CardContent>
          </Card>
        ) : (
          <>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <HistoryStatCard label="总记录" value={taskStats.total} />
              <HistoryStatCard
                label="成功 / 失败"
                value={
                  <>
                    <span className="text-green-600 dark:text-green-400">{taskStats.success}</span>
                    <span className="text-muted-foreground"> / </span>
                    <span className="text-red-600 dark:text-red-400">{taskStats.failed}</span>
                  </>
                }
              />
              <HistoryStatCard label="条目成功 / 总数" value={`${taskStats.filesOk} / ${taskStats.filesTotal}`} />
              <HistoryStatCard label="平均耗时" value={formatElapsed(taskStats.avgDurationMs)} />
            </div>

            <div className="rounded-md border overflow-x-auto [&_table]:text-xs">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="whitespace-nowrap">时间</TableHead>
                    <TableHead>任务 / 路径</TableHead>
                    <TableHead className="whitespace-nowrap">账号</TableHead>
                    <TableHead className="whitespace-nowrap">状态</TableHead>
                    <TableHead className="whitespace-nowrap">条目</TableHead>
                    <TableHead className="whitespace-nowrap">耗时</TableHead>
                    <TableHead className="whitespace-nowrap">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.map((execution) => {
                    const statusConfig = getStatusConfig(execution.status);
                    const StatusIcon = statusConfig.icon;
                    const elapsedMs =
                      execution.durationMs ??
                      (execution.endedAt ? execution.endedAt - execution.startedAt : 0);

                    return (
                      <TableRow key={execution.id}>
                        <TableCell className="whitespace-nowrap text-xs">
                          {formatTime(execution.startedAt)}
                        </TableCell>
                        <TableCell className="min-w-[220px] max-w-[360px]">
                          <div className="truncate font-mono text-xs" title={execution.taskId}>
                            {execution.taskId}
                          </div>
                          <div
                            className="truncate text-xs text-muted-foreground"
                            title={`${execution.originPath || "-"} → ${execution.targetPath || "-"}`}
                          >
                            {execution.originPath || "-"} → {execution.targetPath || "-"}
                          </div>
                          {execution.error && (
                            <div className="truncate text-xs text-destructive" title={execution.error}>
                              {execution.error}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs">
                          {execution.account || "-"}
                        </TableCell>
                        <TableCell>
                          <Badge className={`text-[10px] ${statusConfig.color}`}>
                            <StatusIcon className="mr-1 h-3 w-3" />
                            {statusConfig.label}
                          </Badge>
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs">
                          {execution.summary?.downloadedFiles ?? 0} / {execution.summary?.totalFiles ?? 0}
                          {(execution.summary?.deletedFiles ?? 0) > 0 && (
                            <span className="ml-1 text-muted-foreground">
                              （删 {execution.summary.deletedFiles}）
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs">
                          {formatElapsed(elapsedMs)}
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center gap-1">
                            <Button
                              size="sm"
                              variant="outline"
                              className="h-7 text-xs"
                              onClick={() => viewLogs(execution)}
                            >
                              <Eye className="h-3.5 w-3.5" />
                              日志
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                              onClick={() => deleteHistory(execution.id)}
                              aria-label="删除记录"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          </>
        )
      ) : strmLoading ? (
        <div className="flex items-center justify-center h-64">
          <p className="text-muted-foreground">加载事件处理记录...</p>
        </div>
      ) : strmError ? (
        <Card>
          <CardContent className="p-4 text-sm text-destructive">{strmError}</CardContent>
        </Card>
      ) : (
        <>
          {strmStats && (
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <HistoryStatCard label="总记录" value={strmStats.totalCount} />
              <HistoryStatCard
                label="成功 / 失败"
                value={
                  <>
                    <span className="text-green-600 dark:text-green-400">{strmStats.successCount}</span>
                    <span className="text-muted-foreground"> / </span>
                    <span className="text-red-600 dark:text-red-400">{strmStats.failedCount}</span>
                  </>
                }
              />
              <HistoryStatCard
                label="条目成功 / 总数"
                value={`${strmStats.successFiles} / ${strmStats.totalFiles}`}
              />
              <HistoryStatCard
                label="平均耗时"
                value={formatElapsed(
                  strmStats.totalCount > 0 ? strmStats.totalElapsedMs / strmStats.totalCount : 0
                )}
              />
            </div>
          )}

          {strmHistory.length === 0 ? (
            <Card>
              <CardContent className="flex items-center justify-center h-32">
                <p className="text-muted-foreground">暂无 STRM 执行记录</p>
              </CardContent>
            </Card>
          ) : (
            <div className="rounded-md border overflow-x-auto [&_table]:text-xs">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="whitespace-nowrap">时间</TableHead>
                    <TableHead className="whitespace-nowrap">类型</TableHead>
                    <TableHead className="whitespace-nowrap">任务</TableHead>
                    <TableHead className="whitespace-nowrap">账号</TableHead>
                    <TableHead className="whitespace-nowrap">结果</TableHead>
                    <TableHead className="whitespace-nowrap">条目</TableHead>
                    <TableHead className="whitespace-nowrap">耗时</TableHead>
                    <TableHead className="whitespace-nowrap">API</TableHead>
                    <TableHead className="whitespace-nowrap">错误</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {strmHistory.map((entry) => (
                    <TableRow key={entry.id}>
                      <TableCell className="whitespace-nowrap text-xs">{formatTime(entry.createdAt)}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs">
                        {STRM_KIND_LABELS[entry.kind] || entry.kind || "-"}
                      </TableCell>
                      <TableCell className="whitespace-nowrap font-mono text-xs">{entry.taskId || "-"}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs">{entry.account || "-"}</TableCell>
                      <TableCell>
                        <Badge
                          className={
                            entry.success
                              ? "text-[10px] bg-green-500/20 text-green-700 dark:text-green-400"
                              : "text-[10px] bg-red-500/20 text-red-700 dark:text-red-400"
                          }
                        >
                          {entry.success ? "成功" : "失败"}
                        </Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs">
                        {entry.successFiles} / {entry.totalFiles}
                        {entry.failedFiles > 0 && <span className="text-red-500">（失败 {entry.failedFiles}）</span>}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs">{formatElapsed(entry.elapsedMs)}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs">{entry.apiRequests}</TableCell>
                      <TableCell className="max-w-[240px] truncate text-xs text-red-500" title={entry.errorMsg || ""}>
                        {entry.errorMsg || "-"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </>
      )}
    </div>
  );
}
