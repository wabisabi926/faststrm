// 清空目录确认对话框：移动端 + 全局共享版本。
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

export interface ClearDirectoryDialogProps {
  open: boolean;
  targetPath: string;
  onCancel: () => void;
  onConfirm: () => void;
}

export function ClearDirectoryDialog({
  open,
  targetPath,
  onCancel,
  onConfirm,
}: ClearDirectoryDialogProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>确认清空目录</DialogTitle>
          <DialogDescription>
            你确定要清空目标路径下的所有文件吗？此操作无法撤销。
            <br />
            <span className="text-sm text-gray-500 mt-2 block">
              目标路径: {targetPath}
            </span>
            <br />
            <span className="text-sm text-red-600 mt-2 block font-medium">
              ⚠️ 这将删除该目录下的所有文件和子目录！
            </span>
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onCancel}>
            取消
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            确认清空
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
