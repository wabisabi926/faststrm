// 本地路径单元格：路径 + 清空目录按钮（内嵌确认 Dialog）。
// 从 TaskColumns.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { FolderX } from "lucide-react";
import type { Task } from "./types";

export interface ClearDirectoryCellProps {
  task: Task;
  onClear: (targetPath: string) => void;
}

export function ClearDirectoryCell({
  task,
  onClear,
}: ClearDirectoryCellProps) {
  return (
    <div className="group flex items-center gap-2 max-w-xs">
      <span className="text-sm text-muted-foreground truncate flex-1">
        {task.targetPath}
      </span>
      <Dialog>
        <DialogTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0 text-muted-foreground hover:text-red-500 hover:bg-red-500/10 opacity-0 group-hover:opacity-100 transition-all duration-200 flex-shrink-0"
            title="清空目录"
          >
            <FolderX className="w-4 h-4" />
          </Button>
        </DialogTrigger>
        <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
          <DialogHeader>
            <DialogTitle>确认清空目录</DialogTitle>
            <DialogDescription>
              你确定要清空目标路径下的所有文件吗？此操作无法撤销。
              <br />
              <span className="text-sm text-gray-500 mt-2 block">
                目标路径: {task.targetPath}
              </span>
              <br />
              <span className="text-sm text-red-600 mt-2 block font-medium">
                ⚠️ 这将删除该目录下的所有文件和子目录！
              </span>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline">
              取消
            </Button>
            <Button
              variant="destructive"
              onClick={() => onClear(task.targetPath)}
            >
              确认清空
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
