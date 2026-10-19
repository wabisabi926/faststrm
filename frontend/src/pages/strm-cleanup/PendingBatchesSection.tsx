// 待删批次列表：展示「转入待确认队列」的清理批次，支持 Web UI 内直接执行/取消。
// 对应后端 GET /api/strmCleanup/pending、POST .../pending/cancel、POST .../pending/execute。
// 批次超过 24 小时（后端 BatchMaxAge）会被自动作废，这里显示剩余时间。

import * as React from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Loader2, Trash2, XCircle } from "lucide-react";
import axiosInstance from "@/lib/axios";
import { toast } from "sonner";

const BATCH_MAX_AGE_MS = 24 * 60 * 60 * 1000;

interface PendingBatch {
  requestId: string;
  createdAt: number;
  pathCount: number;
  samplePaths?: string[];
  removeStrm?: boolean;
  removeRelatedFiles?: boolean;
  removeEmptyDirs?: boolean;
}

export function PendingBatchesSection() {
  const [batches, setBatches] = React.useState<PendingBatch[]>([]);
  const [enabled, setEnabled] = React.useState<boolean>(true);
  const [loading, setLoading] = React.useState<boolean>(true);
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const [confirmTarget, setConfirmTarget] = React.useState<PendingBatch | null>(null);

  const fetchBatches = React.useCallback(async () => {
    try {
      setLoading(true);
      const res = await axiosInstance.get("/api/strmCleanup/pending");
      setBatches(Array.isArray(res.data?.batches) ? res.data.batches : []);
      setEnabled(res.data?.enabled !== false);
    } catch {
      // 接口不可用时静默（功能未启用/SQLite 不可用属正常状态）
      setBatches([]);
      setEnabled(false);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void fetchBatches();
  }, [fetchBatches]);

  const handleCancel = async (b: PendingBatch) => {
    setBusyId(b.requestId);
    try {
      await axiosInstance.post("/api/strmCleanup/pending/cancel", { requestId: b.requestId });
      toast.success("已取消该批次（文件未删除）");
      void fetchBatches();
    } catch (e) {
      const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error || "取消失败";
      toast.error(msg);
    } finally {
      setBusyId(null);
    }
  };

  const handleExecute = async (b: PendingBatch) => {
    setBusyId(b.requestId);
    try {
      const res = await axiosInstance.post("/api/strmCleanup/pending/execute", { requestId: b.requestId });
      const deleted = res.data?.deletedCount ?? 0;
      const failed = res.data?.failedCount ?? 0;
      toast.success(`删除完成：成功 ${deleted} 个${failed > 0 ? `，失败 ${failed} 个` : ""}`);
      void fetchBatches();
    } catch (e) {
      const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error || "执行删除失败";
      toast.error(msg);
    } finally {
      setBusyId(null);
    }
  };

  if (!enabled && !loading) {
    return null; // 功能未启用（未开确认模式/SQLite 不可用），不占版面
  }

  return (
    <div className="mt-4 rounded-lg border border-amber-500/30 bg-amber-500/5 p-4">
      <div className="flex items-center justify-between gap-2 mb-3">
        <h3 className="text-sm font-medium flex items-center gap-2">
          <Trash2 className="size-4 text-amber-500" />
          待确认删除批次
          {batches.length > 0 && (
            <Badge variant="secondary" className="text-xs">{batches.length}</Badge>
          )}
        </h3>
        <Button variant="ghost" size="sm" onClick={() => void fetchBatches()} disabled={loading}>
          {loading ? <Loader2 className="size-4 animate-spin" /> : "刷新"}
        </Button>
      </div>

      {!loading && batches.length === 0 && (
        <p className="text-xs text-muted-foreground">暂无待确认批次。扫描发现孤儿 STRM 且超过保护阈值时会转入这里。</p>
      )}

      <div className="space-y-3">
        {batches.map((b) => {
          const ageMs = Date.now() - b.createdAt;
          const remainingMs = BATCH_MAX_AGE_MS - ageMs;
          const expired = remainingMs <= 0;
          const remainingText = expired
            ? "已过期（待自动作废）"
            : `剩余 ${Math.max(Math.floor(remainingMs / 3600000), 0)} 小时`;
          return (
            <div key={b.requestId} className="rounded-md border bg-background p-3 space-y-2">
              <div className="flex items-center justify-between gap-2 flex-wrap">
                <span className="text-sm font-medium">
                  {b.pathCount} 个文件
                  {b.removeRelatedFiles && <span className="text-xs text-muted-foreground ml-2">（连带字幕/nfo/海报）</span>}
                  {b.removeEmptyDirs && <span className="text-xs text-muted-foreground ml-2">（清理空目录）</span>}
                </span>
                <Badge variant={expired ? "destructive" : "outline"} className="text-xs shrink-0">
                  {remainingText}
                </Badge>
              </div>
              {b.samplePaths && b.samplePaths.length > 0 && (
                <div className="text-xs text-muted-foreground space-y-0.5">
                  {b.samplePaths.slice(0, 3).map((p, i) => (
                    <p key={i} className="font-mono truncate" title={p}>{p}</p>
                  ))}
                  {b.pathCount > 3 && <p>… 等共 {b.pathCount} 个</p>}
                </div>
              )}
              <div className="flex gap-2 justify-end">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busyId === b.requestId}
                  onClick={() => void handleCancel(b)}
                >
                  <XCircle className="mr-1 size-3.5" />
                  取消
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={busyId === b.requestId || expired}
                  onClick={() => setConfirmTarget(b)}
                >
                  {busyId === b.requestId ? <Loader2 className="mr-1 size-3.5 animate-spin" /> : <Trash2 className="mr-1 size-3.5" />}
                  确认删除
                </Button>
              </div>
            </div>
          );
        })}
      </div>

      <AlertDialog open={confirmTarget !== null} onOpenChange={(o) => !o && setConfirmTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认执行删除？</AlertDialogTitle>
            <AlertDialogDescription>
              将删除 {confirmTarget?.pathCount ?? 0} 个 STRM 文件
              {confirmTarget?.removeRelatedFiles ? "及其关联的字幕/nfo/海报文件" : ""}
              ，此操作无法撤销。超过 24 小时未确认的批次会被自动作废。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (confirmTarget) void handleExecute(confirmTarget);
                setConfirmTarget(null);
              }}
            >
              确认删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
