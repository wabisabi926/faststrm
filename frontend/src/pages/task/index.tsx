// 任务管理主组件：组合入口，编排子模块。
// 拆分自原 task/index.tsx（910 行 → 此文件仅负责 layout 与状态装配）。
// 详见 v1.1.1 改进任务清单 T5。

import { useMemo, useState } from "react";
import { AlertCircle } from "lucide-react";
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

  // 桌面端 DataTable 列定义（useMemo 避免每次渲染重建）
  const columns = useMemo(
    () =>
      buildTaskColumns({
        startingTasks,
        nowTs,
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
      }),
    // 依赖项中的函数引用由 useTasks 返回，稳定；nowTs 每秒变化，会让 columns 重建一次（可接受）
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nowTs, startingTasks, accounts, accountsLoading]
  );

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
    <div className="mx-auto max-w-7xl space-y-6">
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

      {/* 任务列表：移动端卡片 / 桌面端 DataTable */}
      {data.length === 0 ? (
        <div className="text-center py-12 bg-muted/30 rounded-lg border border-border">
          <AlertCircle className="mx-auto h-12 w-12 text-muted-foreground" />
          <h3 className="mt-4 text-lg font-medium">暂无任务</h3>
          <p className="mt-2 text-muted-foreground">点击上方按钮创建你的第一个任务</p>
        </div>
      ) : isMobile ? (
        <div className="space-y-2.5">
          {data.map((task) => (
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
        <DataTable columns={columns} data={data} />
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
