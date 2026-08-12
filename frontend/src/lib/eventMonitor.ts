import * as fs from "fs";
import * as path from "path";
import { readSettings, readAccounts, writeSettings, notifyEmbyRefresh, LifeMonitorSettings, resolveStrmSettings, getStrmExtensions } from "./serverUtils";
import { appendLifeEventLog } from "./lifeEventLogManager";
import { tryPollMonitor } from "./accountRuntimeState";
import {
  AccountInfo,
  fs_dir_getid,
  fs_files,
  getFileInfoById,
  getPickcodeToId,
} from "./115";
import {
  lifeShow,
  oncePullLifeEvents,
  LifeEvent,
  CREATE_EVENT_TYPES,
  MOVE_EVENT_TYPES,
  RENAME_EVENT_TYPES,
  DELETE_EVENT_TYPES,
  BEHAVIOR_TYPE_TO_NAME,
} from "./115Life";
import { getStrmFileName, generateStrmContent } from "./strmUtils";
import {
  getFilePathEntry as sqliteGetFilePathEntry,
  upsertFilePathEntry as sqliteUpsertFilePathEntry,
  removeFilePathEntry as sqliteRemoveFilePathEntry,
  updatePathPrefixBatch,
} from "./filePathDb";
import {
  syncStrmText,
  removeEmptyParents,
  deleteStrmFile,
  deleteStrmDir,
  findStrmRecursive,
  findDirRecursive,
  getRootDirsFromMappings,
} from "./strmFileOps";
import { waitFor115ApiToken } from "./rateLimiter";

// ==================== Types ====================

export type FirstPullMode = "latest" | "all" | "last";
export type MoveMediaMode = "recreate" | "local_move";

// LifeMonitorConfig 已统一为 serverUtils 中的 LifeMonitorSettings
export type LifeMonitorConfig = LifeMonitorSettings;

export interface LifeMonitorState {
  running: boolean;
  account: string;
  lastFromTime: number;
  lastFromId: number;
  lastCheckTime: number;
  eventsProcessed: number;
  lastError?: string;
  status: "idle" | "starting" | "running" | "stopping" | "error";
}

export interface EventProcessResult {
  eventId: number;
  eventType: number;
  eventTypeName: string;
  action: "create" | "remove" | "move" | "rename" | "skip" | "error";
  filePath?: string;
  localPath?: string;
  message?: string;
  success: boolean;
  timestamp: number;
}

export type LifeMonitorCallback = (
  event: "status" | "event" | "log",
  data: LifeMonitorState | EventProcessResult | string
) => void;

// ==================== Constants ====================

const DEFAULT_CONFIG: LifeMonitorConfig = {
  enabled: false,
  accounts: [],
  pollInterval: 30,
  pathMappings: [],
  removeEmptyDirs: true,
  eventTypes: {
    create: true,
    remove: true,
    rename: true,
    move: true,
  },
  enablePathEncoding: false,
  minFileSize: 0,
  firstPullMode: "latest",
  moveMediaMode: "local_move",
};

const CONFIG_DIR = path.join(process.cwd(), "../config");
const stateFile = path.join(CONFIG_DIR, "lifeMonitorState.json");
const idPathCacheFile = path.join(CONFIG_DIR, "lifeIdPathCache.json");
// filePathDb 已迁移到 SQLite (./filePathDb.ts)，旧 JSON 文件 lifeFilePathDb.json 自动迁移后备份
const apiFallbackFile = path.join(CONFIG_DIR, "lifeApiFallback.json");

const WEB_FALLBACK_DURATION = 24 * 60 * 60;
const MAX_RECURSION_DEPTH = 10;
const MAX_FOLDER_FILES = 1000;
/** 单次 poll 允许处理的删除事件上限（超过则触发熔断并告警） */
const MAX_DELETE_EVENTS_PER_POLL = 100;
/** 删除事件占比阈值（超过此比例也触发熔断） */
const DELETE_RATIO_THRESHOLD_PER_POLL = 0.5;

// ==================== Global State (persist across Next.js HMR via globalThis) ====================

const g = globalThis as unknown as {
  __lifeMonitorStates?: Map<string, LifeMonitorState>;
  __lifeMonitorTimers?: Map<string, NodeJS.Timeout>;
  __lifeMonitorCallbacks?: Map<string, Set<LifeMonitorCallback>>;
  __lifeIdPathMemoryCache?: Map<string, string>;
  __embyDebounceTimers?: Map<string, NodeJS.Timeout>;
  __embyLastFireTime?: Map<string, number>;
  __lifeMonitorsAutoStarted?: boolean;
};

if (!g.__lifeMonitorStates) {
  g.__lifeMonitorStates = new Map();
}
if (!g.__lifeMonitorTimers) {
  g.__lifeMonitorTimers = new Map();
}
if (!g.__lifeMonitorCallbacks) {
  g.__lifeMonitorCallbacks = new Map();
}
if (!g.__lifeIdPathMemoryCache) {
  g.__lifeIdPathMemoryCache = new Map();
}
if (!g.__embyDebounceTimers) {
  g.__embyDebounceTimers = new Map();
}
if (!g.__embyLastFireTime) {
  g.__embyLastFireTime = new Map();
}

const monitorStates = g.__lifeMonitorStates;
const monitorTimers = g.__lifeMonitorTimers;
const monitorCallbacks = g.__lifeMonitorCallbacks;

const embyDebounceTimers = g.__embyDebounceTimers;
const embyLastFireTime = g.__embyLastFireTime;

// In-memory ID→Path cache: key = "accountName:cid"
// 加 LRU 上限，防止长期运行后缓存无限增长
const ID_PATH_MEM_CACHE_MAX = 5000;
const idPathMemoryCache = g.__lifeIdPathMemoryCache;

// Ensure config directory exists
function ensureConfigDir() {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }
}

/**
 * 服务首次懒加载时自动启动监控（每进程一次）
 * 触发条件（同时满足）：
 *   1. 监控配置中 enabled = true
 *   2. 监控账号列表 accounts 非空
 *   3. 每个被监控账号的凭据存在（115：cookie 非空；openlist：url/account/password 非空）
 *   真正的 CK 有效性在首次轮询时自动检测，失败会把 running 状态置为 error
 */
function tryAutoStartMonitorsOnce(): void {
  if (g.__lifeMonitorsAutoStarted) return;
  g.__lifeMonitorsAutoStarted = true;

  try {
    const config = getLifeMonitorConfig();
    if (!config.enabled) {
      console.log("[LifeMonitor] 自动启动：监控未启用，跳过");
      return;
    }
    if (!config.accounts || config.accounts.length === 0) {
      console.log("[LifeMonitor] 自动启动：监控账号列表为空，跳过");
      return;
    }

    const accounts = readAccounts() as unknown as AccountInfo[];
    const accountMap = new Map(accounts.map((a) => [a.name, a]));

    for (const accName of config.accounts) {
      const acc = accountMap.get(accName);
      let hasCredentials = false;
      if (acc) {
        if (acc.accountType === "115") {
          hasCredentials = !!acc.cookie && acc.cookie.length > 0;
        } else if (acc.accountType === "openlist") {
          hasCredentials = !!(acc.url && acc.account && acc.password);
        }
      }
      if (hasCredentials) {
        const r = startMonitor(accName);
        console.log(`[LifeMonitor] 自动启动账号 ${accName}: ${r.success ? "成功" : "跳过 (" + (r.message || "") + ")"}`);
      } else {
        console.log(`[LifeMonitor] 自动启动账号 ${accName}: 跳过（凭据为空或账号不存在）`);
      }
    }
  } catch (err) {
    console.error("[LifeMonitor] 自动启动失败:", err);
  }
}

// ==================== Persistent ID→Path Cache ====================

function readIdPathCache(): Record<string, string> {
  ensureConfigDir();
  if (!fs.existsSync(idPathCacheFile)) return {};
  try {
    return JSON.parse(fs.readFileSync(idPathCacheFile, "utf-8"));
  } catch {
    return {};
  }
}

function writeIdPathCache(cache: Record<string, string>) {
  ensureConfigDir();
  fs.writeFileSync(idPathCacheFile, JSON.stringify(cache, null, 2), "utf-8");
}

const MEDIA_EXT_SUFFIXES = [".mkv", ".mp4", ".avi", ".ts", ".mov", ".wmv", ".flv", ".m4v", ".rmvb", ".rm"];

/**
 * resolvePathByCid / setIdPath 处理的是「目录 cid → 云路径」映射，
 * 一旦 tier 3 fallback（getFileInfoById / export_dir 等）返回了一个以媒体文件扩展名结尾的 path
 * （例如把目录下某文件名当成了目录路径），后续 resolveEventPath 会再次拼接 fileName，
 * 最终形成 X.mkv/X.mkv 的嵌套目录，污染本地结构。
 * 此函数作为双重防卫：命中媒体扩展名时，强制向上回退到父目录，并打印告警。
 */
function sanitizeDirectoryPath(pathStr: string, tag?: string): string {
  if (!pathStr) return pathStr;
  const trimmed = pathStr.replace(/\/+$/, "");
  const lastSlash = trimmed.lastIndexOf("/");
  const lastSegment = lastSlash >= 0 ? trimmed.slice(lastSlash + 1) : trimmed;
  const dotAt = lastSegment.lastIndexOf(".");
  const ext = dotAt > 0 ? lastSegment.slice(dotAt).toLowerCase() : "";
  if (ext && MEDIA_EXT_SUFFIXES.includes(ext)) {
    const parent = lastSlash > 0 ? trimmed.slice(0, lastSlash) : "/";
    console.warn(
      `[LifeMonitor] ${tag || "sanitizeDirectoryPath"}: 目录 path 意外含媒体扩展名，强制回退父目录: ${pathStr} -> ${parent}`
    );
    return parent;
  }
  return pathStr;
}

function getIdPath(account: string, cid: number | string): string | undefined {
  const cacheKey = `${account}:${cid}`;
  const memCached = idPathMemoryCache.get(cacheKey);
  if (memCached) {
    // LRU touch：删除后重新插入，移到 Map 末尾（最新）
    idPathMemoryCache.delete(cacheKey);
    idPathMemoryCache.set(cacheKey, memCached);
    return sanitizeDirectoryPath(memCached, `getIdPath(mem ${account}:${cid})`);
  }

  const diskCache = readIdPathCache();
  const diskCached = diskCache[cacheKey];
  if (diskCached) {
    const sane = sanitizeDirectoryPath(diskCached, `getIdPath(disk ${account}:${cid})`);
    // 写入内存缓存前先淘汰
    evictIdPathCacheIfNeeded();
    idPathMemoryCache.set(cacheKey, sane);
    return sane;
  }
  return undefined;
}

function setIdPath(account: string, cid: number | string, pathStr: string) {
  const sane = sanitizeDirectoryPath(pathStr, `setIdPath(${account}:${cid})`);
  const cacheKey = `${account}:${cid}`;
  // 已存在则先删除，保证 set 后位于 Map 末尾（最新）
  if (idPathMemoryCache.has(cacheKey)) idPathMemoryCache.delete(cacheKey);
  evictIdPathCacheIfNeeded();
  idPathMemoryCache.set(cacheKey, sane);
  const diskCache = readIdPathCache();
  diskCache[cacheKey] = sane;
  writeIdPathCache(diskCache);
}

/** LRU 淘汰：当缓存达到上限时删除最旧（Map 第一个）条目 */
function evictIdPathCacheIfNeeded(): void {
  while (idPathMemoryCache.size >= ID_PATH_MEM_CACHE_MAX) {
    const oldestKey = idPathMemoryCache.keys().next().value;
    if (oldestKey === undefined) break;
    idPathMemoryCache.delete(oldestKey);
  }
}

// ==================== File Path Database (SQLite backend via ./filePathDb) ====================
// 旧 JSON 版已废弃，改为直接调用 SQLite 版
const getFilePathEntry = sqliteGetFilePathEntry;
const upsertFilePathEntry = sqliteUpsertFilePathEntry;
const removeFilePathEntry = sqliteRemoveFilePathEntry;

/**
 * 兜底清理：根据旧云路径遍历所有路径映射，尝试定位并删除对应的旧 STRM。
 * 用于 handleMoveEvent / handleRenameEvent 中 oldMapping 为 null 时的补救。
 */
