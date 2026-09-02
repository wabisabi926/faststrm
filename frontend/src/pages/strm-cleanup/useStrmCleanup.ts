// STRM 清理业务逻辑 hook：抽离所有 API 调用与状态机。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

import * as React from "react";
import axiosInstance from "@/lib/axios";
import { toast } from "sonner";
import {
  type AxiosError,
  type ExecuteResult,
  type LogEntry,
  type MappingResult,
  type MissingStrm,
  type ScanSummary,
  type StaleStrm,
  type StrmPreviewResponse,
  staleKey,
} from "./types";
import {
  buildEntriesFromSelection,
  selectAllStale,
  toggleStaleInSet,
} from "./selection";

export interface UseStrmCleanupResult {
  // 状态
  scanning: boolean;
  scanResult: ScanSummary | null;
  selectedStale: Set<string>;
  executing: boolean;
  reconcileLoading: boolean;
  logs: LogEntry[];
  allStale: StaleStrm[];
  allMissing: MissingStrm[];
  // 选中态相关
  toggleAllStale: (checked: boolean) => void;
  toggleStale: (key: string, checked: boolean) => void;
  // 操作
  handleScan: () => Promise<void>;
  handleReconcile: () => Promise<void>;
  handleExecuteDelete: () => Promise<void>;
  handleDeleteAll: () => Promise<void>;
  handleDeleteAndRegenerate: () => Promise<void>;
  handleRegenerate: () => Promise<void>;
  // 日志
  appendLog: (action: string, detail: string, success: boolean) => void;
  clearLogs: () => void;
  // P3：STRM 内容预览（StaleStrmDialog "查看完整"）
  previewStrm: (p: { localPath: string; relPath: string; maxBytes?: number }) => Promise<StrmPreviewResponse>;
  // P3：扫描缓存 1 分钟窗口 — 当前是否命中（Toolbar 展示 Badge）
  cacheActive: boolean;
  cacheWindowSec: number;
}

