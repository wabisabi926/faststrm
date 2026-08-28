// 任务页面头部：标题 + 刷新状态 + 新建任务按钮。
// 从 task/index.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { Button } from "@/components/ui/button";
import { Plus, RefreshCw } from "lucide-react";
import { AddTaskDialog } from "./components/AddTaskDialog";
import type { AccountBrief } from "./useTasks";

export interface TaskPageHeaderProps {
  isLoading: boolean;
  accounts: AccountBrief[];
  accountsLoading: boolean;
  onRefresh: () => void;
  onSuccess: () => void;
}

export function TaskPageHeader({
  isLoading,
  accounts,
  accountsLoading,
  onRefresh,
  onSuccess,
}: TaskPageHeaderProps) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-xl sm:text-2xl font-semibold break-words">任务管理</h1>
        <p className="text-muted-foreground mt-1 text-sm break-words">管理和监控你的下载任务</p>
      </div>
      <div className="flex gap-2 flex-wrap">
        <Button
          variant="outline"
          size="sm"
          onClick={onRefresh}
          disabled={isLoading}
          className="w-full sm:w-auto"
        >
          <RefreshCw className={`w-4 h-4 mr-1 ${isLoading ? 'animate-spin' : ''}`} />
          刷新状态
        </Button>
        <AddTaskDialog
          onSuccess={onSuccess}
          accounts={accounts}
          accountsLoading={accountsLoading}
          trigger={
            <Button size="sm" className="w-full sm:w-auto">
              <Plus className="w-4 h-4 mr-1" />
              新建任务
            </Button>
          }
        />
      </div>
    </div>
  );
}
