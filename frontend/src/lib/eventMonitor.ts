import * as fs from "fs";
import * as path from "path";
import { readSettings, readAccounts, writeSettings, readTasks, notifyEmbyRefresh } from "./serverUtils";
import { appendLifeEventLog } from "./lifeEventLogManager";
import { tryPollMonitor } from "./accountRuntimeState";
import {
  AccountInfo,
  getDownloadUrlWeb,
  fs_dir_getid,
  fs_files,
  getFileInfoById,
} from "./115";
import {
  lifeShow,
  oncePullLifeEvents,
  LifeEvent,
  CREATE_EVENT_TYPES,
  MOVE_EVENT_TYPES,
  RENAME_EVENT_TYPES,
  DELETE_EVENT_TYPES,
  NEW_FOLDER_EVENT_TYPES,
  BEHAVIOR_TYPE_TO_NAME,
} from "./115Life";

// ==================== Types ====================

export type FirstPullMode = "latest" | "all" | "last";
export type MoveMediaMode = "recreate" | "local_move";

export interface LifeMonitorConfig {
  enabled: boolean;
  accounts: string[];
  pollInterval: number;
  pathMappings: Array<{
    account?: string;
    cloudPath: string;
    localPath: string;
  }>;
  removeEmptyDirs: boolean;
  eventTypes: {
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  };
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  /** 最小文件大小（字节），小于此值的文件跳过 STRM 生成。0 表示不过滤 */
  minFileSize?: number;
  /** 首次拉取模式：latest=从当前时间 / all=拉取全部历史 / last=从上次断点继续 */
  firstPullMode?: FirstPullMode;
  /** 移动事件处理模式：recreate=删除旧 STRM 并重新生成 / local_move=本地直接移动 STRM 文件 */
  moveMediaMode?: MoveMediaMode;
}

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
  strmPrefix: "",
  enablePathEncoding: false,
  minFileSize: 0,
  firstPullMode: "latest",
  moveMediaMode: "local_move",
};

const CONFIG_DIR = path.join(process.cwd(), "../config");
const stateFile = path.join(CONFIG_DIR, "lifeMonitorState.json");
const idPathCacheFile = path.join(CONFIG_DIR, "lifeIdPathCache.json");
const filePathDbFile = path.join(CONFIG_DIR, "lifeFilePathDb.json");
const apiFallbackFile = path.join(CONFIG_DIR, "lifeApiFallback.json");

const WEB_FALLBACK_DURATION = 24 * 60 * 60;
const MAX_RECURSION_DEPTH = 10;
const MAX_FOLDER_FILES = 1000;

// ==================== Global State (persist across Next.js HMR via globalThis) ====================

