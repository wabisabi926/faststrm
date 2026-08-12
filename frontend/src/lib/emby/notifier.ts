// Emby 通知逻辑（全盘移植自 qmediasync + 适配 Next.js）
import type {
  EmbyItemDetail,
  EmbyWebhookEvent,
  EpisodeBuffer,
  PlaybackCacheEntry,
} from "./types";
import { getItemDetail, buildImageUrl } from "./client";
import { createTelegramBot, TelegramBot } from "../telegram";
import { readSettings } from "../serverUtils";
import { handleSyncDelete } from "./syncDel";
import axios from "axios";
import fs from "node:fs";

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

// ========== 通知模板（移植自 qmediasync notificationTemplate，保留评分 🆔） ==========
const MOVIE_TEMPLATE = `
{{title}} ({{year}})

🆔 评分: {{rate}}
🎬 类型: {{genres}}
👤 主演: {{actors}}
⏰ 入库时间: {{addedTime}}

📝 简介
{{overview}}
`;

const SERIES_TEMPLATE = `
{{title}} ({{year}})
{{seasonEpisodes}}
🆔 评分: {{rate}}
🎬 类型: {{genres}}
👤 主演: {{actors}}
⏰ 入库时间: {{addedTime}}

📝 简介
{{overview}}
`;

const DELETED_MOVIE_TEMPLATE = `
🗑️ 电影名称：{{title}}
⏰ 删除时间: {{time}}
`;

const DELETED_SERIES_TEMPLATE = `
🗑️ 电视剧名称：{{title}}
{{seasonEpisodes}}⏰ 删除时间: {{time}}
`;

// ========== 辅助函数 ==========
function formatSeasonEpisodes(seasons: Map<number, number[]>): string {
  if (seasons.size === 0) return "";

  const seasonNumbers = [...seasons.keys()].sort((a, b) => a - b);
  const parts: string[] = [];

  for (const seasonNumber of seasonNumbers) {
    const episodesRaw = [...(seasons.get(seasonNumber) || [])];
    if (episodesRaw.length === 0) continue;

    // 去重
    const uniqueEpisodes = [...new Set(episodesRaw)].sort((a, b) => a - b);

    let range = "";
    let start = uniqueEpisodes[0];
    let prev = uniqueEpisodes[0];

    for (let i = 1; i < uniqueEpisodes.length; i++) {
      if (uniqueEpisodes[i] !== prev + 1) {
        range += start === prev ? `E${start}` : `E${start}-E${prev}`;
        range += ", ";
        start = uniqueEpisodes[i];
      }
      prev = uniqueEpisodes[i];
    }
    range += start === prev ? `E${start}` : `E${start}-E${prev}`;
    parts.push(`S${seasonNumber}${range}`);
  }

  return parts.join("; ");
}

