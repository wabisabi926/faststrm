// 失效 STRM 选中态纯函数：toggleAllStale/toggleStale/buildEntriesFromSelection。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

import type { StaleStrm } from "./types";
import { staleKey, parseStaleKey } from "./types";

export interface DeleteEntry {
  localPath: string;
  staleRelPaths: string[];
}

// 全选 / 全不选
export function selectAllStale(
  allStale: StaleStrm[],
  checked: boolean
): Set<string> {
  if (!checked) return new Set();
  return new Set(allStale.map((s) => staleKey(s.mappingId, s.relPath)));
}

// 切换单个条目（返回新 Set，保持不可变）
export function toggleStaleInSet(
  prev: Set<string>,
  key: string,
  checked: boolean
): Set<string> {
  const next = new Set(prev);
  if (checked) next.add(key);
  else next.delete(key);
  return next;
}

// 把选中态按 localPath 聚合为后端 execute API 所需 entries 结构
export function buildEntriesFromSelection(
  selectedStale: Set<string>,
  allStale: StaleStrm[]
): DeleteEntry[] {
  const entriesByLocal = new Map<string, DeleteEntry>();
  for (const key of selectedStale) {
    const { mappingId, relPath } = parseStaleKey(key);
    const stale = allStale.find(
      (s) => s.mappingId === mappingId && s.relPath === relPath
    );
    if (!stale) continue;
    if (!entriesByLocal.has(stale.localPath)) {
      entriesByLocal.set(stale.localPath, {
        localPath: stale.localPath,
        staleRelPaths: [],
      });
    }
    entriesByLocal.get(stale.localPath)!.staleRelPaths.push(relPath);
  }
  return [...entriesByLocal.values()];
}
