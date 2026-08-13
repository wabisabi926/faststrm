import * as fs from "fs";
import * as path from "path";
import {
  AccountInfo,
  fs_dir_getid,
  fs_files,
  getFileInfoById,
  getPickcodeToId,
} from "./115";
import {
  LifeEvent,
  CREATE_EVENT_TYPES,
  MOVE_EVENT_TYPES,
  RENAME_EVENT_TYPES,
  DELETE_EVENT_TYPES,
  BEHAVIOR_TYPE_TO_NAME,
} from "./115Life";
import {
  getFilePathEntry,
  upsertFilePathEntry,
  removeFilePathEntry,
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
import { getStrmFileName, generateStrmContent } from "./strmUtils";
import { waitFor115ApiToken } from "./rateLimiter";
import { readSettings } from "./serverUtils";

import {
  LifeMonitorConfig,
  EventProcessResult,
  matchPathMapping,
  isMediaFile,
  isValidPickCode,
  readStrmContent,
  tryCleanupOldStrmByPath,
  sanitizePathParts,
  getStrmExtensions,
  appendLifeEventLog,
  MAX_RECURSION_DEPTH,
  MAX_FOLDER_FILES,
} from "./eventMonitorConfig";

import {
  getIdPath,
  setIdPath,
  resolveEventPath,
} from "./eventMonitorPath";

import {
  idPathMemoryCache,
} from "./eventMonitorState";

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

  let strmCount = 0;
  let skippedCount = 0;
  let skipByExt = 0;
  let skipBySize = 0;
  let skipByPickcode = 0;
  let skipByWrite = 0;

  function anyPickCode(item: Record<string, unknown>): string {
    const direct = (item.pc || item.pickcode || item.PickCode || item.pickCode || item.pick_code) as string | undefined;
    if (direct && typeof direct === "string" && isValidPickCode(direct)) return direct;
    for (const key of Object.keys(item)) {
      const val = item[key];
      if (typeof val === "string" && isValidPickCode(val)) return val;
    }
    return typeof direct === "string" ? direct : "";
  }

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
        await waitFor115ApiToken();
        const data = await fs_files(cid, {
          userAgent,
          limit,
          offset,
          accountInfo: accountInfo as AccountInfo,
        });

        const items = data?.data || [];
        if (items.length === 0) break;

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

            setIdPath(accountInfo.name, itemCid, itemCloudPath, idPathMemoryCache);
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

            let pickCode = anyPickCode(item);

            if (!isValidPickCode(pickCode)) {
              try {
                const userAgent = readSettings()["user-agent"];
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

  upsertFilePathEntry(accountInfo.name, {
    fileId: event.file_id,
    path: cloudPath,
    fileName: event.file_name,
    parentId: event.parent_id || 0,
    pickCode: event.pick_code || "",
    updateTime: event.update_time || Math.floor(Date.now() / 1000),
  });
  setIdPath(accountInfo.name, event.file_id, cloudPath, idPathMemoryCache);

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

    if (config.removeEmptyDirs) {
      const rootDirs = getRootDirsFromMappings(config.pathMappings);
      removeEmptyParents(oldDir, { rootDirs, tag: "LifeMonitor/move", account });
    }
  } catch (e) {
    console.error(`[LifeMonitor] cleanupResidualStrmsInOldFolder 出错: ${e instanceof Error ? e.message : String(e)}`);
  }
  return deleted;
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
      if (moveMode === "recreate") {
        const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);

        if (createResult && createResult.success) {
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

          if (config.removeEmptyDirs) {
            const rootDirs = getRootDirsFromMappings(config.pathMappings);
            removeEmptyParents(oldMapping.localPath, { rootDirs, tag: "LifeMonitor/move-recreate", account: accountInfo.name });
          }

          createResult.action = "move";
          createResult.success = true;
          createResult.message = `文件已移动(recreate先建后删): ${oldMapping.localPath} -> ${mapping.localPath}`;
          return createResult;
        } else {
          createResult.action = "error";
          createResult.success = false;
          createResult.message = `recreate 模式创建新 STRM 失败，已保留旧文件不删除: ${createResult?.message || "未知错误"}`;
          console.error(`[LifeMonitor] recreate 创建失败，保留旧文件: ${oldMapping.localPath}`);
          return createResult;
        }
      }

      if (isFolder) {
        if (fs.existsSync(oldMapping.localPath)) {
          const newParentDir = path.dirname(mapping.localPath);
          if (!fs.existsSync(newParentDir)) {
            fs.mkdirSync(newParentDir, { recursive: true });
          }
          if (fs.existsSync(mapping.localPath)) {
            const hasStrm = fs.readdirSync(mapping.localPath).some((f) => f.endsWith(".strm"));
            if (hasStrm) {
              const cleanedResidual = await cleanupResidualStrmsInOldFolder(oldMapping.localPath, mapping.localPath, config, accountInfo.name);
              result.success = true;
              result.message = `目标目录已存在且含 STRM，兜底清理残留 ${cleanedResidual.length} 条后跳过移动`;
            } else {
              return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
            }
          } else {
            fs.renameSync(oldMapping.localPath, mapping.localPath);
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
    const createResult = await handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
    createResult.action = "move";
    createResult.message = `[fallback-cleanup:${cleanup.deleted.length}] ${createResult.message || ""}`;
    return createResult;
  }

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

  const rawEv = event as Record<string, unknown>;
  const newName = String(
    rawEv["new_name"] || rawEv["new_file_name"] || rawEv["to_name"] || rawEv["to_file_name"] || ""
  );
  const effectiveNewName = newName || event.file_name;

  if (oldEntry) {
    const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);

    if (oldMapping) {
      if (isFolder) {
        if (fs.existsSync(oldMapping.localPath)) {
          if (fs.existsSync(mapping.localPath)) {
            const hasStrm = fs.readdirSync(mapping.localPath).some((f) => f.endsWith(".strm"));
            if (hasStrm) {
              const cleanedResidual = await cleanupResidualStrmsInOldFolder(oldMapping.localPath, mapping.localPath, config, accountInfo.name);
              result.success = true;
              result.message = `目标目录已存在且含 STRM，兜底清理残留 ${cleanedResidual.length} 条后跳过重命名`;
            } else {
              try {
                if (fs.existsSync(oldMapping.localPath)) {
                  if (oldEntry?.path) {
                    const updatedCount = updatePathPrefixBatch(accountInfo.name, oldEntry.path, cloudPath);
                    if (updatedCount > 0) {
                      console.log(`[LifeMonitor] rename: 批量更新 ${updatedCount} 条子记录路径前缀: ${oldEntry.path} -> ${cloudPath}`);
                    }
                  }
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
          return handleCreateEvent(accountInfo, event, config, mapping, cloudPath);
        }
      } else {
        const oldStrmFileName = getStrmFileName(oldEntry.fileName);
        const oldStrmPath = path.join(
          path.dirname(oldMapping.localPath),
          oldStrmFileName
        );
        const newStrmFileName = getStrmFileName(effectiveNewName);
        const newStrmPath = path.join(path.dirname(mapping.localPath), newStrmFileName);

        if (fs.existsSync(oldStrmPath)) {
          const newDir = path.dirname(newStrmPath);
          if (!fs.existsSync(newDir)) {
            fs.mkdirSync(newDir, { recursive: true });
          }

          if (path.dirname(oldStrmPath) === path.dirname(newStrmPath)) {
            if (fs.existsSync(newStrmPath) && oldStrmPath !== newStrmPath) {
              deleteStrmFile(newStrmPath, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name });
            }
            if (oldStrmPath !== newStrmPath) {
              fs.renameSync(oldStrmPath, newStrmPath);
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
              syncStrmText(newStrmPath, newContent, { tag: "LifeMonitor/rename", account: accountInfo.name });
            }
          } else {
            const content = readStrmContent(oldStrmPath);
            if (content) {
              syncStrmText(newStrmPath, content, { tag: "LifeMonitor/rename", account: accountInfo.name });
              deleteStrmFile(oldStrmPath, { tag: "LifeMonitor/rename", cleanRelated: false, account: accountInfo.name });
            } else {
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

          handleRelatedFileRenames(oldStrmPath, newStrmPath, oldEntry.fileName, effectiveNewName);

          result.success = true;
          result.message = `STRM 已重命名: ${oldStrmPath} -> ${newStrmPath}`;
        } else {
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

    console.info(`[LifeMonitor] rename-cross-mapping: fileId=${event.file_id}, fileName=${oldEntry.fileName}, oldEntry.path=${oldEntry.path}, newCloudPath=${cloudPath}, fileCategory=${event.file_category}`);
    const cleanup = tryCleanupOldStrmByPath(
      accountInfo.name,
      oldEntry.path,
      oldEntry.fileName,
      event.file_category,
      config.pathMappings
    );
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

  console.info(
    `[LifeMonitor] rename-no-entry: fileId=${event.file_id}, oldName="${event.file_name}", newName="${effectiveNewName}", fileCategory=${event.file_category}, newCloudPath=${cloudPath}`
  );
  const rootDirs = getRootDirsFromMappings(config.pathMappings);
  const newLocalResolved = path.resolve(mapping.localPath);
  let noEntryCleaned = 0;

  if (event.file_category === 0) {
    for (const m of config.pathMappings) {
      if (m.account && m.account !== accountInfo.name) continue;
      if (!fs.existsSync(m.localPath)) continue;
      try {
        const foundDirs = findDirRecursive(m.localPath, event.file_name);
        for (const d of foundDirs) {
          const resolved = path.resolve(d);
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
    const oldStrmName = getStrmFileName(event.file_name);
    for (const m of config.pathMappings) {
      if (m.account && m.account !== accountInfo.name) continue;
      if (!fs.existsSync(m.localPath)) continue;
      try {
        const found = findStrmRecursive(m.localPath, oldStrmName);
        for (const p of found) {
          if (path.resolve(p) === newLocalResolved) continue;
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
    await waitFor115ApiToken();

    const isRenameEvent = RENAME_EVENT_TYPES.has(eventType);
    let renameNewName = "";
    if (isRenameEvent) {
      const rawEvent = event as Record<string, unknown>;
      renameNewName = String(
        rawEvent["new_name"] || rawEvent["new_file_name"] || rawEvent["to_name"] || rawEvent["to_file_name"] || ""
      );
      console.info(
        `[LifeMonitor] rename-event-raw: type=${eventType}, fileId=${event.file_id}, file_name="${event.file_name}", new_name="${renameNewName}", file_category=${event.file_category}, parent_id=${event.parent_id}, pick_code="${event.pick_code}", allKeys=${Object.keys(rawEvent).join(",")}`
      );
    }

    const cloudPath = await resolveEventPath(
      accountInfo,
      event,
      idPathMemoryCache,
      isRenameEvent && renameNewName ? renameNewName : undefined
    );
    if (!cloudPath) {
      result.message = "无法解析文件路径";
      return result;
    }
    result.filePath = cloudPath;

    if (isRenameEvent && !renameNewName) {
      console.warn(
        `[LifeMonitor] rename 事件未找到 new_name 字段! fileId=${event.file_id}, file_name="${event.file_name}", cloudPath="${cloudPath}". 尝试通过 filePathDb 旧记录推断新路径。`
      );
    }

    const isMutationEvent = MOVE_EVENT_TYPES.has(eventType) || RENAME_EVENT_TYPES.has(eventType) || DELETE_EVENT_TYPES.has(eventType);

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
          if (cleanup.deleted.length === 0 && event.file_category === 0) {
            const rootDirs = getRootDirsFromMappings(config.pathMappings);
            for (const m of config.pathMappings) {
              if (m.account && m.account !== accountInfo.name) continue;
              if (!fs.existsSync(m.localPath)) continue;
              try {
                const foundDirs = findDirRecursive(m.localPath, oldEntry.fileName);
                for (const d of foundDirs) {
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
          removeFilePathEntry(accountInfo.name, event.file_id);

          if (config.removeEmptyDirs) {
            const rootDirs = getRootDirsFromMappings(config.pathMappings);
            const oldMapping = matchPathMapping(oldEntry.path, config.pathMappings, accountInfo.name);
            if (oldMapping) removeEmptyParents(oldMapping.localPath, { rootDirs, tag: "LifeMonitor/move", account: accountInfo.name });
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
        if (config.removeEmptyDirs || event.file_category === 0) {
          const rootDirs = getRootDirsFromMappings(config.pathMappings);
          let localDeletedCount = 0;
          for (const m of config.pathMappings) {
            if (m.account && m.account !== accountInfo.name) continue;
            if (!fs.existsSync(m.localPath)) continue;
            try {
              if (event.file_category === 0) {
                const candidateDirs = findDirRecursive(m.localPath, event.file_name);
                for (const candidateDir of candidateDirs) {
                  if (rootDirs.has(path.resolve(candidateDir))) continue;
                  if (fs.existsSync(candidateDir) && fs.statSync(candidateDir).isDirectory()) {
                    deleteStrmDir(candidateDir, { tag: "LifeMonitor/move-outside-nodb", account: accountInfo.name });
                    localDeletedCount++;
                    console.info(`[LifeMonitor] move-outside-nodb: 清理文件夹(无DB记录) ${candidateDir}`);
                    removeEmptyParents(candidateDir, { rootDirs, tag: "LifeMonitor/move-outside-nodb", account: accountInfo.name });
                  }
                }
              } else {
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

    if (event.file_category === 1 && !isMediaFile(event.file_name, getStrmExtensions())) {
      result.action = "skip";
      result.message = `非媒体文件: ${event.file_name}`;
      return result;
    }

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

export {
  handleCreateEvent,
  handleFolderCreateEvent,
  handleDeleteEvent,
  handleMoveEvent,
  handleRenameEvent,
  handleRelatedFileRenames,
  cleanupResidualStrmsInOldFolder,
  processEvent,
  logEventTrace,
};