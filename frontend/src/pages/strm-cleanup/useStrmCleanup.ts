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
  type ReconcileItem,
  type ReconcileResponse,
  type ScanSummary,
  type StaleStrm,
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

  const handleScan = async () => {
    setScanning(true);
    setScanResult(null);
    setSelectedStale(new Set());
    try {
      const res = await axiosInstance.post("/api/strmCleanup/scan", {
        useSettingsDefaults: true,
      });
      const data = res.data as ScanSummary;
      const normalizedMappings: MappingResult[] = data.mappings.map(
        (raw: Omit<MappingResult, "mappingId">, idx: number) => ({
          mappingId: `mapping-${idx}`,
          account: raw.account,
          cloudPath: raw.cloudPath,
          localPath: raw.localPath,
          remoteFileCount: raw.remoteFileCount,
          localStrmCount: raw.localStrmCount,
          staleStrms: (raw.staleStrms || []).map((s: Omit<StaleStrm, "localPath" | "mappingId">) => ({
            ...s,
            localPath: raw.localPath,
            mappingId: `mapping-${idx}`,
          })),
          missingStrms: (raw.missingStrms || []).map((m: Omit<MissingStrm, "mappingId">) => ({
            ...m,
            mappingId: `mapping-${idx}`,
          })),
          error: raw.error,
        })
      );
      setScanResult({
        totalRemoteFiles: data.totalRemoteFiles ?? 0,
        totalLocalStrms: data.totalLocalStrms ?? 0,
        totalStale: data.totalStale ?? 0,
        totalMissing: data.totalMissing ?? 0,
        durationMs: data.durationMs ?? 0,
        mappings: normalizedMappings,
      });
      appendLog("扫描", `完成：${data.totalStale ?? 0} 失效 / ${data.totalMissing ?? 0} 漏生成`, true);
      toast.success(
        `扫描完成：发现 ${data.totalStale ?? 0} 个失效 STRM，${data.totalMissing ?? 0} 个漏生成`
      );
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
    try {
      const res = await axiosInstance.post("/api/strmCleanup/scan", {
        action: "reconcile",
        useSettingsDefaults: true,
      });
      const data = res.data as ReconcileResponse;
      const totalCloud = data.results?.reduce((s: number, r: ReconcileItem) => s + r.cloudFileCount, 0) || 0;
      const totalLocal = data.results?.reduce((s: number, r: ReconcileItem) => s + r.localStrmCount, 0) || 0;
      const totalDb = data.results?.reduce((s: number, r: ReconcileItem) => s + r.dbRecordCount, 0) || 0;
      const totalStale = data.results?.reduce((s: number, r: ReconcileItem) => s + r.staleStrms.length, 0) || 0;
      const totalMissing = data.results?.reduce((s: number, r: ReconcileItem) => s + r.missingStrms.length, 0) || 0;
      const durationMs = data.results?.reduce((s: number, r: ReconcileItem) => s + (r.durationMs || 0), 0) || 0;
      const mappings: MappingResult[] = (data.results || []).map((r, idx) => ({
        mappingId: `reconcile-${idx}`,
        account: r.account,
        cloudPath: r.cloudPath,
        localPath: r.localPath,
        remoteFileCount: r.cloudFileCount,
        localStrmCount: r.localStrmCount,
        staleStrms: (r.staleStrms || []).map((s: Omit<StaleStrm, "localPath" | "mappingId">) => ({
          ...s,
          localPath: r.localPath,
          mappingId: `reconcile-${idx}`,
        })),
        missingStrms: (r.missingStrms || []).map((m: Omit<MissingStrm, "mappingId">) => ({
          ...m,
          mappingId: `reconcile-${idx}`,
        })),
        error: r.error,
      }));
      setScanResult({
        totalRemoteFiles: totalCloud,
        totalLocalStrms: totalLocal,
        totalStale,
        totalMissing,
        durationMs,
        mappings,
      });

      appendLog("全量对账", `云端:${totalCloud} 本地:${totalLocal} DB:${totalDb} 失效:${totalStale} 缺失:${totalMissing}`, true);
      toast.success(`全量对账完成`, {
        description: `云端: ${totalCloud} | 本地STRM: ${totalLocal} | DB记录: ${totalDb} | 失效: ${totalStale} | 缺失: ${totalMissing}`,
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
    setScanResult((prev) => {
      if (!prev) return prev;
      const deletedKeys = new Set(selectedStale);
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        staleStrms: m.staleStrms.filter(
          (s) => !deletedKeys.has(staleKey(s.mappingId, s.relPath))
        ),
      }));
      const remainingStale = newMappings.reduce((s, m) => s + m.staleStrms.length, 0);
      return {
        ...prev,
        mappings: newMappings,
        totalStale: remainingStale,
        totalLocalStrms: prev.totalLocalStrms - r.deletedCount,
      };
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
    setScanResult((prev) => {
      if (!prev) return prev;
      const deletedKeys = new Set(selectedStale);
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        staleStrms: m.staleStrms.filter(
          (s) => !deletedKeys.has(staleKey(s.mappingId, s.relPath))
        ),
      }));
      const remainingStale = newMappings.reduce((s, m) => s + m.staleStrms.length, 0);
      return { ...prev, mappings: newMappings, totalStale: remainingStale };
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
  };
}
