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
  readAccounts,
  readSettings,
} from "./serverUtils";
import { suspendMonitorForFullScan, clearMonitorSuspend } from "./accountRuntimeState";
import {
  FilePathEntry,
  getEntriesByPathPrefix,
  getFilePathEntryByPath,
  removeGhostRecords,
  upsertFilePathEntryBatch,
} from "./filePathDb";
import { removeEmptyParents } from "./strmFileOps";
import { generateStrmContent, getStrmFileName, resolveStrmSettings } from "./strmUtils";
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

export interface ExecuteRequest {
  entries: Array<{
    localPath: string;
    staleRelPaths: string[];
  }>;
  dryRun?: boolean;
  action?: "delete" | "delete_all" | "regenerate" | "delete_and_regenerate";
  /** For regenerate action: missing STRM items to create */
  missingItems?: Array<{
    localPath: string;
    relPath: string;
    mappingId: string;
  }>;
  /** Scan summary for context in delete_all */
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
  /** 新增：清理+补生成组合操作的汇总 */
  cleanupSummary?: {
    deleted: number;
    regenerated: number;
    failed: number;
  };
}

const MEDIA_EXT_SET = new Set([
  ".mkv", ".mp4", ".avi", ".mov", ".rmvb", ".flv", ".webm",
  ".ts", ".mpg", ".mpeg", ".wmv", ".m4v", ".3gp", ".f4v",
  ".iso", ".strm", ".m2ts", ".mts", ".tp", ".trp", ".vob",
]);

function mediaToStrm(filePath: string): string {
  const ext = path.extname(filePath);
  if (ext.toLowerCase() === ".strm") return filePath;
  return filePath.substring(0, filePath.length - ext.length) + ".strm";
}

function isMediaExtension(filePath: string): boolean {
  return MEDIA_EXT_SET.has(path.extname(filePath).toLowerCase());
}

/**
 * 从 exportDirParse 返回的树数据构建 FilePathEntry 列表（含完整云路径）。
 * 用于全量对账时将扫描到的文件写回 DB。
 */
