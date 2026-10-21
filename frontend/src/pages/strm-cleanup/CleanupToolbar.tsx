// 操作工具栏：一键清理全部 / 补生成全部 / 组合操作。
// 从 StrmCleanupCard.tsx 抽出。
// 修复：三个 AlertDialog 必须各自维护独立的 open 状态，
// 否则同时 open 会引起多个 AlertDialogContent 在 body 中叠加渲染、
// 事件互相干扰，导致「清理全部失效」按钮点击后确认动作失效。

import { useState } from "react";
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
  onDeleteAll: () => void;
  onRegenerate: () => void;
  onDeleteAndRegenerate: () => void;
  /** 已勾选的失效 STRM 数量（组合按钮仅删除选中项，非全部） */
  selectedStaleCount: number;
}

export function CleanupToolbar({
  totalStale,
  totalMissing,
  executing,
  onDeleteAll,
  onRegenerate,
  onDeleteAndRegenerate,
  selectedStaleCount,
}: CleanupToolbarProps) {
  // 三个对话框各自独立状态，避免共享 open 导致弹窗叠加冲突
  const [deleteAllConfirmOpen, setDeleteAllConfirmOpen] = useState(false);
  const [regenAllConfirmOpen, setRegenAllConfirmOpen] = useState(false);
  const [combinedConfirmOpen, setCombinedConfirmOpen] = useState(false);

  return (
    <div className="rounded-md border p-3 space-y-2">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:flex-wrap">
        <span className="text-sm font-medium shrink-0">一键操作：</span>
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
          <AlertDialog
            open={deleteAllConfirmOpen}
            onOpenChange={setDeleteAllConfirmOpen}
          >
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
                  将把扫描发现的全部失效 STRM 移入各映射根下的 .faststrm-trash（可在回收站恢复）；是否清理空父目录取决于设置。
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() => {
                    onDeleteAll();
                    setDeleteAllConfirmOpen(false);
                  }}
                >
                  确认清理
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>

          <AlertDialog
            open={regenAllConfirmOpen}
            onOpenChange={setRegenAllConfirmOpen}
          >
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
                <AlertDialogAction
                  onClick={() => {
                    onRegenerate();
                    setRegenAllConfirmOpen(false);
                  }}
                >
                  确认生成
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>

          {(totalStale > 0 || totalMissing > 0) && (
            <AlertDialog
              open={combinedConfirmOpen}
              onOpenChange={setCombinedConfirmOpen}
            >
              <AlertDialogTrigger asChild>
                <Button
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
                  <AlertDialogDescription className="space-y-1" asChild>
                    <div>
                      {selectedStaleCount > 0 ? (
                        <p>
                          将删除您已勾选的 <strong className="text-destructive">{selectedStaleCount}</strong> 个失效 STRM，并补生成 {totalMissing} 个漏项。
                        </p>
                      ) : (
                        <p className="text-amber-600">
                          ⚠️ 当前未勾选任何失效 STRM，此操作将只执行补生成 {totalMissing} 个漏项（不会删除文件）。
                        </p>
                      )}
                      <p className="text-xs text-muted-foreground">
                        注意：「清理全部失效」按钮会删除全部 {totalStale} 个失效，而「组合操作」仅删除您勾选的条目。
                      </p>
                    </div>
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>取消</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={() => {
                      onDeleteAndRegenerate();
                      setCombinedConfirmOpen(false);
                    }}
                  >
                    确认执行
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        组合操作只删除已勾选项；「清理全部失效」会删除所有失效项。
      </p>
    </div>
  );
}
