// STRM 清理功能类型定义。
// 从 StrmCleanupCard.tsx 抽出，便于子模块共享。
// 详见 v1.1.1 改进任务清单 T3。

export type StrmPreviewResponse = {
  exists: boolean;
  size?: number;
  content?: string;
  truncated?: boolean;
  error?: string;
};

export type StaleStrm = {
  relPath: string;
  fullPath?: string;
  /** P3：后端扫描阶段预读 512B；空时保持兼容老字段名 strmContent（同样有值）。truncated=true 时可点"查看完整"调 /preview */
  content?: string;
  /** 兼容字段：与 content 同值，保留给 StaleStrmDialog 现有渲染（s.strmContent || "-"）*/
  strmContent?: string;
  truncated?: boolean;
  size?: number;
  localPath: string;
  mappingId: string;
};

export type MissingStrm = {
  relPath: string;
  mediaExtension: string;
  mappingId: string;
};

// P2：轻量本地 re-scan 返回的 mapping 粒度权威计数（前端用它覆盖增量估算）
export type MappingLocalStats = {
  localPath: string;
  localStrmCount: number;
  associatedFileCount?: number;
};

export type MappingResult = {
  mappingId: string;
  account: string;
  cloudPath: string;
  localPath: string;
  remoteFileCount: number;
  localStrmCount: number;
  // P2：本地关联文件数（.nfo/.jpg/.png/.srt/.sub/.ass/.vtt），与 STRM 分开统计，语义纯净
  associatedFileCount?: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  error?: string;
  // v1.2.5+：后端每次扫描都查 SQLite 并填此字段；无 sqlite 时缺省 undefined
  dbRecordCount?: number;
};

export type ScanSummary = {
  totalRemoteFiles: number;
  totalLocalStrms: number;
  // P2：全部 mappings 关联文件总数（展示在顶部统计卡"关联媒体"，有值才显示 6 列布局）
  totalAssociatedFiles?: number;
  totalStale: number;
  totalMissing: number;
  durationMs: number;
  mappings: MappingResult[];
  // v1.2.5+：后端聚合 totalDbRecords，未开 sqlite 时旧后端不返回该字段 → 前端据此不展示 DB 卡片
  totalDbRecords?: number;
};

export type ExecuteResult = {
  deletedCount: number;
  failedCount: number;
  errors: Array<{ path: string; error: string }>;
  removedEmptyDirs: string[];
  dryRun: boolean;
  durationMs: number;
  regeneratedCount?: number;
  // 已成功生成的 STRM 相对路径列表
  // 前端用于从 scanResult.mappings[].missingStrms 中移除，避免重复点击覆盖生成
  regeneratedPaths?: string[];
  deletedAllCount?: number;
  cleanupSummary?: {
    deleted: number;
    regenerated: number;
    failed: number;
  };
  // P2：execute 结束后，后端对每个 mapping 的本地 Walk 刷新值
  // 前端优先用这里的权威值覆盖"基于 deletedCount/regeneratedCount 的增量估算"，避免累计漂移
  refreshedMappingStats?: MappingLocalStats[];
};

export type LogEntry = {
  time: string;
  action: string;
  detail: string;
  success: boolean;
};

export type AxiosError = {
  response?: { data?: { error?: string } };
  message?: string;
};

// 失效 STRM 选中态的 key 生成与解析
export function staleKey(mappingId: string, relPath: string): string {
  return mappingId + "::" + relPath;
}

export function parseStaleKey(key: string): { mappingId: string; relPath: string } {
  const [mappingId, relPath] = key.split(/::([\s\S]*)/);
  return { mappingId, relPath };
}


