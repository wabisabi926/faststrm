// 删除任务确认对话框：移动端 + 全局共享版本。
// 从 task/index.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import type { Task } from "./types";

export interface DeleteTaskDialogProps {
  open: boolean;
  task: Task | null;
  cleanStrm: boolean;
  onCleanStrmChange: (checked: boolean) => void;
  onCancel: () => void;
  onConfirm: () => void;
}

export function DeleteTaskDialog({
  open,
  task,
  cleanStrm,
  onCleanStrmChange,
  onCancel,
  onConfirm,
}: DeleteTaskDialogProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>确认删除</DialogTitle>
          <DialogDescription>
            你确定要删除这个任务吗？此操作无法撤销。
            <br />
            {task && (
              <>
                <span className="text-sm text-gray-500 mt-2 block">
                  任务ID: {task.id.slice(0, 8)}...
                </span>
                <span className="text-sm text-gray-500 mt-1 block">
                  目标路径: {task.targetPath}
                </span>
              </>
            )}
          </DialogDescription>
        </DialogHeader>
        {task && (
          <div className="flex items-center gap-2 py-2">
            <Checkbox
              id={`clean-strm-${task.id}`}
              checked={cleanStrm}
              onCheckedChange={(checked) => onCleanStrmChange(checked === true)}
            />
            <label htmlFor={`clean-strm-${task.id}`} className="text-sm cursor-pointer">
              同时清理 STRM 目录（{task.targetPath}）
            </label>
          </div>
        )}
        {cleanStrm && (
          <p className="text-xs text-red-600 font-medium">
            ⚠️ 将删除该目录下所有 STRM 文件和子目录！
          </p>
        )}
        <DialogFooter className="gap-2">
          <Button
            variant="outline"
            onClick={() => {
              onCleanStrmChange(false);
              onCancel();
            }}
          >
            取消
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            删除
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
