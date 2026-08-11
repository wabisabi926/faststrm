// Emby 通知逻辑（移植自 qmediasync）
import type {
  EmbyItemDetail,
  EmbyWebhookEvent,
  EpisodeBuffer,
  PlaybackCacheEntry,
} from "./types";
import { getItemDetail, buildImageUrl } from "./client";
import { sendTelegramNotification, sendTelegramPhoto } from "../telegram";
import { readSettings } from "../serverUtils";
import { handleSyncDelete } from "./syncDel";

// ========== 剧集缓冲（移植自 qmediasync addItemToEpisodeBuffer） ==========
const episodeBuffer = new Map<string, EpisodeBuffer>();
const episodeBufferTimers = new Map<string, ReturnType<typeof setTimeout>>();
const episodeBufferStarted = { value: false };
const DEBOUNCE_WINDOW_MS = 10_000;
const BUFFER_CHECK_INTERVAL_MS = 5_000;

// ========== 播放事件去重（移植自 qmediasync） ==========
const playbackCache = new Map<string, PlaybackCacheEntry>();
const PLAYBACK_DEDUP_WINDOW_MS = 60_000;
const PLAYBACK_CACHE_TTL_MS = 5 * 60_000;

// ========== 通知模板（移植自 qmediasync notificationTemplate） ==========
const MOVIE_TEMPLATE = `
{{title}} ({{year}})

🎬 类型: {{genres}}
👤 主演: {{actors}}
⏰ 入库时间: {{addedTime}}

📝 简介
{{overview}}
`;

const SERIES_TEMPLATE = `
{{title}} ({{year}})
{{seasonEpisodes}}
🎬 类型: {{genres}}
👤 主演: {{actors}}
⏰ 入库时间: {{addedTime}}

📝 简介
{{overview}}
`;

const DELETED_MOVIE_TEMPLATE = `
🗑️ <b>电影删除</b>
{{title}}
⏰ {{time}}
`;

const DELETED_SERIES_TEMPLATE = `
🗑️ <b>剧集删除</b>
{{title}}
{{seasonEpisodes}}
⏰ {{time}}
`;

const PLAYBACK_TEMPLATE = `
{{eventEmoji}} <b>{{eventName}}</b> {{name}}

👤 用户: {{userName}}
📱 设备: {{deviceName}} ({{clientName}})
{{seriesInfo}}
{{progressInfo}}
{{durationInfo}}
⏰ {{time}}
`;

