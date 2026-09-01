// 任务管理业务逻辑 hook：抽离 API 调用、状态管理、派生函数。
// 从 task/index.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";
import type { TaskApiResponse } from "@/types/api";
import {
  type Task,
  STATUS_MAP,
  STATUS_LABELS,
  getStatusConfig,
} from "./types";

export interface AccountBrief {
  name: string;
  accountType: string;
}

export interface TaskDisplayStatus {
  status: Task["status"] | "processing";
  label: string;
}

export interface UseTasksResult {
  // 状态
  data: Task[];
  isLoading: boolean;
  accounts: AccountBrief[];
  accountsLoading: boolean;
  startingTasks: Set<string>;
  nowTs: number;
  // 派生函数
  isAccountBusy: (accountName: string) => boolean;
  isTaskDisabled: (task: Task) => boolean;
  getTaskDisplayStatus: (task: Task) => TaskDisplayStatus;
  // API 调用
  fetchTasks: () => Promise<void>;
  fetchAccounts: () => Promise<void>;
  deleteTask: (id: string, cleanStrm: boolean) => Promise<void>;
  startTask: (id: string) => Promise<void>;
  cancelTask: (id: string) => Promise<void>;
  goToLog: (id: string) => Promise<void>;
  clearDirectory: (targetPath: string) => Promise<void>;
}

