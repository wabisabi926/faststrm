import { useEffect, useState, useCallback } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { CheckCircle2, Inbox, Loader2, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import axiosInstance from "@/lib/axios";
import { useTaskEventStream } from "@/lib/sse";

interface Progress {
  filePath?: string;
  percent?: number;
  overallPercent?: string;
  done?: boolean;
  error?: string;
  strm?: boolean;
  cancelled?: boolean;
  message?: string;
}

// 任务状态统一映射：只保留 开始 / 已完成 / 失败 / 已取消 四种可读状态。
// 后端原始值 pending/running 均归为「开始」，避免不准确的中间态误导。
const STATUS_LABEL: Record<string, string> = {
  pending: "开始",
  running: "开始",
  completed: "已完成",
  failed: "失败",
  cancelled: "已取消",
};

const STATUS_BADGE_CLASS: Record<string, string> = {
  "开始": "bg-blue-500/15 text-blue-600 hover:bg-blue-500/20 dark:text-blue-400",
  "已完成": "bg-green-500/15 text-green-600 hover:bg-green-500/20 dark:text-green-400",
  "失败": "bg-red-500/15 text-red-600 hover:bg-red-500/20 dark:text-red-400",
  "已取消": "bg-muted text-muted-foreground",
  "不存在": "bg-muted text-muted-foreground",
};

export default function DownloadProgressPage() {
  const { taskId } = useParams<{ taskId: string }>();
  const [searchParams] = useSearchParams();
  const executionId = searchParams.get("executionId") || undefined;
  const [logs, setLogs] = useState<Progress[]>([]);
  const [rawLines, setRawLines] = useState<string[]>([]);
  const [connectionStatus, setConnectionStatus] = useState<string>("连接中...");
  const [isCancelling, setIsCancelling] = useState<boolean>(false);
  const [taskStatus, setTaskStatus] = useState<string>("开始");

  const loadHistoryLogs = useCallback(async () => {
    try {
      if (!executionId) {
        setConnectionStatus("历史记录不存在");
        setTaskStatus("不存在");
        return;
      }

      // 1) 先查询执行摘要（状态等）
      const historyResponse = await axiosInstance.get("/api/taskHistory");
      const allHistoryRaw = historyResponse.data;
      const allHistory = Array.isArray(allHistoryRaw) ? allHistoryRaw : (allHistoryRaw?.items || []);
      // 后端 id 为 int64，URL 参数为字符串，统一成字符串比较
      const execution = allHistory.find((h: { id: number | string }) => String(h.id) === String(executionId));

      if (execution) {
        setConnectionStatus("历史记录");
        setTaskStatus(STATUS_LABEL[execution.status] ?? "开始");
      } else {
        setConnectionStatus("历史记录不存在");
        setTaskStatus("不存在");
      }

      // 2) 查询该 execution 的日志（后端单独接口，避免 TaskExecution 未携带 logs 字段）
      const logsResponse = await axiosInstance.get(`/api/taskHistory/${executionId}/logs`);
      const rawLogs = Array.isArray(logsResponse.data) ? logsResponse.data : (logsResponse.data?.logs || []);

      const parsedLogs: Progress[] = [];
      rawLogs.forEach((logLine: string) => {
        try {
          const logData = JSON.parse(logLine);
          parsedLogs.push(logData);
        } catch {
          console.error("Failed to parse log line:", logLine);
        }
      });

      setLogs(parsedLogs);
    } catch (error) {
      console.error("Failed to load history logs:", error);
      setConnectionStatus("加载历史记录失败");
    }
  }, [executionId]);

  useEffect(() => {
    // 历史 execution 模式：走 history 接口（SSE 缓冲只覆盖运行中任务的最近 500 帧）
    if (executionId) {
      void loadHistoryLogs();
      return;
    }
    setConnectionStatus("连接中...");
    // 实时模式走 SSE（useTaskEventStream 见下方 effect）；
    // 若 4 秒内未建立连接（SSE 不可用），降级为一次性 REST 拉取
    const fallbackTimer = setTimeout(() => {
      setConnectionStatus((prev) => {
        if (prev === "连接中...") {
          void loadLiveLogsREST();
          return "已加载（非实时）";
        }
        return prev;
      });
    }, 4000);
    return () => clearTimeout(fallbackTimer);
    // 依赖为 setState + axios 单例，引用稳定
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId, executionId]);

  // 实时模式：SSE 订阅该任务，服务端连接时重放最近 500 帧，之后增量推送
  const { connected: sseConnected } = useTaskEventStream(
    executionId ? undefined : (taskId ?? null),
    {
      onProgress: (e) => {
        setConnectionStatus("实时同步中");
        if (!e.filePath) return;
        setLogs((prev) => {
          const idx = prev.findIndex((l) => l.filePath === e.filePath);
          const item: Progress = {
            filePath: e.filePath,
            percent: e.percent,
            overallPercent: e.overallPercent,
            done: e.done,
            error: e.error || undefined,
          };
          if (idx !== -1) {
            const updated = [...prev];
            updated[idx] = { ...updated[idx], ...item };
            return updated;
          }
          return [...prev, item];
        });
      },
      onLog: (e) => {
        setConnectionStatus("实时同步中");
        setRawLines((prev) => {
          if (prev.includes(e.message)) return prev; // 重放帧与增量可能重叠，简单去重
          return [...prev, e.message];
        });
      },
      onComplete: (e) => {
        setTaskStatus(STATUS_LABEL[e.status] ?? "开始");
        setConnectionStatus("任务已结束");
      },
      onError: () => {
        setTaskStatus("失败");
        setConnectionStatus("任务已结束");
      },
      onCancel: () => {
        setTaskStatus("已取消");
        setConnectionStatus("任务已结束");
      },
    }
  );

  // SSE 连接状态同步到展示文案
  useEffect(() => {
    if (executionId) return;
    setConnectionStatus((prev) => {
      if (sseConnected && (prev === "连接中..." || prev === "已加载（非实时）")) {
        return "实时同步中";
      }
      return prev;
    });
  }, [sseConnected, executionId]);

  // SSE 不可用时的兜底：一次性拉取纯文本日志
  const loadLiveLogsREST = useCallback(async () => {
    try {
      const response = await axiosInstance.get(
        `/api/taskLog/${encodeURIComponent(taskId ?? "")}`
      );
      const text: string = typeof response.data === "string" ? response.data : "";
      const lines = text.split("\n").filter((l) => l.trim() !== "");

      const parsed: Progress[] = [];
      const raw: string[] = [];
      for (const line of lines) {
        try {
          const obj = JSON.parse(line) as Record<string, unknown>;
          if (obj && typeof obj === "object") {
            if ("filePath" in obj || "error" in obj) {
              parsed.push(obj as Progress);
              continue;
            }
            if (obj.event === "log" && typeof obj.message === "string") {
              raw.push(obj.message);
              continue;
            }
          }
        } catch {
          // 非 JSON 行
        }
        raw.push(line);
      }

      setLogs((prev) => (prev.length >= parsed.length ? prev : parsed));
      setRawLines((prev) => (prev.length >= raw.length ? prev : raw));
      if (lines.length === 0) {
        setTaskStatus("不存在");
      }
    } catch (error) {
      console.error("加载任务日志失败:", error);
      setConnectionStatus("加载日志失败");
    }
  }, [taskId]);

  const cancelTask = async () => {
    if (isCancelling) return;

    setIsCancelling(true);
    try {
      const response = await axiosInstance.post('/api/cancelTask', { taskId });

      if (response.data.message) {
        setTaskStatus("已取消");
      }
    } catch (error: unknown) {
      console.error('Failed to cancel task:', error);
      const errorMessage = error instanceof Error && 'response' in error
        ? (error as { response?: { data?: { error?: string } } }).response?.data?.error || '取消任务失败，请重试'
        : '取消任务失败，请重试';
      alert(errorMessage);
    } finally {
      setIsCancelling(false);
    }
  };

  const connectionTone = /错误|失败/.test(connectionStatus)
    ? "text-destructive"
    : connectionStatus === "实时同步中"
      ? "text-green-600 dark:text-green-400"
      : "text-muted-foreground";
  const badgeClass = STATUS_BADGE_CLASS[taskStatus] ?? "bg-muted text-muted-foreground";

  return (
    <div className="mx-auto max-w-5xl space-y-4">
      <div className="px-1">
        <h1 className="text-xl sm:text-2xl font-semibold break-words">任务日志</h1>
        <p className="text-sm text-muted-foreground mt-1 break-words">实时进度与历史执行日志</p>
      </div>

      <div className="rounded-xl border bg-card p-4 shadow-sm">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <div className="space-y-1 min-w-0">
            <p className="text-xs text-muted-foreground">任务 ID</p>
            <p className="break-all font-mono text-sm font-medium">{taskId}</p>
          </div>
          <div className="space-y-1 min-w-0">
            <p className="text-xs text-muted-foreground">连接状态</p>
            <p className={`text-sm font-medium ${connectionTone}`}>{connectionStatus}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">文件数量</p>
            <p className="text-sm font-medium">{logs.length}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">任务状态</p>
            <Badge variant="secondary" className={badgeClass}>{taskStatus}</Badge>
          </div>
        </div>

        {taskStatus === "开始" && (
          <div className="mt-4 flex justify-end">
            <Button variant="destructive" size="sm" onClick={cancelTask} disabled={isCancelling}>
              {isCancelling ? (
                <>
                  <Loader2 className="animate-spin" />
                  取消中...
                </>
              ) : (
                "取消任务"
              )}
            </Button>
          </div>
        )}
      </div>

      {logs.length === 0 && rawLines.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border bg-card py-16 text-center">
          <Inbox className="h-10 w-10 text-muted-foreground" />
          <p className="mt-3 text-sm text-muted-foreground">暂无任务日志</p>
        </div>
      ) : (
        <>
          <div className="rounded-xl border bg-card p-4 shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 className="text-base font-medium">任务进度</h2>
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="secondary" className={badgeClass}>{taskStatus}</Badge>
                <span className="text-sm text-muted-foreground">
                  已完成 {logs.filter((l) => l.done).length} / {logs.length} 个文件
                </span>
              </div>
            </div>
          </div>

          <div className="overflow-hidden rounded-xl border bg-card shadow-sm">
            <div className="border-b bg-muted/30 px-4 py-3">
              <h2 className="text-base font-medium">下载文件列表</h2>
            </div>

            <div className="max-h-[600px] overflow-y-auto">
              {logs.slice().reverse().map((log, index) => (
                <div key={index} className="flex items-center gap-3 border-b px-4 py-3 last:border-b-0">
                  <div
                    className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${
                      log.done
                        ? "bg-green-500/15 text-green-600 dark:text-green-400"
                        : log.error
                          ? "bg-destructive/10 text-destructive"
                          : "bg-blue-500/15 text-blue-600 dark:text-blue-400"
                    }`}
                  >
                    {log.done ? (
                      <CheckCircle2 className="h-4 w-4" />
                    ) : log.error ? (
                      <XCircle className="h-4 w-4" />
                    ) : (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    )}
                  </div>

                  <div className="min-w-0 flex-1">
                    <div className="break-all text-xs font-medium">{log.filePath}</div>
                    <div className="mt-1 flex flex-wrap items-center gap-2">
                      <span className="rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
                        {parseFloat(log.percent?.toString() ?? log.overallPercent?.toString() ?? "0").toFixed(2)}%
                      </span>
                      {log.strm && (
                        <span className="rounded bg-green-500/15 px-2 py-0.5 text-xs font-medium text-green-600 dark:text-green-400">
                          STRM
                        </span>
                      )}
                      {log.error && (
                        <span className="break-all rounded bg-destructive/10 px-2 py-0.5 text-xs text-destructive">
                          {log.error}
                        </span>
                      )}
                    </div>
                  </div>

                  {!log.done && !log.error && (
                    <div className="h-1.5 w-16 shrink-0 overflow-hidden rounded-full bg-muted sm:w-24">
                      <div
                        className="h-full rounded-full bg-primary transition-[width] duration-300"
                        style={{ width: `${log.percent ?? 0}%` }}
                      />
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </>
      )}

      {rawLines.length > 0 && (
        <div className="overflow-hidden rounded-xl border bg-card shadow-sm">
          <div className="border-b bg-muted/30 px-4 py-3">
            <h2 className="text-base font-medium">日志输出</h2>
          </div>
          <pre className="max-h-[480px] overflow-auto whitespace-pre-wrap break-all p-4 font-mono text-xs leading-relaxed text-foreground">
            {rawLines.join("\n")}
          </pre>
        </div>
      )}
    </div>
  );
}
