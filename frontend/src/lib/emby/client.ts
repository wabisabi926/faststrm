// Emby REST API 客户端封装（参考 qmediasync/embyclient-rest-go）
import axios from "axios";
import type { EmbyItemDetail, EmbyImageTag } from "./types";

const DEFAULT_TIMEOUT = 10_000;

interface EmbyClientConfig {
  url: string;
  apiKey: string;
}

function buildEmbyBaseUrl(url: string): string {
  return url.replace(/\/$/, "");
}

export async function getItemDetail(
  itemId: string,
  config: EmbyClientConfig
): Promise<EmbyItemDetail | null> {
  const base = buildEmbyBaseUrl(config.url);
  const url = `${base}/emby/Items/${encodeURIComponent(itemId)}`;

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
