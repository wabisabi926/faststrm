/**
 * Emby 删除同步：监听 library.deleted 事件，自动删除本地 STRM + 关联文件
 *
 * 参考项目：MoviePilot-Plugins samediasyncdel（裁剪为 faststrm 场景）
 * 核心链路：
 *   webhook → 白名单匹配 → 去重检查 → 路径映射 → 防误删1(STRM存在) →
 *   防误删2(标题校验) → 防误删3(目录文件数) → 删STRM+关联+空目录 →
 *   更新filePathDb → 写历史 → TG通知
 *
 * 不移植的能力（与 samediasyncdel 的差异）：
 *   - 不删网盘原文件（安全边界：faststrm 只管 STRM）
 *   - 不删转移记录/下载任务/种子（faststrm 无此概念）
 *   - 不需要 TMDB ID（用路径匹配）
 *   - 不监听 deep.delete（用原生 library.deleted）
 */

import * as fs from "fs";
import * as path from "path";
import { readSettings, readAccounts } from "../serverUtils";
import { deleteStrmFile, deleteStrmDir, removeEmptyParents } from "../strmFileOps";
import { deleteByPath, deleteByPathPrefix } from "../filePathDb";
import { createTelegramBot } from "../telegram";
import { addSyncDelRecord } from "../syncDelHistory";
import type { EmbyWebhookEvent } from "./types";

const TAG = "SyncDel";

// 去重窗口：60 秒内生活监控可能已处理同一路径
const DEDUP_WINDOW_MS = 60_000;
// 去重缓存清理阈值（超过 5 分钟的条目清理）
const DEDUP_CLEANUP_MS = 5 * 60_000;
// 整季/整剧删除的目录文件数安全阈值
const MAX_DIR_FILES_THRESHOLD = 100;

// 模块级去重缓存（抗 HMR 用 globalThis）
const globalForDedup = globalThis as unknown as { __syncDelDedup?: Map<string, number> };
if (!globalForDedup.__syncDelDedup) {
  globalForDedup.__syncDelDedup = new Map();
}
const recentDeletions = globalForDedup.__syncDelDedup;

export interface SyncDeleteResult {
  success: boolean;
  itemName: string;
  itemType: string;
  deletedFiles: number;
  deletedDirs: number;
  dryRun: boolean;
  skipped: boolean;
  reason?: string;
}

/**
 * Emby library.deleted 事件处理入口
 */
export async function handleSyncDelete(
  item: EmbyWebhookEvent["Item"]
): Promise<SyncDeleteResult> {
  const settings = readSettings();
  const emby = settings.emby;

  // 基础检查
  if (!emby?.syncDeleteEnabled) {
    return skipResult(item, "sync_delete_disabled");
  }

  const mappings = emby.syncDeletePathMappings || [];
  if (mappings.length === 0) {
    return skipResult(item, "no_path_mappings");
  }

  const embyPath = item.Path;
  if (!embyPath) {
    return skipResult(item, "no_item_path");
  }

  const dryRun = emby.syncDeleteDryRun === true;
  const itemName = item.Name || "";
  const itemType = item.Type || "";

  console.log(`[${TAG}] 收到删除事件: type=${itemType} name=${itemName} path=${embyPath} dryRun=${dryRun}`);

  // 白名单检查：路径必须匹配某个映射前缀
  const mapping = matchPathMapping(embyPath, mappings);
  if (!mapping) {
    console.log(`[${TAG}] 路径未匹配任何映射，跳过: ${embyPath}`);
    return skipResult(item, "path_not_matched", dryRun);
  }

  // 去重检查：60 秒内生活监控可能已处理
  if (isRecentlyDeleted(embyPath)) {
    console.log(`[${TAG}] 60秒内已处理过该路径，跳过: ${embyPath}`);
    return skipResult(item, "recently_deleted", dryRun);
  }

  // 计算网盘路径
  const cloudPath = mapEmbyPathToCloud(embyPath, mapping);
  if (!cloudPath) {
    console.log(`[${TAG}] 路径映射失败: ${embyPath}`);
    return skipResult(item, "mapping_failed", dryRun);
  }

  // 防误删1：STRM 文件/目录必须存在才处理
  if (!fs.existsSync(embyPath)) {
    console.log(`[${TAG}] STRM 路径不存在，跳过（可能已被生活监控处理）: ${embyPath}`);
    return skipResult(item, "strm_not_exists", dryRun);
  }

  // 防误删2：标题校验（仅对 Movie 做严格校验）
  // P0修复：Episode 的 item.Name 通常是 "第一集" 等中文名，而文件名是 S01E01 格式，
  // 两者不匹配会导致所有 Episode 删除事件被跳过。因此 Episode 跳过标题校验，
  // 通过前面的路径存在性校验已足够防误删。
  const stat = fs.statSync(embyPath);
  const isDir = stat.isDirectory();
  if (!isDir && itemType === "Movie" && itemName) {
    const baseName = path.basename(embyPath, path.extname(embyPath));
    if (!baseName.includes(itemName) && !itemName.includes(baseName)) {
      console.warn(`[${TAG}] 电影标题不匹配，防误删跳过: ${baseName} vs ${itemName}`);
      return skipResult(item, "title_mismatch", dryRun);
    }
  }

  // 按类型分支删除
  let deletedFiles = 0;
  let deletedDirs = 0;

  if (dryRun) {
    console.log(`[${TAG}] 试运行模式，仅记录不删除: ${embyPath} (cloudPath=${cloudPath})`);
    // 试运行模式仍统计文件数
    if (isDir) {
      try {
        const entries = fs.readdirSync(embyPath, { recursive: true });
        deletedFiles = entries.length;
      } catch { /* ignore */ }
    } else {
      deletedFiles = 1;
    }
  } else {
    const result = deleteByItemType(embyPath, itemType, mapping);
    deletedFiles = result.deletedFiles;
    deletedDirs = result.deletedDirs;

    // 更新 filePathDb
    cleanupDbRecords(cloudPath, itemType, mapping.account);
  }

  // 记录去重
  markRecentlyDeleted(embyPath);

  // 写历史
  addSyncDelRecord({
    itemPath: embyPath,
    itemName,
    itemType,
    deletedAt: new Date().toISOString(),
    deletedFiles,
    cloudPath,
    dryRun,
  });

  // TG 通知（裸发，不经过 sendTelegramNotification 的 Task Completed 包装）
  if (emby.syncDeleteNotify) {
    const message = formatDeleteNotification(itemName, itemType, deletedFiles, deletedDirs, dryRun);
    await sendSyncDelText(message);
  }

  console.log(`[${TAG}] 完成: type=${itemType} name=${itemName} files=${deletedFiles} dirs=${deletedDirs} dryRun=${dryRun}`);

  return {
    success: true,
    itemName,
    itemType,
    deletedFiles,
    deletedDirs,
    dryRun,
    skipped: false,
  };
}

