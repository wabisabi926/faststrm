// 桌面端任务列表 DataTable 列定义：装配入口。
// 拆分自原 TaskColumns.tsx（528 行 → 此文件仅负责列装配）。
// 单元格渲染逻辑分别见 StatusCell / ClearDirectoryCell / ScheduleCell / ActionsCell。
// 详见 v1.1.1 改进任务清单 T5。

import type { ColumnDef } from "@tanstack/react-table";
import type { MutableRefObject } from "react";
import { Badge } from "@/components/ui/badge";
import { type Task, ACCOUNT_STYLES } from "./types";
import type { AccountBrief, TaskDisplayStatus } from "./useTasks";
import { StatusCell } from "./StatusCell";
import { ClearDirectoryCell } from "./ClearDirectoryCell";
import { ScheduleCell } from "./ScheduleCell";
import { ActionsCell } from "./ActionsCell";

export interface TaskColumnsCallbacks {
  startingTasks: Set<string>;
  nowTsRef: MutableRefObject<number>;
  accounts: AccountBrief[];
  accountsLoading: boolean;
  isAccountBusy: (accountName: string) => boolean;
  isTaskDisabled: (task: Task) => boolean;
  getTaskDisplayStatus: (task: Task) => TaskDisplayStatus;
  startTask: (id: string) => void;
  cancelTask: (id: string) => void;
  goToLog: (id: string) => void;
  fetchTasks: () => void;
  clearDirectory: (targetPath: string) => void;
  onDeleteTask: (task: Task) => void;
}

export function buildTaskColumns(callbacks: TaskColumnsCallbacks): ColumnDef<Task>[] {
  const {
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
    onDeleteTask,
  } = callbacks;

  return [
    {
      accessorKey: "id",
      header: "任务ID",
      cell: ({ row }) => (
        <code className="text-xs bg-muted px-2 py-1 rounded">
          {row.original.id.slice(0, 8)}...
        </code>
      )
    },
    {
      accessorKey: "account",
      header: "账户",
      cell: ({ row }) => {
        const task = row.original;
        const isBusy = isAccountBusy(task.account);

        return (
          <div className="flex items-center gap-2">
            <Badge
              variant="outline"
              className={`text-xs ${
                isBusy ? ACCOUNT_STYLES.busy : ACCOUNT_STYLES.normal
              }`}
            >
              {task.accountType}
            </Badge>
            <span className={`font-medium ${
              isBusy ? "text-orange-700" : ""
            }`}>
              {task.account}
              {isBusy && (
                <span className="ml-1 text-xs text-orange-600">●</span>
              )}
            </span>
          </div>
        );
      }
    },
    {
      accessorKey: "originPath",
      header: "远程路径",
      cell: ({ row }) => {
        const task = row.original;
        return (
          <span className="text-sm text-muted-foreground max-w-xs truncate block">
            {task.originPath}
          </span>
        );
      }
    },
    {
      accessorKey: "targetPath",
      header: "本地路径",
      cell: ({ row }) => {
        const task = row.original;
        return (
          <ClearDirectoryCell
            task={task}
            onClear={clearDirectory}
          />
        );
      }
    },
    {
      accessorKey: "status",
      header: "状态 / 进度",
      cell: ({ row }) => {
        const task = row.original;
        return (
          <StatusCell
            task={task}
            nowTs={nowTsRef.current}
            getTaskDisplayStatus={getTaskDisplayStatus}
          />
        );
      }
    },
    {
      id: "schedule",
      header: "定时",
      cell: ({ row }) => {
        const task = row.original;
        return <ScheduleCell task={task} />;
      },
    },
    {
      id: "actions",
      header: "操作",
      cell: ({ row }) => {
        const task = row.original;
        return (
          <ActionsCell
            task={task}
            startingTasks={startingTasks}
            accounts={accounts}
            accountsLoading={accountsLoading}
            isTaskDisabled={isTaskDisabled}
            isAccountBusy={isAccountBusy}
            startTask={startTask}
            cancelTask={cancelTask}
            goToLog={goToLog}
            fetchTasks={fetchTasks}
            onDeleteTask={onDeleteTask}
          />
        );
      },
    },
  ];
}
