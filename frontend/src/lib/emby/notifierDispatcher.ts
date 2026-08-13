import type {
  EmbyItemDetail,
  EmbyWebhookEvent,
  EpisodeBuffer,
  PlaybackCacheEntry,
} from "./types";
import { getItemDetail } from "./client";
import { readSettings } from "../serverUtils";
import { handleSyncDelete } from "./syncDel";
import {
  formatMovieNotification,
  formatSeriesNotification,
  formatDeletedMovieNotification,
  formatDeletedSeriesNotification,
  formatPlaybackNotification,
  formatSeasonEpisodes,
} from "./notifierTemplates";
import {
  getTgBotAndChat,
  sendEmbyText,
  sendEmbyWithPoster,
} from "./notifierSender";

// ========== 剧集缓冲（入库/删除独立，移植自 qmediasync newSeriesBuffer / deletedSeriesBuffer）==========
const addedEpisodeBuffer = new Map<string, EpisodeBuffer>();
const addedEpisodeTimers = new Map<string, ReturnType<typeof setTimeout>>();
const deletedEpisodeBuffer = new Map<string, EpisodeBuffer>();
const deletedEpisodeTimers = new Map<string, ReturnType<typeof setTimeout>>();
const DEBOUNCE_WINDOW_MS = 10_000;
const BUFFER_CHECK_INTERVAL_MS = 5_000;
let bufferCheckerInterval: ReturnType<typeof setInterval> | null = null;
let bufferCheckerRefCount = 0;

function refBufferChecker(): void {
  bufferCheckerRefCount++;
  if (!bufferCheckerInterval) {
    bufferCheckerInterval = setInterval(() => {
      const now = Date.now();
      // 处理新增缓冲
      for (const [seriesId, buffer] of addedEpisodeBuffer.entries()) {
        if (now - buffer.lastUpdated >= DEBOUNCE_WINDOW_MS) {
          void flushAddedEpisodeBuffer(seriesId);
        }
      }
      // 处理删除缓冲
      for (const [seriesId, buffer] of deletedEpisodeBuffer.entries()) {
        if (now - buffer.lastUpdated >= DEBOUNCE_WINDOW_MS) {
          void flushDeletedEpisodeBuffer(seriesId);
        }
      }
      // 两个缓冲都空了，停掉定时器
      if (
        addedEpisodeBuffer.size === 0 &&
        deletedEpisodeBuffer.size === 0 &&
        bufferCheckerInterval
      ) {
        clearInterval(bufferCheckerInterval);
        bufferCheckerInterval = null;
        bufferCheckerRefCount = 0;
      }
    }, BUFFER_CHECK_INTERVAL_MS);
  }
}

// ========== 播放事件去重（移植自 qmediasync playbackEventCache） ==========
const playbackCache = new Map<string, PlaybackCacheEntry>();
const PLAYBACK_DEDUP_WINDOW_MS = 60_000;
const PLAYBACK_CACHE_TTL_MS = 5 * 60_000;

// ========== 入库通知 ==========

async function handleMovieAdded(item: EmbyWebhookEvent["Item"]): Promise<void> {
  const settings = readSettings();
  if (!settings.emby?.notifyMediaAdded) return;
  if (!item.Id) return;
  if (!getTgBotAndChat()) return; // 前置配置检查

  const cfg = {
    url: settings.emby.url || "",
    apiKey: settings.emby.apiKey || "",
    userAgent: (settings["user-agent"] || "FastStrm/1.0") as string,
  };
  const detail = await getItemDetail(item.Id, cfg);

  if (!detail) {
    const fallback = `<b>📺 Emby 电影入库通知</b>

<b>${item.Name || "未知"}</b>

⏰ 入库时间: ${new Date().toLocaleString()}

<i>（详情获取失败，已降级为简版通知）</i>`;
    await sendEmbyText(fallback);
    return;
  }

  const message = formatMovieNotification(detail);
  await sendEmbyWithPoster(item.Id, detail.ImageTags, message, cfg);
}

