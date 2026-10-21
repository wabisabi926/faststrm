// 统一的二次确认弹窗：清理模块里 5 处 AlertDialog 共用这一份。
//
// 为什么独立管理 open 状态：此前每个调用方各写一个 useState，
// 且必须小心避免多个 AlertDialog 同时 open（会叠加渲染、事件互相干扰，
// 曾导致「清理全部失效」确认后动作失效）。把状态收进组件内部，
// 调用方只给 trigger 与 onConfirm，不可能再写错。
//
// 文案统一提到「移入 .faststrm-trash，可在回收站恢复」，避免各弹窗口径不一。

import { useState, type ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface ConfirmActionDialogProps {
  /** 触发按钮（通常是 <Button>） */
  trigger: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** 确认按钮用危险色（删除类操作） */
  destructive?: boolean;
  /** 弹窗关闭后执行的动作 */
  onConfirm: () => void;
}

export function ConfirmActionDialog({
  trigger,
  title,
  description,
  confirmLabel = "确认",
  cancelLabel = "取消",
  destructive = false,
  onConfirm,
}: ConfirmActionDialogProps) {
  const [open, setOpen] = useState(false);
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description !== undefined && (
            <AlertDialogDescription asChild>
              <div className="space-y-1">{description}</div>
            </AlertDialogDescription>
          )}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction
            className={cn(destructive && buttonVariants({ variant: "destructive" }))}
            onClick={() => {
              setOpen(false);
              onConfirm();
            }}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
