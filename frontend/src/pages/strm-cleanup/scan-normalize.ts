import type { MappingResult, MissingStrm, ScanSummary, StaleStrm } from "./types";

export type RawScanMapping = Omit<MappingResult, "mappingId" | "staleStrms" | "missingStrms"> & {
  staleStrms?: Array<Omit<StaleStrm, "localPath" | "mappingId">>;
  missingStrms?: Array<Omit<MissingStrm, "mappingId">>;
  dbRecordCount?: number;
  associatedFileCount?: number;
  unmanagedCount?: number;
};

export type RawScanResponse = {
  mappings?: RawScanMapping[];
  scanId?: string;
  scanExpiresAt?: number;
  totalRemoteFiles?: number;
  totalLocalStrms?: number;
  totalAssociatedFiles?: number;
  totalUnmanaged?: number;
  totalStale?: number;
  totalMissing?: number;
  totalDbRecords?: number;
  durationMs?: number;
};

// 扫描/对账复用同一 normalize；后端已统一为 ScanResponse。
// 注意：scanId 必须原样保留，否则执行删除/补建时会被后端判定为扫描结果过期。
export function normalizeScanResponse(raw: RawScanResponse, idPrefix: string): ScanSummary {
  const normalizedMappings: MappingResult[] = (raw.mappings || []).map((m, idx) => ({
    mappingId: `${idPrefix}-${idx}`,
    account: m.account,
    cloudPath: m.cloudPath,
    localPath: m.localPath,
    remoteFileCount: Number(m.remoteFileCount ?? 0),
    localStrmCount: Number(m.localStrmCount ?? 0),
    associatedFileCount: m.associatedFileCount,
    dbRecordCount: m.dbRecordCount,
    unmanagedCount: m.unmanagedCount,
    staleStrms: (m.staleStrms || []).map((s) => {
      const content: string | undefined =
        (s as unknown as { content?: string }).content ??
        (s as unknown as { strmContent?: string }).strmContent;
      const truncated = (s as unknown as { truncated?: boolean }).truncated;
      const size = (s as unknown as { size?: number }).size;
      return {
        ...s,
        localPath: m.localPath,
        mappingId: `${idPrefix}-${idx}`,
        content,
        strmContent: content, // 兼容老 StaleStrmDialog（s.strmContent || "-"）
        truncated,
        size,
      } as StaleStrm;
    }),
    missingStrms: (m.missingStrms || []).map((mi) => ({
      ...mi,
      mappingId: `${idPrefix}-${idx}`,
    })),
    error: m.error,
    errorCode: m.errorCode,
  }));
  const totalRemote = raw.totalRemoteFiles ?? normalizedMappings.reduce((s, m) => s + m.remoteFileCount, 0);
  const totalLocal = raw.totalLocalStrms ?? normalizedMappings.reduce((s, m) => s + m.localStrmCount, 0);
  const totalAssoc =
    raw.totalAssociatedFiles ??
    normalizedMappings.reduce((s, m) => s + (m.associatedFileCount ?? 0), 0);
  const totalStale = raw.totalStale ?? normalizedMappings.reduce((s, m) => s + m.staleStrms.length, 0);
  const totalMissing = raw.totalMissing ?? normalizedMappings.reduce((s, m) => s + m.missingStrms.length, 0);
  const totalUnmanaged =
    raw.totalUnmanaged ?? normalizedMappings.reduce((s, m) => s + (m.unmanagedCount ?? 0), 0);
  return {
    scanId: raw.scanId,
    scanExpiresAt: raw.scanExpiresAt,
    totalRemoteFiles: totalRemote,
    totalLocalStrms: totalLocal,
    totalAssociatedFiles: totalAssoc > 0 ? totalAssoc : undefined,
    totalUnmanaged,
    totalStale,
    totalMissing,
    durationMs: raw.durationMs ?? 0,
    totalDbRecords: raw.totalDbRecords,
    mappings: normalizedMappings,
  };
}
