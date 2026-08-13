import { readSettings, readAccounts, resolveStrmSettings } from "./serverUtils";
import { oncePullLifeEvents, DELETE_EVENT_TYPES } from "./115Life";
import { AccountInfo } from "./115";
import { tryPollMonitor } from "./accountRuntimeState";
import { logger } from "./logger";

import {
  LifeMonitorConfig,
  LifeMonitorState,
  _getLifeMonitorConfig,
  _saveLifeMonitorConfig,
  readIdPathCache,
  getPreferredApi,
  record405Error,
  reset405Count,
  readState,
  saveState,
  ensureConfigDir,
  MAX_DELETE_EVENTS_PER_POLL,
  DELETE_RATIO_THRESHOLD_PER_POLL,
  appendLifeEventLog,
} from "./eventMonitorConfig";

import {
  getAllMonitorStates,
  subscribeMonitor,
  updateState,
  scheduleEmbyRefresh,
  maybeRunConsistencyCheck,
  tryAutoStartMonitorsOnce,
  monitorStates,
  monitorTimers,
  idPathMemoryCache,
  notifyCallbacks,
} from "./eventMonitorState";

import { processEvent } from "./eventMonitorHandlers";

export type {
  FirstPullMode,
  MoveMediaMode,
  LifeMonitorConfig,
  LifeMonitorState,
  EventProcessResult,
  LifeMonitorCallback,
} from "./eventMonitorConfig";

export {
  _getLifeMonitorConfig,
  _saveLifeMonitorConfig,
  DEFAULT_CONFIG,
  CONFIG_DIR,
  stateFile,
  idPathCacheFile,
  apiFallbackFile,
  WEB_FALLBACK_DURATION,
  MAX_RECURSION_DEPTH,
  MAX_FOLDER_FILES,
  ID_PATH_MEM_CACHE_MAX,
  MEDIA_EXT_SUFFIXES,
} from "./eventMonitorConfig";

export {
  matchPathMapping,
  sanitizePathParts,
  sanitizeDirectoryPath,
  isMediaFile,
  isValidPickCode,
  readStrmContent,
  tryCleanupOldStrmByPath,
} from "./eventMonitorConfig";

export {
  readIdPathCache,
  writeIdPathCache,
  readState,
  saveState,
  readApiFallback,
  writeApiFallback,
  getPreferredApi,
  record405Error,
  reset405Count,
} from "./eventMonitorConfig";

export {
  getFilePathEntry,
  upsertFilePathEntry,
  removeFilePathEntry,
  notifyEmbyRefresh,
  getStrmExtensions,
  syncStrmText,
  removeEmptyParents,
  deleteStrmFile,
  deleteStrmDir,
  findStrmRecursive,
  findDirRecursive,
  getRootDirsFromMappings,
  getStrmFileName,
  generateStrmContent,
} from "./eventMonitorConfig";

export {
  getIdPath,
  setIdPath,
  resolvePathByCid,
  resolveEventPath,
  extractPathFromExportData,
} from "./eventMonitorPath";

export {
  getAllMonitorStates,
  subscribeMonitor,
  monitorStates,
  monitorTimers,
  monitorCallbacks,
  idPathMemoryCache,
  embyDebounceTimers,
  embyLastFireTime,
} from "./eventMonitorState";

export {
  handleCreateEvent,
  handleFolderCreateEvent,
  handleDeleteEvent,
  handleMoveEvent,
  handleRenameEvent,
  handleRelatedFileRenames,
  cleanupResidualStrmsInOldFolder,
  logEventTrace,
} from "./eventMonitorHandlers";

export function getLifeMonitorConfig(): LifeMonitorConfig {
  const config = _getLifeMonitorConfig();
  tryAutoStartMonitorsOnce(startMonitor);
  return config;
}

export function saveLifeMonitorConfig(config: LifeMonitorConfig): void {
  _saveLifeMonitorConfig(config);
}

