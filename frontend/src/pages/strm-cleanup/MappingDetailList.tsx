// 路径映射扫描明细：展示每个映射的扫描结果。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

import { Badge } from "@/components/ui/badge";
import type { MappingResult } from "./types";

export interface MappingDetailListProps {
  mappings: MappingResult[];
}

export function MappingDetailList({ mappings }: MappingDetailListProps) {
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium">路径映射扫描明细</div>
      <div className="space-y-2">
        {mappings.map((m) => (
          <div
            key={m.mappingId}
            className="p-3 rounded-md border text-sm space-y-1"
          >
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="secondary">{m.account}</Badge>
              <span className="font-mono text-xs">
                {m.cloudPath} → {m.localPath}
              </span>
              {m.error ? (
                <Badge variant="destructive">失败</Badge>
              ) : (
                <Badge>完成</Badge>
              )}
            </div>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-x-4 gap-y-1 text-xs text-muted-foreground">
              <span>网盘文件：{m.remoteFileCount}</span>
              <span>本地 STRM：{m.localStrmCount}</span>
              <span className="text-destructive">失效：{m.staleStrms.length}</span>
              <span className="text-amber-600">漏生成：{m.missingStrms.length}</span>
            </div>
            {m.error && (
              <div className="text-xs text-destructive">错误：{m.error}</div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
