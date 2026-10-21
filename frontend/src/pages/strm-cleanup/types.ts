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

// 回收站条目（清理时移入 .faststrm-trash，保留 7 天）
export type TrashEntry = {
  trashPath: string;
  originalPath: string;
  root: string;
  batch: string;
  size: number;
  modTime: number;
  isStrm: boolean;
};

export type TrashListResponse = {
  entries: TrashEntry[];
  total: number;
  roots: string[];
  truncated?: boolean;
};

export type StaleStrm = {
  relPath: string;
  fullPath?: string;
  /** 失效原因：not_in_cloud=云端已无对应文件；account_missing=STRM 引用的账户已不存在（改名/删除残留）；unmanaged=未托管 */
  reason?: "not_in_cloud" | "account_missing" | "unmanaged";
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
  // 云端不存在但未纳入托管的 STRM（手写/第三方）。默认不参与删除。
  unmanagedStrms?: StaleStrm[];
  unmanagedCount?: number;
  missingStrms: MissingStrm[];
  error?: string;
  /** 结构化扫描错误码：account_not_found（账号不存在/无 Cookie）/ cloud_dir_failed / cloud_walk_failed */
  errorCode?: string;
  // v1.2.5+：后端每次扫描都查 SQLite 并填此字段；无 sqlite 时缺省 undefined
  dbRecordCount?: number;
};

export type ScanSummary = {
  // 服务端扫描快照 ID（执行删除/补建必须回传）
  scanId?: string;
  scanExpiresAt?: number;
  totalRemoteFiles: number;
  totalLocalStrms: number;
  // P2：全部 mappings 关联文件总数（展示在顶部统计卡"关联媒体"，有值才显示 6 列布局）
  totalAssociatedFiles?: number;
  totalStale: number;
  // 云端不存在但未纳入托管的 STRM 总数（默认不删除）
  totalUnmanaged?: number;
  totalMissing: number;
  durationMs: number;
  mappings: MappingResult[];
  // v1.2.5+：后端聚合 totalDbRecords，未开 sqlite 时旧后端不返回该字段 → 前端据此不展示 DB 卡片
  totalDbRecords?: number;
};

export type ExecuteResult = {
  deletedCount: number;
  failedCount: number;
  // 因未托管/指纹变化而跳过、未删除的文件数
  skippedUnmanaged?: number;
  // 删前云端复核发现文件已恢复而保留的数量
  skippedCloudRestored?: number;
  // 因缺少云端路径/复核失败而保留的数量
  skippedCloudUnverified?: number;
  // 是否已转入待确认批次（此时 deletedCount 为 0）
  pending?: boolean;
  pendingRequestId?: string;
  // 保护阈值/入队失败原因；非空表示本次未执行删除
  thresholdError?: string;
  appliedMaxThreshold?: number;
  appliedStableThreshold?: number;
  appliedConfirmMode?: string;
  totalStaleCount?: number;
  errors: Array<{ path: string; error: string }>;
  removedEmptyDirs: string[];
  // 删除 STRM 时一并移入回收站的关联文件（.nfo/.jpg/.srt 等）
  removedRelatedFiles?: string[];
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


