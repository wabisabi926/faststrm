// SSE 实时事件客户端：订阅后端 /api/events/stream（internal/service/sse/server.go）。
// 协议：帧 = "data: <json>\n\n"，无 event: 前缀；事件类型在 JSON 的 event 字段
// （progress/log/complete/error/cancel）。端点为公开路由，无需鉴权。
// EventSource 断线由浏览器自动重连；重连后服务端会重放该任务缓冲的历史帧。

import { useEffect, useRef, useState } from "react";

export interface TaskProgressEvent {
  event: "progress";
  taskId: string;
  filePath?: string;
  percent?: number;
  overallPercent?: string;
  done?: boolean;
  error?: string;
  stage?: string;
  stageDetail?: string;
}

export interface TaskLogEvent {
  event: "log";
  taskId: string;
  level?: string;
  message: string;
  timestamp?: number;
}

export interface TaskCompleteEvent {
  event: "complete";
  taskId: string;
  status: "completed" | "failed" | "cancelled";
  totalFiles?: number;
  downloadedFiles?: number;
  deletedFiles?: number;
  durationMs?: number;
  error?: string;
}

export interface TaskErrorEvent {
  event: "error";
  taskId: string;
  error?: string;
}

export interface TaskCancelEvent {
  event: "cancel";
  taskId: string;
}

export type TaskEvent =
  | TaskProgressEvent
  | TaskLogEvent
  | TaskCompleteEvent
  | TaskErrorEvent
  | TaskCancelEvent;

export interface TaskEventHandlers {
  onProgress?: (e: TaskProgressEvent) => void;
  onLog?: (e: TaskLogEvent) => void;
  onComplete?: (e: TaskCompleteEvent) => void;
  onError?: (e: TaskErrorEvent) => void;
  onCancel?: (e: TaskCancelEvent) => void;
}

/**
 * useTaskEventStream 订阅任务事件流。
 * @param taskId 订阅指定任务；传 null 订阅全部（全局）；传 undefined 不订阅
 * @returns connected 连接状态（EventSource 断开时为 false，浏览器会自动重连）
 */
export function useTaskEventStream(
  taskId: string | null | undefined,
  handlers: TaskEventHandlers
): { connected: boolean } {
  const handlersRef = useRef(handlers);
  handlersRef.current = handlers;
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (taskId === undefined) return;

    const url = taskId
      ? `/api/events/stream?taskId=${encodeURIComponent(taskId)}`
      : "/api/events/stream";
    const es = new EventSource(url);

    es.onopen = () => setConnected(true);
    // onerror 后浏览器自动重连，不手动 close；仅同步状态
    es.onerror = () => setConnected(false);

    es.onmessage = (ev: MessageEvent<string>) => {
      let data: TaskEvent;
      try {
        data = JSON.parse(ev.data) as TaskEvent;
      } catch {
        return; // 心跳 comment 不走 onmessage；非 JSON 帧忽略
      }
      switch (data?.event) {
        case "progress":
          handlersRef.current.onProgress?.(data);
          break;
        case "log":
          handlersRef.current.onLog?.(data);
          break;
        case "complete":
          handlersRef.current.onComplete?.(data);
          break;
        case "error":
          handlersRef.current.onError?.(data);
          break;
        case "cancel":
          handlersRef.current.onCancel?.(data);
          break;
      }
    };

    return () => {
      es.close();
      setConnected(false);
    };
  }, [taskId]);

  return { connected };
}