function tryCleanupOldStrmByPath(
  account: string,
  oldCloudPath: string,
  fileName: string,
  fileCategory: number,
  pathMappings: LifeMonitorConfig["pathMappings"]
): { deleted: string[]; errors: string[] } {
  const deleted: string[] = [];
  const errors: string[] = [];
  const seen = new Set<string>();

  for (const mapping of pathMappings) {
    if (mapping.account && mapping.account !== account) continue;
    const oldMapping = matchPathMapping(oldCloudPath, [mapping], account);
    if (!oldMapping) continue;
    if (seen.has(oldMapping.localPath)) continue;
    seen.add(oldMapping.localPath);

    try {
      if (fileCategory === 0) {
        if (fs.existsSync(oldMapping.localPath)) {
          const dirRes = deleteStrmDir(oldMapping.localPath, { tag: "LifeMonitor/cleanupFallback", account });
          if (dirRes.deleted) deleted.push(`[folder] ${oldMapping.localPath}`);
        }
      } else {
        const strmName = getStrmFileName(fileName);
        const strmPath = path.join(path.dirname(oldMapping.localPath), strmName);
        if (fs.existsSync(strmPath)) {
          const fileRes = deleteStrmFile(strmPath, { tag: "LifeMonitor/cleanupFallback", cleanRelated: false, account });
          if (fileRes.deleted) deleted.push(`[file] ${strmPath}`);
        }
      }
    } catch (e) {
      errors.push(`${oldMapping.localPath}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }
  return { deleted, errors };
}

// ==================== API Fallback State ====================

interface ApiFallbackState {
  ios405Count: number;
  webFallbackUntil: number;
}

function readApiFallback(): Record<string, ApiFallbackState> {
  ensureConfigDir();
  if (!fs.existsSync(apiFallbackFile)) return {};
  try {
    return JSON.parse(fs.readFileSync(apiFallbackFile, "utf-8"));
  } catch {
    return {};
  }
}

function writeApiFallback(state: Record<string, ApiFallbackState>) {
  ensureConfigDir();
  fs.writeFileSync(apiFallbackFile, JSON.stringify(state, null, 2), "utf-8");
}

function getPreferredApi(account: string): "ios" | "web" {
  const state = readApiFallback();
  const fallback = state[account];
  if (fallback?.webFallbackUntil && Date.now() / 1000 < fallback.webFallbackUntil) {
    return "web";
  }
  if (fallback?.webFallbackUntil) {
    delete fallback.webFallbackUntil;
    state[account] = fallback;
    writeApiFallback(state);
  }
  return "ios";
}

function record405Error(account: string, app: "ios" | "web") {
  const state = readApiFallback();
  const fallback = state[account] || { ios405Count: 0, webFallbackUntil: 0 };

  if (app === "ios") {
    fallback.ios405Count++;
    if (fallback.ios405Count >= 3) {
      fallback.webFallbackUntil = Math.floor(Date.now() / 1000) + WEB_FALLBACK_DURATION;
      fallback.ios405Count = 0;
      console.warn(`[LifeMonitor] ${account}: iOS API 连续3次405，24小时内切换为Web API`);
    }
  } else {
    fallback.ios405Count = 0;
  }
  state[account] = fallback;
  writeApiFallback(state);
}

function reset405Count(account: string) {
  const state = readApiFallback();
  if (state[account]) {
    state[account].ios405Count = 0;
    writeApiFallback(state);
  }
}

// ==================== State Management ====================

function readState(): Record<string, { fromTime: number; fromId: number }> {
  ensureConfigDir();
  if (!fs.existsSync(stateFile)) return {};
  try {
    return JSON.parse(fs.readFileSync(stateFile, "utf-8"));
  } catch {
    return {};
  }
}

function saveState(account: string, fromTime: number, fromId: number) {
  const allState = readState();
  allState[account] = { fromTime, fromId };
  ensureConfigDir();
  fs.writeFileSync(stateFile, JSON.stringify(allState, null, 2), "utf-8");
}

export function getLifeMonitorConfig(): LifeMonitorConfig {
  const settings = readSettings();
  const monitor = (settings as Record<string, unknown>).lifeMonitor as LifeMonitorConfig | undefined;
  const config = !monitor
    ? { ...DEFAULT_CONFIG }
    : {
        ...DEFAULT_CONFIG,
        ...monitor,
        eventTypes: { ...DEFAULT_CONFIG.eventTypes, ...(monitor.eventTypes || {}) },
      };
  // 懒加载触发自动启动：首次有用户访问监控相关接口/页面时触发
  // （保护标志在 tryAutoStartMonitorsOnce 内部，且在最顶部先设置，避免 re-entrancy）
  tryAutoStartMonitorsOnce();
  return config;
}

export function saveLifeMonitorConfig(config: LifeMonitorConfig): void {
  const settings = readSettings();
  (settings as Record<string, unknown>).lifeMonitor = config;
  writeSettings(settings);
}

export function getAllMonitorStates(): Map<string, LifeMonitorState> {
  return new Map(monitorStates);
}

export function subscribeMonitor(account: string, callback: LifeMonitorCallback): () => void {
  if (!monitorCallbacks.has(account)) {
    monitorCallbacks.set(account, new Set());
  }
  monitorCallbacks.get(account)!.add(callback);
  const state = monitorStates.get(account);
  if (state) callback("status", state);
  return () => {
    monitorCallbacks.get(account)?.delete(callback);
  };
}

function notifyCallbacks(account: string, type: "status" | "event" | "log", data: unknown) {
  const callbacks = monitorCallbacks.get(account);
  if (callbacks) {
    callbacks.forEach((cb) => {
      try {
        cb(type, data as LifeMonitorState | EventProcessResult | string);
      } catch (err) {
        console.error("Life monitor callback error:", err);
      }
    });
  }
}

function updateState(account: string, partial: Partial<LifeMonitorState>) {
  const current = monitorStates.get(account) || {
    running: false,
    account,
    lastFromTime: 0,
    lastFromId: 0,
    lastCheckTime: 0,
    eventsProcessed: 0,
    status: "idle",
  };
  const updated = { ...current, ...partial };
  monitorStates.set(account, updated);
  notifyCallbacks(account, "status", updated);
}

// ==================== Path Resolution (3-tier cache) ====================

async function resolvePathByCid(
  accountInfo: AccountInfo,
  cid: number | string
): Promise<string> {
  if (Number(cid) === 0) return "/";

  const account = accountInfo.name;

  // Tier 1: Memory cache
  const memCached = getIdPath(account, cid);
  if (memCached) return memCached;

  // Tier 2: Try to resolve from known path mappings
  // Check if this CID corresponds to one of our mapped paths
  const config = getLifeMonitorConfig();
  for (const mapping of config.pathMappings) {
    if (mapping.account && mapping.account !== account) continue;
    try {
      // 每个映射的 fs_dir_getid 都是独立 API 调用，需限流
      await waitFor115ApiToken();
      const mappedCid = await fs_dir_getid(mapping.cloudPath, {
        userAgent: readSettings()["user-agent"],
        accountInfo: accountInfo as AccountInfo,
      });
      if (mappedCid.id === cid) {
        setIdPath(account, cid, mapping.cloudPath);
        return sanitizeDirectoryPath(mapping.cloudPath, `resolvePathByCid(tier2 ${account}:${cid})`);
      }
    } catch {
      // Ignore errors for individual mappings
    }
  }

  // Tier 3: Use API to resolve
  try {
    const { default: axios } = await import("axios");
    const userAgent = readSettings()["user-agent"] || "Mozilla/5.0";

    // Try webapi.115.com/files to get file info including parent path
    // Tier 3 的 API 调用需限流
    await waitFor115ApiToken();
    const fileInfo = await getFileInfoById(cid, {
      userAgent,
      accountInfo: accountInfo as AccountInfo,
    });

    // If file info includes path, use it
    if (fileInfo && typeof fileInfo === "object") {
      const info = fileInfo as Record<string, unknown>;
      const pathVal = info.path as string | undefined;
      if (pathVal) {
        setIdPath(account, cid, pathVal);
        return sanitizeDirectoryPath(pathVal, `resolvePathByCid(tier3-fileInfo ${account}:${cid})`);
      }
    }

    // Fallback: use webapi files listing to find the directory name
    // Then recursively resolve parent
    await waitFor115ApiToken();
    await fs_files(cid, {
      userAgent,
      limit: 1,
      accountInfo: accountInfo as AccountInfo,
    });

    // Use the export_dir approach as last resort for path resolution
    const exportUrl = "https://proapi.115.com/android/2.0/ufile/export_dir";
    const formData = new URLSearchParams();
    formData.append("file_ids", String(cid));
    formData.append("target", "U_1_0");
    formData.append("layer_limit", "1");

    const exportResp = await axios.post(exportUrl, formData, {
      headers: {
        "User-Agent": userAgent,
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        Cookie: accountInfo.cookie,
      },
      timeout: 15000,
    });

    const exportId = exportResp.data?.data?.export_id;
    if (exportId) {
      for (let i = 0; i < 10; i++) {
        await new Promise((r) => setTimeout(r, 500));
        const statusUrl = `https://webapi.115.com/files/export_dir?export_id=${exportId}`;
        const statusResp = await axios.get(statusUrl, {
          headers: {
            "User-Agent": userAgent,
            Cookie: accountInfo.cookie,
          },
          timeout: 10000,
        });

        const exportData = statusResp.data?.data;
        if (exportData) {
          const pathStr = extractPathFromExportData(exportData, cid);
          if (pathStr) {
            setIdPath(account, cid, pathStr);
            return sanitizeDirectoryPath(pathStr, `resolvePathByCid(tier3-exportDir ${account}:${cid})`);
          }
        }
      }
    }

    console.warn(`[LifeMonitor] Cannot resolve path for cid ${cid}`);
    return "";
  } catch (error) {
    console.error(`[LifeMonitor] Error resolving path for cid ${cid}:`, error);
    return "";
  }
}

function extractPathFromExportData(data: unknown, targetCid: number | string): string {
  if (!data || typeof data !== "object") return "";
  const obj = data as Record<string, unknown>;
  // 归一化比较基准为字符串，避免 19 位 file_id 因 JS Number 精度丢失导致比较失败
  const targetStr = String(targetCid);

  const innerData = obj.data as Record<string, unknown> | undefined;
  if (innerData) {
    const list = innerData.list as Array<Record<string, unknown>> | undefined;
    if (Array.isArray(list)) {
      for (const item of list) {
        if (String(item.cid) === targetStr || String(item.id) === targetStr) {
          return (item.path || item.file_path || "") as string;
        }
        if (item.children) {
          const found = extractPathFromExportData(item.children, targetCid);
          if (found) return found;
        }
      }
    }
  }

  if (Array.isArray(obj.list)) {
    for (const item of obj.list as Array<Record<string, unknown>>) {
      if (String(item.cid) === targetStr || String(item.id) === targetStr) {
        return (item.path || item.file_path || "") as string;
      }
    }
  }

  return "";
}

async function resolveEventPath(
  accountInfo: AccountInfo,
  event: LifeEvent,
  nameOverride?: string
): Promise<string> {
  let parentPath = "";
  if (Number(event.parent_id) > 0) {
    parentPath = await resolvePathByCid(accountInfo, event.parent_id);
  } else {
    parentPath = "/";
  }

  // 对于 rename 事件，115 API 的 file_name 存的是旧名，新名在 new_name 字段
  // nameOverride 用于传入 new_name 以解析新路径
  const fileName = nameOverride || event.file_name || "";
  if (parentPath === "/" || parentPath === "") {
    return "/" + fileName;
  }
  return parentPath.endsWith("/") ? parentPath + fileName : parentPath + "/" + fileName;
}

// ==================== Path Utilities ====================

