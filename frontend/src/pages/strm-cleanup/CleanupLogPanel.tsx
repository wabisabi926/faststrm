// 操作日志面板：展示最近 50 条操作日志。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

import { Button } from "@/components/ui/button";
import type { LogEntry } from "./types";

export interface CleanupLogPanelProps {
  logs: LogEntry[];
  onClear: () => void;
}

export function CleanupLogPanel({ logs, onClear }: CleanupLogPanelProps) {
  if (logs.length === 0) return null;

  return (
    <div className="rounded-md border p-3 space-y-2">
      <div className="text-sm font-medium flex items-center gap-2">
        <span>操作日志</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-xs"
          onClick={onClear}
        >
          清空
        </Button>
      </div>
      <div className="space-y-1 max-h-32 overflow-auto">
        {logs.map((log, idx) => (
          <div key={idx} className="flex items-center gap-2 text-xs">
            <span className="text-muted-foreground font-mono w-16 shrink-0">{log.time}</span>
            <span className={`w-14 shrink-0 ${log.success ? "text-green-600" : "text-destructive"}`}>
              {log.action}
            </span>
            <span className="truncate">{log.detail}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
