import axios from "axios";
import { readSettings } from "./serverUtils";
import { AccountInfo } from "./115";

// 生活事件类型映射
export const BEHAVIOR_TYPE_TO_NAME: Record<number, string> = {
  1: "upload_image_file",
  2: "upload_file",
  3: "star_image",
  4: "star_file",
  5: "move_image_file",
  6: "move_file",
  7: "browse_image",
  8: "browse_video",
  9: "browse_audio",
  10: "browse_document",
  14: "receive_files",
  17: "new_folder",
  18: "copy_folder",
  19: "folder_label",
  20: "folder_rename",
  22: "delete_file",
  23: "copy_file",
  24: "rename_file",
};

export interface LifeEvent {
  id: number;
  type: number;
  update_time: number;
  file_id: number;
  parent_id: number;
  file_name: string;
  file_size: number;
  file_category: number; // 0=folder, 1=file
  pick_code: string;
  sha1: string;
  event_name?: string;
  path?: string;
  [key: string]: unknown;
}

// 可处理的事件类型（上传/新建/复制/接收/移动/重命名/删除）
export const HANDLED_EVENT_TYPES = new Set([1, 2, 5, 6, 14, 17, 18, 20, 22, 23, 24]);

// 生成 STRM 的事件类型
export const CREATE_EVENT_TYPES = new Set([1, 2, 14, 18, 23]);

// 移动事件类型
export const MOVE_EVENT_TYPES = new Set([5, 6]);

// 重命名事件类型
export const RENAME_EVENT_TYPES = new Set([20, 24]);

// 删除事件类型
export const DELETE_EVENT_TYPES = new Set([22]);

// 创建文件夹事件
export const NEW_FOLDER_EVENT_TYPES = new Set([17]);

const WEB_BASE = "https://webapi.115.com";
const PRO_BASE = "https://proapi.115.com";

function commonHeaders(cookie: string, userAgent?: string) {
  const ua = userAgent || defaultUA();
  return {
    "User-Agent": ua,
    Accept: "application/json, text/plain, */*",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    Referer: "https://115.com/",
    Origin: "https://115.com",
    Cookie: cookie,
  };
}

function defaultUA() {
  const settings = readSettings();
  return settings["user-agent"] || "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/116.0.5845.89 Mobile/15E148 Safari/604.1";
}

// 检查生活事件是否开启
export async function lifeShow(
  accountInfo: AccountInfo,
  app: "ios" | "web" = "web"
): Promise<{ state: boolean; errno?: number; [key: string]: unknown }> {
  if (!accountInfo?.cookie) throw new Error("accountInfo.cookie is required");

  const baseUrl = app === "ios" ? PRO_BASE : WEB_BASE;
  const url = `${baseUrl}/life/show`;
  const ua = defaultUA();

  const response = await axios.get(url, {
    headers: commonHeaders(accountInfo.cookie, ua),
    params: app === "ios" ? { app: "ios" } : undefined,
    timeout: 10000,
  });

  if (response.data?.errno || response.data?.state === false) {
    throw new Error(`115 life show error: ${JSON.stringify(response.data)}`);
  }

  return response.data;
}

// 拉取生活事件列表（单次）
export async function lifeBehaviorDetail(
  accountInfo: AccountInfo,
  options: {
    from_time: number;
    from_id: number;
    app?: "ios" | "web";
    limit?: number;
  }
): Promise<LifeEvent[]> {
  if (!accountInfo?.cookie) throw new Error("accountInfo.cookie is required");

  const { from_time, from_id, app = "web", limit = 20 } = options;

  const baseUrl = app === "ios" ? PRO_BASE : WEB_BASE;
  const url = `${baseUrl}/life/behavior`;
  const ua = defaultUA();

  const params: Record<string, string> = {
    from_time: String(from_time),
    from_id: String(from_id),
    limit: String(limit),
  };
  if (app === "ios") {
    params.app = "ios";
  }

  const response = await axios.get(url, {
    headers: commonHeaders(accountInfo.cookie, ua),
    params,
    timeout: 15000,
  });

  if (response.data?.errno) {
    throw new Error(`115 life behavior error: ${JSON.stringify(response.data)}`);
  }

  const data = response.data?.data;
  if (!data || !Array.isArray(data)) {
    return [];
  }

  return data as LifeEvent[];
}

// 遍历拉取所有生活事件（处理分页）
export async function* iterLifeBehaviorOnce(
  accountInfo: AccountInfo,
  options: {
    from_time: number;
    from_id: number;
    app?: "ios" | "web";
    pageSize?: number;
  }
): AsyncGenerator<LifeEvent> {
  const { from_time, from_id, app = "web", pageSize = 20 } = options;

  let currentFromTime = from_time;
  let currentFromId = from_id;
  let hasMore = true;

  while (hasMore) {
    const events = await lifeBehaviorDetail(accountInfo, {
      from_time: currentFromTime,
      from_id: currentFromId,
      app,
      limit: pageSize,
    });

    if (events.length === 0) {
      break;
    }

    for (const event of events) {
      yield event;
    }

    // 更新游标到最新事件
    const lastEvent = events[events.length - 1];
    currentFromTime = lastEvent.update_time;
    currentFromId = lastEvent.id;

    // 如果返回的事件数少于 pageSize，说明没有更多数据了
    if (events.length < pageSize) {
      hasMore = false;
    }
  }
}

// 单次拉取生活事件（返回事件数组和新的游标）
export async function oncePullLifeEvents(
  accountInfo: AccountInfo,
  from_time: number,
  from_id: number,
  app: "ios" | "web" = "web"
): Promise<{ events: LifeEvent[]; next_time: number; next_id: number }> {
  const events: LifeEvent[] = [];
  let next_time = from_time;
  let next_id = from_id;

  for await (const event of iterLifeBehaviorOnce(accountInfo, {
    from_time,
    from_id,
    app,
    pageSize: 20,
  })) {
    events.push(event);
    next_time = event.update_time;
    next_id = event.id;
  }

  return { events, next_time, next_id };
}