function matchPathMapping(
  cloudPath: string,
  pathMappings: LifeMonitorConfig["pathMappings"],
  account?: string
): { cloudPath: string; localPath: string; relativePath: string } | null {
  for (const mapping of pathMappings) {
    if (mapping.account && account && mapping.account !== account) continue;

    // Normalize: strip trailing slashes for consistent matching
    const key = mapping.cloudPath.replace(/\/+$/, "");
    const normalizedKey = key + "/";

    if (cloudPath === mapping.cloudPath || cloudPath === key || cloudPath.startsWith(normalizedKey)) {
      const sliceLen = Math.min(mapping.cloudPath.length, key.length + 1);
      const relativePath = cloudPath.slice(sliceLen).replace(/^\//, "");
      const localPath = path.join(mapping.localPath, relativePath);
      return {
        cloudPath: mapping.cloudPath,
        localPath,
        relativePath,
      };
    }
  }
  return null;
}

function sanitizePathParts(relativePath: string): string {
  if (process.platform !== "win32") return relativePath;
  const illegalChars = '<>"|?*';
  const parts = relativePath.split(path.sep);
  return parts.map((part) => {
    let sanitized = part.replace(/:/g, "：");
    for (const char of illegalChars) {
      sanitized = sanitized.split(char).join("_");
    }
    return sanitized;
  }).join(path.sep);
}

function isMediaFile(fileName: string, mediaExtensions: string[]): boolean {
  const ext = path.extname(fileName).toLowerCase();
  return mediaExtensions.some((me) => me.toLowerCase() === ext);
}

function isValidPickCode(pickCode: string): boolean {
  return !!pickCode && pickCode.length === 17 && /^[a-zA-Z0-9]+$/.test(pickCode);
}

function readStrmContent(strmPath: string): string | null {
  try {
    if (!fs.existsSync(strmPath)) return null;
    return fs.readFileSync(strmPath, "utf-8").trim();
  } catch {
    return null;
  }
}

// ==================== Event Processing ====================

async function handleCreateEvent(
  accountInfo: AccountInfo,
  event: LifeEvent,
  config: LifeMonitorConfig,
  mapping: { localPath: string; relativePath: string },
  cloudPath: string
): Promise<EventProcessResult> {
  const result: EventProcessResult = {
    eventId: event.id,
    eventType: event.type,
    eventTypeName: BEHAVIOR_TYPE_TO_NAME[event.type] || "unknown",
    action: "create",
    success: false,
    timestamp: Date.now(),
    filePath: cloudPath,
    localPath: mapping.localPath,
  };

  if (event.file_category === 0) {
    return handleFolderCreateEvent(accountInfo, event, config, mapping, cloudPath);
  }

  if (!isValidPickCode(event.pick_code)) {
    result.message = `无效的 pick_code: ${event.pick_code}`;
    return result;
  }

  const strmFileName = getStrmFileName(event.file_name);
  const strmPath = path.join(path.dirname(mapping.localPath), strmFileName);
  const strmContent = generateStrmContent(cloudPath, config.strmPrefix || "", config.enablePathEncoding || false, {
    enable302: config.enable302,
    account: accountInfo.name,
    pickcode: event.pick_code,
    fileName: event.file_name,
  });

  if (syncStrmText(strmPath, strmContent, { tag: "LifeMonitor/create", account: accountInfo.name }).ok) {
    upsertFilePathEntry(accountInfo.name, {
      fileId: event.file_id,
      path: cloudPath,
      fileName: event.file_name,
      parentId: event.parent_id,
      pickCode: event.pick_code,
      updateTime: event.update_time,
    });

    result.success = true;
    result.message = `STRM 文件已创建: ${strmPath}`;
  } else {
    result.message = `创建 STRM 失败: ${strmPath}`;
  }

  return result;
}

async function handleFolderCreateEvent(
  accountInfo: AccountInfo,
  event: LifeEvent,
  config: LifeMonitorConfig,
  mapping: { localPath: string; relativePath: string },
  cloudPath: string
): Promise<EventProcessResult> {
  const result: EventProcessResult = {
    eventId: event.id,
    eventType: event.type,
    eventTypeName: BEHAVIOR_TYPE_TO_NAME[event.type] || "unknown",
    action: "create",
    success: true,
    timestamp: Date.now(),
    filePath: cloudPath,
    localPath: mapping.localPath,
  };

  const localDir = mapping.localPath;
  if (!fs.existsSync(localDir)) {
    fs.mkdirSync(localDir, { recursive: true });
  }

  // Recursively process folder contents
  let strmCount = 0;
  let skippedCount = 0;
  let skipByExt = 0;      // 扩展名不匹配
  let skipBySize = 0;     // 小于最小文件阈值
  let skipByPickcode = 0; // pickcode 缺失/无效（含兜底接口失败）
  let skipByWrite = 0;    // 写入磁盘失败

  function anyPickCode(item: Record<string, unknown>): string {
    const direct = (item.pc || item.pickcode || item.PickCode || item.pickCode || item.pick_code) as string | undefined;
    if (direct && typeof direct === "string" && isValidPickCode(direct)) return direct;
    // 兜底：遍历所有字段，找任意 17 位字母数字（防止 115 再改字段名）
    for (const key of Object.keys(item)) {
      const val = item[key];
      if (typeof val === "string" && isValidPickCode(val)) return val;
    }
    return typeof direct === "string" ? direct : "";
  }

  // 115 web API /files 返回的 pickcode 字段名是 `pc`（参见 p115client.normalize_attr_web）
  // 之前的实现错误地调用 /files/info（getFileInfoById）来取 pickcode，但该接口不返回 pickcode，
  // 导致转存带文件夹的内容时只能创建空目录，无法生成 STRM。
  // 修复策略：优先使用 fs_files 返回的 item.pc；缺失时调用 /files/file（getPickcodeToId）兜底。
  async function processDirectory(
    cid: number | string,
    depth: number,
    currentLocalDir: string,
    currentCloudPath: string
  ) {
    if (depth > MAX_RECURSION_DEPTH) return;

    try {
      const userAgent = readSettings()["user-agent"];
      let offset = 0;
      const limit = 1000;
      let debugFsLogged = false;

      while (true) {
        // 分页拉取文件列表前限流，避免大文件夹一次性消耗大量 API 配额
        await waitFor115ApiToken();
        const data = await fs_files(cid, {
          userAgent,
          limit,
          offset,
          accountInfo: accountInfo as AccountInfo,
        });

        const items = data?.data || [];
        if (items.length === 0) break;

        // 调试日志：该目录下有媒体文件但前 3 条都拿不到 pc 时，打一次字段名集合方便排查
        const mediaItems = (items as Array<Record<string, unknown>>).filter(
          (it) => it.fc !== 0 && typeof it.n === "string" && isMediaFile(it.n, getStrmExtensions())
        );
        if (!debugFsLogged && mediaItems.length > 0) {
          const sampleKeys = new Set<string>();
          const missingSamples: Array<{ name: string; keys: string[] }> = [];
          for (const it of mediaItems.slice(0, 3)) {
            Object.keys(it).forEach((k) => sampleKeys.add(k));
            if (!isValidPickCode(anyPickCode(it))) {
              missingSamples.push({ name: String(it.n || ""), keys: Object.keys(it) });
            }
          }
          if (missingSamples.length > 0) {
            console.warn(
              `[LifeMonitor] fs_files 媒体文件缺失 pickcode (cid=${cid}, cloudPath=${currentCloudPath}). ` +
                `所有字段名=[${[...sampleKeys].sort().join(", ")}]； ` +
                `缺失样例=${JSON.stringify(missingSamples)}`
            );
          }
          debugFsLogged = true;
        }

        for (const item of items as Array<Record<string, unknown>>) {
          const itemName = item.n as string;
          const itemFid = item.fid as number;
          const itemCid = item.cid as number;
          const isDirectory = item.fc === 0;
          const itemCloudPath = currentCloudPath.endsWith("/")
            ? currentCloudPath + itemName
            : currentCloudPath + "/" + itemName;

          if (isDirectory) {
            const itemLocalPath = path.join(currentLocalDir, sanitizePathParts(itemName));

            if (!fs.existsSync(itemLocalPath)) {
              fs.mkdirSync(itemLocalPath, { recursive: true });
            }

            setIdPath(accountInfo.name, itemCid, itemCloudPath);
            upsertFilePathEntry(accountInfo.name, {
              fileId: itemFid,
              path: itemCloudPath,
              fileName: itemName,
              parentId: cid,
              pickCode: "",
              updateTime: Math.floor(Date.now() / 1000),
            });

            await processDirectory(itemCid, depth + 1, itemLocalPath, itemCloudPath);
          } else {
            if (!isMediaFile(itemName, getStrmExtensions())) {
              skipByExt++;
              skippedCount++;
              continue;
            }

            const folderMinSize = config.minFileSize || 0;
            if (
              folderMinSize > 0 &&
              typeof item.s === "number" &&
              item.s < folderMinSize
            ) {
              skipBySize++;
              skippedCount++;
              continue;
            }

            // 优先使用 fs_files 直接返回的 pc 字段（无需额外请求）
            // 缺失时用 getPickcodeToId 兜底（/files/file 专门用于反查 pickcode）
            let pickCode = anyPickCode(item);

            if (!isValidPickCode(pickCode)) {
              try {
                const userAgent = readSettings()["user-agent"];
                // 反查 pickcode 是独立 API 调用，需单独限流
                await waitFor115ApiToken();
                pickCode = await getPickcodeToId(itemFid, {
                  userAgent,
                  accountInfo: accountInfo as AccountInfo,
                });
              } catch (err) {
                const detail =
                  err && typeof err === "object"
                    ? JSON.stringify({
                        message: (err as Error).message,
                        ...(err as Record<string, unknown>),
                      })
                    : String(err);
                console.warn(
                  `[LifeMonitor] getPickcodeToId 失败 fid=${itemFid} name=${itemName} response=${detail}`
                );
              }
            }

            if (isValidPickCode(pickCode)) {
              const strmFileName = getStrmFileName(itemName);
              const strmPath = path.join(currentLocalDir, strmFileName);
              const strmContent = generateStrmContent(itemCloudPath, config.strmPrefix || "", config.enablePathEncoding || false, {
                enable302: config.enable302,
                account: accountInfo.name,
                pickcode: pickCode,
                fileName: itemName,
              });

              if (syncStrmText(strmPath, strmContent, { tag: "LifeMonitor/create", account: accountInfo.name }).ok) {
                strmCount++;
                // 注意：文件分支不能 setIdPath(itemCid, ...) —— itemCid 是父目录的 cid，
                // 若写入 itemCloudPath（含文件名）会污染父目录 cid→path 缓存，
                // 导致后续同目录其他文件 resolve 成 X.mkv/X.mkv 的嵌套路径。
                // 目录分支（fc===0）上方 L791 的 setIdPath 仍然保留，因为那里 itemCid 是目录自身 cid。
                upsertFilePathEntry(accountInfo.name, {
                  fileId: itemFid,
                  path: itemCloudPath,
                  fileName: itemName,
                  parentId: cid,
                  pickCode,
                  updateTime: Math.floor(Date.now() / 1000),
                });
              } else {
                skipByWrite++;
                skippedCount++;
              }
            } else {
              skipByPickcode++;
              skippedCount++;
            }
          }

          if (strmCount + skippedCount >= MAX_FOLDER_FILES) return;
        }

        offset += items.length;
        if (items.length < limit) break;
      }
    } catch (err) {
      console.error(`[LifeMonitor] Error processing folder cid=${cid}:`, err);
    }
  }

  // 关键修复：文件夹本身也要写入 DB，否则后续 move/rename 事件来时
  // getFilePathEntry(account, event.file_id) 查不到 oldEntry → move-outside-miss → 跳过清理
  upsertFilePathEntry(accountInfo.name, {
    fileId: event.file_id,
    path: cloudPath,
    fileName: event.file_name,
    parentId: event.parent_id || 0,
    pickCode: event.pick_code || "",
    updateTime: event.update_time || Math.floor(Date.now() / 1000),
  });
  // 文件夹 cid → 云路径缓存也写入，供后续子文件事件 resolvePathByCid 命中
  setIdPath(accountInfo.name, event.file_id, cloudPath);

  await processDirectory(event.file_id, 0, localDir, cloudPath);

  result.action = "create";
  result.message =
    `文件夹同步完成: 创建 ${strmCount} 个 STRM；` +
    `跳过明细 — 扩展名不匹配:${skipByExt} / 体积过小:${skipBySize} / pickcode 无效:${skipByPickcode} / 写入失败:${skipByWrite} / 总:${skippedCount}`;
  return result;
}

async function handleDeleteEvent(
  accountInfo: AccountInfo,
  event: LifeEvent,
  config: LifeMonitorConfig,
  mapping: { localPath: string; relativePath: string },
  cloudPath: string
): Promise<EventProcessResult> {
  const result: EventProcessResult = {
    eventId: event.id,
    eventType: event.type,
    eventTypeName: BEHAVIOR_TYPE_TO_NAME[event.type] || "unknown",
    action: "remove",
    success: false,
    timestamp: Date.now(),
    filePath: cloudPath,
    localPath: mapping.localPath,
  };

  const isFolder = event.file_category === 0;
  const rootDirs = getRootDirsFromMappings(config.pathMappings);
  const oldEntry = getFilePathEntry(accountInfo.name, event.file_id);

  // P3.2c + 重试优化：二次验证文件/目录不存在，最多重试 3 次，404 直接判定已删除
  let cloudVerifiedGone = false;
  const MAX_VERIFY_RETRIES = 3;
  let verifyRetries = MAX_VERIFY_RETRIES;

  const isNotFound = (err: unknown): boolean => {
    if (!err || typeof err !== "object") return false;
    const obj = err as Record<string, unknown>;
    const status = obj.status ?? (obj.response as Record<string, unknown> | undefined)?.status;
    return Number(status) === 404;
  };

  verify: while (verifyRetries > 0) {
    verifyRetries--;
    try {
      if (isFolder) {
        try {
          // 删除二次验证的 API 调用需限流
          await waitFor115ApiToken();
          await fs_dir_getid(cloudPath, { accountInfo });
          cloudVerifiedGone = false;
          console.warn(`[LifeMonitor] 二次验证: 目录仍存在于网盘 ${cloudPath}，跳过删除避免误删`);
          break verify;
        } catch (e) {
          if (isNotFound(e)) {
            cloudVerifiedGone = true;
            break verify;
          }
          if (verifyRetries === 0) {
            console.warn(`[LifeMonitor] 二次验证: 目录存在性检查重试耗尽，保守信任删除事件: ${cloudPath}`);
            cloudVerifiedGone = true;
            break verify;
          }
          await new Promise((r) => setTimeout(r, 1000));
          continue verify;
        }
      } else {
        try {
          // 删除二次验证的 API 调用需限流
          await waitFor115ApiToken();
          const info = await getFileInfoById(event.file_id, { accountInfo });
          if (!info || !(info as unknown as Record<string, unknown>).fileName) {
            cloudVerifiedGone = true;
            break verify;
          }
          cloudVerifiedGone = false;
          console.warn(`[LifeMonitor] 二次验证: 文件仍存在于网盘 file_id=${event.file_id}, name=${(info as unknown as Record<string, unknown>).fileName}，跳过删除避免误删`);
          break verify;
        } catch (e) {
          if (isNotFound(e)) {
            cloudVerifiedGone = true;
            break verify;
          }
          if (verifyRetries === 0) {
            console.warn(`[LifeMonitor] 二次验证: 文件存在性检查重试耗尽，保守信任删除事件: file_id=${event.file_id}`);
            cloudVerifiedGone = true;
            break verify;
          }
          await new Promise((r) => setTimeout(r, 1000));
          continue verify;
        }
      }
    } catch {
      if (verifyRetries === 0) {
        console.warn(`[LifeMonitor] 二次验证: 整体验证链路异常重试耗尽，保守信任删除事件继续执行`);
        cloudVerifiedGone = true;
        break verify;
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
  }

  if (!cloudVerifiedGone) {
    result.success = false;
    result.action = "skip";
    result.message = `网盘二次验证: ${isFolder ? "目录" : "文件"}仍存在，跳过删除`;
    return result;
  }

  if (isFolder) {
    if (fs.existsSync(mapping.localPath)) {
      deleteStrmDir(mapping.localPath, { tag: "LifeMonitor/delete", account: accountInfo.name });
      result.success = true;
      result.message = `文件夹已删除: ${mapping.localPath}`;
      if (oldEntry) removeFilePathEntry(accountInfo.name, event.file_id);
      if (config.removeEmptyDirs) {
        removeEmptyParents(mapping.localPath, { rootDirs, tag: "LifeMonitor/delete", account: accountInfo.name });
      }
    } else {
      // 文件不存在时，用兜底查找旧 STRM（可能已被移动但未清理）
      const cleanup = tryCleanupOldStrmByPath(
        accountInfo.name,
        cloudPath,
        event.file_name || "",
        0,
        config.pathMappings
      );
      if (cleanup.deleted.length > 0) {
        result.success = true;
        result.message = `路径不存在但清理了 ${cleanup.deleted.length} 个残留目录`;
      } else {
        result.success = true;
        result.message = `本地文件夹不存在，跳过: ${mapping.localPath}`;
      }
      if (oldEntry) removeFilePathEntry(accountInfo.name, event.file_id);
    }
  } else {
    const strmFileName = getStrmFileName(event.file_name);
    const strmPath = path.join(path.dirname(mapping.localPath), strmFileName);

    if (fs.existsSync(strmPath)) {
      void deleteStrmFile(strmPath, { rootDirs, cleanRelated: true, tag: "LifeMonitor/delete", account: accountInfo.name });
      result.success = true;
      result.message = `STRM 文件已删除: ${strmPath}`;
      if (result.action !== "skip") {
        appendLifeEventLog(
          accountInfo.name,
          event.type,
          true,
          cloudPath,
          mapping.localPath,
          `STRM 文件已删除: ${strmPath}`,
          {
            fileId: event.file_id,
            pickCode: event.pick_code || undefined,
          }
        );
      }
      if (oldEntry) removeFilePathEntry(accountInfo.name, event.file_id);
      if (config.removeEmptyDirs) {
        removeEmptyParents(strmPath, { rootDirs, tag: "LifeMonitor/delete", account: accountInfo.name });
      }
    } else {
      // 兜底：fileId 关联查不到但路径上可能有残留 STRM
      const cleanup = tryCleanupOldStrmByPath(
        accountInfo.name,
        cloudPath,
        event.file_name || "",
        1,
        config.pathMappings
      );
      if (cleanup.deleted.length > 0) {
        result.success = true;
        result.message = `STRM 不存在但清理了 ${cleanup.deleted.length} 个残留文件`;
      } else {
        result.success = true;
        result.message = `本地 STRM 文件不存在，跳过: ${strmPath}`;
      }
      if (oldEntry) removeFilePathEntry(accountInfo.name, event.file_id);
    }
  }

  // 删除成功 → Telegram 通知（异步 fire-and-forget，避免阻塞事件主流程）
  if (result.success && result.action !== "skip") {
    (async () => {
      try {
        const { sendTelegramNotification } = await import("./telegram");
        const kindLabel = isFolder ? "目录" : "文件";
        const msg =
          `🗑️ <b>STRM 已删除</b>\n` +
          `<b>账号：</b>${accountInfo.name}\n` +
          `<b>类型：</b>${kindLabel}\n` +
          `<b>云端路径：</b>${cloudPath}\n` +
          `<b>说明：</b>${result.message || ""}`;
        await sendTelegramNotification(msg, "complete");
      } catch (tgErr) {
        console.error("[LifeMonitor] 删除事件 Telegram 通知失败:", tgErr instanceof Error ? tgErr.message : String(tgErr));
      }
    })();
  }

  return result;
}

async function handleMoveEvent(
  accountInfo: AccountInfo,
  event: LifeEvent,
  config: LifeMonitorConfig,
  mapping: { localPath: string; relativePath: string },
  cloudPath: string
): Promise<EventProcessResult> {
  const result: EventProcessResult = {
    eventId: event.id,
    eventType: event.type,
    eventTypeName: BEHAVIOR_TYPE_TO_NAME[event.type] || "unknown",
    action: "move",
    success: false,
    timestamp: Date.now(),
    filePath: cloudPath,
    localPath: mapping.localPath,
  };

  const oldEntry = getFilePathEntry(accountInfo.name, event.file_id);
  const isFolder = event.file_category === 0;
  const moveMode = config.moveMediaMode || "local_move";

  if (oldEntry) {
    const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);

    if (oldMapping) {
      // ========= 有 oldMapping 的正常路径 =========
      if (moveMode === "recreate") {
        // P1-D: recreate 模式先建后删 — 先创建新位置 STRM，成功后再更新 DB 并删除旧文件
        // STEP 1: 先创建新位置的 STRM（保留旧文件作为备份）
        const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);

        // STEP 2: 验证新文件创建成功后，再更新 DB 路径前缀并删除旧位置的 STRM
        if (createResult && createResult.success) {
          // P1-D 修正: DB 路径前缀更新移到创建成功之后，避免创建失败时 DB 与文件系统不一致
          if (isFolder) {
            if (oldEntry?.path) {
              const updatedCount = updatePathPrefixBatch(accountInfo.name, oldEntry.path, cloudPath);
              if (updatedCount > 0) {
                console.log(`[LifeMonitor] move: 批量更新 ${updatedCount} 条子记录路径前缀: ${oldEntry.path} -> ${cloudPath}`);
              }
            }
          }

          if (isFolder) {
            if (fs.existsSync(oldMapping.localPath)) {
              try {
                deleteStrmDir(oldMapping.localPath, { tag: "LifeMonitor/move-recreate", account: accountInfo.name });
              } catch (err) {
                console.error(`[LifeMonitor] recreate 删除旧目录失败: ${oldMapping.localPath}`, err);
              }
            }
          } else {
            const oldStrmFileName = getStrmFileName(oldEntry.fileName);
            const oldStrmPath = path.join(
              path.dirname(oldMapping.localPath),
              oldStrmFileName
            );
            if (fs.existsSync(oldStrmPath)) {
              try {
                deleteStrmFile(oldStrmPath, { rootDirs: getRootDirsFromMappings(config.pathMappings), cleanRelated: false, tag: "LifeMonitor/move-recreate", account: accountInfo.name });
              } catch (err) {
                console.error(`[LifeMonitor] recreate 删除旧 STRM 失败: ${oldStrmPath}`, err);
              }
            }
          }

          // 清理空父目录
          if (config.removeEmptyDirs) {
            const rootDirs = getRootDirsFromMappings(config.pathMappings);
            removeEmptyParents(oldMapping.localPath, { rootDirs, tag: "LifeMonitor/move-recreate", account: accountInfo.name });
          }

          createResult.action = "move";
          createResult.success = true;
          createResult.message = `文件已移动(recreate先建后删): ${oldMapping.localPath} -> ${mapping.localPath}`;
          return createResult;
        } else {
          // 创建失败，保留旧文件不删除，告警
          createResult.action = "error";
          createResult.success = false;
          createResult.message = `recreate 模式创建新 STRM 失败，已保留旧文件不删除: ${createResult?.message || "未知错误"}`;
          console.error(`[LifeMonitor] recreate 创建失败，保留旧文件: ${oldMapping.localPath}`);
          return createResult;
        }
      }

      // local_move 模式
      if (isFolder) {
        if (fs.existsSync(oldMapping.localPath)) {
          const newParentDir = path.dirname(mapping.localPath);
          if (!fs.existsSync(newParentDir)) {
            fs.mkdirSync(newParentDir, { recursive: true });
          }
          if (fs.existsSync(mapping.localPath)) {
            const hasStrm = fs.readdirSync(mapping.localPath).some((f) => f.endsWith(".strm"));
            if (hasStrm) {
              // 目标含 STRM → 兜底清理旧目录残留后继续
              const cleanedResidual = await cleanupResidualStrmsInOldFolder(oldMapping.localPath, mapping.localPath, config, accountInfo.name);
              result.success = true;
              result.message = `目标目录已存在且含 STRM，兜底清理残留 ${cleanedResidual.length} 条后跳过移动`;
              // 注意：不 return，继续执行后面的 upsertFilePathEntry 更新 path 记录！
            } else {
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          } else {
            fs.renameSync(oldMapping.localPath, mapping.localPath);
            // 批量更新子记录的路径前缀
            if (oldEntry?.path) {
              const updatedCount = updatePathPrefixBatch(accountInfo.name, oldEntry.path, cloudPath);
              if (updatedCount > 0) {
                console.log(`[LifeMonitor] move: 批量更新 ${updatedCount} 条子记录路径前缀: ${oldEntry.path} -> ${cloudPath}`);
              }
            }
            result.success = true;
            result.message = `文件夹已移动: ${oldMapping.localPath} -> ${mapping.localPath}`;
          }
        } else {
          return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        }
      } else {
        const oldStrmFileName = getStrmFileName(oldEntry.fileName);
        const oldStrmPath = path.join(
          path.dirname(oldMapping.localPath),
          oldStrmFileName
        );
        const newStrmFileName = getStrmFileName(event.file_name);
        const newStrmPath = path.join(path.dirname(mapping.localPath), newStrmFileName);

        if (fs.existsSync(oldStrmPath)) {
          const newDir = path.dirname(newStrmPath);
          if (!fs.existsSync(newDir)) {
            fs.mkdirSync(newDir, { recursive: true });
          }

          if (path.dirname(oldStrmPath) === path.dirname(newStrmPath)) {
            if (fs.existsSync(newStrmPath) && oldStrmPath !== newStrmPath) {
              deleteStrmFile(newStrmPath, { tag: "LifeMonitor/move", cleanRelated: false, account: accountInfo.name });
            }
            if (oldStrmPath !== newStrmPath) {
              fs.renameSync(oldStrmPath, newStrmPath);
            }
          } else {
            const content = readStrmContent(oldStrmPath);
            if (content !== null) {
              syncStrmText(newStrmPath, content, { tag: "LifeMonitor/move", account: accountInfo.name });
              deleteStrmFile(oldStrmPath, { tag: "LifeMonitor/move", cleanRelated: false, account: accountInfo.name });
            } else {
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          }

          if (isValidPickCode(event.pick_code)) {
            const newContent = generateStrmContent(
              cloudPath,
              config.strmPrefix || "",
              config.enablePathEncoding || false,
              {
                enable302: config.enable302,
                account: accountInfo.name,
                pickcode: event.pick_code,
                fileName: event.file_name,
              }
            );
            syncStrmText(newStrmPath, newContent, { tag: "LifeMonitor/move", account: accountInfo.name });
          }

          result.success = true;
          result.message = `STRM 已移动: ${oldStrmPath} -> ${newStrmPath}`;
        } else {
          return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        }
      }

      // Update tracking database
      upsertFilePathEntry(accountInfo.name, {
        fileId: event.file_id,
        path: cloudPath,
        fileName: event.file_name,
        parentId: event.parent_id,
        pickCode: event.pick_code || oldEntry.pickCode,
        updateTime: event.update_time,
      });

      if (config.removeEmptyDirs) {
        const rootDirs = getRootDirsFromMappings(config.pathMappings);
        removeEmptyParents(oldMapping.localPath, { rootDirs, tag: "LifeMonitor/move", account: accountInfo.name });
      }

      return result;
    }

    // ========= 有 oldEntry 但 oldMapping 为 null =========
    // 文件从映射 A 移动到映射 B：用兜底函数清理旧路径的 STRM
    const cleanup = tryCleanupOldStrmByPath(
      accountInfo.name,
      oldEntry.path,
      oldEntry.fileName,
      event.file_category,
      config.pathMappings
    );
    if (cleanup.deleted.length > 0) {
      console.info(`[LifeMonitor] move 兜底清理旧 STRM: ${cleanup.deleted.join(", ")}`);
    }
    if (cleanup.errors.length > 0) {
      console.warn(`[LifeMonitor] move 兜底清理出错: ${cleanup.errors.join("; ")}`);
    }
    // 继续走 create 流程在新路径生成
    const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
    createResult.action = "move";
    createResult.message = `[fallback-cleanup:${cleanup.deleted.length}] ${createResult.message || ""}`;
    return createResult;
  }

  // 无 oldEntry — 首次在此路径出现，直接创建
  return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
}

/**
 * 清理旧目录中的残留 STRM（local_move 模式下目标已存在时使用）
 */
async function cleanupResidualStrmsInOldFolder(
  oldDir: string,
  newDir: string,
  config: LifeMonitorConfig,
  account: string
): Promise<string[]> {
  const deleted: string[] = [];
  try {
    if (!fs.existsSync(oldDir)) return deleted;
    if (!fs.existsSync(newDir)) return deleted;

    const oldStrms = fs.readdirSync(oldDir).filter((f) => f.endsWith(".strm"));
    for (const strmFile of oldStrms) {
      const oldPath = path.join(oldDir, strmFile);
      const newPath = path.join(newDir, strmFile);
      if (!fs.existsSync(newPath)) {
        const fileRes = deleteStrmFile(oldPath, { tag: "LifeMonitor/move", cleanRelated: false, account });
        if (fileRes.deleted) {
          deleted.push(oldPath);
          console.info(`[LifeMonitor] 清理残留 STRM: ${oldPath}`);
        }
      }
    }

    // 清理旧空目录
    if (config.removeEmptyDirs) {
      const rootDirs = getRootDirsFromMappings(config.pathMappings);
      removeEmptyParents(oldDir, { rootDirs, tag: "LifeMonitor/move", account });
    }
  } catch (e) {
    console.error(`[LifeMonitor] cleanupResidualStrmsInOldFolder 出错: ${e instanceof Error ? e.message : String(e)}`);
  }
  return deleted;
}

async function handleRenameEvent(
  accountInfo: AccountInfo,
  event: LifeEvent,
  config: LifeMonitorConfig,
  mapping: { localPath: string; relativePath: string },
  cloudPath: string
): Promise<EventProcessResult> {
  const result: EventProcessResult = {
    eventId: event.id,
    eventType: event.type,
    eventTypeName: BEHAVIOR_TYPE_TO_NAME[event.type] || "unknown",
    action: "rename",
    success: false,
    timestamp: Date.now(),
    filePath: cloudPath,
    localPath: mapping.localPath,
  };

  const isFolder = event.file_category === 0;
  const oldEntry = getFilePathEntry(accountInfo.name, event.file_id);

  // ====== 关键修复：rename 事件 file_name 是旧名，new_name 是新名 ======
  // 115 生活事件 API 对 rename 事件：file_name = 旧名, new_name = 新名
  const rawEv = event as Record<string, unknown>;
  const newName = String(
    rawEv["new_name"] || rawEv["new_file_name"] || rawEv["to_name"] || rawEv["to_file_name"] || ""
  );
  // 如果 new_name 为空（某些 API 版本可能不返回），回退到 file_name
  // 注意：此时 file_name 可能是新名也可能是旧名，需要结合 oldEntry 判断
  const effectiveNewName = newName || event.file_name;

  if (oldEntry) {
    const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);

    if (oldMapping) {
      if (isFolder) {
        // Folder rename
        if (fs.existsSync(oldMapping.localPath)) {
          if (fs.existsSync(mapping.localPath)) {
            const hasStrm = fs.readdirSync(mapping.localPath).some((f) => f.endsWith(".strm"));
            if (hasStrm) {
              // 目标含 STRM → 兜底清理旧目录残留后继续（不 return，保持 filePathDb 更新）
              const cleanedResidual = await cleanupResidualStrmsInOldFolder(oldMapping.localPath, mapping.localPath, config, accountInfo.name);
              result.success = true;
              result.message = `目标目录已存在且含 STRM，兜底清理残留 ${cleanedResidual.length} 条后跳过重命名`;
              // 注意：不 return，继续执行后面的 upsertFilePathEntry 更新 path 记录！
            } else {
              // 目标存在但无 STRM：先清理/搬迁旧目录中所有内容后再创建新的
              // 否则 oldMapping.localPath 及里面的 STRM 会完整遗留
              try {
                if (fs.existsSync(oldMapping.localPath)) {
                  // 批量更新子记录的路径前缀（在删除旧目录前同步 DB）
                  if (oldEntry?.path) {
                    const updatedCount = updatePathPrefixBatch(accountInfo.name, oldEntry.path, cloudPath);
                    if (updatedCount > 0) {
                      console.log(`[LifeMonitor] rename: 批量更新 ${updatedCount} 条子记录路径前缀: ${oldEntry.path} -> ${cloudPath}`);
                    }
                  }
                  // 1) 先把旧目录下所有 STRM 搬过去（避免重扫生成缺失 pickcode）
                  const oldStrms = fs.readdirSync(oldMapping.localPath).filter((f) => f.endsWith(".strm"));
                  for (const f of oldStrms) {
                    const src = path.join(oldMapping.localPath, f);
                    const dst = path.join(mapping.localPath, f);
                    if (!fs.existsSync(dst)) {
                      const content = readStrmContent(src);
                      if (content) syncStrmText(dst, content, { tag: "LifeMonitor/rename", account: accountInfo.name });
                    }
                    try { deleteStrmFile(src, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name }); } catch {}
                  }
                  // 2) 搬相关文件（字幕/nfo/图片等）
                  try {
                    const siblings = fs.readdirSync(oldMapping.localPath);
                    for (const sib of siblings) {
                      if (sib.endsWith(".strm")) continue;
                      const src = path.join(oldMapping.localPath, sib);
                      const dst = path.join(mapping.localPath, sib);
                      const st = fs.statSync(src);
                      if (st.isFile() && !fs.existsSync(dst)) {
                        try { fs.renameSync(src, dst); } catch {}
                      }
                    }
                  } catch {}
                  // 3) 删除旧目录本身
                  try {
                    deleteStrmDir(oldMapping.localPath, { tag: "LifeMonitor/rename", account: accountInfo.name });
                  } catch {
                    if (config.removeEmptyDirs) {
                      removeEmptyParents(oldMapping.localPath, { rootDirs: getRootDirsFromMappings(config.pathMappings), tag: "LifeMonitor/rename", account: accountInfo.name });
                    }
                  }
                }
              } catch (e) {
                console.warn(`[LifeMonitor] rename 目标存在时搬迁旧目录失败: ${e instanceof Error ? e.message : String(e)}`);
              }
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          } else {
            fs.renameSync(oldMapping.localPath, mapping.localPath);
            // 批量更新子记录的路径前缀
            if (oldEntry?.path) {
              const updatedCount = updatePathPrefixBatch(accountInfo.name, oldEntry.path, cloudPath);
              if (updatedCount > 0) {
                console.log(`[LifeMonitor] rename: 批量更新 ${updatedCount} 条子记录路径前缀: ${oldEntry.path} -> ${cloudPath}`);
              }
            }
            result.success = true;
            result.message = `文件夹已重命名: ${oldMapping.localPath} -> ${mapping.localPath}`;
          }
        } else {
          // Old directory doesn't exist, create new folder and generate STRM files
          return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        }
      } else {
        // File rename: rename the STRM file
        const oldStrmFileName = getStrmFileName(oldEntry.fileName);
        const oldStrmPath = path.join(
          path.dirname(oldMapping.localPath),
          oldStrmFileName
        );
        // 修复：用 effectiveNewName（new_name 字段）而非 event.file_name（旧名）
        const newStrmFileName = getStrmFileName(effectiveNewName);
        const newStrmPath = path.join(path.dirname(mapping.localPath), newStrmFileName);

        if (fs.existsSync(oldStrmPath)) {
          const newDir = path.dirname(newStrmPath);
          if (!fs.existsSync(newDir)) {
            fs.mkdirSync(newDir, { recursive: true });
          }

          if (path.dirname(oldStrmPath) === path.dirname(newStrmPath)) {
            // Same directory, simple rename
            if (fs.existsSync(newStrmPath) && oldStrmPath !== newStrmPath) {
              deleteStrmFile(newStrmPath, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name });
            }
            if (oldStrmPath !== newStrmPath) {
              fs.renameSync(oldStrmPath, newStrmPath);
            }
            // Update STRM content with new cloud path
            if (isValidPickCode(event.pick_code)) {
              const newContent = generateStrmContent(
                cloudPath,
                config.strmPrefix || "",
                config.enablePathEncoding || false,
                {
                  enable302: config.enable302,
                  account: accountInfo.name,
                  pickcode: event.pick_code,
                  fileName: event.file_name,
                }
              );
              syncStrmText(newStrmPath, newContent, { tag: "LifeMonitor/rename", account: accountInfo.name });
            }
          } else {
            // Different directories
            const content = readStrmContent(oldStrmPath);
            if (content) {
              syncStrmText(newStrmPath, content, { tag: "LifeMonitor/rename", account: accountInfo.name });
              deleteStrmFile(oldStrmPath, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name });
            } else {
              // 兜底：先递归搜索旧 STRM 再创建
              const foundOldStrms = findStrmRecursive(
                path.dirname(oldMapping.localPath),
                oldStrmFileName
              );
              for (const p of foundOldStrms) {
                try { deleteStrmFile(p, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name }); console.info(`[LifeMonitor] rename 兜底清理旧 STRM: ${p}`); } catch {}
              }
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          }

          // Handle related files (subtitles, nfo, etc.)
          handleRelatedFileRenames(oldStrmPath, newStrmPath, oldEntry.fileName, effectiveNewName);

          result.success = true;
          result.message = `STRM 已重命名: ${oldStrmPath} -> ${newStrmPath}`;
        } else {
          // 兜底：oldStrmPath 不在预期位置，可能在其他子目录，递归按文件名搜索并清理
          console.info(`[LifeMonitor] rename: expected oldStrmPath=${oldStrmPath} 不存在，启动递归兜底搜索`);
          let cleanedCount = 0;
          for (const m of config.pathMappings) {
            if (m.account && m.account !== accountInfo.name) continue;
            if (!fs.existsSync(m.localPath)) continue;
            const found = findStrmRecursive(m.localPath, oldStrmFileName);
            for (const p of found) {
              try {
                const delRes = deleteStrmFile(p, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name });
                if (delRes.deleted) {
                  cleanedCount++;
                  console.info(`[LifeMonitor] rename 递归搜索清理旧 STRM: ${p}`);
                }
              } catch (e) {
                console.warn(`[LifeMonitor] rename 递归搜索删除失败 ${p}: ${e instanceof Error ? e.message : String(e)}`);
              }
            }
          }
          if (cleanedCount === 0 && oldEntry.fileName !== effectiveNewName) {
            // 也按新文件名检查一遍，防止之前已经有重名的残留
            for (const m of config.pathMappings) {
              if (m.account && m.account !== accountInfo.name) continue;
              if (!fs.existsSync(m.localPath)) continue;
              const found = findStrmRecursive(m.localPath, newStrmFileName);
              for (const p of found) {
                try { const delRes = deleteStrmFile(p, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name }); if (delRes.deleted) { cleanedCount++; console.info(`[LifeMonitor] rename 兜底清理新文件名冲突 STRM: ${p}`); } } catch {}
              }
            }
          }
          return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        }
      }

      // Update tracking
      upsertFilePathEntry(accountInfo.name, {
        fileId: event.file_id,
        path: cloudPath,
        fileName: effectiveNewName,
        parentId: event.parent_id,
        pickCode: event.pick_code || oldEntry.pickCode,
        updateTime: event.update_time,
      });

      if (config.removeEmptyDirs) {
        const rootDirs = getRootDirsFromMappings(config.pathMappings);
        removeEmptyParents(oldMapping.localPath, { rootDirs, tag: "LifeMonitor/rename", account: accountInfo.name });
      }

      return result;
    }

    // ========= 有 oldEntry 但 oldMapping 为 null（跨映射重命名，相当于改名+换路径移出老映射） =========
    // 用和 move-outside 相同强度的多层兜底
    console.info(`[LifeMonitor] rename-cross-mapping: fileId=${event.file_id}, fileName=${oldEntry.fileName}, oldEntry.path=${oldEntry.path}, newCloudPath=${cloudPath}, fileCategory=${event.file_category}`);
    const cleanup = tryCleanupOldStrmByPath(
      accountInfo.name,
      oldEntry.path,
      oldEntry.fileName,
      event.file_category,
      config.pathMappings
    );
    // 兜底 1：guessed path
    if (cleanup.deleted.length === 0) {
      for (const m of config.pathMappings) {
        if (m.account && m.account !== accountInfo.name) continue;
        const guessedPath = (m.cloudPath.endsWith("/") ? m.cloudPath : (m.cloudPath + "/")) + oldEntry.fileName;
        const deeper = tryCleanupOldStrmByPath(
          accountInfo.name,
          guessedPath,
          oldEntry.fileName,
          event.file_category,
          [m]
        );
        cleanup.deleted.push(...deeper.deleted);
        cleanup.errors.push(...deeper.errors);
      }
    }
    // 兜底 2：文件事件按文件名递归搜索所有映射本地路径
    if (cleanup.deleted.length === 0 && event.file_category === 1) {
      const strmName = getStrmFileName(oldEntry.fileName);
      for (const m of config.pathMappings) {
        if (m.account && m.account !== accountInfo.name) continue;
        if (!fs.existsSync(m.localPath)) continue;
        try {
          const found = findStrmRecursive(m.localPath, strmName);
          for (const p of found) {
            const delRes = deleteStrmFile(p, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name });
            if (delRes.deleted) {
              cleanup.deleted.push(`[file-search] ${p}`);
              console.info(`[LifeMonitor] rename 跨映射递归清理 STRM: ${p}`);
            }
          }
        } catch (e) {
          cleanup.errors.push(`${m.localPath} 搜索 ${strmName} 失败: ${e instanceof Error ? e.message : String(e)}`);
        }
      }
    }
    // 兜底 2b：文件夹事件按目录名递归搜索所有映射本地路径
    if (cleanup.deleted.length === 0 && event.file_category === 0) {
      const rootDirs = getRootDirsFromMappings(config.pathMappings);
      const newLocalResolved = path.resolve(mapping.localPath);
      for (const m of config.pathMappings) {
        if (m.account && m.account !== accountInfo.name) continue;
        if (!fs.existsSync(m.localPath)) continue;
        try {
          const foundDirs = findDirRecursive(m.localPath, oldEntry.fileName);
          for (const d of foundDirs) {
            const resolved = path.resolve(d);
            // 保护新路径本身及其直接父目录
            if (resolved === newLocalResolved || resolved === path.dirname(newLocalResolved)) continue;
            if (rootDirs.has(resolved)) continue;
            const dirRes = deleteStrmDir(d, { tag: "LifeMonitor/rename-cross-mapping", account: accountInfo.name });
            if (dirRes.deleted) {
              cleanup.deleted.push(`[dir-search] ${d}`);
              console.info(`[LifeMonitor] rename 跨映射递归清理文件夹: ${d}`);
              removeEmptyParents(d, { rootDirs, tag: "LifeMonitor/rename-cross-mapping", account: accountInfo.name });
            }
          }
        } catch (e) {
          cleanup.errors.push(`${m.localPath} 搜索目录 ${oldEntry.fileName} 失败: ${e instanceof Error ? e.message : String(e)}`);
        }
      }
    }
    if (cleanup.deleted.length > 0) {
      console.info(`[LifeMonitor] rename 兜底清理旧 STRM: ${cleanup.deleted.join(", ")}`);
    }
    // 无条件空目录清理
    if (config.removeEmptyDirs) {
      const rootDirs = getRootDirsFromMappings(config.pathMappings);
      const oldMatched = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);
      if (oldMatched) removeEmptyParents(oldMatched.localPath, { rootDirs, tag: "LifeMonitor/rename", account: accountInfo.name });
      for (const m of config.pathMappings) {
        removeEmptyParents(m.localPath, { rootDirs, tag: "LifeMonitor/rename", account: accountInfo.name });
      }
    }
    const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
    createResult.action = "rename";
    createResult.message = `[fallback-cleanup:${cleanup.deleted.length}] ${createResult.message || ""}`;
    return createResult;
  }

  // ====== No old entry：DB 无记录但本地可能仍有旧 STRM/文件夹（rename 前未跟踪） ======
  // rename 事件中 event.file_name 是旧名，effectiveNewName 是新名。
  // 按旧名递归搜索所有映射本地路径，清理旧 STRM 文件 / 旧目录树后再创建新内容。
  console.info(
    `[LifeMonitor] rename-no-entry: fileId=${event.file_id}, oldName="${event.file_name}", newName="${effectiveNewName}", fileCategory=${event.file_category}, newCloudPath=${cloudPath}`
  );
  const rootDirs = getRootDirsFromMappings(config.pathMappings);
  const newLocalResolved = path.resolve(mapping.localPath);
  let noEntryCleaned = 0;

  if (event.file_category === 0) {
    // 文件夹 rename：递归查找旧名目录并删除整棵树
    for (const m of config.pathMappings) {
      if (m.account && m.account !== accountInfo.name) continue;
      if (!fs.existsSync(m.localPath)) continue;
      try {
        const foundDirs = findDirRecursive(m.localPath, event.file_name);
        for (const d of foundDirs) {
          const resolved = path.resolve(d);
          // 保护新路径本身及其祖先：不能删掉新目录或它的父目录
          if (resolved === newLocalResolved || resolved === path.dirname(newLocalResolved)) continue;
          if (rootDirs.has(resolved)) continue;
          const dirRes = deleteStrmDir(d, { tag: "LifeMonitor/rename-no-entry", account: accountInfo.name });
          if (dirRes.deleted) {
            noEntryCleaned++;
            console.info(`[LifeMonitor] rename-no-entry: 清理旧文件夹 ${d}`);
            removeEmptyParents(d, { rootDirs, tag: "LifeMonitor/rename-no-entry", account: accountInfo.name });
          }
        }
      } catch (e) {
        console.warn(`[LifeMonitor] rename-no-entry 目录搜索失败 ${m.localPath}: ${e instanceof Error ? e.message : String(e)}`);
      }
    }
  } else {
    // 文件 rename：递归查找旧名 STRM 并删除（排除新路径）
    const oldStrmName = getStrmFileName(event.file_name);
    for (const m of config.pathMappings) {
      if (m.account && m.account !== accountInfo.name) continue;
      if (!fs.existsSync(m.localPath)) continue;
      try {
        const found = findStrmRecursive(m.localPath, oldStrmName);
        for (const p of found) {
          if (path.resolve(p) === newLocalResolved) continue; // 不删新文件
          const delRes = deleteStrmFile(p, { tag: "LifeMonitor/rename-no-entry", cleanRelated: false, account: accountInfo.name });
          if (delRes.deleted) {
            noEntryCleaned++;
            console.info(`[LifeMonitor] rename-no-entry: 清理旧 STRM ${p}`);
          }
        }
      } catch (e) {
        console.warn(`[LifeMonitor] rename-no-entry STRM 搜索失败 ${m.localPath}: ${e instanceof Error ? e.message : String(e)}`);
      }
    }
  }

  // 无条件空目录清理（兜底）
  if (config.removeEmptyDirs) {
    for (const m of config.pathMappings) {
      removeEmptyParents(m.localPath, { rootDirs, tag: "LifeMonitor/rename-no-entry", account: accountInfo.name });
    }
  }

  const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
  createResult.action = "rename";
  createResult.message = `[no-entry-cleanup:${noEntryCleaned}] ${createResult.message || ""}`;
  return createResult;
}

