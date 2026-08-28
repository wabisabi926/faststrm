// 操作工具栏：一键清理全部 / 补生成全部 / 组合操作。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

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
import { Button } from "@/components/ui/button";
import { RefreshCw, Trash2, Zap } from "lucide-react";

export interface CleanupToolbarProps {
  totalStale: number;
  totalMissing: number;
  executing: boolean;
  confirmOpen: boolean;
  setConfirmOpen: (open: boolean) => void;
  regenAllConfirmOpen: boolean;
  setRegenAllConfirmOpen: (open: boolean) => void;
  onDeleteAll: () => void;
  onRegenerate: () => void;
  onDeleteAndRegenerate: () => void;
}

export function CleanupToolbar({
  totalStale,
  totalMissing,
  executing,
  confirmOpen,
  setConfirmOpen,
  regenAllConfirmOpen,
  setRegenAllConfirmOpen,
  onDeleteAll,
  onRegenerate,
  onDeleteAndRegenerate,
}: CleanupToolbarProps) {
  return (
    <div className="rounded-md border p-3 space-y-2">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:flex-wrap">
        <span className="text-sm font-medium shrink-0">一键操作：</span>
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
          <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
            <AlertDialogTrigger asChild>
              <Button
                variant="destructive"
                size="sm"
                disabled={totalStale === 0 || executing}
                className="w-full sm:w-auto"
              >
                <Trash2 className="mr-2 size-4" />
                {executing ? "删除中..." : `清理全部失效 (${totalStale})`}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  一键清理全部 {totalStale} 个失效 STRM？
                </AlertDialogTitle>
                <AlertDialogDescription>
                  将删除扫描发现的全部失效 STRM 文件，并自动清理空父目录。此操作不可撤销。
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction onClick={onDeleteAll}>
                  确认清理
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>

          <AlertDialog open={regenAllConfirmOpen} onOpenChange={setRegenAllConfirmOpen}>
            <AlertDialogTrigger asChild>
              <Button
                size="sm"
                disabled={totalMissing === 0 || executing}
                className="w-full sm:w-auto"
              >
                <Zap className="mr-2 size-4" />
                {executing ? "生成中..." : `补生成全部漏项 (${totalMissing})`}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  确认补生成 {totalMissing} 个缺失 STRM？
                </AlertDialogTitle>
                <AlertDialogDescription>
                  将为所有漏生成的文件创建 STRM 文件。
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction onClick={onRegenerate}>
                  确认生成
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>

          {(totalStale > 0 || totalMissing > 0) && (
            <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
              <AlertDialogTrigger asChild>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={executing}
                  className="w-full sm:w-auto"
                >
                  <RefreshCw className="mr-2 size-4" />
                  {executing ? "执行中..." : "清理失效 + 补生成漏项"}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    确认执行清理+补生成组合操作？
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    将删除 {totalStale} 个失效 STRM 并补生成 {totalMissing} 个漏项。
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>取消</AlertDialogCancel>
                  <AlertDialogAction onClick={onDeleteAndRegenerate}>
                    确认执行
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        提示：先清理失效 STRM 避免冲突，再补生成缺失项。建议执行完后重新扫描验证。
      </p>
    </div>
  );
}