async function oncePoll(account: string): Promise<void> {
  if (!monitorStates.get(account)?.running) {
    return;
  }

  const pollStatus = tryPollMonitor(account);
  if (!pollStatus.ok) {
    logger.info(
      `[eventMonitor] monitor suspended for ${account} (fullscan active), skip this poll. resume @ ${new Date(pollStatus.suspendedUntil!).toISOString()}`
    );
    return;
  }

  const settings = readSettings();
  let config = getLifeMonitorConfig();

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

  let fromTime: number;
  let fromId: number;
  if (firstPullMode === "all" && !hasSavedState) {
    fromTime = 0;
    fromId = 0;
  } else if (hasSavedState) {
    fromTime = state!.fromTime;
    fromId = state!.fromId;
  } else {
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

    if (!monitorStates.get(account)?.running) {
      logger.info(`[LifeMonitor] ${account}: monitor stopped during pull, skip event processing`);
      return;
    }

    reset405Count(account);

    if (events.length === 0) {
      saveState(account, fromTime, fromId);
      updateState(account, {
        status: "running",
        lastFromTime: fromTime,
        lastFromId: fromId,
      });
      return;
    }

    logger.info(`[LifeMonitor] Pulled ${events.length} events for ${account}`);

    const deleteEventTypes = new Set(DELETE_EVENT_TYPES);
    const deleteEvents = events.filter(e => deleteEventTypes.has(e.type));
    const deleteCount = deleteEvents.length;
    const totalCount = events.length;
    const deleteRatio = totalCount > 0 ? deleteCount / totalCount : 0;

    let effectiveEvents = events;

    if (deleteCount > 0 && (deleteCount > MAX_DELETE_EVENTS_PER_POLL || deleteRatio > DELETE_RATIO_THRESHOLD_PER_POLL)) {
      effectiveEvents = events.filter(e => !deleteEventTypes.has(e.type));
      logger.error(
        `[LifeMonitor] ⚠️ 删除事件熔断触发! 删除事件数=${deleteCount}/${totalCount} (比例=${(deleteRatio*100).toFixed(1)}%), ` +
        `阈值: count>${MAX_DELETE_EVENTS_PER_POLL} 或 ratio>${DELETE_RATIO_THRESHOLD_PER_POLL*100}%。` +
        `已跳过本次 poll 的所有删除事件，请手动前往 settings 页面执行全量扫描确认后再清理。`
      );
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

    for (let i = effectiveEvents.length - 1; i >= 0; i--) {
      const event = effectiveEvents[i];
      const result = await processEvent(accountInfo as AccountInfo, event, config);
      processedCount++;

      notifyCallbacks(account, "event", result);

      if (result.action !== "skip") {
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
        // skip, not counted as error
      } else if (!result.success || result.action === "error") {
        errorCount++;
        logger.error(`[LifeMonitor] Event ${event.id} failed: ${result.message}`);
      } else {
        scheduleEmbyRefresh(account);
      }
    }

    const allFailed = errorCount > 0 && errorCount >= processedCount;
    if (!allFailed) {
      saveState(account, next_time, next_id);
      updateState(account, {
        status: "running",
        lastFromTime: next_time,
        lastFromId: next_id,
        eventsProcessed: (monitorStates.get(account)?.eventsProcessed || 0) + processedCount,
        lastError: errorCount > 0 ? `${errorCount}/${processedCount} events failed` : undefined,
      });
    } else {
      logger.warn(
        `[LifeMonitor] ${account}: 所有 ${processedCount} 事件处理失败，不推进游标以允许重试`
      );
      updateState(account, {
        status: "error",
        lastError: `All ${processedCount} events failed, cursor not advanced`,
      });
    }

    maybeRunConsistencyCheck(account, config);

  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    logger.error(`[LifeMonitor] Poll error for ${account}:`, errorMsg);

    if (errorMsg.includes("405")) {
      record405Error(account, preferredApi);

      const fallbackApi = preferredApi === "web" ? "ios" : "web";
      try {
        logger.info(`[LifeMonitor] Falling back to ${fallbackApi} API for ${account}`);
        const { events, next_time, next_id } = await oncePullLifeEvents(
          accountInfo as AccountInfo,
          fromTime,
          fromId,
          fallbackApi as "ios" | "web"
        );

        if (events.length > 0) {
          const fbDeleteEventTypes = new Set(DELETE_EVENT_TYPES);
          const fbDeleteEvents = events.filter(e => fbDeleteEventTypes.has(e.type));
          const fbDeleteCount = fbDeleteEvents.length;
          const fbTotalCount = events.length;
          const fbDeleteRatio = fbTotalCount > 0 ? fbDeleteCount / fbTotalCount : 0;

          let fbEffectiveEvents = events;
          if (fbDeleteCount > 0 && (fbDeleteCount > MAX_DELETE_EVENTS_PER_POLL || fbDeleteRatio > DELETE_RATIO_THRESHOLD_PER_POLL)) {
            fbEffectiveEvents = events.filter(e => !fbDeleteEventTypes.has(e.type));
            logger.error(
              `[LifeMonitor] ⚠️ Fallback 删除事件熔断触发! 删除事件数=${fbDeleteCount}/${fbTotalCount} (比例=${(fbDeleteRatio*100).toFixed(1)}%), ` +
              `已跳过本次 fallback 的所有删除事件。`
            );
            appendLifeEventLog(
              account,
              "delete",
              false,
              undefined,
              undefined,
              `⚠️ Fallback 删除事件熔断: 删除数=${fbDeleteCount}/${fbTotalCount}, 已跳过。请执行全量扫描确认`
            );
          }

          let processedCount = 0;
          let fallbackErrorCount = 0;
          for (let i = fbEffectiveEvents.length - 1; i >= 0; i--) {
            const result = await processEvent(accountInfo as AccountInfo, fbEffectiveEvents[i], config);
            processedCount++;
            notifyCallbacks(account, "event", result);
            if (result.action !== "skip") {
              if (!DELETE_EVENT_TYPES.has(fbEffectiveEvents[i].type)) {
                appendLifeEventLog(
                  account,
                  fbEffectiveEvents[i].type,
                  result.success,
                  result.filePath,
                  result.localPath,
                  result.message || ""
                );
              }
            }
            if (result.action === "skip") {
              // skip, not error
            } else if (!result.success || result.action === "error") {
              fallbackErrorCount++;
              logger.error(`[LifeMonitor] Fallback event ${fbEffectiveEvents[i].id} failed: ${result.message}`);
            } else {
              scheduleEmbyRefresh(account);
            }
          }

          const fallbackAllFailed = fallbackErrorCount > 0 && fallbackErrorCount >= processedCount;
          if (!fallbackAllFailed) {
            saveState(account, next_time, next_id);
            updateState(account, {
              status: "running",
              lastFromTime: next_time,
              lastFromId: next_id,
              eventsProcessed: (monitorStates.get(account)?.eventsProcessed || 0) + processedCount,
              lastError: fallbackErrorCount > 0 ? `${fallbackErrorCount}/${processedCount} events failed` : undefined,
            });
          } else {
            logger.warn(
              `[LifeMonitor] ${account}: Fallback 所有 ${processedCount} 事件失败，不推进游标`
            );
            updateState(account, {
              status: "error",
              lastError: `Fallback: all ${processedCount} events failed, cursor not advanced`,
            });
          }
          return;
        } else {
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
        logger.error(`[LifeMonitor] Fallback also failed:`, fallbackErr);
      }
    }

    updateState(account, {
      status: "error",
      lastError: errorMsg,
    });
  }
}

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

  logger.info(`[LifeMonitor] Starting monitor for ${account}, interval: ${pollInterval}ms`);

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
      logger.error(`[LifeMonitor] Poll error for ${account}:`, err);
    });
  }, pollInterval);

  monitorTimers.set(account, timer);

  return { success: true, message: `监控已启动: ${account}` };
}

export function stopMonitor(account: string): void {
  logger.info(`[LifeMonitor] Stopping monitor for ${account}`);

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
    const { lifeShow } = await import("./115Life");
    const status = await lifeShow(accountInfo as unknown as AccountInfo, "web");
    if (status.state) {
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

export function _readIdPathCacheForTest() {
  return readIdPathCache();
}

export async function _readFilePathDbForTest() {
  const { getEntryCount } = await import("./filePathDb");
  return { totalEntries: getEntryCount() };
}