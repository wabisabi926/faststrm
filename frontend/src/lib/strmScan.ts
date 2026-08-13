import * as fs from "fs";
import * as path from "path";
import {
  AccountInfo,
  exportDirParse,
  fs_dir_getid,
} from "./115";
import {
  buildTree,
  collectFilesAndTopEmptyDirs,
  getLocalTree,
  getStrmExtensions,
  readAccounts,
  readSettings,
} from "./serverUtils";
import { suspendMonitorForFullScan, clearMonitorSuspend } from "./accountRuntimeState";
import {
  FilePathEntry,
  getEntriesByPathPrefix,
  removeGhostRecords,
  upsertFilePathEntryBatch,
} from "./filePathDb";
import { resolveStrmSettings } from "./strmUtils";
import { waitFor115ApiToken } from "./rateLimiter";

export interface MappingScanRequest {
  account: string;
  cloudPath: string;
  localPath: string;
}

export interface StaleStrm {
  relPath: string;
  fullPath: string;
  strmContent?: string;
}

export interface MissingStrm {
  relPath: string;
  mediaExtension: string;
}

export interface MappingScanResult {
  account: string;
  cloudPath: string;
  localPath: string;
  remoteFileCount: number;
  localStrmCount: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  error?: string;
}

export interface ScanResult {
  mappings: MappingScanResult[];
  totalRemoteFiles: number;
  totalLocalStrms: number;
  totalStale: number;
  totalMissing: number;
  durationMs: number;
}

export interface ReconcileResult {
  account: string;
  cloudPath: string;
  localPath: string;
  cloudFileCount: number;
  localStrmCount: number;
  dbRecordCount: number;
  dbUpserted: number;
  dbGhostsRemoved: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  durationMs: number;
  error?: string;
}

function mediaToStrm(filePath: string): string {
  const ext = path.extname(filePath);
  if (ext.toLowerCase() === ".strm") return filePath;
  return filePath.substring(0, filePath.length - ext.length) + ".strm";
}

function isMediaExtension(filePath: string): boolean {
  const ext = path.extname(filePath).toLowerCase();
  return getStrmExtensions().includes(ext);
}

export function buildFilePathEntriesFromTree(
  account: string,
  cloudPath: string,
  data: Array<{ key: number; name: string; parent_key: number; depth: number; children?: unknown }>
): FilePathEntry[] {
  const entries: FilePathEntry[] = [];
  const cloudBase = cloudPath.replace(/\/+$/, "");

  const nodeMap = new Map<number, typeof data[0]>();
  for (const node of data) {
    nodeMap.set(node.key, node);
  }

  const pathCache = new Map<number, string>();
  function getPath(key: number): string {
    if (pathCache.has(key)) return pathCache.get(key)!;
    const node = nodeMap.get(key);
    if (!node) return "";
    const parentPath = node.parent_key > 0 ? getPath(node.parent_key) : cloudBase;
    const fullPath = parentPath ? `${parentPath}/${node.name}` : node.name;
    pathCache.set(key, fullPath);
    return fullPath;
  }

  const now = Math.floor(Date.now() / 1000);
  for (const node of data) {
    const cloudFilePath = getPath(node.key);
    entries.push({
      fileId: -(node.key + 1),
      path: cloudFilePath,
      fileName: node.name,
      parentId: node.parent_key > 0 ? -(node.parent_key + 1) : 0,
      pickCode: "",
      updateTime: now,
    });
  }
  return entries;
}

