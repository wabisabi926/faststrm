// 操作单元格：开始 / 取消 / 日志 / 定时 / 编辑 / 删除。
// 从 TaskColumns.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { Button } from "@/components/ui/button";
import {
  Play,
  Square,
  FileText,
  Edit,
  Trash2,
  Loader2,
  Clock,
} from "lucide-react";
import { AddTaskDialog } from "./components/AddTaskDialog";
import { TaskScheduleDialog } from "./components/TaskScheduleDialog";
import {
  type Task,
  BUTTON_STYLES,
  STATUS_LABELS,
} from "./types";
import type { AccountBrief } from "./useTasks";

export interface ActionsCellProps {
  task: Task;
  startingTasks: Set<string>;
  accounts: AccountBrief[];
  accountsLoading: boolean;
  isTaskDisabled: (task: Task) => boolean;
  isAccountBusy: (accountName: string) => boolean;
  startTask: (id: string) => void;
  cancelTask: (id: string) => void;
  goToLog: (id: string) => void;
  fetchTasks: () => void;
  onDeleteTask: (task: Task) => void;
}

export function ActionsCell({
  task,
  startingTasks,
  accounts,
  accountsLoading,
  isTaskDisabled,
  isAccountBusy,
  startTask,
  cancelTask,
  goToLog,
  fetchTasks,
  onDeleteTask,
}: ActionsCellProps) {
  const isStarting = startingTasks.has(task.id);
  const isDisabled = isTaskDisabled(task);

  return (
    <div className="flex gap-1">
      <Button
        variant="ghost"
        size="sm"
        onClick={() => startTask(task.id)}
        disabled={isDisabled}
        className={`h-8 w-8 p-0 ${
          isDisabled
            ? BUTTON_STYLES.disabled
            : task.status === "processing"
              ? "bg-blue-500/20 hover:bg-blue-500/30"
              : BUTTON_STYLES.enabled
        }`}
        title={
          isStarting ? `${STATUS_LABELS.starting}...` :
          task.status === "processing" ? "任务运行中" :
          isAccountBusy(task.account) ? `账户 ${task.account} 有任务正在运行` :
          "开始任务"
        }
      >
        {isStarting ? (
          <Loader2 className={`w-4 h-4 animate-spin ${BUTTON_STYLES.loading}`} />
        ) : task.status === "processing" ? (
          <div className="w-4 h-4 flex items-center justify-center">
            <div className="w-2 h-2 bg-blue-600 rounded-full animate-pulse"></div>
          </div>
        ) : (
          <Play className={`w-4 h-4 ${
            isDisabled ? BUTTON_STYLES.icon.disabled : BUTTON_STYLES.icon.enabled
          }`} />
        )}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => cancelTask(task.id)}
        className="h-8 w-8 p-0"
        title="取消任务"
      >
        <Square className="w-4 h-4" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => goToLog(task.id)}
        className="h-8 w-8 p-0"
        title="查看日志"
      >
        <FileText className="w-4 h-4" />
      </Button>
      <TaskScheduleDialog
        task={task}
        onSuccess={fetchTasks}
        trigger={
          <Button
            variant="ghost"
            size="sm"
            className={`h-8 w-8 p-0 ${
              task.schedule?.enabled
                ? "text-indigo-600 hover:text-indigo-700 hover:bg-indigo-50"
                : ""
            }`}
            title={
              task.schedule?.enabled
                ? "定时任务已启用，点击编辑"
                : "配置定时任务"
            }
          >
            <Clock className="w-4 h-4" />
          </Button>
        }
      />
      <AddTaskDialog
        task={task}
        accounts={accounts}
        accountsLoading={accountsLoading}
        trigger={
          <Button
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0"
            title="编辑任务"
          >
            <Edit className="w-4 h-4" />
          </Button>
        }
        onSuccess={fetchTasks}
      />
      <Button
        variant="ghost"
        size="sm"
        onClick={() => onDeleteTask(task)}
        className="h-8 w-8 p-0 text-red-500 hover:text-red-600 hover:bg-red-500/10"
        title="删除任务"
      >
        <Trash2 className="w-4 h-4" />
      </Button>
    </div>
  );
}