export function buildFilePathEntriesFromTree(
  account: string,
  cloudPath: string,
  data: Array<{ key: number; name: string; parent_key: number; depth: number; children?: unknown }>
): FilePathEntry[] {
  const entries: FilePathEntry[] = [];
  const cloudBase = cloudPath.replace(/\/+$/, "");

  // 构建 key → node 映射
  const nodeMap = new Map<number, typeof data[0]>();
  for (const node of data) {
    nodeMap.set(node.key, node);
  }

  // 递归构建路径（带缓存避免重复计算）
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
  // P2-10: exportDirParse 返回的 key 是本地自增计数器而非真实 115 file_id，
  // 使用负值标记避免与真实 file_id（正整数）冲突，life event 到来时会以真实 ID 覆盖
  for (const node of data) {
    const cloudFilePath = getPath(node.key);
    entries.push({
      fileId: -(node.key + 1),
      path: cloudFilePath,
      fileName: node.name,
      parentId: node.parent_key > 0 ? -(node.parent_key + 1) : 0,
      pickCode: "", // exportDirParse 不返回 pickcode
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

  // P3.2f: 读取全局设置，解析预期 strmPrefix 用于 stale STRM 前缀过滤
  const settings = readSettings();
  const expectedStrm = resolveStrmSettings(req.account, null, settings);

  // P0-C: localPath 统一用 path.resolve 解析（与事件处理器保持一致）
  const saveDir = path.resolve(req.localPath);
  try {
    const idRes = await fs_dir_getid(req.cloudPath, { accountInfo });

    // P5.A: 限流 — exportDirParse 是高频 115 API 调用，等待令牌避免封控
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

    // 收集远程文件 ID，用于后续清理 DB 中的幽灵记录
    const seenFileIds = new Set((data as Array<{ key: number }>).map((n) => n.key));

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

        // P3.2f: stale STRM 前缀校验 — 只匹配当前配置 strmPrefix 的 STRM 才视为 stale
        // （若 prefix 为空则不过滤，兼容无前缀生成场景）
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

    // P4.1: 将扫描到的文件写回 DB（全量对账：DB ← cloud）
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

    // P3.2a (part2): 空数据防护 — exportDirParse 可能因网络异常返回空数组，此时跳过 removeGhostRecords
    if (seenFileIds.size === 0) {
      console.warn(`[strmCleanup] 警告: exportDirParse 返回空数据 (cloudPath=${req.cloudPath})，跳过幽灵记录清理以避免误删DB`);
    } else {
      // DB 同步：清理幽灵记录（DB 有但网盘已不存在的记录）
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

// ==================== P5.B: 断点续传（扫描状态持久化） ====================

const SCAN_STATE_FILE = path.join(process.cwd(), "../config/scanState.json");

interface ScanState {
  startTime: number;
  totalMappings: number;
  completedMappings: string[]; // "account:cloudPath" 格式
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

// ==================== P4.2: 三方对账结果 ====================

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

export async function runScan(reqs: MappingScanRequest[]): Promise<ScanResult> {
  const started = Date.now();
  const accounts = readAccounts() as unknown as AccountInfo[];
  const results: MappingScanResult[] = [];

  // 暂停增量监控，避免与全量扫描竞争 API 和 DB
  const uniqueAccounts = [...new Set(reqs.map((r) => r.account))];
  for (const account of uniqueAccounts) {
    suspendMonitorForFullScan(account);
  }

  // P5.B: 断点续传 — 读取之前的扫描状态
  const scanStateKey = (r: MappingScanRequest) => `${r.account}:${r.cloudPath}`;
  const prevState = readScanState();
  const completedSet = new Set<string>(prevState?.completedMappings || []);
  // 仅当映射数量一致时才尝试续传，否则视为全新扫描
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

      // P5.B: 跳过已完成的映射（断点续传）
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

      // P5.B: 记录已完成
      completedSet.add(key);
      writeScanState({
        startTime: started,
        totalMappings: reqs.length,
        completedMappings: [...completedSet],
        lastUpdated: Date.now(),
      });
    }
  } finally {
    // P1-E: clearScanState 必须在 finally 中执行，避免扫描中断后 scanState 残留导致
    // 下次调用时已完成的映射被跳过（mappingSetMatches 仍为 true 时）
    clearScanState();
    // P2-4: 恢复增量监控，避免扫描异常时监控永久挂起
    for (const account of uniqueAccounts) {
      clearMonitorSuspend(account);
    }
  }

  const totalRemoteFiles = results.reduce((s, r) => s + r.remoteFileCount, 0);
  const totalLocalStrms = results.reduce((s, r) => s + r.localStrmCount, 0);
  const totalStale = results.reduce((s, r) => s + r.staleStrms.length, 0);
  const totalMissing = results.reduce((s, r) => s + r.missingStrms.length, 0);

  // P5.C: 大结果集告警
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

/**
 * P4.2: 三方对账（cloud + local + DB sync）
 *
 * 复用 runScan 的扫描逻辑（内部已完成 DB ← cloud 的 upsert + 幽灵清理），
 * 并补充查询每个映射的 DB 记录数，返回完整的对账报告。
 */
export async function runReconcile(
  reqs: MappingScanRequest[]
): Promise<{ results: ReconcileResult[]; totalDurationMs: number }> {
  const started = Date.now();
  // runScan 内部已调用 suspendMonitorForFullScan，对账完成后必须恢复监控
  const involvedAccounts = new Set(reqs.map((r) => r.account));
  try {
    // 复用 runScan 的扫描逻辑，但返回更详细的对账报告
    const scanResult = await runScan(reqs);

    const results: ReconcileResult[] = scanResult.mappings.map((m) => ({
      account: m.account,
      cloudPath: m.cloudPath,
      localPath: m.localPath,
      cloudFileCount: m.remoteFileCount,
      localStrmCount: m.localStrmCount,
      dbRecordCount: 0, // 下方查询填充
      dbUpserted: 0, // 已在 scanSingleMapping 中完成
      dbGhostsRemoved: 0, // 已在 scanSingleMapping 中完成
      staleStrms: m.staleStrms,
      missingStrms: m.missingStrms,
      durationMs: 0,
      error: m.error,
    }));

    // 查询每个 account+cloudPath 的 DB 记录数
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
    // 【修复】runReconcile 复用了 runScan 的 suspend，但 runScan 的 clear 在其 finally 里
    // 再次调用 clear 确保对账异常抛错时也能恢复监控
    for (const account of involvedAccounts) {
      clearMonitorSuspend(account);
    }
  }
}

export function runExecute(req: ExecuteRequest): ExecuteResult {
  const started = Date.now();
  const dryRun = !!req.dryRun;
  const errors: ExecuteResult["errors"] = [];
  const removedEmptyDirs: string[] = [];
  let deletedCount = 0;
  let regeneratedCount = 0;

  const action = req.action || "delete";

  // 从 scanSummary 提取涉及账号，用于执行完成后恢复监控
  const involvedAccounts = new Set<string>();
  if (req.scanSummary) {
    for (const m of req.scanSummary.mappings) {
      if (m.account) involvedAccounts.add(m.account);
    }
  }

  try {
    // ==== delete_all: 收集所有 stale STRM 的 entries ====
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

    // ==== delete / delete_all 执行删除 ====
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
                  // 使用统一工具层清理空父目录
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

    // ==== regenerate: 补生成缺失 STRM ====
    if (action === "regenerate" || action === "delete_and_regenerate") {
      if (req.missingItems && req.missingItems.length > 0) {
        // 读取全局设置，解析用户配置的 strmPrefix（全局默认 + 302 拼接）
        const settings = readSettings();
        for (const item of req.missingItems) {
          try {
            const localDir = path.resolve(item.localPath);
            const fileName = path.basename(item.relPath);
            const strmName = getStrmFileName(fileName);
            const strmDir = path.resolve(localDir, path.dirname(item.relPath));
            const strmPath = path.join(strmDir, strmName);

            if (!dryRun) {
              // 从 scanSummary 获取映射信息（cloudPath + account）
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
              // 使用用户配置的 strmPrefix（替代原来硬编码的 "" 和 false）
              const resolvedStrm = resolveStrmSettings(account, null, settings);
              const cloudPath = cloudBase ? `${cloudBase}/${item.relPath}` : item.relPath;

              // 302 模式下尝试从 filePathDb 反查 pickcode
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
    // 执行完成后恢复增量监控（无论成功或失败）
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
  // P0-C: 统一用 path.resolve 解析，与事件处理器保持一致
  return path.resolve(localPath);
}

export function getDefaultScanRequestsFromSettings(): MappingScanRequest[] {
  const settings = readSettings();
  const lifeMonitor =
    (settings.lifeMonitor as { accounts?: string[]; pathMappings?: Array<{ cloudPath: string; localPath: string }> } | undefined);
  if (!lifeMonitor?.pathMappings || lifeMonitor.pathMappings.length === 0) return [];
  const account = lifeMonitor.accounts?.[0];
  if (!account) return [];
  return lifeMonitor.pathMappings.map((pm) => ({
    account,
    cloudPath: pm.cloudPath,
    localPath: pm.localPath,
  }));
}
