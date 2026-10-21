import * as React from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Loader2, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import { ConfirmActionDialog } from "./ConfirmActionDialog";
import axiosInstance from "@/lib/axios";
import { toast } from "sonner";
import type { TrashEntry, TrashListResponse } from "./types";

function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let idx = 0;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx++;
  }
  return `${value.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`;
}

export interface TrashSectionProps {
  /** 父级刷新计数：变化时重新拉取（页头「刷新」或批次执行后） */
  refreshToken?: number;
}

export function TrashSection({ refreshToken = 0 }: TrashSectionProps) {
  const [entries, setEntries] = React.useState<TrashEntry[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [restoring, setRestoring] = React.useState<string | null>(null);
  const [purging, setPurging] = React.useState(false);
  const [truncated, setTruncated] = React.useState(false);
  const [error, setError] = React.useState("");

  const fetchTrash = React.useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const res = await axiosInstance.get<TrashListResponse>("/api/strmCleanup/trash");
      setEntries(Array.isArray(res.data?.entries) ? res.data.entries : []);
      setTruncated(!!res.data?.truncated);
    } catch (e) {
      const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error;
      setError(msg || "读取回收站失败");
      setEntries([]);
      setTruncated(false);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void fetchTrash();
  }, [fetchTrash]);

  React.useEffect(() => {
    if (refreshToken > 0) void fetchTrash();
  }, [refreshToken, fetchTrash]);

  const restore = async (entry: TrashEntry) => {
    setRestoring(entry.trashPath);
    try {
      await axiosInstance.post("/api/strmCleanup/trash/restore", {
        localPath: entry.root,
        trashPath: entry.trashPath,
      });
      toast.success("已恢复到原路径");
      setEntries((prev) => prev.filter((item) => item.trashPath !== entry.trashPath));
    } catch (e) {
      const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error || "恢复失败";
      toast.error(msg);
    } finally {
      setRestoring(null);
    }
  };

  const purgeAll = async () => {
    setPurging(true);
    try {
      const res = await axiosInstance.post<{ removedBatches: number }>("/api/strmCleanup/trash/purge", {});
      toast.success(`已清空回收站（${res.data?.removedBatches ?? 0} 个批次）`);
      setEntries([]);
    } catch (e) {
      const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error || "清空回收站失败";
      toast.error(msg);
    } finally {
      setPurging(false);
    }
  };

  return (
    <section className="border rounded-md p-3 sm:p-4 space-y-3">
      <div className="flex items-center gap-2">
        <Trash2 className="h-4 w-4 text-muted-foreground" />
        <h3 className="text-sm font-medium">回收站</h3>
        {entries.length > 0 && (
          <Badge variant="outline" className="text-[10px]">
            {entries.length}
          </Badge>
        )}
        <div className="ml-auto flex items-center gap-1">
          {entries.length > 0 && (
            <ConfirmActionDialog
              destructive
              title={`清空回收站（${entries.length} 个文件）？`}
              description="回收站里的文件将被永久删除，无法恢复。仅在确认不再需要回滚时使用。"
              confirmLabel="永久删除"
              onConfirm={() => void purgeAll()}
              trigger={
                <Button variant="ghost" size="sm" disabled={purging} className="text-destructive">
                  {purging ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : <Trash2 className="mr-1 h-3.5 w-3.5" />}
                  清空
                </Button>
              }
            />
          )}
          <Button variant="ghost" size="sm" onClick={() => void fetchTrash()} disabled={loading}>
            {loading ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="mr-1 h-3.5 w-3.5" />}
            刷新
          </Button>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        清理的文件会移到对应目录的 <code>.faststrm-trash</code>，保留 7 天，过期项会在后续清理/任务执行时清除。恢复会回到原路径；
        原路径已有文件时会被拒绝，避免覆盖。
      </p>

      {loading ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          正在读取回收站...
        </div>
      ) : entries.length === 0 ? (
        <p className="text-xs text-muted-foreground">{error || "暂无待恢复文件。"}</p>
      ) : (
        <div className="space-y-2">
          {entries.map((entry) => (
            <div
              key={entry.trashPath}
              className="flex flex-col gap-2 rounded-md border p-2.5 sm:flex-row sm:items-center"
            >
              <div className="min-w-0 flex-1">
                <div className="break-all font-mono text-xs">{entry.originalPath}</div>
                <div className="mt-1 text-xs text-muted-foreground">
                  批次 {entry.batch} · {formatSize(entry.size)} · {new Date(entry.modTime).toLocaleString()}
                </div>
              </div>
              <Button
                variant="outline"
                size="sm"
                className="shrink-0"
                onClick={() => void restore(entry)}
                disabled={restoring !== null}
              >
                {restoring === entry.trashPath ? (
                  <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />
                ) : (
                  <RotateCcw className="mr-1 h-3.5 w-3.5" />
                )}
                恢复
              </Button>
            </div>
          ))}
        </div>
      )}

      {truncated && (
        <p className="text-xs text-amber-600">回收站条目较多，仅显示前 500 条；请先恢复或等待自动清理后再刷新。</p>
      )}
      {!loading && error && entries.length > 0 && <p className="text-xs text-destructive">{error}</p>}
    </section>
  );
}