export function useStrmCleanup(
  // 对话框开关由组件层管理，hook 通过回调通知关闭
  callbacks: {
    setStaleDialogOpen: (open: boolean) => void;
  }
): UseStrmCleanupResult {
  const [scanning, setScanning] = React.useState(false);
  const [scanResult, setScanResult] = React.useState<ScanSummary | null>(null);
  const [selectedStale, setSelectedStale] = React.useState<Set<string>>(new Set());
  const [executing, setExecuting] = React.useState(false);
  const [reconcileLoading, setReconcileLoading] = React.useState(false);
  const [logs, setLogs] = React.useState<LogEntry[]>([]);

  const allStale = React.useMemo(() => {
    if (!scanResult) return [] as StaleStrm[];
    return scanResult.mappings.flatMap((m) => m.staleStrms);
  }, [scanResult]);

  const allMissing = React.useMemo(() => {
    if (!scanResult) return [] as MissingStrm[];
    return scanResult.mappings.flatMap((m) => m.missingStrms);
  }, [scanResult]);

  const appendLog = (action: string, detail: string, success: boolean) => {
    setLogs((prev) => [
      { time: new Date().toLocaleTimeString(), action, detail, success },
      ...prev.slice(0, 49),
    ]);
  };

  const clearLogs = () => setLogs([]);

  const toggleAllStale = (checked: boolean) => {
    setSelectedStale(selectAllStale(allStale, checked));
  };

  const toggleStale = (key: string, checked: boolean) => {
    setSelectedStale((prev) => toggleStaleInSet(prev, key, checked));
  };

  // v1.2.5：扫描/对账复用同一 normalize；后端已统一为 ScanResponse
  type ScanMode = "scan" | "reconcile";
  const normalizeScanResponse = (
    raw: {
      mappings: Array<
        Omit<MappingResult, "mappingId" | "staleStrms" | "missingStrms"> & {
          staleStrms?: Array<Omit<StaleStrm, "localPath" | "mappingId">>;
          missingStrms?: Array<Omit<MissingStrm, "mappingId">>;
          dbRecordCount?: number;
          associatedFileCount?: number;
        }
      >;
      totalRemoteFiles?: number;
      totalLocalStrms?: number;
      totalAssociatedFiles?: number;
      totalStale?: number;
      totalMissing?: number;
      totalDbRecords?: number;
      durationMs?: number;
    },
    idPrefix: string
  ): ScanSummary => {
    const normalizedMappings: MappingResult[] = (raw.mappings || []).map((m, idx) => ({
      mappingId: `${idPrefix}-${idx}`,
      account: m.account,
      cloudPath: m.cloudPath,
      localPath: m.localPath,
      remoteFileCount: Number(m.remoteFileCount ?? 0),
      localStrmCount: Number(m.localStrmCount ?? 0),
      associatedFileCount: m.associatedFileCount,
      dbRecordCount: m.dbRecordCount,
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
    }));
    const totalRemote = raw.totalRemoteFiles ?? normalizedMappings.reduce((s, m) => s + m.remoteFileCount, 0);
    const totalLocal = raw.totalLocalStrms ?? normalizedMappings.reduce((s, m) => s + m.localStrmCount, 0);
    const totalAssoc =
      raw.totalAssociatedFiles ??
      normalizedMappings.reduce((s, m) => s + (m.associatedFileCount ?? 0), 0);
    const totalStale = raw.totalStale ?? normalizedMappings.reduce((s, m) => s + m.staleStrms.length, 0);
    const totalMissing = raw.totalMissing ?? normalizedMappings.reduce((s, m) => s + m.missingStrms.length, 0);
    return {
      totalRemoteFiles: totalRemote,
      totalLocalStrms: totalLocal,
      totalAssociatedFiles: totalAssoc > 0 ? totalAssoc : undefined,
      totalStale,
      totalMissing,
      durationMs: raw.durationMs ?? 0,
      totalDbRecords: raw.totalDbRecords,
      mappings: normalizedMappings,
    };
  };
  // P2：用后端 refreshedMappingStats 覆盖 mappings[i] 的 localStrmCount / associatedFileCount，再重新计算聚合计数
  // 避免"基于 deletedCount / regeneratedCount 的增量估算"造成累计漂移（删除相关文件、补生成同目录重叠文件等特殊场景会产生误差）
  const applyRefreshedStats = (r: ExecuteResult, prev: ScanSummary): ScanSummary => {
    if (!r.refreshedMappingStats || r.refreshedMappingStats.length === 0) return prev;
    const byPath = new Map(r.refreshedMappingStats.filter((s) => s.localPath).map((s) => [s.localPath, s]));
    if (byPath.size === 0) return prev;
    let recomputeAssoc = false;
    const newMappings = prev.mappings.map((m) => {
      const hit = byPath.get(m.localPath);
      if (!hit) return m;
      if (hit.associatedFileCount !== undefined) recomputeAssoc = true;
      return {
        ...m,
        localStrmCount: hit.localStrmCount,
        associatedFileCount:
          hit.associatedFileCount !== undefined ? hit.associatedFileCount : m.associatedFileCount,
      };
    });
    const newTotalLocal = newMappings.reduce((s, m) => s + m.localStrmCount, 0);
    const baseAssoc = newMappings.reduce((s, m) => s + (m.associatedFileCount ?? 0), 0);
    return {
      ...prev,
      mappings: newMappings,
      totalLocalStrms: newTotalLocal,
      totalAssociatedFiles: recomputeAssoc ? (baseAssoc > 0 ? baseAssoc : undefined) : prev.totalAssociatedFiles,
    };
  };
  // P3：上次成功扫描的时间戳（scan 和 reconcile 共享），1 分钟内再扫 → 自动 useCache=true, cacheTTLMs=60000
  const lastScanAtRef = React.useRef<{ ts: number; mode: ScanMode } | null>(null);
  const postScanInternal = async (mode: ScanMode) => {
    const body: {
      useSettingsDefaults: boolean;
      action?: string;
      useCache?: boolean;
      cacheTTLMs?: number;
    } = { useSettingsDefaults: true };
    if (mode === "reconcile") body.action = "reconcile";
    const now = Date.now();
    const last = lastScanAtRef.current;
    const P3_CACHE_WINDOW = 60 * 1000; // 1 分钟
    if (last && now - last.ts < P3_CACHE_WINDOW) {
      body.useCache = true;
      body.cacheTTLMs = P3_CACHE_WINDOW;
    }
    const res = await axiosInstance.post("/api/strmCleanup/scan", body);
    lastScanAtRef.current = { ts: now, mode };
    const normalized = normalizeScanResponse(res.data, mode === "reconcile" ? "reconcile" : "mapping");
    // 是否命中缓存：后端 mapping[*].error 有 fallback 到 network scan 文案的情况未命中；这里传 header 判断
    (normalized as unknown as { __fromCacheHint?: boolean }).__fromCacheHint = Boolean(body.useCache);
    return normalized;
  };

  // P3：preview 指定 .strm 文件（供 StaleStrmDialog "查看完整"按钮）
  const previewStrm = async (p: { localPath: string; relPath: string; maxBytes?: number }): Promise<StrmPreviewResponse> => {
    const res = await axiosInstance.post("/api/strmCleanup/preview", {
      localPath: p.localPath,
      relPath: p.relPath,
      maxBytes: p.maxBytes,
    });
    return res.data as StrmPreviewResponse;
  };

  const handleScan = async () => {
    setScanning(true);
    setScanResult(null);
    setSelectedStale(new Set());
    try {
      const data = await postScanInternal("scan");
      setScanResult(data);
      appendLog("扫描", `完成：${data.totalStale} 失效 / ${data.totalMissing} 漏生成`, true);
      toast.success(`扫描完成：发现 ${data.totalStale} 个失效 STRM，${data.totalMissing} 个漏生成`);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "扫描失败";
      appendLog("扫描", msg, false);
      toast.error(msg);
      console.error(err);
    } finally {
      setScanning(false);
    }
  };

  const handleReconcile = async () => {
    setReconcileLoading(true);
    setScanResult(null);
    setSelectedStale(new Set());
    try {
      const data = await postScanInternal("reconcile");
      setScanResult(data);
      const totalDb = data.totalDbRecords ?? "-";
      const dbDiffs: string[] = [];
      for (const m of data.mappings) {
        const db = m.dbRecordCount;
        if (db === undefined) continue;
        const diff = m.remoteFileCount - db;
        if (Math.abs(diff) >= 5) {
          dbDiffs.push(`${m.cloudPath}: 云${m.remoteFileCount} vs DB${db} (差${diff})`);
        }
      }
      appendLog(
        "全量对账",
        `云端:${data.totalRemoteFiles} 本地:${data.totalLocalStrms} DB:${totalDb} 失效:${data.totalStale} 缺失:${data.totalMissing}`,
        true
      );
      const diffTip =
        dbDiffs.length > 0 ? `\n⚠️ 差异较大的映射：\n${dbDiffs.slice(0, 3).join("\n")}` : "";
      toast.success(`全量对账完成`, {
        description:
          `云端: ${data.totalRemoteFiles} | 本地STRM: ${data.totalLocalStrms} | DB记录: ${totalDb} | 失效: ${data.totalStale} | 缺失: ${data.totalMissing}` +
          diffTip,
        duration: 10000,
      });
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "对账失败";
      appendLog("全量对账", msg, false);
      toast.error("对账失败", { description: msg });
      console.error(err);
    } finally {
      setReconcileLoading(false);
    }
  };

  const handleDeleteResult = (r: ExecuteResult) => {
    appendLog("删除", `删除 ${r.deletedCount} 个失效 STRM，失败 ${r.failedCount} 个`, r.failedCount === 0);
    toast.success(
      `已删除 ${r.deletedCount} 个失效 STRM，清理了 ${r.removedEmptyDirs.length} 个空目录` +
        (r.failedCount > 0 ? `，失败 ${r.failedCount} 个` : "")
    );
    const deletedKeys = new Set(selectedStale);
    setScanResult((prev) => {
      if (!prev) return prev;
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        staleStrms: m.staleStrms.filter(
          (s) => !deletedKeys.has(staleKey(s.mappingId, s.relPath))
        ),
      }));
      const remainingStale = newMappings.reduce((s, m) => s + m.staleStrms.length, 0);
      const next = {
        ...prev,
        mappings: newMappings,
        totalStale: remainingStale,
        totalLocalStrms: prev.totalLocalStrms - r.deletedCount,
      };
      return applyRefreshedStats(r, next);
    });
    setSelectedStale(new Set());
    callbacks.setStaleDialogOpen(false);
  };

  const handleRegenerateResult = (r: ExecuteResult) => {
    appendLog("补生成", `生成 ${r.regeneratedCount || 0} 个 STRM，失败 ${r.failedCount} 个`, r.failedCount === 0);
    toast.success(
      `已补生成 ${r.regeneratedCount || 0} 个 STRM` +
        (r.failedCount > 0 ? `，失败 ${r.failedCount} 个` : "")
    );
    // 从 scanResult.mappings[].missingStrms 中移除已成功生成的项，避免重复点击覆盖
    const regenSet = new Set(r.regeneratedPaths || []);
    const hasStats = r.refreshedMappingStats && r.refreshedMappingStats.length > 0;
    if (regenSet.size === 0 && !hasStats) return;
    setScanResult((prev) => {
      if (!prev) return prev;
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        missingStrms: m.missingStrms.filter((mi) => !regenSet.has(mi.relPath)),
      }));
      const remainingMissing = newMappings.reduce((s, m) => s + m.missingStrms.length, 0);
      const next = { ...prev, mappings: newMappings, totalMissing: remainingMissing };
      return applyRefreshedStats(r, next);
    });
  };

  const handleCombinedResult = (r: ExecuteResult) => {
    const summary = r.cleanupSummary;
    const parts: string[] = [];
    if (summary) {
      parts.push(`清理 ${summary.deleted}`);
      parts.push(`补生成 ${summary.regenerated}`);
      if (summary.failed > 0) parts.push(`失败 ${summary.failed}`);
    }
    appendLog("清理+补生成", parts.join(" / "), (summary?.failed || 0) === 0);
    toast.success(`组合操作完成：${parts.join("，")}`);
    // 同步移除：已删失效 STRM + 已生成漏项 STRM
    const regenSet = new Set(r.regeneratedPaths || []);
    const deletedKeys = new Set(selectedStale);
    setScanResult((prev) => {
      if (!prev) return prev;
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        staleStrms: m.staleStrms.filter(
          (s) => !deletedKeys.has(staleKey(s.mappingId, s.relPath))
        ),
        missingStrms: m.missingStrms.filter((mi) => !regenSet.has(mi.relPath)),
      }));
      const remainingStale = newMappings.reduce((s, m) => s + m.staleStrms.length, 0);
      const remainingMissing = newMappings.reduce((s, m) => s + m.missingStrms.length, 0);
      const next = { ...prev, mappings: newMappings, totalStale: remainingStale, totalMissing: remainingMissing };
      return applyRefreshedStats(r, next);
    });
    setSelectedStale(new Set());
    callbacks.setStaleDialogOpen(false);
  };

  const handleExecuteDelete = async () => {
    setExecuting(true);
    try {
      const entries = buildEntriesFromSelection(selectedStale, allStale);
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries,
        action: "delete",
        dryRun: false,
      });
      handleDeleteResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "删除失败";
      appendLog("删除", msg, false);
      toast.error(msg);
      console.error(err);
    } finally {
      setExecuting(false);
    }
  };

  const handleDeleteAll = async () => {
    setExecuting(true);
    try {
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries: [],
        action: "delete_all",
        scanSummary: { mappings: scanResult!.mappings },
        dryRun: false,
      });
      handleDeleteResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "批量删除失败";
      appendLog("批量删除", msg, false);
      toast.error(msg);
    } finally {
      setExecuting(false);
    }
  };

  const handleDeleteAndRegenerate = async () => {
    setExecuting(true);
    try {
      const entries = buildEntriesFromSelection(selectedStale, allStale);
      const missingItems = allMissing.map((m) => ({
        localPath: scanResult!.mappings.find((x) => x.mappingId === m.mappingId)?.localPath || "",
        relPath: m.relPath,
        mappingId: m.mappingId,
      }));
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries,
        action: "delete_and_regenerate",
        scanSummary: { mappings: scanResult!.mappings },
        missingItems,
        dryRun: false,
      });
      handleCombinedResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "组合操作失败";
      appendLog("清理+补生成", msg, false);
      toast.error(msg);
    } finally {
      setExecuting(false);
    }
  };

  const handleRegenerate = async () => {
    setExecuting(true);
    try {
      const missingItems = allMissing.map((m) => ({
        localPath: scanResult!.mappings.find((x) => x.mappingId === m.mappingId)?.localPath || "",
        relPath: m.relPath,
        mappingId: m.mappingId,
      }));
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries: [],
        action: "regenerate",
        scanSummary: { mappings: scanResult!.mappings },
        missingItems,
        dryRun: false,
      });
      handleRegenerateResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "补生成失败";
      appendLog("补生成", msg, false);
      toast.error(msg);
    } finally {
      setExecuting(false);
    }
  };

  return {
    scanning,
    scanResult,
    selectedStale,
    executing,
    reconcileLoading,
    logs,
    allStale,
    allMissing,
    toggleAllStale,
    toggleStale,
    handleScan,
    handleReconcile,
    handleExecuteDelete,
    handleDeleteAll,
    handleDeleteAndRegenerate,
    handleRegenerate,
    appendLog,
    clearLogs,
    previewStrm,
    cacheWindowSec: 60,
    cacheActive: Boolean(
      lastScanAtRef.current && Date.now() - lastScanAtRef.current.ts < 60000
    ),
  };
}