function handleRelatedFileRenames(
  oldStrmPath: string,
  newStrmPath: string,
  oldFileName: string,
  newFileName: string
) {
  const oldDir = path.dirname(oldStrmPath);
  const oldStem = path.extname(oldFileName) ? path.basename(oldFileName, path.extname(oldFileName)) : oldFileName;
  const newStem = path.extname(newFileName) ? path.basename(newFileName, path.extname(newFileName)) : newFileName;

  try {
    const siblings = fs.readdirSync(oldDir);
    for (const sibling of siblings) {
      if (sibling.endsWith(".strm")) continue;
      if (!sibling.startsWith(oldStem)) continue;

      const ext = sibling.slice(oldStem.length);
      const newName = newStem + ext;
      const oldPath = path.join(oldDir, sibling);
      const newPath = path.join(oldDir, newName);

      if (!fs.existsSync(newPath)) {
        fs.renameSync(oldPath, newPath);
        console.log(`[LifeMonitor] Related file renamed: ${sibling} -> ${newName}`);
      }
    }
  } catch {
    // Ignore errors for related files
  }
}

async function processEvent(
  accountInfo: AccountInfo,
  event: LifeEvent,
  config: LifeMonitorConfig
): Promise<EventProcessResult> {
  const eventType = event.type;
  const result: EventProcessResult = {
    eventId: event.id,
    eventType,
    eventTypeName: BEHAVIOR_TYPE_TO_NAME[eventType] || "unknown",
    action: "skip",
    success: false,
    timestamp: Date.now(),
  };

  try {
    // P5.A: 限流 — 每个事件处理前等待 115 API 令牌，避免高频调用触发封控
    await waitFor115ApiToken();
    // ====== 关键修复：rename 事件的 file_name 是旧名，新名在 new_name 字段 ======
    // 115 生活事件 API 对 rename 事件（type 20/24）：
    //   file_name = 旧名字, new_name = 新名字
    // 如果用 file_name 构造路径，会得到旧路径 → 匹配到旧映射 → handler 收到的 mapping 和 oldMapping 相同 → no-op
    // 修复：对 rename 事件提取 new_name，用它来构造新路径
    const isRenameEvent = RENAME_EVENT_TYPES.has(eventType);
    let renameNewName = "";
    if (isRenameEvent) {
      // 115 API 可能用 new_name 或其他字段名
      const rawEvent = event as Record<string, unknown>;
      renameNewName = String(
        rawEvent["new_name"] || rawEvent["new_file_name"] || rawEvent["to_name"] || rawEvent["to_file_name"] || ""
      );
      // 诊断日志：打印 rename 事件的全部字段，帮助确认 API 返回格式
      console.info(
        `[LifeMonitor] rename-event-raw: type=${eventType}, fileId=${event.file_id}, file_name="${event.file_name}", new_name="${renameNewName}", file_category=${event.file_category}, parent_id=${event.parent_id}, pick_code="${event.pick_code}", allKeys=${Object.keys(rawEvent).join(",")}`
      );
    }

    // 对 rename 事件用 new_name 构造新路径；其他事件用 file_name（即当前路径）
    const cloudPath = await resolveEventPath(
      accountInfo,
      event,
      isRenameEvent && renameNewName ? renameNewName : undefined
    );
    if (!cloudPath) {
      result.message = "无法解析文件路径";
      return result;
    }
    result.filePath = cloudPath;

    // 对 rename 事件，如果 new_name 为空，说明 API 可能没返回该字段
    // 此时 cloudPath 是用旧名构造的旧路径，后续 handler 仍需正确处理
    if (isRenameEvent && !renameNewName) {
      console.warn(
        `[LifeMonitor] rename 事件未找到 new_name 字段! fileId=${event.file_id}, file_name="${event.file_name}", cloudPath="${cloudPath}". 尝试通过 filePathDb 旧记录推断新路径。`
      );
    }

    // For move/delete/rename events: 不提前更新 filePathDb，交给具体 handler 在处理完成后更新
    // （双写保护：防止事件处理过程中 oldEntry 被提前覆盖导致兜底查找失败）
    const isMutationEvent = MOVE_EVENT_TYPES.has(eventType) || RENAME_EVENT_TYPES.has(eventType) || DELETE_EVENT_TYPES.has(eventType);

    // 诊断日志：对 move/delete 事件也打印关键字段
    if (isMutationEvent && !isRenameEvent) {
      const oldEntry = getFilePathEntry(accountInfo.name, event.file_id);
      console.info(
        `[LifeMonitor] ${BEHAVIOR_TYPE_TO_NAME[eventType] || "event"}-raw: type=${eventType}, fileId=${event.file_id}, file_name="${event.file_name}", file_category=${event.file_category}, parent_id=${event.parent_id}, cloudPath="${cloudPath}", oldEntry.path="${oldEntry?.path || "(none)"}", oldEntry.fileName="${oldEntry?.fileName || "(none)"}"`
      );
    }

    if (!isMutationEvent && (isValidPickCode(event.pick_code) || event.file_category === 0)) {
      upsertFilePathEntry(accountInfo.name, {
        fileId: event.file_id,
        path: cloudPath,
        fileName: event.file_name,
        parentId: event.parent_id,
        pickCode: event.pick_code || "",
        updateTime: event.update_time,
      });
    }

    const mapping = matchPathMapping(cloudPath, config.pathMappings, accountInfo.name);
    if (!mapping) {
      // ====== 关键修复：move/rename/delete 事件在新路径不匹配时，
      // 将其视为"从监控区域移出"来清理旧 STRM ======
      if (MOVE_EVENT_TYPES.has(eventType) || RENAME_EVENT_TYPES.has(eventType)) {
        const oldEntry = getFilePathEntry(accountInfo.name, event.file_id);
        if (oldEntry) {
          console.info(`[LifeMonitor] move-outside: fileId=${event.file_id}, fileName=${oldEntry.fileName}, oldEntry.path=${oldEntry.path}, newCloudPath=${cloudPath}, fileCategory=${event.file_category}`);
          const cleanup = tryCleanupOldStrmByPath(
            accountInfo.name,
            oldEntry.path,
            oldEntry.fileName,
            event.file_category,
            config.pathMappings
          );
          // 兜底 1：如果 oldEntry.path 过期，尝试用每个 mapping.cloudPath + fileName 组合猜测
          if (cleanup.deleted.length === 0) {
            for (const m of config.pathMappings) {
              if (m.account && m.account !== accountInfo.name) continue;
              const guessedPath = (m.cloudPath.endsWith("/") ? m.cloudPath : (m.cloudPath + "/")) + oldEntry.fileName;
              const deeper = tryCleanupOldStrmByPath(
                accountInfo.name,
                guessedPath,
                oldEntry.fileName,
                event.file_category,
                [m]
              );
              cleanup.deleted.push(...deeper.deleted);
              cleanup.errors.push(...deeper.errors);
            }
          }
          // 兜底 2：文件事件（非文件夹）按文件名在所有映射本地路径中搜索 STRM 并删除
          if (cleanup.deleted.length === 0 && event.file_category === 1) {
            const strmName = getStrmFileName(oldEntry.fileName);
            for (const m of config.pathMappings) {
              if (m.account && m.account !== accountInfo.name) continue;
              if (!fs.existsSync(m.localPath)) continue;
              try {
                const found = findStrmRecursive(m.localPath, strmName);
                for (const p of found) {
                  const delRes = deleteStrmFile(p, { tag: "LifeMonitor/move", cleanRelated: false, account: accountInfo.name });
                  if (delRes.deleted) {
                    cleanup.deleted.push(`[file-search] ${p}`);
                    console.info(`[LifeMonitor] 文件名递归搜索清理 STRM: ${p}`);
                  }
                }
              } catch (e) {
                cleanup.errors.push(`${m.localPath} 搜索 ${strmName} 失败: ${e instanceof Error ? e.message : String(e)}`);
              }
            }
          }
          // 兜底 2b：文件夹事件按目录名在所有映射本地路径中递归搜索并删除
          // （oldEntry.path 过期或嵌套较深时，guessed path 可能也命中失败）
          if (cleanup.deleted.length === 0 && event.file_category === 0) {
            const rootDirs = getRootDirsFromMappings(config.pathMappings);
            for (const m of config.pathMappings) {
              if (m.account && m.account !== accountInfo.name) continue;
              if (!fs.existsSync(m.localPath)) continue;
              try {
                const foundDirs = findDirRecursive(m.localPath, oldEntry.fileName);
                for (const d of foundDirs) {
                  // 保护根目录边界，避免误删映射根本身
                  if (rootDirs.has(path.resolve(d))) continue;
                  const dirRes = deleteStrmDir(d, { tag: "LifeMonitor/move-dir-search", account: accountInfo.name });
                  if (dirRes.deleted) {
                    cleanup.deleted.push(`[dir-search] ${d}`);
                    console.info(`[LifeMonitor] 目录名递归搜索清理文件夹: ${d}`);
                    removeEmptyParents(d, { rootDirs, tag: "LifeMonitor/move-dir-search", account: accountInfo.name });
                  }
                }
              } catch (e) {
                cleanup.errors.push(`${m.localPath} 搜索目录 ${oldEntry.fileName} 失败: ${e instanceof Error ? e.message : String(e)}`);
              }
            }
          }
          // 无论 deleted 是否有值，都从 filePathDb 移除该记录（它已不在监控范围内）
          removeFilePathEntry(accountInfo.name, event.file_id);

          // 无论 cleanup.deleted 是否有值，只要启用了 removeEmptyDirs，就做一次空目录清理
          // （避免 oldEntry.path 虽然匹配但目录为空无 STRM 可删、或 guessed 清理后仍残留空父目录的情况）
          if (config.removeEmptyDirs) {
            const rootDirs = getRootDirsFromMappings(config.pathMappings);
            const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);
            if (oldMapping) removeEmptyParents(oldMapping.localPath, { rootDirs, tag: "LifeMonitor/move", account: accountInfo.name });
            // 也在所有 mapping 的 localPath 下扫一遍（安全，只删真正空的）
            for (const m of config.pathMappings) {
              removeEmptyParents(m.localPath, { rootDirs, tag: "LifeMonitor/move", account: accountInfo.name });
            }
          }

          if (cleanup.deleted.length > 0) {
            result.action = "remove";
            result.success = true;
            result.message = `移出监控范围，清理 ${cleanup.deleted.length} 个旧 STRM/目录`;
            logEventTrace("move-outside", event, null, result);
            return result;
          }

          result.action = "skip";
          result.message = `路径不在监控范围内（oldEntry.path=${oldEntry.path}，已尝试 guessed 路径及空目录清理，仍无残留 STRM）: ${cloudPath}`;
          logEventTrace("move-outside-skip", event, null, result);
          return result;
        }
        result.action = "skip";
        result.message = `路径不在监控范围内（无 filePathDb 记录）: ${cloudPath}`;
        // 兜底 3：即使 DB 无记录，也应按 fileName 在本地递归搜索并清理 STRM 和文件夹
        // （旧数据中文件夹本身可能从未写入 DB，但本地 STRM 文件确实存在）
        if (config.removeEmptyDirs || event.file_category === 0) {
          const rootDirs = getRootDirsFromMappings(config.pathMappings);
          let localDeletedCount = 0;
          for (const m of config.pathMappings) {
            if (m.account && m.account !== accountInfo.name) continue;
            if (!fs.existsSync(m.localPath)) continue;
            try {
              if (event.file_category === 0) {
                // 文件夹事件：递归搜索同名目录（不局限于映射根直接子级，
                // 文件夹可能嵌套在映射的子目录如 /电影/分类/小王子）
                const candidateDirs = findDirRecursive(m.localPath, event.file_name);
                for (const candidateDir of candidateDirs) {
                  // 保护根目录边界，避免误删映射根本身
                  if (rootDirs.has(path.resolve(candidateDir))) continue;
                  if (fs.existsSync(candidateDir) && fs.statSync(candidateDir).isDirectory()) {
                    deleteStrmDir(candidateDir, { tag: "LifeMonitor/move-outside-nodb", account: accountInfo.name });
                    localDeletedCount++;
                    console.info(`[LifeMonitor] move-outside-nodb: 清理文件夹(无DB记录) ${candidateDir}`);
                    // 清理空父目录
                    removeEmptyParents(candidateDir, { rootDirs, tag: "LifeMonitor/move-outside-nodb", account: accountInfo.name });
                  }
                }
              } else {
                // 文件事件：按文件名搜索 STRM
                const strmName = getStrmFileName(event.file_name);
                const found = findStrmRecursive(m.localPath, strmName);
                for (const p of found) {
                  const delRes = deleteStrmFile(p, { tag: "LifeMonitor/move-outside-nodb", cleanRelated: false, account: accountInfo.name });
                  if (delRes.deleted) {
                    localDeletedCount++;
                    console.info(`[LifeMonitor] move-outside-nodb: 清理STRM(无DB记录) ${p}`);
                  }
                }
              }
            } catch (e) {
              console.warn(`[LifeMonitor] move-outside-nodb 搜索失败 ${m.localPath}: ${e instanceof Error ? e.message : String(e)}`);
            }
          }
          if (localDeletedCount > 0) {
            result.action = "remove";
            result.success = true;
            result.message = `移出监控范围(无DB记录)，本地清理 ${localDeletedCount} 个 STRM/文件夹`;
            logEventTrace("move-outside-nodb", event, null, result);
            return result;
          }
        }
        logEventTrace("move-outside-miss", event, null, result);
        return result;
      }

      if (DELETE_EVENT_TYPES.has(eventType)) {
        // delete 事件也兜底清理
        const cleanup = tryCleanupOldStrmByPath(
          accountInfo.name,
          cloudPath,
          event.file_name || "",
          event.file_category,
          config.pathMappings
        );
        if (config.removeEmptyDirs) {
          const rootDirs = getRootDirsFromMappings(config.pathMappings);
          for (const m of config.pathMappings) {
            removeEmptyParents(m.localPath, { rootDirs, tag: "LifeMonitor/delete", account: accountInfo.name });
          }
        }
        if (cleanup.deleted.length > 0) {
          result.action = "remove";
          result.success = true;
          result.message = `删除事件移出监控范围，清理 ${cleanup.deleted.length} 个残留`;
          logEventTrace("delete-outside", event, null, result);
          return result;
        }
        result.action = "skip";
        result.message = `删除路径不在监控范围内: ${cloudPath}`;
        logEventTrace("delete-outside-skip", event, null, result);
        return result;
      }

      result.action = "skip";
      result.message = `路径不在监控范围内: ${cloudPath}`;
      return result;
    }
    result.localPath = mapping.localPath;

    // For file events, check media extension (统一使用全局 strmExtensions)
    if (event.file_category === 1 && !isMediaFile(event.file_name, getStrmExtensions())) {
      result.action = "skip";
      result.message = `非媒体文件: ${event.file_name}`;
      return result;
    }

    // 最小文件大小过滤（对文件事件生效，文件夹不受限）
    const minSize = config.minFileSize || 0;
    if (
      minSize > 0 &&
      event.file_category === 1 &&
      typeof event.file_size === "number" &&
      event.file_size < minSize
    ) {
      result.action = "skip";
      result.message = `文件小于最小阈值 (${event.file_size} < ${minSize} bytes): ${event.file_name}`;
      return result;
    }

    if (CREATE_EVENT_TYPES.has(eventType) && config.eventTypes.create) {
      const r = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
      logEventTrace("create", event, mapping, r);
      return r;
    } else if (DELETE_EVENT_TYPES.has(eventType) && config.eventTypes.remove) {
      const r = await handleDeleteEvent(accountInfo, event, config, mapping, cloudPath);
      logEventTrace("delete", event, mapping, r);
      return r;
    } else if (MOVE_EVENT_TYPES.has(eventType) && config.eventTypes.move) {
      const r = await handleMoveEvent(accountInfo, event, config, mapping, cloudPath);
      logEventTrace("move", event, mapping, r);
      return r;
    } else if (RENAME_EVENT_TYPES.has(eventType) && config.eventTypes.rename) {
      const r = await handleRenameEvent(accountInfo, event, config, mapping, cloudPath);
      logEventTrace("rename", event, mapping, r);
      return r;
    } else {
      result.action = "skip";
      result.message = `事件类型 ${eventType} 未启用处理`;
      return result;
    }
  } catch (err) {
    result.action = "error";
    result.message = err instanceof Error ? err.message : String(err);
    console.error(`[LifeMonitor] processEvent error type=${eventType} file_id=${event.file_id}: ${result.message}`);
    return result;
  }
}