async function scanSingleMapping(
  req: MappingScanRequest,
  accountInfo: AccountInfo
): Promise<MappingScanResult> {
  const result: MappingScanResult = {
    account: req.account,
    cloudPath: req.cloudPath,
    localPath: req.localPath,
    remoteFileCount: 0,
    localStrmCount: 0,
    staleStrms: [],
    missingStrms: [],
  };

  const settings = readSettings();
  const expectedStrm = resolveStrmSettings(req.account, null, settings);

  const saveDir = path.resolve(req.localPath);
  try {
    const idRes = await fs_dir_getid(req.cloudPath, { accountInfo });

    await waitFor115ApiToken();
    const data = await exportDirParse({
      exportFileIds: idRes.id,
      targetPid: 0,
      layerLimit: 0,
      deleteAfter: true,
      timeoutMs: 300000,
      checkIntervalMs: 1000,
      accountInfo,
    });

    const seenFileIds = new Set(
        (data as Array<{ key: number }>).map((n) => String(-(n.key + 1)))
    );

    const tree = buildTree(data);
    const remoteFiles: string[] = [];
    for (const node of tree) {
      if (node.children && node.children.length > 0) {
        remoteFiles.push(...collectFilesAndTopEmptyDirs(node.children));
      } else if (/\.[a-z0-9]+$/i.test(node.name)) {
        remoteFiles.push(node.name);
      }
    }
    result.remoteFileCount = remoteFiles.filter(isMediaExtension).length;

    const remoteMediaStrms = new Set(
      remoteFiles
        .filter(isMediaExtension)
        .map((p) => mediaToStrm(p).replace(/\\/g, "/").toLowerCase())
    );

    if (!fs.existsSync(saveDir)) {
      result.localStrmCount = 0;
      return result;
    }

    const localAllFiles = collectFilesAndTopEmptyDirs(getLocalTree(saveDir));
    const localStrmFiles = localAllFiles.filter((p) =>
      p.toLowerCase().endsWith(".strm")
    );
    result.localStrmCount = localStrmFiles.length;

    for (const relPath of localStrmFiles) {
      const norm = relPath.replace(/\\/g, "/").toLowerCase();
      if (!remoteMediaStrms.has(norm)) {
        const fullPath = path.join(saveDir, relPath);
        let strmContent: string | undefined;
        try {
          strmContent = fs.readFileSync(fullPath, "utf-8").trim();
        } catch {
          // ignore
        }

        const matchesPrefix =
          !expectedStrm.strmPrefix ||
          (strmContent && strmContent.startsWith(expectedStrm.strmPrefix));
        if (matchesPrefix) {
          result.staleStrms.push({ relPath, fullPath, strmContent });
        }
      }
    }

    const localStrmNormSet = new Set(
      localStrmFiles.map((p) => p.replace(/\\/g, "/").toLowerCase())
    );
    for (const remoteFile of remoteFiles) {
      if (!isMediaExtension(remoteFile)) continue;
      const asStrm = mediaToStrm(remoteFile).replace(/\\/g, "/").toLowerCase();
      if (!localStrmNormSet.has(asStrm)) {
        result.missingStrms.push({
          relPath: remoteFile,
          mediaExtension: path.extname(remoteFile),
        });
      }
    }

    if (seenFileIds.size > 0) {
      const entries = buildFilePathEntriesFromTree(
        req.account,
        req.cloudPath,
        data as Array<{ key: number; name: string; parent_key: number; depth: number; children?: unknown }>
      );
      if (entries.length > 0) {
        upsertFilePathEntryBatch(req.account, entries);
        console.log(
          `[strmCleanup] 全量对账: 写回 ${entries.length} 条记录到 DB (account=${req.account}, cloudPath=${req.cloudPath})`
        );
      }
    }

    if (seenFileIds.size === 0) {
      console.warn(`[strmCleanup] 警告: exportDirParse 返回空数据 (cloudPath=${req.cloudPath})，跳过幽灵记录清理以避免误删DB`);
    } else {
      const ghostRemoved = removeGhostRecords(req.account, req.cloudPath, seenFileIds);
      if (ghostRemoved > 0) {
        console.log(
          `[strmCleanup] 清理幽灵 DB 记录: ${ghostRemoved} 条 (account=${req.account}, prefix=${req.cloudPath})`
        );
      }
    }
  } catch (err: unknown) {
    result.error = err instanceof Error ? err.message : String(err);
    console.error(`[strmCleanup] Mapping failed: ${req.cloudPath} -> ${req.localPath}`, err);
  }
  return result;
}

const SCAN_STATE_FILE = path.join(process.cwd(), "../config/scanState.json");

interface ScanState {
  startTime: number;
  totalMappings: number;
  completedMappings: string[];
  lastUpdated: number;
}

function readScanState(): ScanState | null {
  try {
    if (!fs.existsSync(SCAN_STATE_FILE)) return null;
    return JSON.parse(fs.readFileSync(SCAN_STATE_FILE, "utf-8")) as ScanState;
  } catch {
    return null;
  }
}

function writeScanState(state: ScanState): void {
  try {
    const dir = path.dirname(SCAN_STATE_FILE);
    if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(SCAN_STATE_FILE, JSON.stringify(state, null, 2), "utf-8");
  } catch (err) {
    console.error("[strmCleanup] 写入扫描状态失败:", err);
  }
}

