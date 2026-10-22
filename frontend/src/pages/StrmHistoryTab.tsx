import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { CheckCircle, Eye, RefreshCw, XCircle } from "lucide-react";
import axiosInstance from "@/lib/axios";

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

const KIND_LABELS: Record<string, string> = {
  full: "全量扫描",
  increment: "增量扫描",
  monitor: "生活事件",
  rename: "重命名",
  move: "移动",
  delete: "删除",
};

function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind ?? "-";
}

function formatTime(ts: number): string {
  if (!ts) return "-";
  return new Date(ts).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "-";
  return `${(ms / 1000).toFixed(1)}s`;
}

function StatCard({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Card>
      <CardContent className="p-3">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="text-2xl font-semibold">{value}</p>
      </CardContent>
    </Card>
  );
}

export function StrmHistoryTab() {
  const [items, setItems] = useState<StrmHistoryEntry[]>([]);
  const [stats, setStats] = useState<StrmHistoryStats | null>(null);
  const [kind, setKind] = useState("all");
  const [loading, setLoading] = useState(true);
  const [detail, setDetail] = useState<StrmHistoryEntry | null>(null);

  const fetchHistory = useCallback(async () => {
    setLoading(true);
    try {
      const kindQuery = kind === "all" ? "" : `&kind=${encodeURIComponent(kind)}`;
      const [listRes, statsRes] = await Promise.all([
        axiosInstance.get(`/api/history/strm?limit=100${kindQuery}`),
        axiosInstance.get(`/api/history/strm/stats${kind === "all" ? "" : `?kind=${encodeURIComponent(kind)}`}`),
      ]);
      const list = listRes.data;
      setItems(Array.isArray(list) ? list : (list?.items || []));
      setStats(statsRes.data || null);
    } catch (error) {
      console.error("Failed to fetch STRM history:", error);
      toast.error("获取 STRM 操作历史失败");
    } finally {
      setLoading(false);
    }
  }, [kind]);

  useEffect(() => {
    void fetchHistory();
  }, [fetchHistory]);

  const avgDuration = useMemo(() => {
    if (!stats || stats.totalCount <= 0) return 0;
    return stats.totalElapsedMs / stats.totalCount;
  }, [stats]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger className="h-8 w-[150px] text-xs">
            <SelectValue placeholder="全部类型" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部类型</SelectItem>
            <SelectItem value="full">全量扫描</SelectItem>
            <SelectItem value="increment">增量扫描</SelectItem>
            <SelectItem value="monitor">生活事件</SelectItem>
            <SelectItem value="rename">重命名</SelectItem>
            <SelectItem value="move">移动</SelectItem>
            <SelectItem value="delete">删除</SelectItem>
          </SelectContent>
        </Select>
        <Button
          variant="outline"
          size="sm"
          className="h-8"
          onClick={() => void fetchHistory()}
          disabled={loading}
        >
          <RefreshCw className={`mr-1 h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
          刷新
        </Button>
      </div>

      {stats && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <StatCard label="总记录" value={stats.totalCount} />
          <StatCard
            label="成功 / 失败"
            value={
              <>
                <span className="text-green-600 dark:text-green-400">{stats.successCount}</span>
                <span className="text-muted-foreground"> / </span>
                <span className="text-red-600 dark:text-red-400">{stats.failedCount}</span>
              </>
            }
          />
          <StatCard label="文件成功 / 总数" value={`${stats.successFiles} / ${stats.totalFiles}`} />
          <StatCard label="平均耗时" value={formatElapsed(avgDuration)} />
        </div>
      )}

      {loading ? (
        <div className="rounded-md border py-10 text-center text-sm text-muted-foreground">
          正在加载 STRM 操作历史...
        </div>
      ) : items.length === 0 ? (
        <div className="rounded-md border py-10 text-center text-sm text-muted-foreground">
          暂无 STRM 操作记录
        </div>
      ) : (
        <div className="rounded-md border overflow-x-auto [&_table]:text-xs">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="whitespace-nowrap">时间</TableHead>
                <TableHead className="whitespace-nowrap">类型</TableHead>
                <TableHead>任务 / 账号</TableHead>
                <TableHead className="whitespace-nowrap">状态</TableHead>
                <TableHead className="whitespace-nowrap">文件</TableHead>
                <TableHead className="whitespace-nowrap">API</TableHead>
                <TableHead className="whitespace-nowrap">耗时</TableHead>
                <TableHead className="whitespace-nowrap">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="whitespace-nowrap text-xs">{formatTime(item.createdAt)}</TableCell>
                  <TableCell>
                    <Badge variant="secondary" className="text-[10px]">{kindLabel(item.kind)}</Badge>
                  </TableCell>
                  <TableCell className="min-w-[180px] max-w-[320px]">
                    <div className="truncate font-mono text-xs" title={item.taskId}>
                      {item.taskId || "-"}
                    </div>
                    <div className="truncate text-xs text-muted-foreground">{item.account || "-"}</div>
                    {item.errorMsg && (
                      <div className="truncate text-xs text-destructive" title={item.errorMsg}>
                        {item.errorMsg}
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge
                      className={`text-[10px] ${
                        item.success
                          ? "bg-green-500/15 text-green-700 dark:text-green-400"
                          : "bg-red-500/15 text-red-700 dark:text-red-400"
                      }`}
                    >
                      {item.success ? (
                        <CheckCircle className="mr-1 h-3 w-3" />
                      ) : (
                        <XCircle className="mr-1 h-3 w-3" />
                      )}
                      {item.success ? "成功" : "失败"}
                    </Badge>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs">
                    {item.successFiles} / {item.totalFiles}
                    {item.failedFiles > 0 && (
                      <span className="ml-1 text-destructive">（失败 {item.failedFiles}）</span>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs">{item.apiRequests}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs">{formatElapsed(item.elapsedMs)}</TableCell>
                  <TableCell>
                    <Button
                      size="sm"
                      variant="outline"
                      className="h-7 text-xs"
                      onClick={() => setDetail(item)}
                    >
                      <Eye className="mr-1 h-3.5 w-3.5" />
                      详情
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog open={detail !== null} onOpenChange={(open) => !open && setDetail(null)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>STRM 操作详情</DialogTitle>
            <DialogDescription>记录 #{detail?.id}</DialogDescription>
          </DialogHeader>
          {detail && (
            <dl className="grid grid-cols-[100px_1fr] gap-x-3 gap-y-2 text-sm">
              <dt className="text-muted-foreground">时间</dt>
              <dd>{formatTime(detail.createdAt)}</dd>
              <dt className="text-muted-foreground">类型</dt>
              <dd>{kindLabel(detail.kind)}</dd>
              <dt className="text-muted-foreground">任务</dt>
              <dd className="font-mono break-all">{detail.taskId || "-"}</dd>
              <dt className="text-muted-foreground">账号</dt>
              <dd>{detail.account || "-"}</dd>
              <dt className="text-muted-foreground">结果</dt>
              <dd>{detail.success ? "成功" : "失败"}</dd>
              <dt className="text-muted-foreground">文件</dt>
              <dd>
                {detail.successFiles} / {detail.totalFiles}，失败 {detail.failedFiles}
              </dd>
              <dt className="text-muted-foreground">API 请求</dt>
              <dd>{detail.apiRequests}</dd>
              <dt className="text-muted-foreground">耗时</dt>
              <dd>{formatElapsed(detail.elapsedMs)}</dd>
              {detail.errorMsg && (
                <>
                  <dt className="text-muted-foreground">错误</dt>
                  <dd className="text-destructive break-all">{detail.errorMsg}</dd>
                </>
              )}
            </dl>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