/**
 * 结构化事件日志追踪 — 为每个事件处理结果输出一行可追溯日志
 */
function logEventTrace(
  action: string,
  event: LifeEvent,
  mapping: { localPath: string; relativePath: string } | null,
  result: EventProcessResult
) {
  const tag = result.success ? "info" : "warn";
  const isFolder = event.file_category === 0 ? "folder" : "file";
  const extra =
    result.action === "skip" ? ` reason=${result.message?.slice(0, 60)}` : "";
  console[tag === "info" ? "info" : "warn"](
    `[STRM-SYNC] ${action} ${isFolder} file_id=${event.file_id} ` +
    `path=${mapping?.relativePath || "-"} → local=${mapping?.localPath || "-"} ` +
    `status=${result.success ? "ok" : "fail"} action=${result.action} ` +
    `${result.message ? `msg=${result.message.slice(0, 100)}` : ""}${extra}`
  );
}

// ==================== Emby Refresh (debounced) ====================

const EMBY_DEBOUNCE_MS = 3000;
const EMBY_MIN_INTERVAL_MS = 30000;

function scheduleEmbyRefresh(account: string) {
  const existing = embyDebounceTimers.get(account);
  if (existing) clearTimeout(existing);

  const timer = setTimeout(async () => {
    embyDebounceTimers.delete(account);

    const now = Date.now();
    const lastFire = embyLastFireTime.get(account) || 0;
    if (now - lastFire < EMBY_MIN_INTERVAL_MS) {
      const waitMs = EMBY_MIN_INTERVAL_MS - (now - lastFire);
      embyDebounceTimers.set(
        account,
        setTimeout(() => {
          embyDebounceTimers.delete(account);
          embyLastFireTime.set(account, Date.now());
          notifyEmbyRefresh();
        }, waitMs)
      );
      return;
    }

    embyLastFireTime.set(account, now);
    console.log(`[LifeMonitor] 触发 Emby 刷新 (account=${account})`);
    notifyEmbyRefresh();
  }, EMBY_DEBOUNCE_MS);

  embyDebounceTimers.set(account, timer);
}