// ==================== 路径映射 ====================

function matchPathMapping(
  embyPath: string,
  mappings: Array<{ embyPath: string; cloudPath: string; account?: string }>
): { embyPath: string; cloudPath: string; account?: string } | null {
  const normalized = path.normalize(embyPath).replace(/\\/g, "/");

  // P1修复：选择最长前缀匹配（最具体的路径），而非按配置顺序取第一个。
  // 当存在 /media 和 /media/movies 两条映射时，/media/movies/foo 应匹配后者。
  let bestMatch: { embyPath: string; cloudPath: string; account?: string } | null = null;
  let bestPrefixLen = -1;

  for (const m of mappings) {
    const prefix = path.normalize(m.embyPath).replace(/\\/g, "/");
    if (normalized === prefix || normalized.startsWith(prefix + "/")) {
      if (prefix.length > bestPrefixLen) {
        bestPrefixLen = prefix.length;
        bestMatch = m;
      }
    }
  }
  return bestMatch;
}

function mapEmbyPathToCloud(
  embyPath: string,
  mapping: { embyPath: string; cloudPath: string }
): string | null {
  const normalizedEmby = path.normalize(embyPath).replace(/\\/g, "/");
  const prefix = path.normalize(mapping.embyPath).replace(/\\/g, "/");
  const cloudPrefix = mapping.cloudPath.replace(/\/+$/, "");

  const relativePath = path.relative(prefix, normalizedEmby).replace(/\\/g, "/");
  if (relativePath === "" || relativePath === ".") {
    return cloudPrefix;
  }
  return `${cloudPrefix}/${relativePath}`;
}

// ==================== 去重 ====================

function isRecentlyDeleted(strmPath: string): boolean {
  const ts = recentDeletions.get(strmPath);
  if (ts && Date.now() - ts < DEDUP_WINDOW_MS) {
    return true;
  }
  if (ts) recentDeletions.delete(strmPath);
  return false;
}

function markRecentlyDeleted(strmPath: string): void {
  recentDeletions.set(strmPath, Date.now());
  // 顺便清理过期条目
  const now = Date.now();
  for (const [key, ts] of recentDeletions) {
    if (now - ts > DEDUP_CLEANUP_MS) {
      recentDeletions.delete(key);
    }
  }
}

// ==================== 按类型删除 ====================

