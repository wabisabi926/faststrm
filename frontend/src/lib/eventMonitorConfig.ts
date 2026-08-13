import * as fs from "fs";
import * as path from "path";
import { readSettings, writeSettings, LifeMonitorSettings, notifyEmbyRefresh, getStrmExtensions } from "./serverUtils";
import { appendLifeEventLog } from "./lifeEventLogManager";
import {
  getFilePathEntry as sqliteGetFilePathEntry,
  upsertFilePathEntry as sqliteUpsertFilePathEntry,
  removeFilePathEntry as sqliteRemoveFilePathEntry,
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
import { getStrmFileName, generateStrmContent } from "./strmUtils";

export type FirstPullMode = "latest" | "all" | "last";
export type MoveMediaMode = "recreate" | "local_move";

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
const apiFallbackFile = path.join(CONFIG_DIR, "lifeApiFallback.json");

const WEB_FALLBACK_DURATION = 24 * 60 * 60;
const MAX_RECURSION_DEPTH = 10;
const MAX_FOLDER_FILES = 1000;
const MAX_DELETE_EVENTS_PER_POLL = 100;
const DELETE_RATIO_THRESHOLD_PER_POLL = 0.5;
const ID_PATH_MEM_CACHE_MAX = 5000;
const EMBY_DEBOUNCE_MS = 3000;
const EMBY_MIN_INTERVAL_MS = 30000;
const CONSISTENCY_CHECK_INTERVAL_MS = 10 * 60 * 1000;

const MEDIA_EXT_SUFFIXES = [".mkv", ".mp4", ".avi", ".ts", ".mov", ".wmv", ".flv", ".m4v", ".rmvb", ".rm"];

function ensureConfigDir() {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }
}

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

export function _getLifeMonitorConfig(): LifeMonitorConfig {
  const settings = readSettings();
  const monitor = (settings as Record<string, unknown>).lifeMonitor as LifeMonitorConfig | undefined;
  return !monitor
    ? { ...DEFAULT_CONFIG }
    : {
        ...DEFAULT_CONFIG,
        ...monitor,
        eventTypes: { ...DEFAULT_CONFIG.eventTypes, ...(monitor.eventTypes || {}) },
      };
}

export function _saveLifeMonitorConfig(config: LifeMonitorConfig): void {
  const settings = readSettings();
  (settings as Record<string, unknown>).lifeMonitor = config;
  writeSettings(settings);
}

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

function matchPathMapping(
  cloudPath: string,
  pathMappings: LifeMonitorConfig["pathMappings"],
  account?: string
): { cloudPath: string; localPath: string; relativePath: string } | null {
  const candidates = pathMappings.filter((mapping) => {
    if (mapping.account && account && mapping.account !== account) return false;
    return true;
  });
  candidates.sort(
    (a, b) => b.cloudPath.replace(/\/+$/, "").length - a.cloudPath.replace(/\/+$/, "").length
  );

  for (const mapping of candidates) {
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

const getFilePathEntry = sqliteGetFilePathEntry;
const upsertFilePathEntry = sqliteUpsertFilePathEntry;
const removeFilePathEntry = sqliteRemoveFilePathEntry;

export {
  DEFAULT_CONFIG,
  CONFIG_DIR,
  stateFile,
  idPathCacheFile,
  apiFallbackFile,
  WEB_FALLBACK_DURATION,
  MAX_RECURSION_DEPTH,
  MAX_FOLDER_FILES,
  MAX_DELETE_EVENTS_PER_POLL,
  DELETE_RATIO_THRESHOLD_PER_POLL,
  ID_PATH_MEM_CACHE_MAX,
  EMBY_DEBOUNCE_MS,
  EMBY_MIN_INTERVAL_MS,
  CONSISTENCY_CHECK_INTERVAL_MS,
  MEDIA_EXT_SUFFIXES,
  ensureConfigDir,
  readIdPathCache,
  writeIdPathCache,
  readState,
  saveState,
  readApiFallback,
  writeApiFallback,
  getPreferredApi,
  record405Error,
  reset405Count,
  matchPathMapping,
  sanitizePathParts,
  sanitizeDirectoryPath,
  isMediaFile,
  isValidPickCode,
  readStrmContent,
  tryCleanupOldStrmByPath,
  getFilePathEntry,
  upsertFilePathEntry,
  removeFilePathEntry,
  notifyEmbyRefresh,
  getStrmExtensions,
  appendLifeEventLog,
  syncStrmText,
  removeEmptyParents,
  deleteStrmFile,
  deleteStrmDir,
  findStrmRecursive,
  findDirRecursive,
  getRootDirsFromMappings,
  getStrmFileName,
  generateStrmContent,
};