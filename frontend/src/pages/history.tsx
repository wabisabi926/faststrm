import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Navigate, useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
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
  /** 记录创建时间（分页游标用） */
  createdAt: number;
}

/** 服务端返回的全量统计（不受分页影响） */
interface TaskHistoryStats {
  total: number;
  completed: number;
  failed: number;
  cancelled: number;
  running: number;
  filesDownloaded: number;
  filesTotal: number;
  avgDurationMs: number;
}

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
  const [searchParams] = useSearchParams();
  const [history, setHistory] = useState<TaskExecutionHistory[]>([]);
  const [stats, setStats] = useState<TaskHistoryStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);

  // 兜底：服务端统计不可用时，退回「已加载记录」的口径，并在卡片上标注
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

  // 卡片数据：优先服务端全量统计
  const cards = stats
    ? {
        total: stats.total,
        success: stats.completed,
        failed: stats.failed,
        filesOk: stats.filesDownloaded,
        filesTotal: stats.filesTotal,
        avgDurationMs: stats.avgDurationMs,
        scope: "全部记录",
      }
    : { ...taskStats, scope: "已加载记录" };

  useEffect(() => {
    void fetchHistory();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const PAGE_SIZE = 100;

  const fetchStats = async () => {
    try {
      const response = await axiosInstance.get("/api/taskHistory/stats");
      const data = response.data;
      if (data && typeof data.total === "number") {
        setStats(data as TaskHistoryStats);
      } else {
        setStats(null);
      }
    } catch (error) {
      // 统计失败不影响列表展示，退回本地口径
      logger.error("Failed to fetch task history stats:", error);
      setStats(null);
    }
  };

  const fetchHistory = async () => {
    try {
      setLoading(true);
      const [response] = await Promise.all([
        axiosInstance.get(`/api/taskHistory?limit=${PAGE_SIZE}`),
        fetchStats(),
      ]);
      const data = response.data;
      const items = Array.isArray(data) ? data : (data?.items || []);
      setHistory(items);
      setHasMore(items.length >= PAGE_SIZE);
    } catch (error) {
      logger.error("Failed to fetch task history:", error);
      toast.error("获取任务历史失败");
    } finally {
      setLoading(false);
    }
  };

  const loadMoreHistory = async () => {
    const last = history[history.length - 1];
    if (!last || loadingMore) return;
    setLoadingMore(true);
    try {
      // 后端按 created_at 倒序分页，游标必须用 createdAt（startedAt 可能为 0 或晚于创建时间）
      const cursor = last.createdAt > 0 ? `&before=${last.createdAt}` : "";
      const response = await axiosInstance.get(
        `/api/taskHistory?limit=${PAGE_SIZE}${cursor}`
      );
      const data = response.data;
      const items: TaskExecutionHistory[] = Array.isArray(data) ? data : (data?.items || []);
      setHistory(prev => [...prev, ...items]);
      setHasMore(items.length >= PAGE_SIZE);
    } catch (error) {
      logger.error("Failed to load more task history:", error);
      toast.error("加载更多历史失败");
    } finally {
      setLoadingMore(false);
    }
  };

  const deleteHistory = async (executionId: number) => {
    try {
      await axiosInstance.delete(`/api/taskHistory?executionId=${executionId}`);
      setHistory(history.filter(h => h.id !== executionId));
      toast.success("删除成功");
      void fetchStats();
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

  // 兼容旧链接：STRM 操作页签已移除，明细统一看生活事件日志
  if (searchParams.get("tab") === "strm") {
    return <Navigate to="/life-events" replace />;
  }

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 min-w-0">
        <div className="min-w-0 break-words">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">任务记录</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            每次全量/定时任务的运行结果、耗时与历史日志。
          </p>
        </div>
        <div className="flex flex-wrap gap-2 shrink-0">
          <Button onClick={fetchHistory} variant="outline">
            刷新
          </Button>
          <Button onClick={deleteAllHistory} variant="outline">
            删除所有历史
          </Button>
        </div>
      </div>

      {loading ? (
          <div className="flex items-center justify-center h-64">
            <p className="text-sm text-muted-foreground">加载中...</p>
          </div>
        ) : history.length === 0 ? (
          <Card>
            <CardContent className="flex items-center justify-center h-32">
              <div className="text-center">
                <FileText className="h-8 w-8 text-muted-foreground mx-auto mb-2" />
                <p className="text-sm text-muted-foreground">暂无任务记录</p>
              </div>
            </CardContent>
          </Card>
        ) : (
          <>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <HistoryStatCard label="记录总数" value={cards.total} />
              <HistoryStatCard
                label="成功 / 失败"
                value={
                  <>
                    <span className="text-green-600 dark:text-green-400">{cards.success}</span>
                    <span className="text-muted-foreground"> / </span>
                    <span className="text-red-600 dark:text-red-400">{cards.failed}</span>
                  </>
                }
              />
              <HistoryStatCard label="条目成功 / 总数" value={`${cards.filesOk} / ${cards.filesTotal}`} />
              <HistoryStatCard label="平均耗时" value={formatElapsed(cards.avgDurationMs)} />
            </div>
            <p className="text-xs text-muted-foreground">
              统计口径：{cards.scope}
              {hasMore && `，当前列表已加载 ${history.length} 条`}
            </p>

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
            {hasMore && (
              <div className="flex justify-center">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void loadMoreHistory()}
                  disabled={loadingMore}
                >
                  {loadingMore ? "加载中..." : "加载更多"}
                </Button>
              </div>
            )}
          </>
      )}
    </div>
  );
}
