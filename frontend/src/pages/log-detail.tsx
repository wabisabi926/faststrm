import { useEffect, useState, useCallback } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import axiosInstance from "@/lib/axios";

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

const STATUS_COLOR: Record<string, string> = {
  "开始": "#3b82f6",
  "已完成": "#059669",
  "失败": "#dc2626",
  "已取消": "#6b7280",
  "不存在": "#6b7280",
};

export default function DownloadProgressPage() {
  const { taskId } = useParams<{ taskId: string }>();
  const [searchParams] = useSearchParams();
  const executionId = searchParams.get("executionId") || undefined;
  const [logs, setLogs] = useState<Progress[]>([]);
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
    let abortController: AbortController | null = null;

    const startSSE = async () => {
      abortController = new AbortController();
      setConnectionStatus("连接中...");

      try {
        if (executionId) {
          await loadHistoryLogs();
          return;
        }

        const response = await fetch(`/api/taskLog/${taskId}`, {
          method: 'GET',
          headers: {
            'Accept': 'text/event-stream',
            'Authorization': `Bearer ${localStorage.getItem('auth-token')}`,
          },
          signal: abortController.signal,
        });

        if (!response.ok) {
          throw new Error(`HTTP error! status: ${response.status}`);
        }

        setConnectionStatus("已连接");

        const reader = response.body?.getReader();
        if (!reader) {
          throw new Error('No reader available');
        }

        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) {
            setConnectionStatus("连接已断开");
            break;
          }

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() || '';

          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const dataStr = line.slice(6);
              if (dataStr.trim()) {
                try {
                  const data: Progress = JSON.parse(dataStr);

                  if (data.error) {
                    setLogs([]);
                    setConnectionStatus("任务不存在");
                    setTaskStatus("不存在");
                    return;
                  }

                  if (data.cancelled) {
                    setTaskStatus("已取消");
                  } else if (data.done) {
                    setTaskStatus("已完成");
                  } else {
                    setTaskStatus("开始");
                  }

                  setLogs((prev) => {
                    const idx = prev.findIndex((log) => log.filePath === data.filePath);
                    if (idx !== -1) {
                      const updated = [...prev];
                      updated[idx] = { ...updated[idx], ...data };
                      return updated;
                    } else {
                      return [...prev, data];
                    }
                  });
                } catch (e) {
                  console.error('Error parsing SSE data:', e, 'Raw data:', dataStr);
                }
              }
            }
          }
        }
      } catch (error) {
        if (error instanceof Error && error.name === 'AbortError') {
          setConnectionStatus("连接已取消");
        } else {
          console.error('SSE connection error:', error);
          setConnectionStatus(`连接错误: ${error instanceof Error ? error.message : '未知错误'}`);
        }
      }
    };

    startSSE();

    return () => {
      if (abortController) {
        abortController.abort();
      }
    };
  }, [taskId, executionId, loadHistoryLogs]);

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

  return (
    <div style={{
      padding: 12,
      maxWidth: 1200,
      margin: '0 auto',
      fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif'
    }}>
      <div style={{
        marginBottom: 16,
        padding: 16,
        backgroundColor: "#ffffff",
        borderRadius: 12,
        boxShadow: "0 2px 8px rgba(0,0,0,0.1)",
        border: "1px solid #e5e7eb"
      }}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
          <div>
            <p style={{ margin: 0, fontSize: 14, color: "#6b7280" }}>任务ID</p>
            <p style={{ margin: 0, fontSize: 16, fontWeight: 600, color: "#111827" }}>{taskId}</p>
          </div>
          <div>
            <p style={{ margin: 0, fontSize: 14, color: "#6b7280" }}>连接状态</p>
            <p style={{
              margin: 0,
              fontSize: 16,
              fontWeight: 600,
              color: connectionStatus === "已连接" ? "#059669" :
                     connectionStatus.includes("错误") ? "#dc2626" : "#6b7280"
            }}>
              {connectionStatus}
            </p>
          </div>
          <div>
            <p style={{ margin: 0, fontSize: 14, color: "#6b7280" }}>文件数量</p>
            <p style={{ margin: 0, fontSize: 16, fontWeight: 600, color: "#111827" }}>{logs.length}</p>
          </div>
          <div>
            <p style={{ margin: 0, fontSize: 14, color: "#6b7280" }}>任务状态</p>
            <p style={{
              margin: 0,
              fontSize: 16,
              fontWeight: 600,
              color: STATUS_COLOR[taskStatus] ?? "#6b7280"
            }}>
              {taskStatus}
            </p>
          </div>
        </div>

        {taskStatus === "开始" && (
          <div style={{ marginTop: 16, textAlign: 'center' }}>
            <button
              onClick={cancelTask}
              disabled={isCancelling}
              style={{
                padding: "8px 16px",
                backgroundColor: isCancelling ? "#9ca3af" : "#dc2626",
                color: "white",
                border: "none",
                borderRadius: 6,
                fontSize: 14,
                fontWeight: 500,
                cursor: isCancelling ? "not-allowed" : "pointer",
                transition: "background-color 0.2s",
                display: "flex",
                alignItems: "center",
                gap: 8,
                marginLeft: "auto"
              }}
            >
              {isCancelling ? (
                <>
                  <div style={{
                    width: 12,
                    height: 12,
                    border: "2px solid transparent",
                    borderTop: "2px solid white",
                    borderRadius: "50%",
                    animation: "spin 1s linear infinite"
                  }} />
                  取消中...
                </>
              ) : (
                <>
                  ⏹️ 取消任务
                </>
              )}
            </button>
          </div>
        )}
      </div>

      {logs.length === 0 ? (
        <div style={{
          textAlign: 'center',
          padding: 48,
          backgroundColor: "#ffffff",
          borderRadius: 12,
          boxShadow: "0 2px 8px rgba(0,0,0,0.1)",
          border: "1px solid #e5e7eb"
        }}>
          <div style={{ fontSize: 48, marginBottom: 16 }}>📭</div>
          <p style={{ color: "#6b7280", fontSize: 16, margin: 0 }}>暂无下载任务</p>
        </div>
      ) : (
        <>
          <div style={{
            marginBottom: 24,
            padding: 24,
            backgroundColor: "#ffffff",
            borderRadius: 12,
            boxShadow: "0 2px 8px rgba(0,0,0,0.1)",
            border: "1px solid #e5e7eb"
          }}>
            <h2 style={{ margin: 0, fontSize: 20, fontWeight: 600, color: "#111827", marginBottom: 12 }}>任务进度</h2>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <span style={{
                display: 'inline-flex',
                alignItems: 'center',
                padding: '6px 16px',
                borderRadius: 999,
                fontSize: 15,
                fontWeight: 600,
                color: "#fff",
                backgroundColor: STATUS_COLOR[taskStatus] ?? "#6b7280"
              }}>
                {taskStatus}
              </span>
              <span style={{ fontSize: 14, color: "#6b7280" }}>
                已完成 {logs.filter((l) => l.done).length} / {logs.length} 个文件
              </span>
            </div>
          </div>

          <div style={{
            backgroundColor: "#ffffff",
            borderRadius: 12,
            boxShadow: "0 2px 8px rgba(0,0,0,0.1)",
            border: "1px solid #e5e7eb",
            overflow: 'hidden'
          }}>
            <div style={{
              padding: 16,
              borderBottom: "1px solid #e5e7eb",
              backgroundColor: "#f9fafb"
            }}>
              <h3 style={{ margin: 0, fontSize: 18, fontWeight: 600, color: "#111827" }}>下载文件列表</h3>
            </div>

            <div style={{ maxHeight: 600, overflowY: 'auto' }}>
              {logs.slice().reverse().map((log, index) => (
                <div key={index} style={{
                  padding: 12,
                  borderBottom: index < logs.length - 1 ? "1px solid #f3f4f6" : "none",
                  display: 'flex',
                  alignItems: 'center',
                  gap: 10,
                  transition: "background-color 0.2s"
                }}>
                  <div style={{
                    width: 32,
                    height: 32,
                    borderRadius: 6,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 16,
                    backgroundColor: log.done ? "#dcfce7" : log.error ? "#fee2e2" : "#dbeafe"
                  }}>
                    {log.done ? "✅" : log.error ? "❌" : "⏳"}
                  </div>

                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{
                      fontSize: 14,
                      fontWeight: 500,
                      color: "#111827",
                      marginBottom: 4,
                      wordBreak: 'break-all'
                    }}>
                      {log.filePath}
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{
                        fontSize: 12,
                        color: "#6b7280",
                        backgroundColor: "#f3f4f6",
                        padding: "2px 8px",
                        borderRadius: 4
                      }}>
                        {parseFloat(log.percent?.toString() ?? log.overallPercent?.toString() ?? "0").toFixed(2)}%
                      </span>
                      {log.strm && (
                        <span style={{
                          fontSize: 12,
                          color: "#059669",
                          backgroundColor: "#dcfce7",
                          padding: "2px 8px",
                          borderRadius: 4,
                          fontWeight: 500
                        }}>
                          STRM
                        </span>
                      )}
                      {log.error && (
                        <span style={{
                          fontSize: 12,
                          color: "#dc2626",
                          backgroundColor: "#fee2e2",
                          padding: "2px 8px",
                          borderRadius: 4
                        }}>
                          {log.error}
                        </span>
                      )}
                    </div>
                  </div>

                  {!log.done && !log.error && (
                    <div style={{
                      width: 100,
                      height: 6,
                      backgroundColor: "#f3f4f6",
                      borderRadius: 3,
                      overflow: 'hidden'
                    }}>
                      <div style={{
                        width: `${log.percent ?? 0}%`,
                        height: "100%",
                        background: "linear-gradient(90deg, #3b82f6, #1d4ed8)",
                        borderRadius: 3,
                        transition: "width 0.3s ease"
                      }} />
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </>
      )}

      <style>{`
        @keyframes shimmer {
          0% { transform: translateX(-100%); }
          100% { transform: translateX(100%); }
        }
        @keyframes spin {
          0% { transform: rotate(0deg); }
          100% { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  );
}
