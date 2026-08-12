// Emby REST API 客户端封装（参考 qmediasync/embyclient-rest-go）
import axios, { type AxiosError } from "axios";
import type { EmbyItemDetail, EmbyImageTag } from "./types";

const DEFAULT_TIMEOUT = 10_000;

interface EmbyClientConfig {
  url: string;
  apiKey: string;
}

function buildEmbyBaseUrl(url: string): string {
  return url.replace(/\/$/, "");
}

// ========== Emby userId 缓存（对齐 qmediasync GetUsersWithAllLibrariesAccess） ==========
// Emby 详情 API 必须带 userId 上下文：/emby/Users/{userId}/Items/{itemId}
// 直接用 /emby/Items/{itemId} 会返回 404「找不到文件」
let cachedUserId: string | null = null;
let cachedUserIdTs = 0;
const USER_ID_CACHE_TTL_MS = 10 * 60_000; // 10 分钟

interface EmbyUser {
  Id?: string;
  Name?: string;
}

async function getEmbyUserId(config: EmbyClientConfig): Promise<string | null> {
  // 命中缓存
  if (cachedUserId && Date.now() - cachedUserIdTs < USER_ID_CACHE_TTL_MS) {
    return cachedUserId;
  }

  const base = buildEmbyBaseUrl(config.url);
  const url = `${base}/emby/Users?api_key=${encodeURIComponent(config.apiKey)}`;
  try {
    const response = await axios.get<EmbyUser[]>(url, {
      timeout: DEFAULT_TIMEOUT,
      headers: { Accept: "application/json" },
    });
    const users = Array.isArray(response.data) ? response.data : [];
    if (users.length === 0 || !users[0].Id) {
      console.warn("[Emby] /emby/Users 返回空用户列表");
      return null;
    }
    cachedUserId = users[0].Id;
    cachedUserIdTs = Date.now();
    console.log(`[Emby] 获取用户成功: ${users[0].Name || "unknown"} (ID: ${cachedUserId})`);
    return cachedUserId;
  } catch (error) {
    const err = error as AxiosError;
    console.warn(`[Emby] 获取 Emby 用户列表失败:`, err.message);
    return null;
  }
}

export async function getItemDetail(
  itemId: string,
  config: EmbyClientConfig
): Promise<EmbyItemDetail | null> {
  const base = buildEmbyBaseUrl(config.url);
  const userId = await getEmbyUserId(config);
  if (!userId) {
    console.warn(`[Emby] 无法获取 userId，跳过详情查询 itemId=${itemId}`);
    return null;
  }

  // 关键：详情 API 必须使用 /emby/Users/{userId}/Items/{itemId}（对齐 qmediasync GetItemDetailByUser）
  // 直接用 /emby/Items/{itemId} 会 404「找不到文件」
  const url = `${base}/emby/Users/${encodeURIComponent(userId)}/Items/${encodeURIComponent(itemId)}`;

  const params = {
    api_key: config.apiKey,
    Fields: [
      "Overview",
      "Genres",
      "People",
      "ImageTags",
      "CommunityRating",
      "ProductionYear",
      "DateCreated",
      "SeriesName",
      "ParentIndexNumber",
      "IndexNumber",
      "Name",
      "Type",
      "SeriesId",
      "SeasonId",
    ].join(","),
  };

  try {
    const response = await axios.get(url, {
      params,
      timeout: DEFAULT_TIMEOUT,
      headers: { Accept: "application/json" },
    });

    return response.data as EmbyItemDetail;
  } catch (error) {
    const err = error as AxiosError;
    console.warn(`[Emby] 获取媒体详情失败 itemId=${itemId}:`, err.message);
    return null;
  }
}

// 直接构造图片 URL（避免再发一次请求）
export function buildImageUrl(
  itemId: string,
  tag: string | undefined,
  type: "Primary" | "Backdrop" = "Primary",
  config: EmbyClientConfig
): string | null {
  if (!tag) return null;
  const base = buildEmbyBaseUrl(config.url);
  return `${base}/emby/Items/${encodeURIComponent(itemId)}/Images/${type}?tag=${encodeURIComponent(tag)}&api_key=${encodeURIComponent(config.apiKey)}`;
}

export function getImageTags(detail: EmbyItemDetail): EmbyImageTag {
  return {
    Primary: detail.ImageTags?.Primary,
    Backdrop: detail.ImageTags?.Backdrop,
    Banner: detail.ImageTags?.Banner,
    Thumb: detail.ImageTags?.Thumb,
    Logo: detail.ImageTags?.Logo,
  };
}

export { buildEmbyBaseUrl };
