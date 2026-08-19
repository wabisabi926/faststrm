"use client";

import { useEffect, useState, useCallback } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
  RefreshCw,
  Trash2,
  CheckCircle,
  XCircle,
  FileText,
  Filter,
} from "lucide-react";
import { logger } from "@/lib/logger";

type LifeEventType = "create" | "delete" | "move" | "rename" | "folder-sync";

interface LifeEventLog {
  id: string;
  timestamp: number;
  account: string;
  eventType: LifeEventType;
  success: boolean;
  filePath?: string;
  localPath?: string;
  message: string;
}

const TYPE_LABELS: Record<LifeEventType, { label: string; color: string }> = {
  create: { label: "创建", color: "bg-green-500/20 text-green-400" },
  delete: { label: "删除", color: "bg-red-500/20 text-red-400" },
  move: { label: "移动", color: "bg-blue-500/20 text-blue-400" },
  rename: { label: "重命名", color: "bg-purple-500/20 text-purple-400" },
  "folder-sync": { label: "文件夹同步", color: "bg-orange-500/20 text-orange-400" },
};

function formatTime(ts: number) {
  const d = new Date(ts);
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export default function LifeEventsPage() {
  const [items, setItems] = useState<LifeEventLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [accountFilter, setAccountFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [successFilter, setSuccessFilter] = useState<string>("all");
  const [autoRefresh, setAutoRefresh] = useState(true);

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      const params = new URLSearchParams();
      if (accountFilter) params.set("account", accountFilter);
      if (typeFilter !== "all") params.set("eventType", typeFilter);
      if (successFilter !== "all") params.set("success", successFilter);
      params.set("limit", "500");

      const res = await axiosInstance.get(
        `/api/lifeEvents?${params.toString()}`
      );
      setItems(res.data.items || []);
    } catch (err) {
      toast.error("获取生活事件日志失败");
      logger.error(err);
    } finally {
      setLoading(false);
    }
  }, [accountFilter, typeFilter, successFilter]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (!autoRefresh) return;
    const t = setInterval(fetchData, 10000);
    return () => clearInterval(t);
  }, [autoRefresh, fetchData]);

  const handleClear = async () => {
    if (!confirm("确定要清空所有生活事件日志吗？")) return;
    try {
      await axiosInstance.delete("/api/lifeEvents?action=clear");
      toast.success("已清空");
      fetchData();
    } catch {
      toast.error("清空失败");
    }
  };

  const handleDeleteOne = async (id: string) => {
    try {
      await axiosInstance.delete(`/api/lifeEvents?id=${encodeURIComponent(id)}`);
      setItems(items.filter((i) => i.id !== id));
    } catch {
      toast.error("删除失败");
    }
  };

  const accounts = Array.from(new Set(items.map((i) => i.account)));

  const successCount = items.filter((i) => i.success).length;
  const failCount = items.length - successCount;

  return (
    <div className="mx-auto max-w-7xl space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">生活事件日志</h1>
          <p className="text-sm text-muted-foreground mt-1">
            115 生活事件监控处理记录，保留最近 7 天
          </p>
        </div>
        <div className="flex space-x-2">
          <Button onClick={() => setAutoRefresh(!autoRefresh)} variant="outline">
            {autoRefresh ? "⏸ 暂停自动刷新" : "▶ 自动刷新"}
          </Button>
          <Button onClick={fetchData} variant="outline">
            <RefreshCw className="h-4 w-4 mr-1" />
            刷新
          </Button>
          <Button onClick={handleClear} variant="outline">
            <Trash2 className="h-4 w-4 mr-1" />
            清空
          </Button>
        </div>
      </div>

      {/* 过滤条 */}
      <Card>
        <CardContent className="py-3">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
            <div className="flex items-center gap-1.5">
              <Filter className="h-4 w-4 text-muted-foreground" />
              <span className="text-sm font-medium text-muted-foreground">过滤</span>
            </div>
            <div className="flex items-center gap-2">
              <label className="text-sm text-muted-foreground whitespace-nowrap">账号</label>
              <select
                className="h-8 rounded-md border border-input bg-background px-2 text-sm min-w-[100px]"
                value={accountFilter}
                onChange={(e) => setAccountFilter(e.target.value)}
              >
                <option value="">全部</option>
                {accounts.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex items-center gap-2">
              <label className="text-sm text-muted-foreground whitespace-nowrap">事件类型</label>
              <select
                className="h-8 rounded-md border border-input bg-background px-2 text-sm min-w-[100px]"
                value={typeFilter}
                onChange={(e) => setTypeFilter(e.target.value)}
              >
                <option value="all">全部</option>
                <option value="create">创建</option>
                <option value="delete">删除</option>
                <option value="move">移动</option>
                <option value="rename">重命名</option>
                <option value="folder-sync">文件夹同步</option>
              </select>
            </div>
            <div className="flex items-center gap-2">
              <label className="text-sm text-muted-foreground whitespace-nowrap">状态</label>
              <select
                className="h-8 rounded-md border border-input bg-background px-2 text-sm min-w-[100px]"
                value={successFilter}
                onChange={(e) => setSuccessFilter(e.target.value)}
              >
                <option value="all">全部</option>
                <option value="true">成功</option>
                <option value="false">失败</option>
              </select>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 统计 */}
      <div className="flex items-center gap-4 text-sm">
        <span>
          共 <strong>{items.length}</strong> 条
        </span>
        <span className="flex items-center gap-1 text-green-500">
          <CheckCircle className="h-3.5 w-3.5" />
          成功 {successCount}
        </span>
        <span className="flex items-center gap-1 text-red-500">
          <XCircle className="h-3.5 w-3.5" />
          失败 {failCount}
        </span>
      </div>

      {loading && items.length === 0 ? (
        <Card>
          <CardContent className="flex items-center justify-center h-32">
            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-foreground"></div>
          </CardContent>
        </Card>
      ) : items.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center justify-center h-40">
            <FileText className="h-10 w-10 text-muted-foreground/60 mb-2" />
            <p className="text-muted-foreground">暂无生活事件日志</p>
            <p className="text-xs text-muted-foreground mt-1">
              启动生活事件监控后，新事件会自动记录在这里
            </p>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base">事件列表</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-32">时间</TableHead>
                    <TableHead className="w-20">账号</TableHead>
                    <TableHead className="w-24">类型</TableHead>
                    <TableHead className="w-20">状态</TableHead>
                    <TableHead>处理结果</TableHead>
                    <TableHead className="w-10"></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((item) => (
                    <TableRow key={item.id} className="hover:bg-muted/50">
                      <TableCell className="text-xs text-muted-foreground font-mono">
                        {formatTime(item.timestamp)}
                      </TableCell>
                      <TableCell className="text-xs">{item.account}</TableCell>
                      <TableCell>
                        <Badge
                          className={
                            TYPE_LABELS[item.eventType]?.color ||
                            "bg-muted text-muted-foreground"
                          }
                        >
                          {TYPE_LABELS[item.eventType]?.label || item.eventType}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {item.success ? (
                          <CheckCircle className="h-4 w-4 text-green-500" />
                        ) : (
                          <XCircle className="h-4 w-4 text-red-500" />
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground max-w-md truncate" title={item.message}>
                        {item.message}
                      </TableCell>
                      <TableCell>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7"
                          onClick={() => handleDeleteOne(item.id)}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