// ==================== Consistency Check (lightweight) ====================

const CONSISTENCY_CHECK_INTERVAL_MS = 10 * 60 * 1000; // 10 minutes
const consistencyCheckTimers = new Map<string, number>();

/**
 * 轻量级一致性检查：定期遍历本地 STRM 文件，
 * 清理可能因事件丢失而残留的失效 STRM（如源文件已被删除但未收到事件）。
 * 每 10 分钟最多执行一次，且只在事件处理成功后触发。
 */
function maybeRunConsistencyCheck(account: string, config: LifeMonitorConfig) {
  const now = Date.now();
  const last = consistencyCheckTimers.get(account) || 0;
  if (now - last < CONSISTENCY_CHECK_INTERVAL_MS) return;
  consistencyCheckTimers.set(account, now);

  // 异步执行，不阻塞主循环
  setTimeout(async () => {
    try {
      const stats = { scanned: 0, cleaned: 0, errors: 0 };
      for (const mapping of config.pathMappings || []) {
        const accountFilter = mapping.account;
        if (accountFilter && accountFilter !== account) continue;

        // P0-C: localPath 统一用 path.resolve 解析（与事件处理器保持一致）
        const localDir = path.resolve(mapping.localPath);
        if (!fs.existsSync(localDir)) continue;

        const cleanDir = (dir: string) => {
          try {
            const entries = fs.readdirSync(dir, { withFileTypes: true });
            for (const entry of entries) {
              const fullPath = path.join(dir, entry.name);
              if (entry.isDirectory()) {
                cleanDir(fullPath);
              } else if (entry.isFile() && entry.name.endsWith(".strm")) {
                stats.scanned++;
                // 检查 STRM 内容指向的源文件是否仍存在
                try {
                  const content = fs.readFileSync(fullPath, "utf-8");
                  const url = content.trim();
                  // 简单检查：如果 URL 格式异常或为空，清理它
                  if (!url || (!url.startsWith("http") && !url.startsWith("/"))) {
                    const delRes = deleteStrmFile(fullPath, { tag: "LifeMonitor/consistencyCheck", cleanRelated: false, account });
                    if (delRes.deleted) stats.cleaned++;
                    console.info(`[ConsistencyCheck] 清理无效 STRM: ${fullPath}`);
                  }
                } catch {
                  // 读取失败的 STRM 也清理
                  try { const delRes = deleteStrmFile(fullPath, { tag: "LifeMonitor/consistencyCheck", cleanRelated: false, account }); if (delRes.deleted) stats.cleaned++; } catch {}
                }
              }
            }
            // 清理空目录
            try {
              const remaining = fs.readdirSync(dir);
              if (remaining.length === 0 && dir !== localDir) {
                fs.rmdirSync(dir);
              }
            } catch {}
          } catch {
            stats.errors++;
          }
        };
        cleanDir(localDir);
      }

      if (stats.scanned > 0) {
        console.info(
          `[ConsistencyCheck] ${account}: 扫描 ${stats.scanned} STRM, 清理 ${stats.cleaned}, 错误 ${stats.errors}`
        );
      }
    } catch (e) {
      console.error(`[ConsistencyCheck] ${account} 异常:`, e);
    }
  }, 0);
}

