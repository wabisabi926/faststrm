// 操作工具栏：一键清理全部 / 补生成全部 / 组合操作。
// 二次确认统一走 ConfirmActionDialog（内部管理 open 状态），
// 不再需要三个独立 useState——此前正是共享/错配 open 状态导致弹窗叠加、确认失效。

import { Button } from "@/components/ui/button";
import { Trash2, Zap } from "lucide-react";
import { ConfirmActionDialog } from "./ConfirmActionDialog";

export interface CleanupToolbarProps {
  totalStale: number;
  totalMissing: number;
  executing: boolean;
  onDeleteAll: () => void;
  onRegenerate: () => void;
  /** 设置项「删除 STRM 本身」；false 时确认弹窗必须说明只清关联文件 */
  removeStrm?: boolean;
}

export function CleanupToolbar({
  totalStale,
  totalMissing,
  executing,
  onDeleteAll,
  onRegenerate,
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

        </div>
      </div>
    </div>
  );
}
