import * as fs from "fs";
import * as path from "path";
import { readSettings } from "./serverUtils";
import { clearMonitorSuspend } from "./accountRuntimeState";
import {
  getFilePathEntryByPath,
} from "./filePathDb";
import { removeEmptyParents } from "./strmFileOps";
import { generateStrmContent, getStrmFileName, resolveStrmSettings } from "./strmUtils";
import type { MappingScanRequest, MappingScanResult } from "./strmScan";

export interface ExecuteRequest {
  entries: Array<{
    localPath: string;
    staleRelPaths: string[];
  }>;
  dryRun?: boolean;
  action?: "delete" | "delete_all" | "regenerate" | "delete_and_regenerate";
  missingItems?: Array<{
    localPath: string;
    relPath: string;
    mappingId: string;
  }>;
  scanSummary?: {
    mappings: MappingScanResult[];
  };
}

export interface ExecuteResult {
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
}

export function runExecute(req: ExecuteRequest): ExecuteResult {
  const started = Date.now();
  const dryRun = !!req.dryRun;
  const errors: ExecuteResult["errors"] = [];
  const removedEmptyDirs: string[] = [];
  let deletedCount = 0;
  let regeneratedCount = 0;

  const action = req.action || "delete";

  const involvedAccounts = new Set<string>();
  if (req.scanSummary) {
    for (const m of req.scanSummary.mappings) {
      if (m.account) involvedAccounts.add(m.account);
    }
  }

  try {
    let effectiveEntries = req.entries;
    if (action === "delete_all" && req.scanSummary) {
      const collected = new Map<string, string[]>();
      for (const m of req.scanSummary.mappings) {
        for (const s of m.staleStrms) {
          const localPath = m.localPath;
          if (!collected.has(localPath)) collected.set(localPath, []);
          collected.get(localPath)!.push(s.relPath);
        }
      }
      effectiveEntries = [...collected.entries()].map(([localPath, staleRelPaths]) => ({
        localPath,
        staleRelPaths,
      }));
    }

    if (action === "delete" || action === "delete_all" || action === "delete_and_regenerate") {
      for (const entry of effectiveEntries) {
        const saveDir = path.resolve(entry.localPath);
        if (!dryRun) {
          const rootDirs = new Set([saveDir]);
          for (const relPath of entry.staleRelPaths) {
            const fullPath = path.join(saveDir, relPath);
            try {
              if (fs.existsSync(fullPath)) {
                const stat = fs.statSync(fullPath);
                if (stat.isFile()) {
                  fs.unlinkSync(fullPath);
                  deletedCount++;
                  const dirs = removeEmptyParents(fullPath, { rootDirs, tag: "strmCleanup" });
                  removedEmptyDirs.push(...dirs);
                }
              }
            } catch (err: unknown) {
              errors.push({
                path: relPath,
                error: err instanceof Error ? err.message : String(err),
              });
            }
          }
        } else {
          deletedCount += entry.staleRelPaths.length;
        }
      }
    }

    if (action === "regenerate" || action === "delete_and_regenerate") {
      if (req.missingItems && req.missingItems.length > 0) {
        const settings = readSettings();
        for (const item of req.missingItems) {
          try {
            const localDir = path.resolve(item.localPath);
            const fileName = path.basename(item.relPath);
            const strmName = getStrmFileName(fileName);
            const strmDir = path.resolve(localDir, path.dirname(item.relPath));
            const strmPath = path.join(strmDir, strmName);

            if (!dryRun) {
              let account = "";
              let cloudBase = "";
              if (req.scanSummary) {
                const mapping = req.scanSummary.mappings.find(
                  (m) => m.localPath === item.localPath
                );
                if (mapping) {
                  cloudBase = mapping.cloudPath;
                  account = mapping.account;
                }
              }
              const resolvedStrm = resolveStrmSettings(account, null, settings);
              const cloudPath = cloudBase ? `${cloudBase}/${item.relPath}` : item.relPath;

              let pickcode: string | undefined;
              if (resolvedStrm.enable302 && account) {
                try {
                  const entry = getFilePathEntryByPath(account, cloudPath);
                  if (entry?.pickCode) pickcode = entry.pickCode;
                } catch {}
              }

              const content = generateStrmContent(
                cloudPath,
                resolvedStrm.strmPrefix,
                resolvedStrm.enablePathEncoding,
                {
                  enable302: resolvedStrm.enable302,
                  account,
                  pickcode,
                  fileName: path.basename(item.relPath),
                }
              );
              if (!content) {
                errors.push({
                  path: item.relPath,
                  error: `pickcode 缺失，无法生成 302 STRM`,
                });
                continue;
              }
              if (!fs.existsSync(strmDir)) {
                fs.mkdirSync(strmDir, { recursive: true });
              }
              fs.writeFileSync(strmPath, content, "utf-8");
            }
            regeneratedCount++;
          } catch (err: unknown) {
            errors.push({
              path: item.relPath,
              error: `STRM 补生成失败: ${err instanceof Error ? err.message : String(err)}`,
            });
          }
        }
      }
    }
  } finally {
    for (const account of involvedAccounts) {
      clearMonitorSuspend(account);
    }
  }

  const result: ExecuteResult = {
    deletedCount,
    failedCount: errors.length,
    errors,
    removedEmptyDirs,
    dryRun,
    durationMs: Date.now() - started,
    regeneratedCount: regeneratedCount || undefined,
    deletedAllCount: action === "delete_all" ? deletedCount : undefined,
  };

  if (action === "delete_and_regenerate") {
    result.cleanupSummary = {
      deleted: deletedCount,
      regenerated: regeneratedCount,
      failed: errors.length,
    };
  }

  return result;
}

export function resolveDataDir(localPath: string): string {
  return path.resolve(localPath);
}

export function getDefaultScanRequestsFromSettings(): MappingScanRequest[] {
  const settings = readSettings();
  const lifeMonitor =
    (settings.lifeMonitor as { accounts?: string[]; pathMappings?: Array<{ account?: string; cloudPath: string; localPath: string }> } | undefined);
  if (!lifeMonitor?.pathMappings || lifeMonitor.pathMappings.length === 0) return [];
  if (!lifeMonitor.accounts || lifeMonitor.accounts.length === 0) return [];

  const requests: MappingScanRequest[] = [];
  for (const account of lifeMonitor.accounts) {
    for (const pm of lifeMonitor.pathMappings) {
      if (pm.account && pm.account !== account) continue;
      requests.push({
        account,
        cloudPath: pm.cloudPath,
        localPath: pm.localPath,
      });
    }
  }
  return requests;
}