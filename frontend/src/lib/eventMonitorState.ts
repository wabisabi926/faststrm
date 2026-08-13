import * as fs from "fs";
import * as path from "path";
import {
  LifeMonitorState,
  LifeMonitorCallback,
  EventProcessResult,
  LifeMonitorConfig,
  EMBY_DEBOUNCE_MS,
  EMBY_MIN_INTERVAL_MS,
  CONSISTENCY_CHECK_INTERVAL_MS,
  deleteStrmFile,
  getRootDirsFromMappings,
  _getLifeMonitorConfig,
  notifyEmbyRefresh,
  appendLifeEventLog,
  getStrmExtensions,
} from "./eventMonitorConfig";
import { readSettings, readAccounts } from "./serverUtils";
import { AccountInfo } from "./115";
import { tryPollMonitor } from "./accountRuntimeState";
import { logger } from "@/lib/logger";

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

export const monitorStates = g.__lifeMonitorStates;
export const monitorTimers = g.__lifeMonitorTimers;
export const monitorCallbacks = g.__lifeMonitorCallbacks;
export const idPathMemoryCache = g.__lifeIdPathMemoryCache;
export const embyDebounceTimers = g.__embyDebounceTimers;
export const embyLastFireTime = g.__embyLastFireTime;

const consistencyCheckTimers = new Map<string, number>();

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
        logger.error("Life monitor callback error:", err);
      }
    });
  }
}

export function updateState(account: string, partial: Partial<LifeMonitorState>) {
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

export function scheduleEmbyRefresh(account: string) {
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
    logger.info(`[LifeMonitor] 触发 Emby 刷新 (account=${account})`);
    notifyEmbyRefresh();
  }, EMBY_DEBOUNCE_MS);

  embyDebounceTimers.set(account, timer);
}

export function maybeRunConsistencyCheck(account: string, config: LifeMonitorConfig) {
  const now = Date.now();
  const last = consistencyCheckTimers.get(account) || 0;
  if (now - last < CONSISTENCY_CHECK_INTERVAL_MS) return;
  consistencyCheckTimers.set(account, now);

  setTimeout(async () => {
    try {
      const stats = { scanned: 0, cleaned: 0, errors: 0 };
      for (const mapping of config.pathMappings || []) {
        const accountFilter = mapping.account;
        if (accountFilter && accountFilter !== account) continue;

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
                try {
                  const content = fs.readFileSync(fullPath, "utf-8");
                  const url = content.trim();
                  if (!url || (!url.startsWith("http") && !url.startsWith("/"))) {
                    const delRes = deleteStrmFile(fullPath, { tag: "LifeMonitor/consistencyCheck", cleanRelated: false, account });
                    if (delRes.deleted) stats.cleaned++;
                    logger.info(`[ConsistencyCheck] 清理无效 STRM: ${fullPath}`);
                  }
                } catch {
                  try { const delRes = deleteStrmFile(fullPath, { tag: "LifeMonitor/consistencyCheck", cleanRelated: false, account }); if (delRes.deleted) stats.cleaned++; } catch {}
                }
              }
            }
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
        logger.info(
          `[ConsistencyCheck] ${account}: 扫描 ${stats.scanned} STRM, 清理 ${stats.cleaned}, 错误 ${stats.errors}`
        );
      }
    } catch (e) {
      logger.error(`[ConsistencyCheck] ${account} 异常:`, e);
    }
  }, 0);
}

export function tryAutoStartMonitorsOnce(startMonitorFn: (account: string) => { success: boolean; message?: string }): void {
  if (g.__lifeMonitorsAutoStarted) return;
  g.__lifeMonitorsAutoStarted = true;

  try {
    const config = _getLifeMonitorConfig();
    if (!config.enabled) {
      logger.info("[LifeMonitor] 自动启动：监控未启用，跳过");
      return;
    }
    if (!config.accounts || config.accounts.length === 0) {
      logger.info("[LifeMonitor] 自动启动：监控账号列表为空，跳过");
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
        const r = startMonitorFn(accName);
        logger.info(`[LifeMonitor] 自动启动账号 ${accName}: ${r.success ? "成功" : "跳过 (" + (r.message || "") + ")"}`);
      } else {
        logger.info(`[LifeMonitor] 自动启动账号 ${accName}: 跳过（凭据为空或账号不存在）`);
      }
    }
  } catch (err) {
    logger.error("[LifeMonitor] 自动启动失败:", err);
  }
}

export {
  notifyCallbacks,
  getRootDirsFromMappings,
  deleteStrmFile,
  appendLifeEventLog,
  getStrmExtensions,
  readSettings,
  readAccounts,
  tryPollMonitor,
};