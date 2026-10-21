// 操作工具栏：一键清理全部 / 补生成全部 / 组合操作。
// 二次确认统一走 ConfirmActionDialog（内部管理 open 状态），
// 不再需要三个独立 useState——此前正是共享/错配 open 状态导致弹窗叠加、确认失效。

import { Button } from "@/components/ui/button";
import { RefreshCw, Trash2, Zap } from "lucide-react";
import { ConfirmActionDialog } from "./ConfirmActionDialog";

export interface CleanupToolbarProps {
  totalStale: number;
  totalMissing: number;
  executing: boolean;
  onDeleteAll: () => void;
  onRegenerate: () => void;
  onDeleteAndRegenerate: () => void;
  /** 已勾选的失效 STRM 数量（组合按钮仅删除选中项，非全部） */
  selectedStaleCount: number;
  /** 设置项「删除 STRM 本身」；false 时确认弹窗必须说明只清关联文件 */
  removeStrm?: boolean;
}

export function CleanupToolbar({
  totalStale,
  totalMissing,
  executing,
  onDeleteAll,
  onRegenerate,
  onDeleteAndRegenerate,
  selectedStaleCount,
  removeStrm = true,
}: CleanupToolbarProps) {
  return (
    <div className="rounded-md border p-3 space-y-2">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:flex-wrap">
        <span className="text-sm font-medium shrink-0">一键操作：</span>
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
          <ConfirmActionDialog
            destructive
            title={`一键清理全部 ${totalStale} 个失效 STRM？`}
            description={
              removeStrm
                ? "将把扫描发现的全部失效 STRM 移入各映射根下的 .faststrm-trash（可在回收站恢复）；是否清理空父目录取决于设置。"
                : "⚠️ 设置里「删除 STRM 本身」已关闭：本次只清理同名 nfo/海报/字幕，STRM 文件会保留，失效项仍会出现在列表里。"
            }
            confirmLabel="确认清理"
            onConfirm={onDeleteAll}
            trigger={
              <Button
                variant="destructive"
                size="sm"
                disabled={totalStale === 0 || executing}
                className="w-full sm:w-auto"
              >
                <Trash2 className="mr-2 size-4" />
                {executing ? "删除中..." : `清理全部失效 (${totalStale})`}
              </Button>
            }
          />

          <ConfirmActionDialog
            title={`确认补生成 ${totalMissing} 个缺失 STRM？`}
            description="将为所有漏生成的文件创建 STRM 文件（已按扩展名/黑名单/BDMV 规则过滤）。"
            confirmLabel="确认生成"
            onConfirm={onRegenerate}
            trigger={
              <Button size="sm" disabled={totalMissing === 0 || executing} className="w-full sm:w-auto">
                <Zap className="mr-2 size-4" />
                {executing ? "生成中..." : `补生成全部漏项 (${totalMissing})`}
              </Button>
            }
          />

          {(totalStale > 0 || totalMissing > 0) && (
            <ConfirmActionDialog
              title="确认执行清理+补生成组合操作？"
              description={
                <>
                  {selectedStaleCount > 0 ? (
                    <p>
                      将删除您已勾选的 <strong className="text-destructive">{selectedStaleCount}</strong> 个失效
                      STRM，并补生成 {totalMissing} 个漏项。
                    </p>
                  ) : (
                    <p className="text-amber-600">
                      ⚠️ 当前未勾选任何失效 STRM，此操作将只执行补生成 {totalMissing} 个漏项（不会删除文件）。
                    </p>
                  )}
                  <p className="text-xs text-muted-foreground">
                    注意：「清理全部失效」会删除全部 {totalStale} 个失效，而「组合操作」仅删除您勾选的条目。
                  </p>
                </>
              }
              confirmLabel="确认执行"
              onConfirm={onDeleteAndRegenerate}
              trigger={
                <Button size="sm" disabled={executing} className="w-full sm:w-auto">
                  <RefreshCw className="mr-2 size-4" />
                  {executing ? "执行中..." : "清理失效 + 补生成漏项"}
                </Button>
              }
            />
          )}
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        组合操作只删除已勾选项；「清理全部失效」会删除所有失效项。
      </p>
    </div>
  );
}