// ==================== Polling ====================

async function oncePoll(account: string): Promise<void> {
  // 入口检查：若已被 stop 则直接退出，避免 in-flight poll 继续写状态
  if (!monitorStates.get(account)?.running) {
    return;
  }

  const pollStatus = tryPollMonitor(account);
  if (!pollStatus.ok) {
    console.log(
      `[eventMonitor] monitor suspended for ${account} (fullscan active), skip this poll. resume @ ${new Date(pollStatus.suspendedUntil!).toISOString()}`
    );
    return;
  }

  // 读取一次 settings，后续所有函数共用
  const settings = readSettings();
  let config = getLifeMonitorConfig();

  // 使用统一的 STRM 设置解析（全局默认 + 任务级覆盖 + 302 拼接）
  const resolvedStrm = resolveStrmSettings(account, null, settings);
  config = { ...config, strmPrefix: resolvedStrm.strmPrefix, enablePathEncoding: resolvedStrm.enablePathEncoding, enable302: resolvedStrm.enable302 };

  const accounts = readAccounts() as unknown as AccountInfo[];

  const accountInfo = accounts.find(
    (acc) => acc.name === account
  );
  if (!accountInfo) {
    updateState(account, {
      status: "error",
      lastError: `Account not found: ${account}`,
    });
    return;
  }

  if (!accountInfo.cookie) {
    updateState(account, {
      status: "error",
      lastError: `Account ${account} has no cookie`,
    });
    return;
  }

  const savedState = readState();
  const state = savedState[account];
  const hasSavedState = !!state?.fromTime;
  const firstPullMode = config.firstPullMode || "latest";

  // 首拉模式决定起始游标：
  // - latest: 从当前时间开始（只处理新事件）
  // - all: **首次（无断点）** 从 0 开始拉取全部历史；**后续 poll（已有断点）** 从保存的断点继续
  // - last: 从上次保存的断点继续；若无断点则退化为 latest
  let fromTime: number;
  let fromId: number;
  if (firstPullMode === "all" && !hasSavedState) {
    // 仅首次（lifeMonitorState.json 中无此账号记录）拉取全部历史
    fromTime = 0;
    fromId = 0;
  } else if (hasSavedState) {
    // latest / last / all-非首次 均优先使用已保存的断点，避免 poll 间新事件被跳过或重复处理历史
    fromTime = state!.fromTime;
    fromId = state!.fromId;
  } else {
    // 首次 poll：latest 从当前时间开始（不拉历史），last 退化为 latest
    fromTime = Math.floor(Date.now() / 1000);
    fromId = 0;
  }

  updateState(account, {
    status: "running",
    lastCheckTime: Date.now(),
  });

  const preferredApi = getPreferredApi(account);

  try {
    const { events, next_time, next_id } = await oncePullLifeEvents(
      accountInfo as AccountInfo,
      fromTime,
      fromId,
      preferredApi
    );

    // stop 后快速退出：不再处理事件，避免状态闪烁
    if (!monitorStates.get(account)?.running) {
      console.log(`[LifeMonitor] ${account}: monitor stopped during pull, skip event processing`);
      return;
    }

    // Reset 405 counter on success
    reset405Count(account);

    if (events.length === 0) {
      // 无新事件也要保存断点，下次 poll 从该时间继续，避免 poll 间事件丢失
      saveState(account, fromTime, fromId);
      updateState(account, {
        status: "running",
        lastFromTime: fromTime,
        lastFromId: fromId,
      });
      return;
    }

    console.log(`[LifeMonitor] Pulled ${events.length} events for ${account}`);

    // P3.2b: 单 poll 删除事件熔断 — 统计删除类事件数量
    const deleteEventTypes = new Set(DELETE_EVENT_TYPES);
    const deleteEvents = events.filter(e => deleteEventTypes.has(e.type));
    const deleteCount = deleteEvents.length;
    const totalCount = events.length;
    const deleteRatio = totalCount > 0 ? deleteCount / totalCount : 0;

    let effectiveEvents = events;

    if (deleteCount > 0 && (deleteCount > MAX_DELETE_EVENTS_PER_POLL || deleteRatio > DELETE_RATIO_THRESHOLD_PER_POLL)) {
      // 触发删除熔断：只保留非删除事件
      effectiveEvents = events.filter(e => !deleteEventTypes.has(e.type));
      console.error(
        `[LifeMonitor] ⚠️ 删除事件熔断触发! 删除事件数=${deleteCount}/${totalCount} (比例=${(deleteRatio*100).toFixed(1)}%), ` +
        `阈值: count>${MAX_DELETE_EVENTS_PER_POLL} 或 ratio>${DELETE_RATIO_THRESHOLD_PER_POLL*100}%。` +
        `已跳过本次 poll 的所有删除事件，请手动前往 settings 页面执行全量扫描确认后再清理。`
      );
      // 追加到生命周期日志让用户看到
      appendLifeEventLog(
        account,
        "delete",
        false,
        undefined,
        undefined,
        `⚠️ 删除事件熔断: 删除数=${deleteCount}/${totalCount}, 已跳过。请执行全量扫描确认`
      );
    }

    let processedCount = 0;
    let errorCount = 0;

    // Process events in reverse order (newest first)
    for (let i = effectiveEvents.length - 1; i >= 0; i--) {
      const event = effectiveEvents[i];
      const result = await processEvent(accountInfo as AccountInfo, event, config);
      processedCount++;

      notifyCallbacks(account, "event", result);

      if (result.action !== "skip") {
        // P1-F: delete 事件已在 handleDeleteEvent 内部记录日志（含 fileId/pickCode 元数据），
        // 此处跳过避免重复记录
        if (!DELETE_EVENT_TYPES.has(event.type)) {
          appendLifeEventLog(
            account,
            event.type,
            result.success,
            result.filePath,
            result.localPath,
            result.message || ""
          );
        }
      }

      if (result.action === "skip") {
        // 跳过不在监控范围内的事件，不计入错误
      } else if (!result.success || result.action === "error") {
        errorCount++;
        console.error(`[LifeMonitor] Event ${event.id} failed: ${result.message}`);
      } else {
        scheduleEmbyRefresh(account);
      }
    }

    saveState(account, next_time, next_id);
    updateState(account, {
      status: "running",
      lastFromTime: next_time,
      lastFromId: next_id,
      eventsProcessed: (monitorStates.get(account)?.eventsProcessed || 0) + processedCount,
      lastError: errorCount > 0 ? `${errorCount} events failed` : undefined,
    });

    // 轻量级一致性检查：每 10 分钟在事件处理成功后自动运行
    maybeRunConsistencyCheck(account, config);

  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    console.error(`[LifeMonitor] Poll error for ${account}:`, errorMsg);

    if (errorMsg.includes("405")) {
      record405Error(account, preferredApi);

      // Try fallback API
      const fallbackApi = preferredApi === "web" ? "ios" : "web";
      try {
        console.log(`[LifeMonitor] Falling back to ${fallbackApi} API for ${account}`);
        const { events, next_time, next_id } = await oncePullLifeEvents(
          accountInfo as AccountInfo,
          fromTime,
          fromId,
          fallbackApi as "ios" | "web"
        );

        if (events.length > 0) {
          let processedCount = 0;
          for (let i = events.length - 1; i >= 0; i--) {
            const result = await processEvent(accountInfo as AccountInfo, events[i], config);
            processedCount++;
            notifyCallbacks(account, "event", result);
            if (result.action !== "skip") {
              // P1-F: delete 事件已在 handleDeleteEvent 内部记录日志，此处跳过避免重复
              if (!DELETE_EVENT_TYPES.has(events[i].type)) {
                appendLifeEventLog(
                  account,
                  events[i].type,
                  result.success,
                  result.filePath,
                  result.localPath,
                  result.message || ""
                );
              }
            }
            if (result.action !== "skip" && result.success) {
              scheduleEmbyRefresh(account);
            }
          }

          saveState(account, next_time, next_id);
          updateState(account, {
            status: "running",
            lastFromTime: next_time,
            lastFromId: next_id,
            eventsProcessed: (monitorStates.get(account)?.eventsProcessed || 0) + processedCount,
          });
          return;
        } else {
          // Fallback 成功但无新事件，保存当前断点
          saveState(account, fromTime, fromId);
          updateState(account, {
            status: "running",
            lastFromTime: fromTime,
            lastFromId: fromId,
            lastError: undefined,
          });
          return;
        }
      } catch (fallbackErr) {
        console.error(`[LifeMonitor] Fallback also failed:`, fallbackErr);
      }
    }

    updateState(account, {
      status: "error",
      lastError: errorMsg,
    });
  }
}