const g = globalThis as unknown as {
  __lifeMonitorStates?: Map<string, LifeMonitorState>;
  __lifeMonitorTimers?: Map<string, NodeJS.Timeout>;
  __lifeMonitorCallbacks?: Map<string, Set<LifeMonitorCallback>>;
  __lifeIdPathMemoryCache?: Map<string, string>;
  __embyDebounceTimers?: Map<string, NodeJS.Timeout>;
  __embyLastFireTime?: Map<string, number>;
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
const idPathMemoryCache = g.__lifeIdPathMemoryCache;

// Ensure config directory exists
function ensureConfigDir() {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
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

function getIdPath(account: string, cid: number): string | undefined {
  const cacheKey = `${account}:${cid}`;
  const memCached = idPathMemoryCache.get(cacheKey);
  if (memCached) return memCached;

  const diskCache = readIdPathCache();
  const diskCached = diskCache[cacheKey];
  if (diskCached) {
    idPathMemoryCache.set(cacheKey, diskCached);
    return diskCached;
  }
  return undefined;
}

function setIdPath(account: string, cid: number, pathStr: string) {
  const cacheKey = `${account}:${cid}`;
  idPathMemoryCache.set(cacheKey, pathStr);
  const diskCache = readIdPathCache();
  diskCache[cacheKey] = pathStr;
  writeIdPathCache(diskCache);
}

// ==================== File Path Database (for old path lookup) ====================

interface FilePathEntry {
  fileId: number;
  path: string;
  fileName: string;
  parentId: number;
  pickCode: string;
  updateTime: number;
}

function readFilePathDb(): Record<string, FilePathEntry> {
  ensureConfigDir();
  if (!fs.existsSync(filePathDbFile)) return {};
  try {
    return JSON.parse(fs.readFileSync(filePathDbFile, "utf-8"));
  } catch {
    return {};
  }
}

function writeFilePathDb(db: Record<string, FilePathEntry>) {
  ensureConfigDir();
  fs.writeFileSync(filePathDbFile, JSON.stringify(db, null, 2), "utf-8");
}

function filePathDbKey(account: string, fileId: number): string {
  return `${account}:${fileId}`;
}

function getFilePathEntry(account: string, fileId: number): FilePathEntry | undefined {
  const db = readFilePathDb();
  return db[filePathDbKey(account, fileId)];
}

function upsertFilePathEntry(account: string, entry: FilePathEntry) {
  const db = readFilePathDb();
  db[filePathDbKey(account, entry.fileId)] = entry;
  writeFilePathDb(db);
}

function removeFilePathEntry(account: string, fileId: number) {
  const db = readFilePathDb();
  delete db[filePathDbKey(account, fileId)];
  writeFilePathDb(db);
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
  let fallback = state[account] || { ios405Count: 0, webFallbackUntil: 0 };

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
  if (!monitor) return { ...DEFAULT_CONFIG };
  return {
    ...DEFAULT_CONFIG,
    ...monitor,
    eventTypes: { ...DEFAULT_CONFIG.eventTypes, ...(monitor.eventTypes || {}) },
  };
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
  cid: number
): Promise<string> {
  if (cid === 0) return "/";

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
      const mappedCid = await fs_dir_getid(mapping.cloudPath, {
        userAgent: readSettings()["user-agent"],
        accountInfo: accountInfo as unknown as AccountInfo,
      });
      if (mappedCid.id === cid) {
        setIdPath(account, cid, mapping.cloudPath);
        return mapping.cloudPath;
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
        return pathVal;
      }
    }

    // Fallback: use webapi files listing to find the directory name
    // Then recursively resolve parent
    const filesData = await fs_files(cid, {
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
            return pathStr;
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

function extractPathFromExportData(data: unknown, targetCid: number): string {
  if (!data || typeof data !== "object") return "";
  const obj = data as Record<string, unknown>;

  const innerData = obj.data as Record<string, unknown> | undefined;
  if (innerData) {
    const list = innerData.list as Array<Record<string, unknown>> | undefined;
    if (Array.isArray(list)) {
      for (const item of list) {
        if (item.cid === targetCid || item.id === targetCid) {
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
      if (item.cid === targetCid || item.id === targetCid) {
        return (item.path || item.file_path || "") as string;
      }
    }
  }

  return "";
}

async function resolveEventPath(
  accountInfo: AccountInfo,
  event: LifeEvent
): Promise<string> {
  let parentPath = "";
  if (event.parent_id > 0) {
    parentPath = await resolvePathByCid(accountInfo, event.parent_id);
  } else {
    parentPath = "/";
  }

  const fileName = event.file_name || "";
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

    const normalizedCloudPath = mapping.cloudPath.endsWith("/")
      ? mapping.cloudPath
      : mapping.cloudPath + "/";

    if (cloudPath === mapping.cloudPath || cloudPath.startsWith(normalizedCloudPath)) {
      const relativePath = cloudPath.slice(mapping.cloudPath.length).replace(/^\//, "");
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

function getStrmFileName(fileName: string): string {
  const ext = path.extname(fileName);
  if (!ext) return fileName + ".strm";
  const lowerExt = ext.toLowerCase();
  if (lowerExt === ".iso") return fileName; // .iso files keep original name
  return fileName.replace(new RegExp(ext + "$", "i"), ".strm");
}

function isValidPickCode(pickCode: string): boolean {
  return !!pickCode && pickCode.length === 17 && /^[a-zA-Z0-9]+$/.test(pickCode);
}

function generateStrmContent(
  cloudPath: string,
  config: LifeMonitorConfig
): string {
  const strmPrefix = config.strmPrefix || "";
  const normalized = cloudPath.startsWith("/") ? cloudPath : "/" + cloudPath;
  const format = `${strmPrefix}${normalized}`;
  return config.enablePathEncoding ? encodeURI(format) : format;
}

function readStrmContent(strmPath: string): string | null {
  try {
    if (!fs.existsSync(strmPath)) return null;
    return fs.readFileSync(strmPath, "utf-8").trim();
  } catch {
    return null;
  }
}

function writeStrmContent(strmPath: string, content: string): boolean {
  try {
    const dir = path.dirname(strmPath);
    if (!fs.existsSync(dir)) {
      fs.mkdirSync(dir, { recursive: true });
    }
    const existing = readStrmContent(strmPath);
    if (existing === content) return true; // No change
    fs.writeFileSync(strmPath, content, "utf-8");
    return true;
  } catch {
    return false;
  }
}

function removeEmptyParentDirs(startPath: string, rootDirs: Set<string>) {
  let currentDir = path.dirname(startPath);
  while (currentDir && !rootDirs.has(currentDir)) {
    try {
      const files = fs.readdirSync(currentDir);
      if (files.length === 0) {
        fs.rmdirSync(currentDir);
        console.log(`[LifeMonitor] Removed empty directory: ${currentDir}`);
        currentDir = path.dirname(currentDir);
      } else {
        break;
      }
    } catch {
      break;
    }
  }
}

function getRootDirs(): Set<string> {
  const config = getLifeMonitorConfig();
  const roots = new Set<string>();
  for (const mapping of config.pathMappings) {
    roots.add(path.resolve(mapping.localPath));
  }
  return roots;
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
  const strmContent = generateStrmContent(cloudPath, config);

  if (writeStrmContent(strmPath, strmContent)) {
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

  async function processDirectory(cid: number, depth: number) {
    if (depth > MAX_RECURSION_DEPTH) return;

    try {
      const userAgent = readSettings()["user-agent"];
      let offset = 0;
      const limit = 1000;

      while (true) {
        const data = await fs_files(cid, {
          userAgent,
          limit,
          offset,
          accountInfo: accountInfo as AccountInfo,
        });

        const items = data?.data || [];
        if (items.length === 0) break;

        for (const item of items) {
          const itemName = item.n;
          const itemFid = item.fid;
          const itemCid = item.cid;
          const isDirectory = item.fc === 0;

          if (isDirectory) {
            const itemPath = cloudPath.endsWith("/")
              ? cloudPath + itemName
              : cloudPath + "/" + itemName;
            const itemLocalPath = path.join(localDir, sanitizePathParts(itemName));

            if (!fs.existsSync(itemLocalPath)) {
              fs.mkdirSync(itemLocalPath, { recursive: true });
            }

            setIdPath(accountInfo.name, itemCid, itemPath);
            upsertFilePathEntry(accountInfo.name, {
              fileId: itemFid,
              path: itemPath,
              fileName: itemName,
              parentId: cid,
              pickCode: "",
              updateTime: Math.floor(Date.now() / 1000),
            });

            await processDirectory(itemCid, depth + 1);
          } else {
            const itemPath = cloudPath.endsWith("/")
              ? cloudPath + itemName
              : cloudPath + "/" + itemName;

            if (!isMediaFile(itemName, getStrmExtensions())) {
              skippedCount++;
              continue;
            }

            const folderMinSize = config.minFileSize || 0;
            if (
              folderMinSize > 0 &&
              typeof item.s === "number" &&
              item.s < folderMinSize
            ) {
              skippedCount++;
              continue;
            }

            try {
              const userAgent = readSettings()["user-agent"];
              let fileInfo: unknown = null;
              let lastErr: unknown = null;
              for (let attempt = 0; attempt < 3; attempt++) {
                if (attempt > 0) {
                  await new Promise((r) => setTimeout(r, 1000 * attempt));
                }
                try {
                  fileInfo = await getFileInfoById(itemFid, {
                    userAgent,
                    accountInfo: accountInfo as AccountInfo,
                  });
                  lastErr = null;
                  break;
                } catch (e) {
                  lastErr = e;
                }
              }
              if (lastErr) throw lastErr;

              const info = fileInfo as Record<string, unknown> | null;
              const pickCode = (info?.pickcode || info?.pick_code || "") as string;

              if (isValidPickCode(pickCode)) {
                const strmFileName = getStrmFileName(itemName);
                const strmPath = path.join(localDir, strmFileName);
                const strmContent = generateStrmContent(itemPath, config);

                if (writeStrmContent(strmPath, strmContent)) {
                  strmCount++;
                  setIdPath(accountInfo.name, itemCid, itemPath);
                  upsertFilePathEntry(accountInfo.name, {
                    fileId: itemFid,
                    path: itemPath,
                    fileName: itemName,
                    parentId: cid,
                    pickCode,
                    updateTime: Math.floor(Date.now() / 1000),
                  });
                }
              } else {
                skippedCount++;
              }
            } catch {
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

  await processDirectory(event.file_id, 0);

  result.action = "create";
  result.message = `文件夹同步完成: 创建 ${strmCount} 个 STRM，跳过 ${skippedCount} 个非媒体文件`;
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

  const rootDirs = getRootDirs();

  if (event.file_category === 0) {
    if (fs.existsSync(mapping.localPath)) {
      fs.rmSync(mapping.localPath, { recursive: true, force: true });
      result.success = true;
      result.message = `文件夹已删除: ${mapping.localPath}`;
      removeFilePathEntry(accountInfo.name, event.file_id);
      if (config.removeEmptyDirs) {
        removeEmptyParentDirs(mapping.localPath, rootDirs);
      }
    } else {
      result.success = true;
      result.message = `本地文件夹不存在，跳过: ${mapping.localPath}`;
    }
  } else {
    const strmFileName = getStrmFileName(event.file_name);
    const strmPath = path.join(path.dirname(mapping.localPath), strmFileName);

    if (fs.existsSync(strmPath)) {
      fs.unlinkSync(strmPath);
      result.success = true;
      result.message = `STRM 文件已删除: ${strmPath}`;
      removeFilePathEntry(accountInfo.name, event.file_id);
      if (config.removeEmptyDirs) {
        removeEmptyParentDirs(strmPath, rootDirs);
      }
    } else {
      result.success = true;
      result.message = `本地 STRM 文件不存在，跳过: ${strmPath}`;
    }
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

  // Look up old path from tracking database
  const oldEntry = getFilePathEntry(accountInfo.name, event.file_id);
  const isFolder = event.file_category === 0;
  const moveMode = config.moveMediaMode || "local_move";

  if (oldEntry) {
    const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);

    if (oldMapping) {
      // recreate 模式：删除旧 STRM/目录后走 create 流程重新生成
      // 适用场景：pickcode 可能变化、或本地 STRM 内容已损坏需要重建
      if (moveMode === "recreate") {
        if (isFolder) {
          if (fs.existsSync(oldMapping.localPath)) {
            try {
              fs.rmSync(oldMapping.localPath, { recursive: true, force: true });
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
              fs.unlinkSync(oldStrmPath);
            } catch (err) {
              console.error(`[LifeMonitor] recreate 删除旧 STRM 失败: ${oldStrmPath}`, err);
            }
          }
        }

        // 清理旧空目录
        if (config.removeEmptyDirs) {
          const rootDirs = getRootDirs();
          removeEmptyParentDirs(oldMapping.localPath, rootDirs);
        }

        // 走 create 流程在新路径重新生成
        const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        createResult.action = "move";
        createResult.message = `[recreate] ${createResult.message || ""}`;
        return createResult;
      }

      // local_move 模式：本地直接移动 STRM 文件
      if (isFolder) {
        // Folder move: move the entire local directory
        if (fs.existsSync(oldMapping.localPath)) {
          const newParentDir = path.dirname(mapping.localPath);
          if (!fs.existsSync(newParentDir)) {
            fs.mkdirSync(newParentDir, { recursive: true });
          }
          // 目标已存在时不覆盖，避免误删
          if (fs.existsSync(mapping.localPath)) {
            result.success = true;
            result.message = `目标目录已存在，跳过移动: ${mapping.localPath}`;
          } else {
            fs.renameSync(oldMapping.localPath, mapping.localPath);
            result.success = true;
            result.message = `文件夹已移动: ${oldMapping.localPath} -> ${mapping.localPath}`;
          }
        } else {
          result.success = true;
          result.message = `旧目录不存在，直接创建: ${mapping.localPath}`;
          if (!fs.existsSync(mapping.localPath)) {
            fs.mkdirSync(mapping.localPath, { recursive: true });
          }
        }
      } else {
        // File move: move the STRM file
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

          // If old and new are in same directory, just rename
          if (path.dirname(oldStrmPath) === path.dirname(newStrmPath)) {
            if (fs.existsSync(newStrmPath) && oldStrmPath !== newStrmPath) {
              fs.unlinkSync(newStrmPath);
            }
            if (oldStrmPath !== newStrmPath) {
              fs.renameSync(oldStrmPath, newStrmPath);
            }
          } else {
            // Different directories: copy content, delete old
            const content = readStrmContent(oldStrmPath);
            if (content !== null) {
              writeStrmContent(newStrmPath, content);
              fs.unlinkSync(oldStrmPath);
            } else {
              // 旧 STRM 内容读取失败，退化为 recreate
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          }

          // 若事件携带新 pickcode，更新 STRM 内容
          if (isValidPickCode(event.pick_code)) {
            const newContent = generateStrmContent(
              cloudPath,
              config
            );
            writeStrmContent(newStrmPath, newContent);
          }

          result.success = true;
          result.message = `STRM 已移动: ${oldStrmPath} -> ${newStrmPath}`;
        } else {
          // Old STRM doesn't exist, create new one
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

      // Clean up old empty directories
      if (config.removeEmptyDirs) {
        const rootDirs = getRootDirs();
        removeEmptyParentDirs(oldMapping.localPath, rootDirs);
      }

      return result;
    }
  }

  // No old entry or old path not in our mappings - create new
  return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
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

  if (oldEntry) {
    const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);

    if (oldMapping) {
      if (isFolder) {
        // Folder rename
        if (fs.existsSync(oldMapping.localPath)) {
          if (fs.existsSync(mapping.localPath)) {
            result.success = true;
            result.message = `目标目录已存在，跳过重命名: ${mapping.localPath}`;
            return result;
          }
          fs.renameSync(oldMapping.localPath, mapping.localPath);
          result.success = true;
          result.message = `文件夹已重命名: ${oldMapping.localPath} -> ${mapping.localPath}`;
        } else {
          result.success = true;
          result.message = `旧目录不存在，创建新目录: ${mapping.localPath}`;
          if (!fs.existsSync(mapping.localPath)) {
            fs.mkdirSync(mapping.localPath, { recursive: true });
          }
        }
      } else {
        // File rename: rename the STRM file
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
            // Same directory, simple rename
            fs.renameSync(oldStrmPath, newStrmPath);
            // Update STRM content with new cloud path
            if (isValidPickCode(event.pick_code)) {
              const newContent = generateStrmContent(
                cloudPath,
                config
              );
              writeStrmContent(newStrmPath, newContent);
            }
          } else {
            // Different directories
            const content = readStrmContent(oldStrmPath);
            if (content) {
              writeStrmContent(newStrmPath, content);
              fs.unlinkSync(oldStrmPath);
            } else {
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          }

          // Handle related files (subtitles, nfo, etc.)
          handleRelatedFileRenames(oldStrmPath, newStrmPath, oldEntry.fileName, event.file_name);

          result.success = true;
          result.message = `STRM 已重命名: ${oldStrmPath} -> ${newStrmPath}`;
        } else {
          return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        }
      }

      // Update tracking
      upsertFilePathEntry(accountInfo.name, {
        fileId: event.file_id,
        path: cloudPath,
        fileName: event.file_name,
        parentId: event.parent_id,
        pickCode: event.pick_code || oldEntry.pickCode,
        updateTime: event.update_time,
      });

      if (config.removeEmptyDirs) {
        const rootDirs = getRootDirs();
        removeEmptyParentDirs(oldMapping.localPath, rootDirs);
      }

      return result;
    }
  }

  // No old entry - create new STRM
  return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
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

function handleNewFolderEvent(
  event: LifeEvent,
  mapping: { localPath: string; relativePath: string },
  cloudPath: string
): EventProcessResult {
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

  if (!fs.existsSync(mapping.localPath)) {
    fs.mkdirSync(mapping.localPath, { recursive: true });
    result.message = `本地目录已创建: ${mapping.localPath}`;
  } else {
    result.message = `本地目录已存在: ${mapping.localPath}`;
  }

  return result;
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
    const cloudPath = await resolveEventPath(accountInfo, event);
    if (!cloudPath) {
      result.message = "无法解析文件路径";
      return result;
    }
    result.filePath = cloudPath;

    // Record the path in tracking DB regardless of event type
    if (isValidPickCode(event.pick_code) || event.file_category === 0) {
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
      return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
    } else if (DELETE_EVENT_TYPES.has(eventType) && config.eventTypes.remove) {
      return handleDeleteEvent(accountInfo, event, config, mapping, cloudPath);
    } else if (MOVE_EVENT_TYPES.has(eventType) && config.eventTypes.move) {
      return handleMoveEvent(accountInfo, event, config, mapping, cloudPath);
    } else if (RENAME_EVENT_TYPES.has(eventType) && config.eventTypes.rename) {
      return handleRenameEvent(accountInfo, event, config, mapping, cloudPath);
    } else {
      result.action = "skip";
      result.message = `事件类型 ${eventType} 未启用处理`;
      return result;
    }
  } catch (err) {
    result.action = "error";
    result.message = err instanceof Error ? err.message : String(err);
    return result;
  }
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

// ==================== Polling ====================

function getStrmExtensions(): string[] {
  try {
    const settings = readSettings();
    return (settings.strmExtensions || []).map((e: string) =>
      e.startsWith(".") ? e.toLowerCase() : "." + e.toLowerCase()
    );
  } catch {
    return [".mkv", ".mp4", ".avi", ".mov", ".rmvb", ".flv", ".webm"];
  }
}

async function oncePoll(account: string): Promise<void> {
  const pollStatus = tryPollMonitor(account);
  if (!pollStatus.ok) {
    console.log(
      `[eventMonitor] monitor suspended for ${account} (fullscan active), skip this poll. resume @ ${new Date(pollStatus.suspendedUntil!).toISOString()}`
    );
    return;
  }

  let config = getLifeMonitorConfig();

  // 从 task 配置覆盖 strmPrefix / enablePathEncoding（与全量生成保持一致）
  try {
    const tasks = readTasks();
    const matchedTask = tasks.find((t: { account: string }) => t.account === account);
    if (matchedTask) {
      if (matchedTask.strmPrefix) {
        config = { ...config, strmPrefix: matchedTask.strmPrefix };
      }
      if (typeof matchedTask.enablePathEncoding === "boolean") {
        config = { ...config, enablePathEncoding: matchedTask.enablePathEncoding };
      }
    }
  } catch { /* tasks.json 可能不存在，忽略 */ }

  const accounts = readAccounts();

  const accountInfo = accounts.find(
    (acc: { name: string }) => acc.name === account
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
  // - all: 从 0 开始拉取全部历史（首次部署补历史）
  // - last: 从上次保存的断点继续；若无断点则退化为 latest
  let fromTime: number;
  let fromId: number;
  if (firstPullMode === "all") {
    fromTime = 0;
    fromId = 0;
  } else if (hasSavedState) {
    // latest / last 均优先使用已保存的断点，避免 poll 间新事件被跳过
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

    let processedCount = 0;
    let errorCount = 0;

    // Process events in reverse order (newest first)
    for (let i = events.length - 1; i >= 0; i--) {
      const event = events[i];
      const result = await processEvent(accountInfo as AccountInfo, event, config);
      processedCount++;

      notifyCallbacks(account, "event", result);

      if (result.action !== "skip") {
        appendLifeEventLog(
          account,
          event.type,
          result.success,
          result.filePath,
          result.localPath,
          result.message || ""
        );
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
              appendLifeEventLog(
                account,
                events[i].type,
                result.success,
                result.filePath,
                result.localPath,
                result.message || ""
              );
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
    const status = await lifeShow(accountInfo as AccountInfo, "web");
    if (status.state) {
      // 验证时用 from_time=0 拉取所有事件（不限时间范围）
      const { events } = await oncePullLifeEvents(
        accountInfo as AccountInfo,
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
export function _readFilePathDbForTest() {
  return readFilePathDb();
}