// 剧集入库缓冲
function handleSeriesEpisodeAdded(item: EmbyWebhookEvent["Item"]): void {
  if (!item.SeriesId) return;
  const seriesId = item.SeriesId;

  const buffer: EpisodeBuffer = addedEpisodeBuffer.get(seriesId) || {
    seriesId,
    seriesName: item.SeriesName || item.Name || "未知",
    seasons: new Map(),
    lastUpdated: Date.now(),
  };
  const seasonNumber = item.ParentIndexNumber || 0;
  const episodeNumber = item.IndexNumber || 0;
  if (!buffer.seasons.has(seasonNumber)) buffer.seasons.set(seasonNumber, []);
  const episodes = buffer.seasons.get(seasonNumber)!;
  if (!episodes.includes(episodeNumber)) episodes.push(episodeNumber);
  buffer.lastUpdated = Date.now();
  addedEpisodeBuffer.set(seriesId, buffer);

  const existingTimer = addedEpisodeTimers.get(seriesId);
  if (existingTimer) clearTimeout(existingTimer);
  const timer = setTimeout(() => void flushAddedEpisodeBuffer(seriesId), DEBOUNCE_WINDOW_MS);
  addedEpisodeTimers.set(seriesId, timer);
  refBufferChecker();
}

async function flushAddedEpisodeBuffer(seriesId: string): Promise<void> {
  const timer = addedEpisodeTimers.get(seriesId);
  if (timer) {
    clearTimeout(timer);
    addedEpisodeTimers.delete(seriesId);
  }
  const buffer = addedEpisodeBuffer.get(seriesId);
  if (!buffer) return;
  addedEpisodeBuffer.delete(seriesId);

  if (Date.now() - buffer.lastUpdated < DEBOUNCE_WINDOW_MS - 500) return;

  const settings = readSettings();
  if (!settings.emby?.notifyMediaAdded) return;
  if (!settings.emby.url || !settings.emby.apiKey) return;
  if (!getTgBotAndChat()) return;

  const cfg = {
    url: settings.emby.url,
    apiKey: settings.emby.apiKey,
    userAgent: (settings["user-agent"] || "FastStrm/1.0") as string,
  };

  try {
    const detail = await getItemDetail(seriesId, cfg);
    const message = detail
      ? formatSeriesNotification(detail, buffer.seasons)
      : `<b>📺 Emby 电视剧入库通知</b>

<b>${buffer.seriesName}</b>
📺 入库季集: ${formatSeasonEpisodes(buffer.seasons)}
⏰ 入库时间: ${new Date().toLocaleString()}

<i>（详情获取失败，已降级为简版通知）</i>`;

    if (detail) {
      await sendEmbyWithPoster(seriesId, detail.ImageTags, message, cfg);
    } else {
      await sendEmbyText(message);
    }
  } catch (err) {
    console.error(`[Emby] 发送剧集入库通知失败 seriesId=${seriesId}:`, err);
  }
}

// ========== 删除通知 ==========

async function handleMovieDeleted(item: EmbyWebhookEvent["Item"]): Promise<void> {
  const settings = readSettings();
  if (!settings.emby?.notifyMediaRemoved) return;
  if (!getTgBotAndChat()) return;
  const text = formatDeletedMovieNotification(item.Name || "未知");
  await sendEmbyText(text);
}

function handleSeriesEpisodeDeleted(item: EmbyWebhookEvent["Item"]): void {
  // P13修复：纯 Series/Season 项没有 SeriesId，原逻辑直接 return 导致通知被丢弃
  // 改为：无 SeriesId 时走直接通知（不走防抖聚合），保证用户能收到删除通知
  if (!item.SeriesId) {
    if (item.Type === "Series" || item.Type === "Season") {
      const settings = readSettings();
      if (!settings.emby?.notifyMediaRemoved) return;
      if (!getTgBotAndChat()) return;
      const typeLabel = item.Type === "Series" ? "整剧" : "季";
      const text = `🗑️ <b>${typeLabel}已删除</b>\n<b>标题:</b> ${item.Name || "未知"}`;
      void sendEmbyText(text);
    }
    return;
  }
  const seriesId = item.SeriesId;

  const buffer: EpisodeBuffer = deletedEpisodeBuffer.get(seriesId) || {
    seriesId,
    seriesName: item.SeriesName || item.Name || "未知",
    seasons: new Map(),
    lastUpdated: Date.now(),
  };
  const seasonNumber = item.ParentIndexNumber || 0;
  const episodeNumber = item.IndexNumber || 0;
  if (!buffer.seasons.has(seasonNumber)) buffer.seasons.set(seasonNumber, []);
  const episodes = buffer.seasons.get(seasonNumber)!;
  if (!episodes.includes(episodeNumber)) episodes.push(episodeNumber);
  buffer.lastUpdated = Date.now();
  deletedEpisodeBuffer.set(seriesId, buffer);

  const existingTimer = deletedEpisodeTimers.get(seriesId);
  if (existingTimer) clearTimeout(existingTimer);
  const timer = setTimeout(() => void flushDeletedEpisodeBuffer(seriesId), DEBOUNCE_WINDOW_MS);
  deletedEpisodeTimers.set(seriesId, timer);
  refBufferChecker();
}

