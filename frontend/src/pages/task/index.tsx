// 任务管理主组件：组合入口，编排子模块。
// 拆分自原 task/index.tsx（910 行 → 此文件仅负责 layout 与状态装配）。
// 详见 v1.1.1 改进任务清单 T5。

import { useEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, Play, Search, Square, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";
import { DataTable } from "@/components/data-table";
import { useIsMobile } from "@/hooks/use-mobile";
import { MobileTaskCard } from "./MobileTaskCard";
import { useTasks } from "./useTasks";
import { type Task } from "./types";
import { buildTaskColumns } from "./TaskColumns";
import { TaskPageHeader } from "./TaskPageHeader";
import { DeleteTaskDialog } from "./DeleteTaskDialog";

export default function Home() {
  const isMobile = useIsMobile();
  const [deleteDialogOpen, setDeleteDialogOpen] = useState<string | null>(null);
  const [deleteCleanStrm, setDeleteCleanStrm] = useState(false);
  const [search, setSearch] = useState("");
  const [accountFilter, setAccountFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const {
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
    deleteTask,
    startTask,
    cancelTask,
    goToLog,
    clearDirectory,
  } = useTasks();

  // nowTs 每秒变化，用 ref 包装避免触发 columns 重建导致表格重渲染、Dialog 状态丢失
  const nowTsRef = useRef(nowTs);
  nowTsRef.current = nowTs;

  // 桌面端 DataTable 列定义（useMemo 避免每次渲染重建）
  // nowTs 变化不触发 columns 重建（通过 ref 传递），避免表格重挂载 → Dialog 闪退
  const columns = useMemo(
    () =>
      buildTaskColumns({
        startingTasks,
        nowTsRef,
        accounts,
        accountsLoading,
        isAccountBusy,
        isTaskDisabled,
        getTaskDisplayStatus,
        startTask,
        cancelTask,
        goToLog,
        fetchTasks,
        clearDirectory,
        onDeleteTask: (task: Task) => setDeleteDialogOpen(task.id),
        selectedIds,
        onToggleSelect: (id: string) =>
          setSelectedIds((prev) => {
            const next = new Set(prev);
            if (next.has(id)) next.delete(id);
            else next.add(id);
            return next;
          }),
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [startingTasks, accounts, accountsLoading, selectedIds]
  );

  const filteredTasks = useMemo(() => {
    const query = search.trim().toLowerCase();
    return data.filter((task) => {
      if (accountFilter !== "all" && task.account !== accountFilter) return false;
      if (statusFilter !== "all" && task.status !== statusFilter) return false;
      if (!query) return true;
      return [task.id, task.name, task.account, task.originPath, task.targetPath]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(query));
    });
  }, [data, search, accountFilter, statusFilter]);

  const selectedTasks = useMemo(
    () => data.filter((task) => selectedIds.has(task.id)),
    [data, selectedIds]
  );

  // 筛选条件变化后隐藏的已选任务不能被无意批量操作，直接清空选择
  useEffect(() => {
    setSelectedIds(new Set());
  }, [search, accountFilter, statusFilter]);

  const clearSelection = () => setSelectedIds(new Set());

  const handleBulkStart = async () => {
    const candidates = selectedTasks.filter(
      (task) => task.status !== "processing" && !isAccountBusy(task.account)
    );
    if (candidates.length === 0) {
      toast.info("没有可启动的任务（运行中或账号忙）");
      return;
    }
    let ok = 0;
    let failed = 0;
    for (const task of candidates) {
      try {
        const res = await axiosInstance.post("/api/startTask", { taskId: task.id });
        if (res.data?.success === false) failed += 1;
        else ok += 1;
      } catch {
        failed += 1;
      }
    }
    clearSelection();
    toast.success(`已提交 ${ok} 个任务${failed > 0 ? `，${failed} 个失败` : ""}`);
    await fetchTasks();
  };

  const handleBulkCancel = async () => {
    if (selectedTasks.length === 0) return;
    let ok = 0;
    let failed = 0;
    for (const task of selectedTasks) {
      try {
        await axiosInstance.post("/api/cancelTask", { taskId: task.id });
        ok += 1;
      } catch {
        failed += 1;
      }
    }
    clearSelection();
    toast.success(`已提交取消 ${ok} 个任务${failed > 0 ? `，${failed} 个失败` : ""}`);
    await fetchTasks();
  };

  const handleBulkDelete = async () => {
    if (selectedTasks.length === 0) return;
    if (!window.confirm(`确定删除选中的 ${selectedTasks.length} 个任务？不会清理 STRM 文件。`)) {
      return;
    }
    let ok = 0;
    let failed = 0;
    for (const task of selectedTasks) {
      try {
        await axiosInstance.delete(`/api/task?id=${encodeURIComponent(task.id)}`);
        ok += 1;
      } catch {
        failed += 1;
      }
    }
    clearSelection();
    toast.success(`已删除 ${ok} 个任务${failed > 0 ? `，${failed} 个失败` : ""}`);
    await fetchTasks();
  };

  // 加载中骨架
  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  // 当前选中删除的任务对象
  const deleteTarget = deleteDialogOpen
    ? data.find((t) => t.id === deleteDialogOpen) || null
    : null;

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      {/* 动画 keyframes（供 TaskColumns 的骨架条使用） */}
      <style>{`
        @keyframes skeleton-shimmer {
          0%   { background-position: -200% 0; }
          100% { background-position:  200% 0; }
        }
        @keyframes scanning-sweep {
          0%   { left: -20%; width: 15%; }
          50%  { left:  60%; width: 25%; }
          100% { left: 110%; width: 15%; }
        }
      `}</style>

      {/* 页面头部：标题 + 刷新 + 新建任务 */}
      <TaskPageHeader
        isLoading={isLoading}
        accounts={accounts}
        accountsLoading={accountsLoading}
        onRefresh={fetchTasks}
        onSuccess={fetchTasks}
      />

      {data.length > 0 && (
        <div className="flex flex-col gap-2 rounded-md border bg-card p-2.5 sm:flex-row sm:items-center">
          <div className="relative flex-1 min-w-[180px]">
            <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索任务 ID、名称、账号或路径"
              className="pl-8 pr-8"
              aria-label="搜索任务"
            />
            {search && (
              <button
                type="button"
                onClick={() => setSearch("")}
                className="absolute right-2 top-2.5 rounded-sm text-muted-foreground hover:text-foreground"
                aria-label="清除搜索"
              >
                <X className="h-4 w-4" />
              </button>
            )}
          </div>
          <Select value={accountFilter} onValueChange={setAccountFilter}>
            <SelectTrigger className="w-full sm:w-[150px]">
              <SelectValue placeholder="全部账号" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部账号</SelectItem>
              {accounts.map((acc) => (
                <SelectItem key={acc.name} value={acc.name}>
                  {acc.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={statusFilter} onValueChange={setStatusFilter}>
            <SelectTrigger className="w-full sm:w-[135px]">
              <SelectValue placeholder="全部状态" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              <SelectItem value="processing">运行中</SelectItem>
              <SelectItem value="pending">等待</SelectItem>
              <SelectItem value="success">成功</SelectItem>
              <SelectItem value="failed">失败</SelectItem>
              <SelectItem value="cancelled">已取消</SelectItem>
            </SelectContent>
          </Select>
          <span className="shrink-0 text-xs text-muted-foreground">
            {filteredTasks.length} / {data.length}
          </span>
        </div>
      )}

      {selectedIds.size > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-sm">
          <span className="font-medium">已选 {selectedIds.size} 个任务</span>
          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            <Button size="sm" variant="outline" onClick={() => void handleBulkStart()}>
              <Play className="mr-1 h-3.5 w-3.5" />
              启动
            </Button>
            <Button size="sm" variant="outline" onClick={() => void handleBulkCancel()}>
              <Square className="mr-1 h-3.5 w-3.5" />
              取消
            </Button>
            <Button size="sm" variant="destructive" onClick={() => void handleBulkDelete()}>
              <Trash2 className="mr-1 h-3.5 w-3.5" />
              删除
            </Button>
            <Button size="sm" variant="ghost" onClick={clearSelection} aria-label="清除选择">
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}

      {/* 任务列表：移动端卡片 / 桌面端 DataTable */}
      {data.length === 0 ? (
        <div className="text-center py-12 bg-muted/30 rounded-lg border border-border">
          <AlertCircle className="mx-auto h-12 w-12 text-muted-foreground" />
          <h3 className="mt-4 text-base font-medium">暂无任务</h3>
          <p className="mt-2 text-muted-foreground">点击上方按钮创建你的第一个任务</p>
        </div>
      ) : filteredTasks.length === 0 ? (
        <div className="text-center py-12 bg-muted/30 rounded-lg border border-border">
          <Search className="mx-auto h-10 w-10 text-muted-foreground" />
          <h3 className="mt-4 text-base font-medium">没有匹配的任务</h3>
          <p className="mt-2 text-sm text-muted-foreground">调整搜索词或筛选条件后再试。</p>
          <Button
            variant="outline"
            size="sm"
            className="mt-4"
            onClick={() => {
              setSearch("");
              setAccountFilter("all");
              setStatusFilter("all");
            }}
          >
            清除筛选
          </Button>
        </div>
      ) : isMobile ? (
        <div className="space-y-2.5">
          {filteredTasks.map((task) => (
            <MobileTaskCard
              key={task.id}
              task={task}
              nowTs={nowTs}
              startingTasks={startingTasks}
              accounts={accounts}
              accountsLoading={accountsLoading}
              isAccountBusy={isAccountBusy}
              isTaskDisabled={isTaskDisabled}
              getTaskDisplayStatus={getTaskDisplayStatus}
              startTask={startTask}
              cancelTask={cancelTask}
              goToLog={goToLog}
              fetchTasks={fetchTasks}
              setDeleteDialogOpen={setDeleteDialogOpen}
            />
          ))}
        </div>
      ) : (
        <div className="[&_table]:text-xs">
          <DataTable columns={columns} data={filteredTasks} align="center" />
        </div>
      )}

      {/* 删除任务对话框（移动端 + 桌面端共用） */}
      <DeleteTaskDialog
        open={!!deleteDialogOpen}
        task={deleteTarget}
        cleanStrm={deleteCleanStrm}
        onCleanStrmChange={setDeleteCleanStrm}
        onCancel={() => {
          setDeleteDialogOpen(null);
          setDeleteCleanStrm(false);
        }}
        onConfirm={() => {
          if (deleteDialogOpen) {
            void deleteTask(deleteDialogOpen, deleteCleanStrm);
            setDeleteDialogOpen(null);
            setDeleteCleanStrm(false);
          }
        }}
      />
    </div>
  );
}
