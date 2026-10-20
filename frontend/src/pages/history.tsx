import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { PageTabs, type PageTabItem } from "@/components/page-tabs";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
  Calendar,
  User,
  Folder,
  FileText,
  Download,
  Trash,
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
  { id: "tasks", label: "任务执行", icon: FileText },
  { id: "strm", label: "STRM 记录", icon: Activity },
];

const STRM_KIND_LABELS: Record<string, string> = {
  full: "全量任务",
  increment: "增量任务",
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
const getDuration = (startTime: number, endTime?: number) => {
  const end = endTime || Date.now();
  const duration = end - startTime;
  const seconds = Math.floor(duration / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    return `${hours}小时${minutes % 60}分钟`;
  } else if (minutes > 0) {
    return `${minutes}分钟${seconds % 60}秒`;
  } else {
    return `${seconds}秒`;
  }
};

function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "-";
  return `${(ms / 1000).toFixed(1)}s`;
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
      setStrmError("获取 STRM 执行历史失败");
      toast.error("获取 STRM 执行历史失败");
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
          <h1 className="text-2xl font-semibold break-words">执行历史</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            查看任务级执行记录与 STRM 生成/删除明细
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
        ariaLabel="执行历史类型"
      />

      {activeTab === "tasks" ? (
        loading ? (
          <div className="flex items-center justify-center h-64">
            <div className="text-center">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-foreground mx-auto"></div>
              <p className="mt-2 text-muted-foreground">加载中...</p>
            </div>
          </div>
        ) : history.length === 0 ? (
          <Card>
            <CardContent className="flex items-center justify-center h-32">
              <div className="text-center">
                <FileText className="h-8 w-8 text-muted-foreground mx-auto mb-2" />
                <p className="text-muted-foreground">暂无任务执行历史</p>
              </div>
            </CardContent>
          </Card>
        ) : (
          <div className="grid gap-4">
            {history.map((execution) => {
              const statusConfig = getStatusConfig(execution.status);
              const StatusIcon = statusConfig.icon;

              return (
                <Card key={execution.id} className="hover:shadow-md transition-shadow">
                  <CardHeader className="pb-3">
                    <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
                      <div className="flex items-center space-x-3 min-w-0">
                        <StatusIcon className="h-5 w-5 shrink-0" />
                        <div className="min-w-0">
                          <CardTitle className="text-base font-medium break-words">
                            任务 {execution.taskId}
                          </CardTitle>
                          <CardDescription className="flex flex-col sm:flex-row sm:items-center gap-2 sm:space-x-4 mt-1">
                            <span className="flex items-center space-x-1">
                              <User className="h-4 w-4" />
                              <span>{execution.account || '-'}</span>
                            </span>
                            <span className="flex items-center space-x-1 min-w-0">
                              <Folder className="h-4 w-4 shrink-0" />
                              <span className="truncate">{execution.originPath || '-'}</span>
                            </span>
                          </CardDescription>
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-2 shrink-0">
                        <Badge className={statusConfig.color}>
                          {statusConfig.label}
                        </Badge>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => viewLogs(execution)}
                        >
                          <Eye className="h-4 w-4 mr-1" />
                          查看日志
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => deleteHistory(execution.id)}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    </div>
                  </CardHeader>

                  <CardContent className="pt-0">
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                      <div className="space-y-2">
                        <div className="flex items-center space-x-2 text-sm">
                          <Calendar className="h-4 w-4 text-muted-foreground" />
                          <span className="font-medium">开始时间:</span>
                          <span>{formatTime(execution.startedAt)}</span>
                        </div>
                        {execution.endedAt && (
                          <div className="flex items-center space-x-2 text-sm">
                            <Clock className="h-4 w-4 text-muted-foreground" />
                            <span className="font-medium">执行时长:</span>
                            <span>{getDuration(execution.startedAt, execution.endedAt)}</span>
                          </div>
                        )}
                      </div>

                      <div className="space-y-2">
                        <div className="flex items-center space-x-2 text-sm">
                          <Download className="h-4 w-4 text-muted-foreground" />
                          <span className="font-medium">下载文件:</span>
                          <span>{execution.summary.downloadedFiles ?? 0}/{execution.summary.totalFiles ?? 0}</span>
                        </div>
                        {(execution.summary.deletedFiles ?? 0) > 0 && (
                          <div className="flex items-center space-x-2 text-sm">
                            <Trash className="h-4 w-4 text-muted-foreground" />
                            <span className="font-medium">删除文件:</span>
                            <span>{execution.summary.deletedFiles}</span>
                          </div>
                        )}
                      </div>

                      <div className="space-y-2">
                        <div className="text-sm">
                          <span className="font-medium">目标路径:</span>
                          <span className="ml-2 text-muted-foreground">{execution.targetPath || '-'}</span>
                        </div>
                        {execution.error && (
                          <div className="text-sm text-red-500 break-words">
                            <span className="font-medium">错误信息:</span>
                            <span className="ml-2">{execution.error}</span>
                          </div>
                        )}
                      </div>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        )
      ) : strmLoading ? (
        <div className="flex items-center justify-center h-64">
          <p className="text-muted-foreground">加载 STRM 执行历史...</p>
        </div>
      ) : strmError ? (
        <Card>
          <CardContent className="p-4 text-sm text-destructive">{strmError}</CardContent>
        </Card>
      ) : (
        <>
          {strmStats && (
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <Card>
                <CardContent className="p-3">
                  <p className="text-xs text-muted-foreground">总记录</p>
                  <p className="text-xl font-semibold">{strmStats.totalCount}</p>
                </CardContent>
              </Card>
              <Card>
                <CardContent className="p-3">
                  <p className="text-xs text-muted-foreground">成功 / 失败</p>
                  <p className="text-xl font-semibold">
                    <span className="text-green-600">{strmStats.successCount}</span>
                    <span className="text-muted-foreground"> / </span>
                    <span className="text-red-600">{strmStats.failedCount}</span>
                  </p>
                </CardContent>
              </Card>
              <Card>
                <CardContent className="p-3">
                  <p className="text-xs text-muted-foreground">文件成功 / 总数</p>
                  <p className="text-xl font-semibold">{strmStats.successFiles} / {strmStats.totalFiles}</p>
                </CardContent>
              </Card>
              <Card>
                <CardContent className="p-3">
                  <p className="text-xs text-muted-foreground">API 请求</p>
                  <p className="text-xl font-semibold">{strmStats.totalApiRequests}</p>
                </CardContent>
              </Card>
            </div>
          )}

          {strmHistory.length === 0 ? (
            <Card>
              <CardContent className="flex items-center justify-center h-32">
                <p className="text-muted-foreground">暂无 STRM 执行记录</p>
              </CardContent>
            </Card>
          ) : (
            <div className="rounded-md border overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="whitespace-nowrap">时间</TableHead>
                    <TableHead className="whitespace-nowrap">类型</TableHead>
                    <TableHead className="whitespace-nowrap">任务</TableHead>
                    <TableHead className="whitespace-nowrap">账号</TableHead>
                    <TableHead className="whitespace-nowrap">结果</TableHead>
                    <TableHead className="whitespace-nowrap">文件</TableHead>
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
                              ? "bg-green-500/20 text-green-700 dark:text-green-400"
                              : "bg-red-500/20 text-red-700 dark:text-red-400"
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
