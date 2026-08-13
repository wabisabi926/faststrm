import type {
  EmbyItemDetail,
  EmbyWebhookEvent,
} from "./types";
import { getItemDetail } from "./client";
import { readSettings } from "../serverUtils";

// ========== 通知模板（对齐 qmediasync 格式 + HTML parse_mode 加粗） ==========
const MOVIE_TEMPLATE = `<b>📺 Emby 电影入库通知</b>

<b>{{title}}</b> ({{year}})

🆔 评分: {{rate}}
🎬 类型: {{genres}}
👤 主演: {{actors}}
⏰ 入库时间: {{addedTime}}

📝 简介
{{overview}}`;

const SERIES_TEMPLATE = `<b>📺 Emby 电视剧入库通知</b>

<b>{{title}}</b> ({{year}})
{{seasonEpisodes}}🆔 评分: {{rate}}
🎬 类型: {{genres}}
👤 主演: {{actors}}
⏰ 入库时间: {{addedTime}}

📝 简介
{{overview}}`;

const DELETED_MOVIE_TEMPLATE = `<b>🗑️ Emby 媒体删除通知</b>

<b>电影名称：</b>{{title}}
⏰ 删除时间: {{time}}`;

const DELETED_SERIES_TEMPLATE = `<b>🗑️ Emby 媒体删除通知</b>

<b>电视剧名称：</b>{{title}}
{{seasonEpisodes}}⏰ 删除时间: {{time}}`;

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
    `${getEventTypeEmoji(event)} <b>${getEventTypeName(event)} ${item.Name || "未知"}</b>\n`;
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

export {
  MOVIE_TEMPLATE,
  SERIES_TEMPLATE,
  DELETED_MOVIE_TEMPLATE,
  DELETED_SERIES_TEMPLATE,
  formatSeasonEpisodes,
  formatTicksToTime,
  getEventTypeEmoji,
  getEventTypeName,
  fillTemplate,
  formatDateCreated,
  formatMovieNotification,
  formatSeriesNotification,
  formatDeletedMovieNotification,
  formatDeletedSeriesNotification,
  formatPlaybackNotification,
};