async function flushDeletedEpisodeBuffer(seriesId: string): Promise<void> {
  const timer = deletedEpisodeTimers.get(seriesId);
  if (timer) {
    clearTimeout(timer);
    deletedEpisodeTimers.delete(seriesId);
  }
  const buffer = deletedEpisodeBuffer.get(seriesId);
  if (!buffer) return;
  deletedEpisodeBuffer.delete(seriesId);

  if (Date.now() - buffer.lastUpdated < DEBOUNCE_WINDOW_MS - 500) return;

  const settings = readSettings();
  if (!settings.emby?.notifyMediaRemoved) return;
  if (!getTgBotAndChat()) return;

  const body = formatDeletedSeriesNotification(buffer.seriesName, buffer.seasons);
  await sendEmbyText(body);
}

// ========== 播放通知（带海报，对齐 qmediasync） ==========
async function handlePlaybackEvent(event: EmbyWebhookEvent): Promise<void> {
  const settings = readSettings();
  if (!settings.emby?.notifyPlayback) return;
  if (!getTgBotAndChat()) return;

  // 去重（1分钟内不重复）
  const cacheKey =
    `${event.User?.Id || "unknown"}_${event.Item?.Type || "unknown"}_` +
    `${event.Item?.Name || "unknown"}_${event.Session?.DeviceName || "unknown"}_${event.Event}`;
  const now = Date.now();
  const cached = playbackCache.get(cacheKey);
  if (cached && now - cached.timestamp < PLAYBACK_DEDUP_WINDOW_MS) return;
  playbackCache.set(cacheKey, { timestamp: now });
  // 清理过期
  for (const [k, v] of playbackCache.entries()) {
    if (now - v.timestamp > PLAYBACK_CACHE_TTL_MS) playbackCache.delete(k);
  }

  const message = await formatPlaybackNotification(event.Event, event);

  // 尝试带海报发送（对齐 qmediasync 的 createPlaybackNotification）
  const cfg = {
    url: settings.emby?.url || "",
    apiKey: settings.emby?.apiKey || "",
    userAgent: (settings["user-agent"] || "FastStrm/1.0") as string,
  };
  const itemId = event.Item?.Id;
  const imageTags = event.Item?.ImageTags;
  if (itemId && imageTags && cfg.url && cfg.apiKey) {
    await sendEmbyWithPoster(itemId, imageTags as EmbyItemDetail["ImageTags"], message, cfg);
  } else {
    await sendEmbyText(message);
  }
}

// ========== 主事件分发 ==========
export async function handleEmbyWebhookEvent(event: EmbyWebhookEvent): Promise<void> {
  if (!event || !event.Event) return;

  console.log(`[Emby] 收到事件: ${event.Event}, 项目: ${event.Item?.Name || "unknown"}`);

  switch (event.Event) {
    case "library.new":
      if (event.Item?.Type === "Movie") {
        void handleMovieAdded(event.Item);
      } else if (event.Item?.Type === "Episode") {
        handleSeriesEpisodeAdded(event.Item);
      }
      break;

    case "library.deleted": {
      // 删除同步：删 STRM + 关联文件 + DB 记录（独立于通知逻辑）
      if (event.Item?.Path) {
        handleSyncDelete(event.Item).catch(err => {
          console.error("[SyncDel] 处理失败:", err);
        });
      }
      const s = readSettings();
      const skipOriginalNotify = !!s.emby?.syncDeleteEnabled && !!s.emby?.syncDeleteNotify;
      if (!skipOriginalNotify) {
        if (event.Item?.Type === "Movie") {
          void handleMovieDeleted(event.Item);
        } else if (
          event.Item?.Type === "Episode" ||
          event.Item?.Type === "Series" ||
          event.Item?.Type === "Season"
        ) {
          handleSeriesEpisodeDeleted(event.Item);
        }
      }
      break;
    }

    case "playback.start":
    case "playback.pause":
    case "playback.stop":
      void handlePlaybackEvent(event);
      break;

    default:
      console.log(`[Emby] 未处理的事件类型: ${event.Event}`);
  }
}

export {
  handleMovieAdded,
  handleSeriesEpisodeAdded,
  flushAddedEpisodeBuffer,
  handleMovieDeleted,
  handleSeriesEpisodeDeleted,
  flushDeletedEpisodeBuffer,
  handlePlaybackEvent,
};