// STRM 清理功能类型定义。
// 从 StrmCleanupCard.tsx 抽出，便于子模块共享。
// 详见 v1.1.1 改进任务清单 T3。

export type StaleStrm = {
  relPath: string;
  fullPath?: string;
  strmContent?: string;
  localPath: string;
  mappingId: string;
};

export type MissingStrm = {
  relPath: string;
  mediaExtension: string;
  mappingId: string;
};

export type MappingResult = {
  mappingId: string;
  account: string;
  cloudPath: string;
  localPath: string;
  remoteFileCount: number;
  localStrmCount: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  error?: string;
};

export type ScanSummary = {
  totalRemoteFiles: number;
  totalLocalStrms: number;
  totalStale: number;
  totalMissing: number;
  durationMs: number;
  mappings: MappingResult[];
};

export type ExecuteResult = {
  deletedCount: number;
  failedCount: number;
  errors: Array<{ path: string; error: string }>;
  removedEmptyDirs: string[];
  dryRun: boolean;
  durationMs: number;
  regeneratedCount?: number;
  deletedAllCount?: number;
  cleanupSummary?: {
    deleted: number;
    regenerated: number;
    failed: number;
  };
};

export type LogEntry = {
  time: string;
  action: string;
  detail: string;
  success: boolean;
};

export type ReconcileItem = {
  account: string;
  cloudPath: string;
  localPath: string;
  cloudFileCount: number;
  localStrmCount: number;
  dbRecordCount: number;
  durationMs?: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  error?: string;
};

export type ReconcileResponse = {
  results?: ReconcileItem[];
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