// ==================== Monitor Control ====================

export function startMonitor(account: string): { success: boolean; message?: string } {
  const config = getLifeMonitorConfig();

  if (!config.enabled) {
    return { success: false, message: "请先勾选「启用监控」并保存配置" };
  }

  if (!config.accounts.includes(account)) {
    return { success: false, message: `账号 ${account} 不在监控列表中，请先添加并保存` };
  }

  if (monitorTimers.has(account)) {
    const currentState = monitorStates.get(account);
    if (!currentState?.running) {
      updateState(account, { running: true, status: "running" });
    }
    return { success: false, message: `账号 ${account} 的监控已在运行中` };
  }

  updateState(account, {
    running: true,
    status: "starting",
    eventsProcessed: 0,
    lastError: undefined,
  });

  const pollInterval = Math.max(5, config.pollInterval || 30) * 1000;

  console.log(`[LifeMonitor] Starting monitor for ${account}, interval: ${pollInterval}ms`);

  oncePoll(account).finally(() => {
    if (monitorStates.get(account)?.running) {
      updateState(account, { status: "running" });
    }
  });

  const timer = setInterval(() => {
    const state = monitorStates.get(account);
    if (!state?.running) {
      stopMonitor(account);
      return;
    }
    oncePoll(account).catch((err) => {
      console.error(`[LifeMonitor] Poll error for ${account}:`, err);
    });
  }, pollInterval);

  monitorTimers.set(account, timer);

  return { success: true, message: `监控已启动: ${account}` };
}

export function stopMonitor(account: string): void {
  console.log(`[LifeMonitor] Stopping monitor for ${account}`);

  const timer = monitorTimers.get(account);
  if (timer) {
    clearInterval(timer);
    monitorTimers.delete(account);
  }

  // 设 stopping 标志：让 in-flight oncePoll 在下一个检查点自行退出，
  // 避免在 stop 后仍写状态导致状态闪烁
  updateState(account, {
    running: false,
    status: "idle",
  });
}

export function startAllMonitors(): string[] {
  const config = getLifeMonitorConfig();
  const startedAccounts: string[] = [];

  for (const account of config.accounts) {
    const result = startMonitor(account);
    if (result.success) {
      startedAccounts.push(account);
    }
  }

  return startedAccounts;
}

export function stopAllMonitors(): void {
  for (const account of monitorTimers.keys()) {
    stopMonitor(account);
  }
}

export function getMonitorState(account: string): LifeMonitorState | undefined {
  return monitorStates.get(account);
}

export function getMonitorStatus(): {
  config: LifeMonitorConfig;
  states: LifeMonitorState[];
} {
  const config = getLifeMonitorConfig();
  const states = config.accounts.map((account): LifeMonitorState => {
    const timerExists = monitorTimers.has(account);
    const monitorState = monitorStates.get(account);

    if (monitorState) {
      return {
        ...monitorState,
        running: timerExists,
      };
    }

    return {
      running: timerExists,
      account,
      lastFromTime: 0,
      lastFromId: 0,
      lastCheckTime: 0,
      eventsProcessed: 0,
      status: timerExists ? "running" as const : "idle" as const,
    };
  });

  return { config, states };
}

export async function verifyAccount(account: string): Promise<{
  success: boolean;
  message: string;
  details?: Record<string, unknown>;
}> {
  const accounts = readAccounts();
  const accountInfo = accounts.find(
    (acc: { name: string }) => acc.name === account
  );

  if (!accountInfo) {
    return { success: false, message: `Account not found: ${account}` };
  }

  if (!accountInfo.cookie) {
    return { success: false, message: `Account ${account} has no cookie` };
  }

  try {
    const status = await lifeShow(accountInfo as unknown as AccountInfo, "web");
    if (status.state) {
      // 验证时用 from_time=0 拉取所有事件（不限时间范围）
      const { events } = await oncePullLifeEvents(
        accountInfo as unknown as AccountInfo,
        0,
        0,
        "ios"
      );

      if (events.length === 0) {
        return {
          success: true,
          message: `生活事件已开启，接口正常响应（最近 7 天暂无事件记录）`,
          details: {
            lifeEnabled: true,
            recentEvents: 0,
            note: "接口可用但最近无事件，属正常情况",
          },
        };
      }

      return {
        success: true,
        message: `生活事件已开启，最近 7 天内有 ${events.length} 个事件`,
        details: {
          lifeEnabled: true,
          recentEvents: events.length,
        },
      };
    } else {
      return {
        success: false,
        message: "生活事件未开启，请在 115 网页端或 App 中开启此功能",
        details: { lifeEnabled: false },
      };
    }
  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    return {
      success: false,
      message: `验证失败: ${errorMsg}`,
    };
  }
}

// Export for testing / direct API manipulation
export function _readIdPathCacheForTest() {
  return readIdPathCache();
}
export async function _readFilePathDbForTest() {
  // SQLite 版通过 filePathDb.ts 导出的函数访问
  const { getEntryCount } = await import("./filePathDb");
  return { totalEntries: getEntryCount() };
}