/** 参考 qmediasync：HH:MM:SS 格式 */
function formatTicksToTime(ticks: number): string {
  // Emby ticks: 1 tick = 10,000 nanoseconds = 0.00001 seconds
  const totalSeconds = Math.floor(ticks / 10_000_000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const pad = (n: number) => n.toString().padStart(2, "0");
  if (hours > 0) return `${pad(hours)}:${pad(minutes)}:${pad(seconds)}`;
  return `${pad(minutes)}:${pad(seconds)}`;
}

function getEventTypeEmoji(event: string): string {
  switch (event) {
    case "playback.start": return "▶️";
    case "playback.pause": return "⏸️";
    case "playback.stop": return "⏹️";
    default: return "📺";
  }
}

function getEventTypeName(event: string): string {
  switch (event) {
    case "playback.start": return "播放开始";
    case "playback.pause": return "播放暂停";
    case "playback.stop": return "播放结束";
    default: return "播放事件";
  }
}

// ========== 格式化通知内容 ==========
function fillTemplate(
  template: string,
  data: Record<string, string | number>
): string {
  let result = template;
  for (const [key, value] of Object.entries(data)) {
    result = result.replaceAll(`{{${key}}}`, String(value));
  }
  return result.trim();
}

/** 解析 Emby DateCreated，失败回退当前时间 */
function formatDateCreated(dateCreated?: string): string {
  if (!dateCreated) return new Date().toLocaleString();
  const t = new Date(dateCreated);
  if (Number.isNaN(t.getTime())) return new Date().toLocaleString();
  return t.toLocaleString();
}

function formatMovieNotification(detail: EmbyItemDetail): string {
  const genres = detail.Genres?.length ? detail.Genres.join(", ") : "暂无数据";
  const actors =
    detail.People?.filter(p => p.Type === "Actor").slice(0, 5).map(p => p.Name).join(", ") ||
    "暂无数据";
  const overview = detail.Overview || "暂无简介";
  const rating = detail.CommunityRating && detail.CommunityRating > 0
    ? detail.CommunityRating.toFixed(1)
    : "暂无数据";
  const addedTime = formatDateCreated(detail.DateCreated);

  return fillTemplate(MOVIE_TEMPLATE, {
    title: detail.Name || "未知",
    year: detail.ProductionYear || "未知",
    rate: rating,
    genres,
    actors,
    addedTime,
    overview,
  });
}

function formatSeriesNotification(
  detail: EmbyItemDetail,
  seasons: Map<number, number[]>
): string {
  const genres = detail.Genres?.length ? detail.Genres.join(", ") : "暂无数据";
  const actors =
    detail.People?.filter(p => p.Type === "Actor").slice(0, 5).map(p => p.Name).join(", ") ||
    "暂无数据";
  const overview = detail.Overview || "暂无简介";
  const rating = detail.CommunityRating && detail.CommunityRating > 0
    ? detail.CommunityRating.toFixed(1)
    : "暂无数据";
  const addedTime = formatDateCreated(detail.DateCreated);
  const seasonEpisodesStr = formatSeasonEpisodes(seasons);
  const seasonEpisodesLine = seasonEpisodesStr ? `📺 入库季集: ${seasonEpisodesStr}\n` : "";

  return fillTemplate(SERIES_TEMPLATE, {
    title: detail.Name || "未知",
    year: detail.ProductionYear || "未知",
    rate: rating,
    genres,
    actors,
    addedTime,
    overview,
    seasonEpisodes: seasonEpisodesLine,
  });
}

function formatDeletedMovieNotification(itemName: string): string {
  return fillTemplate(DELETED_MOVIE_TEMPLATE, {
    title: itemName,
    time: new Date().toLocaleString(),
  });
}

function formatDeletedSeriesNotification(
  seriesName: string,
  seasons: Map<number, number[]>
): string {
  const seasonEpisodesStr = formatSeasonEpisodes(seasons);
  const seasonEpisodesLine = seasonEpisodesStr ? `删除季集: ${seasonEpisodesStr}\n` : "";
  return fillTemplate(DELETED_SERIES_TEMPLATE, {
    title: seriesName,
    time: new Date().toLocaleString(),
    seasonEpisodes: seasonEpisodesLine,
  });
}

async function formatPlaybackNotification(
  event: string,
  eventData: EmbyWebhookEvent
): Promise<string> {
  const item = eventData.Item;
  const user = eventData.User;
  const session = eventData.Session;
  const playbackInfo = eventData.PlaybackInfo;

  const titleLine =
    `${getEventTypeEmoji(event)} <b>${getEventTypeName(event)}</b> ${item.Name || "未知"}\n`;
  let body = "";
  body += `👤 用户: ${user?.Name || "未知"}\n`;
  body += `📱 设备: ${session?.DeviceName || "未知"} (${session?.Client || "未知"})\n`;
  if (item.Type === "Episode") {
    if (item.SeriesName) body += `📺 电视剧: ${item.SeriesName}\n`;
    if (item.ParentIndexNumber != null && item.IndexNumber != null) {
      body += `👟 季集: S${item.ParentIndexNumber}E${item.IndexNumber}\n`;
    }
  }

  // 播放进度
  const settings = readSettings();
  if (settings.emby?.playbackShowProgress && playbackInfo) {
    const positionTicks = playbackInfo.PositionTicks || 0;
    const totalTicks = playbackInfo.MediaSource?.RunTimeTicks || 0;
    if (positionTicks > 0 && totalTicks > 0) {
      const progress = Math.round((positionTicks / totalTicks) * 100);
      body +=
        `⏱️ 进度: ${formatTicksToTime(positionTicks)} / ${formatTicksToTime(totalTicks)} (${progress}%)\n`;
    } else if (totalTicks > 0) {
      body += `⏱️ 时长: ${formatTicksToTime(totalTicks)}\n`;
    }
  }

  // 播放结束：观看时长
  if (event === "playback.stop" && playbackInfo) {
    const positionTicks = playbackInfo.PositionTicks || 0;
    if (positionTicks > 0) {
      body += `⏱️ 观看时长: ${formatTicksToTime(positionTicks)}\n`;
    }
  }

  // 简介（需要 Emby 详情）
  if (settings.emby?.playbackShowOverview && item.Id) {
    const embyCfg = { url: settings.emby.url || "", apiKey: settings.emby.apiKey || "" };
    if (embyCfg.url && embyCfg.apiKey) {
      const detail = await getItemDetail(item.Id, embyCfg);
      if (detail?.Overview) {
        let overview = detail.Overview;
        if (overview.length > 100) overview = overview.slice(0, 100) + "...";
        body += `📝 简介: ${overview}\n`;
      }
    }
  }

  return titleLine + body.trimEnd();
}

// ========== 下载 Emby 海报到本地临时文件（移植自 qmediasync helpers.DownloadFile） ==========
async function downloadPosterToTemp(imageUrl: string, tempPath: string, userAgent: string): Promise<boolean> {
  try {
    const resp = await axios.get(imageUrl, {
      responseType: "arraybuffer",
      timeout: 15_000,
      headers: { "User-Agent": userAgent },
    });
    if (resp.status !== 200 || !resp.data) return false;
    fs.writeFileSync(tempPath, Buffer.from(resp.data));
    return true;
  } catch (err) {
    console.error("[Emby] 下载海报失败:", err);
    return false;
  }
}

// ========== 核心发送函数（完全裸发，不经过 sendTelegramNotification 二次包装） ==========

/** 检查 Telegram 是否配置完整，否则直接 return */
function getTgBotAndChat(): { bot: TelegramBot; chatId: string } | null {
  const s = readSettings();
  const tg = s.telegram;
  if (!tg?.enabled || !tg.botToken || !tg.chatId) {
    return null;
  }
  return { bot: createTelegramBot(tg.botToken), chatId: tg.chatId };
}

/** 裸发纯文本 Emby 通知（不加 Task Completed 等前缀） */
async function sendEmbyText(text: string): Promise<void> {
  const ctx = getTgBotAndChat();
  if (!ctx) return;
  try {
    await ctx.bot.sendNotification(text, ctx.chatId);
  } catch (err) {
    console.error("[Emby] 文本通知发送失败:", err);
  }
}

/** 带图片通知：先下载 Emby 海报到本地临时文件，再 multipart 上传 TG，失败降级纯文本 */
async function sendEmbyWithPoster(
  itemId: string,
  imageTags: EmbyItemDetail["ImageTags"],
  text: string,
  config: { url: string; apiKey: string; userAgent?: string }
): Promise<void> {
  const ctx = getTgBotAndChat();
  if (!ctx) return;
  if (!config.url || !config.apiKey) {
    // 拿不到 Emby URL 直接纯文字
    await sendEmbyText(text);
    return;
  }

  // 1) 构造图片 URL，优先 Backdrop（参考 qmediasync）
  let imageUrl: string | null = null;
  const bd = imageTags?.Backdrop || (imageTags as unknown as { backdrop?: string })?.backdrop;
  const primary = imageTags?.Primary || (imageTags as unknown as { Primary?: string })?.Primary;
  if (bd) {
    imageUrl = buildImageUrl(itemId, bd, "Backdrop", config);
  } else if (primary) {
    imageUrl = buildImageUrl(itemId, primary, "Primary", config);
  }

  if (!imageUrl) {
    await sendEmbyText(text);
    return;
  }

  // 2) 下载到临时文件
  const tempPath = TelegramBot.makeTempPosterPath(itemId);
  const ua = config.userAgent || "FastStrm/1.0";
  const ok = await downloadPosterToTemp(imageUrl, tempPath, ua);

  if (!ok) {
    await sendEmbyText(text);
    return;
  }

  // 3) multipart 上传
  try {
    const r = await ctx.bot.sendPhotoFromFile(ctx.chatId, tempPath, text);
    if (!r?.ok) {
      console.warn("[Emby] 图片通知失败，降级纯文本:", r?.error);
      await sendEmbyText(text);
    }
  } catch (err) {
    console.error("[Emby] sendPhotoFromFile 异常，降级纯文本:", err);
    await sendEmbyText(text);
  } finally {
    try { fs.unlinkSync(tempPath); } catch { /* 忽略 */ }
  }
}

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
    const fallback = `📚 <b>电影入库通知</b>\n${item.Name}\n⏰ ${new Date().toLocaleString()}`;
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
      : `📚 <b>剧集入库通知</b>\n${buffer.seriesName}\n📺 ${formatSeasonEpisodes(buffer.seasons)}\n⏰ ${new Date().toLocaleString()}`;

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
  const text = `🗑️ <b>Emby 媒体删除通知</b>\n${formatDeletedMovieNotification(item.Name || "未知")}`;
  await sendEmbyText(text);
}

function handleSeriesEpisodeDeleted(item: EmbyWebhookEvent["Item"]): void {
  if (!item.SeriesId) return;
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
  await sendEmbyText(`🗑️ <b>Emby 媒体删除通知</b>\n${body}`);
}

// ========== 播放通知 ==========
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
  await sendEmbyText(message);
}

// ========== 旧的 sendNotificationWithImage 彻底删除；
//            保留一个兼容性占位函数（空实现，防止外部有 import 调用；
//            这里本来就是纯内部 export-free，直接干掉即可）。

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
