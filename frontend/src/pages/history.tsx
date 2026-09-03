import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
  Trash
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

// 状态图标和颜色映射
const getStatusConfig = (status: TaskExecutionHistory["status"]) => {
  const configs = {
    running: { icon: Clock, color: "bg-blue-500/20 text-blue-400", label: "运行中" },
    completed: { icon: CheckCircle, color: "bg-green-500/20 text-green-400", label: "已完成" },
    failed: { icon: XCircle, color: "bg-red-500/20 text-red-400", label: "失败" },
    cancelled: { icon: AlertCircle, color: "bg-yellow-500/20 text-yellow-400", label: "已取消" },
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

export default function TaskHistoryPage() {
  const [history, setHistory] = useState<TaskExecutionHistory[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchHistory();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
      // 重新加载历史记录
      fetchHistory();
    } catch (error) {
      logger.error("Failed to delete history:", error);
      toast.error("删除失败");
    }
  };

  const viewLogs = (execution: TaskExecutionHistory) => {
    // 跳转到日志查看页面
    window.open(`/log/${execution.taskId}?executionId=${String(execution.id)}`, "_blank");
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-foreground mx-auto"></div>
          <p className="mt-2 text-muted-foreground">加载中...</p>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-7xl space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 min-w-0">
        <div className="min-w-0 break-words">
          <h1 className="text-2xl font-semibold break-words">任务执行历史</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            查看所有任务的执行记录和状态
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

      {history.length === 0 ? (
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
                        <CardTitle className="text-lg break-words">
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
      )}
    </div>
  );
}