function clearScanState(): void {
  try {
    if (fs.existsSync(SCAN_STATE_FILE)) {
      fs.unlinkSync(SCAN_STATE_FILE);
    }
  } catch {
    // 忽略清理失败
  }
}

export async function runScan(reqs: MappingScanRequest[]): Promise<ScanResult> {
  const started = Date.now();
  const accounts = readAccounts() as unknown as AccountInfo[];
  const results: MappingScanResult[] = [];

  const uniqueAccounts = [...new Set(reqs.map((r) => r.account))];
  for (const account of uniqueAccounts) {
    suspendMonitorForFullScan(account);
  }

  const scanStateKey = (r: MappingScanRequest) => `${r.account}:${r.cloudPath}`;
  const prevState = readScanState();
  const completedSet = new Set<string>(prevState?.completedMappings || []);
  const mappingSetMatches = !!prevState && prevState.totalMappings === reqs.length;

  if (!mappingSetMatches) {
    writeScanState({
      startTime: started,
      totalMappings: reqs.length,
      completedMappings: [],
      lastUpdated: Date.now(),
    });
    completedSet.clear();
  }

  try {
    for (const req of reqs) {
      const key = scanStateKey(req);

      if (completedSet.has(key)) {
        console.log(`[strmCleanup] 断点续传: 跳过已完成的映射 ${key}`);
        results.push({
          account: req.account,
          cloudPath: req.cloudPath,
          localPath: req.localPath,
          remoteFileCount: 0,
          localStrmCount: 0,
          staleStrms: [],
          missingStrms: [],
          error: "已跳过（断点续传）",
        });
        continue;
      }

      const accountInfo = accounts.find(
        (a) => a.name === req.account
      );
      if (!accountInfo?.cookie) {
        results.push({
          account: req.account,
          cloudPath: req.cloudPath,
          localPath: req.localPath,
          remoteFileCount: 0,
          localStrmCount: 0,
          staleStrms: [],
          missingStrms: [],
          error: `未找到账号或缺少 cookie: ${req.account}`,
        });
        continue;
      }
      results.push(await scanSingleMapping(req, accountInfo));

      completedSet.add(key);
      writeScanState({
        startTime: started,
        totalMappings: reqs.length,
        completedMappings: [...completedSet],
        lastUpdated: Date.now(),
      });
    }
  } finally {
    clearScanState();
    for (const account of uniqueAccounts) {
      clearMonitorSuspend(account);
    }
  }

  const totalRemoteFiles = results.reduce((s, r) => s + r.remoteFileCount, 0);
  const totalLocalStrms = results.reduce((s, r) => s + r.localStrmCount, 0);
  const totalStale = results.reduce((s, r) => s + r.staleStrms.length, 0);
  const totalMissing = results.reduce((s, r) => s + r.missingStrms.length, 0);

  const totalResultItems = totalStale + totalMissing;
  if (totalResultItems > 5000) {
    console.warn(
      `[strmCleanup] 警告: 扫描结果集较大 (${totalResultItems} 项)，建议分批执行清理操作以避免内存压力`
    );
  }

  return {
    mappings: results,
    totalRemoteFiles,
    totalLocalStrms,
    totalStale,
    totalMissing,
    durationMs: Date.now() - started,
  };
}

export async function runReconcile(
  reqs: MappingScanRequest[]
): Promise<{ results: ReconcileResult[]; totalDurationMs: number }> {
  const started = Date.now();
  const involvedAccounts = new Set(reqs.map((r) => r.account));
  try {
    const scanResult = await runScan(reqs);

    const results: ReconcileResult[] = scanResult.mappings.map((m) => ({
      account: m.account,
      cloudPath: m.cloudPath,
      localPath: m.localPath,
      cloudFileCount: m.remoteFileCount,
      localStrmCount: m.localStrmCount,
      dbRecordCount: 0,
      dbUpserted: 0,
      dbGhostsRemoved: 0,
      staleStrms: m.staleStrms,
      missingStrms: m.missingStrms,
      durationMs: 0,
      error: m.error,
    }));

    for (const r of results) {
      if (r.error) continue;
      try {
        const entries = getEntriesByPathPrefix(r.account, r.cloudPath);
        r.dbRecordCount = entries.length;
      } catch {
        // 忽略 DB 查询失败，保持 0
      }
    }

    return { results, totalDurationMs: Date.now() - started };
  } finally {
    for (const account of involvedAccounts) {
      clearMonitorSuspend(account);
    }
  }
}