export function useTasks(): UseTasksResult {
  const navigate = useNavigate();
  const [data, setData] = useState<Task[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [accounts, setAccounts] = useState<AccountBrief[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [startingTasks, setStartingTasks] = useState<Set<string>>(new Set());
  const [nowTs, setNowTs] = useState<number>(Date.now());

  // 每秒刷新一次 nowTs（驱动所有 running 任务的已用时间显示）
  useEffect(() => {
    const t = setInterval(() => setNowTs(Date.now()), 1000);
    return () => clearInterval(t);
    // 仅做挂壁时钟，无外部依赖
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    void fetchTasks();
    void fetchAccounts();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 检查账户是否有任务正在运行或启动
  const isAccountBusy = (accountName: string) => {
    return data.some(
      (task) =>
        task.account === accountName &&
        (task.status === "processing" || startingTasks.has(task.id))
    );
  };

  // 检查任务是否应该被禁用
  const isTaskDisabled = (task: Task) => {
    const isStarting = startingTasks.has(task.id);
    const isRunning = task.status === "processing";
    const hasSameAccountRunning = isAccountBusy(task.account);

    return isStarting || isRunning || hasSameAccountRunning;
  };

  // 获取任务显示状态
  const getTaskDisplayStatus = (task: Task): TaskDisplayStatus => {
    const isStarting = startingTasks.has(task.id);
    const isRunning = task.status === "processing";

    if (isStarting) {
      return { status: "processing" as const, label: STATUS_LABELS.starting };
    } else if (isRunning) {
      return { status: "processing" as const, label: STATUS_LABELS.running };
    } else {
      return { status: task.status, label: getStatusConfig(task.status).label };
    }
  };

  // 获取任务列表
  const fetchTasks = async () => {
    try {
      setIsLoading(true);
      const res = await axiosInstance.get("/api/tasks");
      const payload = res.data;
      const tasks = Array.isArray(payload) ? payload : (payload.tasks || []);
      const mapped: Task[] = tasks.map((t: TaskApiResponse) => ({
        id: t.id || '',
        name: t.name || '',
        account: t.account || '',
        accountType: t.accountType || '',
        originPath: t.originPath || '',
        targetPath: t.targetPath || '',
        strmType: t.strmType || '',
        strmPrefix: t.strmPrefix || '',
        removeExtraFiles: t.removeExtraFiles ?? false,
        // 使用状态映射将后端状态转换为前端状态
        status: STATUS_MAP[(t.runtime?.status || t.status || "") as keyof typeof STATUS_MAP] || "pending",
        error: t.runtime?.error ?? null,
        schedule: t.schedule ? {
          enabled: t.schedule.enabled ?? false,
          mode: t.schedule.mode || "interval",
          intervalMinutes: t.schedule.intervalMinutes,
          time: t.schedule.time,
          weekdays: t.schedule.weekdays,
          lastRunAt: t.schedule.lastRunAt,
          nextRunAt: t.scheduleNext?.nextRunAt,
        } : undefined,
        _computedNextRunAt: t.scheduleNext?.nextRunAt ?? null,
      }));
      setData(mapped);
    } catch {
      toast.error("获取任务列表失败");
    } finally {
      setIsLoading(false);
    }
  };

  // 获取账户列表
  const fetchAccounts = async () => {
    try {
      setAccountsLoading(true);
      const res = await axiosInstance.get("/api/account");
      setAccounts(res.data.map((a: { name: string; accountType: string }) => ({ name: a.name, accountType: a.accountType })));
    } catch {
      toast.error("获取账户列表失败");
    } finally {
      setAccountsLoading(false);
    }
  };

  // 删除任务
  const deleteTask = async (id: string, cleanStrm: boolean) => {
    try {
      await axiosInstance.delete(`/api/task?id=${id}${cleanStrm ? "&cleanStrm=true" : ""}`);
      toast.success(cleanStrm ? "任务删除成功，STRM 目录已清理" : "任务删除成功");
      void fetchTasks();
    } catch {
      toast.error("删除失败");
    }
  };

  // 开始任务
  const startTask = async (id: string) => {
    // 添加到正在启动的任务集合
    setStartingTasks((prev) => new Set(prev).add(id));

    try {
      const res = await axiosInstance.post(
        "/api/startTask",
        { taskId: id },
        { timeout: 180000 } // 设置180秒超时
      );
      toast.success(`任务已开始: ${res.data.message}`);

      // 只有在API成功返回后才更新状态为processing
      setData((prevData) =>
        prevData.map((task) =>
          task.id === id ? { ...task, status: "processing" as const, error: null } : task
        )
      );

      // 启动后自动轮询刷新状态：3s、10s 各一次（快速失败能立即看到）
      setTimeout(() => {
        void fetchTasks();
      }, 3000);
      setTimeout(() => {
        void fetchTasks();
      }, 10000);
    } catch (error: unknown) {
      if (error && typeof error === 'object' && 'code' in error && error.code === 'ECONNABORTED') {
        toast.error("任务启动超时，请稍后检查任务状态");
      } else if (error && typeof error === 'object' && 'response' in error) {
        // 处理API错误响应
        const apiError = error as { response?: { data?: { message?: string; error?: string } } };
        const message = apiError.response?.data?.message || "任务开始失败";
        const detail = apiError.response?.data?.error;
        const errorText = detail ? `${message}: ${detail}` : message;
        toast.error(errorText);
      } else {
        toast.error("任务开始失败");
      }
    } finally {
      // 从正在启动的任务集合中移除
      setStartingTasks((prev) => {
        const newSet = new Set(prev);
        newSet.delete(id);
        return newSet;
      });
    }
  };

  // 取消任务
  const cancelTask = async (id: string) => {
    try {
      await axiosInstance.post("/api/cancelTask", { taskId: id });
      toast.success("任务已取消");
    } catch {
      toast.error("任务取消失败");
    }
  };

  // 查看日志
  const goToLog = async (id: string) => {
    try {
      const logRes = await axiosInstance.get(`/api/taskLog/${id}`);
      const logText: string = logRes.data || "";
      if (logText.trim()) {
        navigate(`/log/${id}`);
      } else {
        toast.info("任务尚未执行，暂无日志");
      }
    } catch {
      toast.error("获取日志失败");
    }
  };

  // 清空目录
  const clearDirectory = async (targetPath: string) => {
    try {
      await axiosInstance.post("/api/directory/clear", { targetPath });
      toast.success(`目录 ${targetPath} 清空成功`);
    } catch (error: unknown) {
      const errorMessage = (error as { response?: { data?: { error?: string } } })?.response?.data?.error || "清空目录失败";
      toast.error(errorMessage);
    }
  };

  return {
    data,
    isLoading,
    accounts,
    accountsLoading,
    startingTasks,
    nowTs,
    isAccountBusy,
    isTaskDisabled,
    getTaskDisplayStatus,
    fetchTasks,
    fetchAccounts,
    deleteTask,
    startTask,
    cancelTask,
    goToLog,
    clearDirectory,
  };
}