function deleteByItemType(
  strmPath: string,
  itemType: string,
  mapping: { embyPath: string; cloudPath: string; account?: string }
): { deletedFiles: number; deletedDirs: number } {
  const rootDirs = new Set<string>([path.normalize(mapping.embyPath)]);

  if (itemType === "Movie" || itemType === "Episode") {
    // 删单个 STRM 文件 + 关联文件 + 空目录
    const result = deleteStrmFile(strmPath, {
      rootDirs,
      cleanRelated: true,
      tag: TAG,
    });
    return {
      deletedFiles: (result.deleted ? 1 : 0) + result.relatedDeleted.length,
      deletedDirs: result.removedDirs.length,
    };
  }

  if (itemType === "Season" || itemType === "Series") {
    // 计算要删除的目录
    let dirPath = strmPath;
    const stat = safeStat(strmPath);
    if (stat && !stat.isDirectory()) {
      // 文件路径 → 取父目录
      dirPath = path.dirname(strmPath);
    }
    // P11修复：移除 Series 上移一级逻辑
    // Emby library.deleted 事件对 Series 类型传的 item.Path 已是剧集根目录，
    // 上移一级会误删媒体库分类目录。直接使用 item.Path 作为删除目标。

    // 防误删3：目录文件数校验
    let fileCount = 0;
    try {
      const entries = fs.readdirSync(dirPath, { recursive: true });
      fileCount = entries.length;
      if (fileCount === 0) {
        console.warn(`[${TAG}] 目录为空，跳过: ${dirPath}`);
        return { deletedFiles: 0, deletedDirs: 0 };
      }
      if (fileCount > MAX_DIR_FILES_THRESHOLD) {
        console.error(`[${TAG}] 目录文件数异常（${fileCount}），疑似误判，跳过: ${dirPath}`);
        return { deletedFiles: 0, deletedDirs: 0 };
      }
    } catch (e) {
      console.error(`[${TAG}] 目录读取失败: ${dirPath}`, e);
      return { deletedFiles: 0, deletedDirs: 0 };
    }

    // 目录级删除
    const result = deleteStrmDir(dirPath, { tag: TAG });
    if (!result.deleted) {
      console.error(`[${TAG}] 目录删除失败: ${dirPath} error=${result.error}`);
      return { deletedFiles: 0, deletedDirs: 0 };
    }

    // 清理空父目录
    const removedDirs = removeEmptyParents(dirPath, { rootDirs, tag: TAG });

    return {
      deletedFiles: fileCount,
      deletedDirs: 1 + removedDirs.length,
    };
  }

  console.warn(`[${TAG}] 未知类型: ${itemType}`);
  return { deletedFiles: 0, deletedDirs: 0 };
}

function safeStat(p: string): fs.Stats | null {
  try {
    return fs.statSync(p);
  } catch {
    return null;
  }
}

// ==================== DB 清理 ====================

function cleanupDbRecords(cloudPath: string, itemType: string, account?: string): void {
  const accounts = account
    ? [account]
    : readAccounts()
        .filter((a: { accountType?: string }) => a.accountType === "115")
        .map((a: { name: string }) => a.name);

  for (const acc of accounts) {
    try {
      if (itemType === "Movie" || itemType === "Episode") {
        const deleted = deleteByPath(acc, cloudPath);
        if (deleted > 0) {
          console.log(`[${TAG}] DB 删除 ${deleted} 条记录: account=${acc} path=${cloudPath}`);
        }
      } else {
        // 整季/整剧：按前缀删除
        const deleted = deleteByPathPrefix(acc, cloudPath);
        if (deleted > 0) {
          console.log(`[${TAG}] DB 前缀删除 ${deleted} 条记录: account=${acc} prefix=${cloudPath}`);
        }
      }
    } catch (e) {
      console.error(`[${TAG}] DB 清理失败: account=${acc} path=${cloudPath}`, e);
    }
  }
}

// ==================== 通知 ====================

/**
 * 裸发 TG 文本通知（与 notifier.ts 的 sendEmbyText 同语义）
 * 避免 sendTelegramNotification 的 ✅ Task Completed 二次包装
 */
async function sendSyncDelText(text: string): Promise<void> {
  try {
    const s = readSettings();
    const tg = s.telegram;
    if (!tg?.enabled || !tg.botToken || !tg.chatId) return;
    const bot = createTelegramBot(tg.botToken);
    await bot.sendNotification(text, tg.chatId);
  } catch (err) {
    console.error(`[${TAG}] TG 通知发送失败:`, err);
  }
}

function formatDeleteNotification(
  itemName: string,
  itemType: string,
  deletedFiles: number,
  deletedDirs: number,
  dryRun: boolean
): string {
  const typeMap: Record<string, string> = {
    Movie: "电影",
    Episode: "剧集",
    Season: "季",
    Series: "整剧",
  };
  const typeText = typeMap[itemType] || itemType;
  const dryRunTag = dryRun ? " [试运行]" : "";

  return `🗑️ 媒体删除同步${dryRunTag}
<b>标题:</b> ${itemName}
<b>类型:</b> ${typeText}
<b>删除文件:</b> ${deletedFiles}
<b>清理目录:</b> ${deletedDirs}
<b>时间:</b> ${new Date().toLocaleString("zh-CN")}`;
}

// ==================== 工具 ====================

function skipResult(
  item: EmbyWebhookEvent["Item"],
  reason: string,
  dryRun = false
): SyncDeleteResult {
  return {
    success: false,
    itemName: item.Name || "",
    itemType: item.Type || "",
    deletedFiles: 0,
    deletedDirs: 0,
    dryRun,
    skipped: true,
    reason,
  };
}
