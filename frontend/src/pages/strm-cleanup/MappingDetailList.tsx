// 路径映射扫描明细：展示每个映射的扫描结果。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

import { Badge } from "@/components/ui/badge";
import type { MappingResult } from "./types";

export interface MappingDetailListProps {
  mappings: MappingResult[];
}

export function MappingDetailList({ mappings }: MappingDetailListProps) {
  const hasAnyAssoc = mappings.some((m) => m.associatedFileCount !== undefined);
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium flex items-center gap-2">
        路径映射扫描明细
        {hasAnyAssoc && (
          <Badge variant="outline" className="text-[10px] text-sky-700 border-sky-300">
            P2：已含关联文件列
          </Badge>
        )}
      </div>
      <div className="space-y-2">
        {mappings.map((m) => (
          <div
            key={m.mappingId}
            className="p-3 rounded-md border text-sm space-y-2"
          >
            <div className="flex flex-wrap items-center gap-2 min-w-0">
              <Badge variant="secondary">{m.account}</Badge>
              <span className="font-mono text-xs break-all min-w-0">
                {m.cloudPath} → {m.localPath}
              </span>
              {m.error ? (
                <Badge variant="destructive">失败</Badge>
              ) : (
                <Badge>完成</Badge>
              )}
            </div>
            {/* 移动端默认 2 列，小屏(<=380) 强压 1 列；sm 以上按字段数铺。配合 break-all 保证长路径不撑破卡片 */}
            <div className={`grid gap-x-4 gap-y-1 text-xs text-muted-foreground grid-cols-2 max-[380px]:grid-cols-1 ${hasAnyAssoc ? "sm:grid-cols-6" : m.dbRecordCount !== undefined ? "sm:grid-cols-5" : "sm:grid-cols-4"}`}>
              <span className="break-all">网盘文件：{m.remoteFileCount}</span>
              {/* DB 列：有数据时展示（差值 = DB - 网盘），≥5 用琥珀色 Badge 高亮 */}
              {m.dbRecordCount !== undefined &&
                (() => {
                  const db = m.dbRecordCount;
                  const diff = db - m.remoteFileCount;
                  const abs = Math.abs(diff);
                  if (abs === 0) return <span className="text-emerald-600">DB：{db} ✓</span>;
                  if (abs >= 5) {
                    return (
                      <span className="break-all">
                        DB：
                        <Badge variant="outline" className="border-amber-400 text-amber-700 align-middle">
                          {db}（差{diff > 0 ? "+" : ""}{diff}）
                        </Badge>
                      </span>
                    );
                  }
                  return <span className="break-all">DB：{db}（差{diff > 0 ? "+" : ""}{diff}）</span>;
                })()}
              <span className="break-all">本地 STRM：{m.localStrmCount}</span>
              {/* P2：关联文件列，仅当至少有一个 mapping 提供 associatedFileCount 时才展示 */}
              {m.associatedFileCount !== undefined && (
                <span className="text-sky-700 break-all">关联：{m.associatedFileCount}</span>
              )}
              <span className="text-destructive break-all">失效：{m.staleStrms.length}</span>
              <span className="text-amber-600 break-all">漏生成：{m.missingStrms.length}</span>
            </div>
            {m.error && (
              <div className="text-xs text-destructive break-all">错误：{m.error}</div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