// ========== 辅助函数 ==========
function formatSeasonEpisodes(seasons: Map<number, number[]>): string {
  if (seasons.size === 0) return "";

  const seasonNumbers = [...seasons.keys()].sort((a, b) => a - b);
  const parts: string[] = [];

  for (const seasonNumber of seasonNumbers) {
    const episodes = [...(seasons.get(seasonNumber) || [])].sort((a, b) => a - b);
    if (episodes.length === 0) continue;

    // 去重
    const uniqueEpisodes = [...new Set(episodes)];

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

function formatTicksToTime(ticks: number): string {
  // Emby ticks: 1 tick = 10,000 nanoseconds = 0.00001 seconds
  const ms = Math.floor(ticks / 10000);
  const totalSeconds = Math.floor(ms / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}小时${minutes}分`;
  if (minutes > 0) return `${minutes}分${seconds}秒`;
  return `${seconds}秒`;
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

function formatMovieNotification(detail: EmbyItemDetail): string {
  const genres = detail.Genres?.length ? detail.Genres.join(", ") : "暂无数据";
  const actors = detail.People?.filter(p => p.Type === "Actor").slice(0, 5).map(p => p.Name).join(", ") || "暂无数据";
  const overview = detail.Overview || "暂无简介";
  const addedTime = new Date().toLocaleString();

  return fillTemplate(MOVIE_TEMPLATE, {
    title: detail.Name || "未知",
    year: detail.ProductionYear || "未知",
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
  const actors = detail.People?.filter(p => p.Type === "Actor").slice(0, 5).map(p => p.Name).join(", ") || "暂无数据";
  const overview = detail.Overview || "暂无简介";
  const addedTime = new Date().toLocaleString();
  const seasonEpisodes = formatSeasonEpisodes(seasons);

  let template = fillTemplate(SERIES_TEMPLATE, {
    title: detail.Name || "未知",
    year: detail.ProductionYear || "未知",
    genres,
    actors,
    addedTime,
    overview,
    seasonEpisodes: seasonEpisodes ? `📺 入库季集: ${seasonEpisodes}\n` : "",
  });

  // 如果没有季集信息，移除模板中的 seasonEpisodes 行
  if (!seasonEpisodes) {
    template = template.replace("{{seasonEpisodes}}\n", "");
  }

  return template;
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
  const seasonEpisodes = formatSeasonEpisodes(seasons);
  let template = fillTemplate(DELETED_SERIES_TEMPLATE, {
    title: seriesName,
    time: new Date().toLocaleString(),
    seasonEpisodes: seasonEpisodes ? `删除季集: ${seasonEpisodes}\n` : "",
  });
  if (!seasonEpisodes) {
    template = template.replace("{{seasonEpisodes}}\n", "");
  }
  return template;
}

function formatPlaybackNotification(
  event: string,
  eventData: EmbyWebhookEvent
): string {
  const item = eventData.Item;
  const user = eventData.User;
  const session = eventData.Session;
  const playbackInfo = eventData.PlaybackInfo;

  let progressInfo = "";
  let durationInfo = "";
  let seriesInfo = "";

  if (item.Type === "Episode") {
    seriesInfo = `📺 剧集: ${item.SeriesName || "未知"}\n`;
    if (item.ParentIndexNumber && item.IndexNumber) {
      seriesInfo += `👟 季集: S${item.ParentIndexNumber}E${item.IndexNumber}\n`;
    }
  }

  // 进度（如果启用）
  const settings = readSettings();
  if (settings.emby?.playbackShowProgress && playbackInfo) {
    const positionTicks = playbackInfo.PositionTicks || 0;
    const mediaSource = playbackInfo.MediaSource;
    const totalTicks = mediaSource?.RunTimeTicks || 0;
    if (positionTicks > 0 && totalTicks > 0) {
      const progress = Math.round((positionTicks / totalTicks) * 100);
      const currentTime = formatTicksToTime(positionTicks);
      const totalTime = formatTicksToTime(totalTicks);
      progressInfo = `⏱️ 进度: ${currentTime} / ${totalTime} (${progress}%)\n`;
    }
  }

  // 播放时长（仅 stop 事件）
  if (event === "playback.stop" && playbackInfo) {
    const positionTicks = playbackInfo.PositionTicks || 0;
    if (positionTicks > 0) {
      durationInfo = `⏱️ 观看时长: ${formatTicksToTime(positionTicks)}\n`;
    }
  }

  return fillTemplate(PLAYBACK_TEMPLATE, {
    eventEmoji: getEventTypeEmoji(event),
    eventName: getEventTypeName(event),
    name: item.Name || "未知",
    userName: user?.Name || "未知",
    deviceName: session?.DeviceName || "未知",
    clientName: session?.Client || "",
    seriesInfo,
    progressInfo,
    durationInfo,
    time: new Date().toLocaleString(),
  });
}

// ========== 获取图片 URL ==========
function getPosterUrl(
  itemId: string,
  imageTags: EmbyItemDetail["ImageTags"],
  config: { url: string; apiKey: string }
): string | null {
  const primaryTag = imageTags?.Primary;
  const backdropTag = imageTags?.Backdrop;
  return (
    buildImageUrl(itemId, primaryTag, "Primary", config) ||
    buildImageUrl(itemId, backdropTag, "Backdrop", config)
  );
}

// ========== 核心通知函数 ==========

// 发送带图片或不带图片的通知
async function sendNotificationWithImage(
  message: string,
  imageUrl: string | null,
  chatId: string | undefined
): Promise<void> {
  if (!chatId) return;
  try {
    if (imageUrl) {
      await sendTelegramPhoto(chatId, imageUrl, message);
    } else {
      await sendTelegramNotification(message, "complete");
    }
  } catch (err) {
    console.error("[Emby] 发送通知失败:", err);
    // 降级：不带图片重试
    if (imageUrl) {
      try {
        await sendTelegramNotification(message, "complete");
      } catch {
        // 忽略
      }
    }
  }
}

// ========== 入库通知 ==========

// 电影入库
async function handleMovieAdded(item: EmbyWebhookEvent["Item"]): Promise<void> {
  const settings = readSettings();
  if (!settings.emby || !settings.emby.notifyMediaAdded) return;
  if (!item.Id) return;

  const config = { url: settings.emby.url || "", apiKey: settings.emby.apiKey || "" };
  const detail = await getItemDetail(item.Id, config);

  const message = detail
    ? formatMovieNotification(detail)
    : `📺 <b>电影入库</b>\n${item.Name}\n⏰ ${new Date().toLocaleString()}`;

  const imageUrl = detail ? getPosterUrl(item.Id, detail.ImageTags, config) : null;
  await sendNotificationWithImage(message, imageUrl, settings.telegram?.chatId);
}

// 剧集入库（带缓冲合并）
function handleSeriesEpisodeAdded(item: EmbyWebhookEvent["Item"]): void {
  if (!item.SeriesId) return;

  const seriesId = item.SeriesId;
  const buffer = episodeBuffer.get(seriesId) || {
    seriesId,
    seriesName: item.SeriesName || item.Name || "未知",
    seasons: new Map(),
    lastUpdated: Date.now(),
  };

  const seasonNumber = item.ParentIndexNumber || 0;
  const episodeNumber = item.IndexNumber || 0;

  if (!buffer.seasons.has(seasonNumber)) {
    buffer.seasons.set(seasonNumber, []);
  }
  const episodes = buffer.seasons.get(seasonNumber)!;
  if (!episodes.includes(episodeNumber)) {
    episodes.push(episodeNumber);
  }
  buffer.lastUpdated = Date.now();
  episodeBuffer.set(seriesId, buffer);

  // 重置定时器
  const existingTimer = episodeBufferTimers.get(seriesId);
  if (existingTimer) clearTimeout(existingTimer);
  const timer = setTimeout(() => flushEpisodeBuffer(seriesId), DEBOUNCE_WINDOW_MS);
  episodeBufferTimers.set(seriesId, timer);

  // 启动缓冲检查器（只启动一次）
  if (!episodeBufferStarted.value) {
    episodeBufferStarted.value = true;
    startBufferChecker();
  }
}

// 剧集入库缓冲检查器（定时检查是否有缓冲已超过窗口时间）
function startBufferChecker(): void {
  const interval = setInterval(() => {
    const now = Date.now();
    for (const [seriesId, buffer] of episodeBuffer.entries()) {
      if (now - buffer.lastUpdated >= DEBOUNCE_WINDOW_MS) {
        flushEpisodeBuffer(seriesId);
      }
    }
    if (episodeBuffer.size === 0) {
      clearInterval(interval);
      episodeBufferStarted.value = false;
    }
  }, BUFFER_CHECK_INTERVAL_MS);
}

// 刷新剧集缓冲（发送通知）
async function flushEpisodeBuffer(seriesId: string): Promise<void> {
  const timer = episodeBufferTimers.get(seriesId);
  if (timer) {
    clearTimeout(timer);
    episodeBufferTimers.delete(seriesId);
  }

  const buffer = episodeBuffer.get(seriesId);
  if (!buffer) return;
  episodeBuffer.delete(seriesId);

  // 检查时间戳是否足够老（防止定时器与用户手动触发冲突）
  if (Date.now() - buffer.lastUpdated < DEBOUNCE_WINDOW_MS - 500) {
    return;
  }

  const settings = readSettings();
  if (!settings.emby?.notifyMediaAdded) return;
  if (!settings.emby.url || !settings.emby.apiKey) return;

  const config = { url: settings.emby.url, apiKey: settings.emby.apiKey };

  try {
    const detail = await getItemDetail(seriesId, config);
    const message = detail
      ? formatSeriesNotification(detail, buffer.seasons)
      : `📺 <b>剧集入库</b>\n${buffer.seriesName}\n${formatSeasonEpisodes(buffer.seasons)}\n⏰ ${new Date().toLocaleString()}`;

    const imageUrl = detail ? getPosterUrl(seriesId, detail.ImageTags, config) : null;
    await sendNotificationWithImage(message, imageUrl, settings.telegram?.chatId);
  } catch (err) {
    console.error(`[Emby] 发送剧集入库通知失败 seriesId=${seriesId}:`, err);
  }
}

// ========== 删除通知 ==========

// 电影删除
async function handleMovieDeleted(item: EmbyWebhookEvent["Item"]): Promise<void> {
  const settings = readSettings();
  if (!settings.emby?.notifyMediaRemoved) return;
  const message = formatDeletedMovieNotification(item.Name);
  await sendTelegramNotification(message, "complete");
}

// 剧集删除（带缓冲合并）
function handleSeriesEpisodeDeleted(item: EmbyWebhookEvent["Item"]): void {
  if (!item.SeriesId) return;

  const seriesId = item.SeriesId;
  const buffer = episodeBuffer.get(seriesId) || {
    seriesId,
    seriesName: item.SeriesName || item.Name || "未知",
    seasons: new Map(),
    lastUpdated: Date.now(),
  };

  const seasonNumber = item.ParentIndexNumber || 0;
  const episodeNumber = item.IndexNumber || 0;

  if (!buffer.seasons.has(seasonNumber)) {
    buffer.seasons.set(seasonNumber, []);
  }
  const episodes = buffer.seasons.get(seasonNumber)!;
  if (!episodes.includes(episodeNumber)) {
    episodes.push(episodeNumber);
  }
  buffer.lastUpdated = Date.now();
  episodeBuffer.set(seriesId, buffer);

  const existingTimer = episodeBufferTimers.get(seriesId);
  if (existingTimer) clearTimeout(existingTimer);
  const timer = setTimeout(() => flushDeletedEpisodeBuffer(seriesId), DEBOUNCE_WINDOW_MS);
  episodeBufferTimers.set(seriesId, timer);

  if (!episodeBufferStarted.value) {
    episodeBufferStarted.value = true;
    startBufferChecker();
  }
}

async function flushDeletedEpisodeBuffer(seriesId: string): Promise<void> {
  const timer = episodeBufferTimers.get(seriesId);
  if (timer) {
    clearTimeout(timer);
    episodeBufferTimers.delete(seriesId);
  }

  const buffer = episodeBuffer.get(seriesId);
  if (!buffer) return;
  episodeBuffer.delete(seriesId);

  if (Date.now() - buffer.lastUpdated < DEBOUNCE_WINDOW_MS - 500) {
    return;
  }

  const settings = readSettings();
  if (!settings.emby?.notifyMediaRemoved) return;

  const message = formatDeletedSeriesNotification(buffer.seriesName, buffer.seasons);
  await sendTelegramNotification(message, "complete");
}

// ========== 播放通知 ==========
async function handlePlaybackEvent(event: EmbyWebhookEvent): Promise<void> {
  const settings = readSettings();
  if (!settings.emby?.notifyPlayback) return;

  // 暂停事件通常不通知
  if (event.Event === "playback.pause") return;

  // 去重（1分钟内不重复）
  const cacheKey = `${event.User?.Id || "unknown"}_${event.Item?.Type || "unknown"}_${event.Item?.Name || "unknown"}_${event.Session?.DeviceName || "unknown"}_${event.Event}`;
  const now = Date.now();

  const cached = playbackCache.get(cacheKey);
  if (cached && now - cached.timestamp < PLAYBACK_DEDUP_WINDOW_MS) {
    console.log(`[Emby] 播放事件去重: ${cacheKey}`);
    return;
  }

  playbackCache.set(cacheKey, { timestamp: now });

  // 清理过期缓存
  for (const [key, entry] of playbackCache.entries()) {
    if (now - entry.timestamp > PLAYBACK_CACHE_TTL_MS) {
      playbackCache.delete(key);
    }
  }

  const message = formatPlaybackNotification(event.Event, event);
  await sendTelegramNotification(message, "complete");
}

// ========== 主事件分发 ==========
export async function handleEmbyWebhookEvent(event: EmbyWebhookEvent): Promise<void> {
  if (!event || !event.Event) return;

  console.log(`[Emby] 收到事件: ${event.Event}, 项目: ${event.Item?.Name || "unknown"}`);

  switch (event.Event) {
    case "library.new":
      if (event.Item?.Type === "Movie") {
        await handleMovieAdded(event.Item);
      } else if (event.Item?.Type === "Episode") {
        handleSeriesEpisodeAdded(event.Item);
      }
      break;

    case "library.deleted":
      // 删除同步：删 STRM + 关联文件 + DB 记录（独立于通知逻辑）
      if (event.Item?.Path) {
        handleSyncDelete(event.Item).catch(err => {
          console.error("[SyncDel] 处理失败:", err);
        });
      }
      // 原有通知逻辑：若 syncDeleteNotify 已开启则跳过重复通知
      {
        const s = readSettings();
        const skipOriginalNotify = s.emby?.syncDeleteEnabled && s.emby?.syncDeleteNotify;
        if (!skipOriginalNotify) {
          if (event.Item?.Type === "Movie") {
            await handleMovieDeleted(event.Item);
          } else if (event.Item?.Type === "Episode" || event.Item?.Type === "Series" || event.Item?.Type === "Season") {
            handleSeriesEpisodeDeleted(event.Item);
          }
        }
      }
      break;

    case "playback.start":
    case "playback.pause":
    case "playback.stop":
      await handlePlaybackEvent(event);
      break;

    default:
      console.log(`[Emby] 未处理的事件类型: ${event.Event}`);
  }
}
