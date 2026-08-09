import axios from "axios";
import { readSettings } from "./serverUtils";
import { AccountInfo } from "./115";

/**
 * 检查 115 Cookie 是否包含必要的字段
 * 115 API 需要 UID、CID、SEID、KID 四个字段
 */
export function validate115Cookie(cookie: string): { valid: boolean; missing: string[]; keys: string[] } {
  if (!cookie) return { valid: false, missing: ["(空)"], keys: [] };
  const pairs = cookie.split(";").map(s => s.trim()).filter(Boolean);
  const keys = pairs.map(p => p.split("=")[0].trim());
  const required = ["UID", "CID", "SEID", "KID"];
  const missing = required.filter(r => !keys.includes(r));
  return { valid: missing.length === 0, missing, keys };
}

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

// 生成 STRM 的事件类型（17=new_folder 也会触发递归遍历）
export const CREATE_EVENT_TYPES = new Set([1, 2, 14, 17, 18, 23]);

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
const LIFE_BASE = "https://life.115.com";

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
  return settings["user-agent"] || "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 115wangpan_ios/36.2.20";
}

// 检查并开启生活事件功能（参照 p115client 的 life_calendar_setoption）
// POST https://life.115.com/api/1.0/web/1.0/calendar/setoption
export async function lifeShow(
  accountInfo: AccountInfo,
  app: string = "ios"
): Promise<{ state: boolean; [key: string]: unknown }> {
  if (!accountInfo?.cookie) throw new Error("accountInfo.cookie is required");

  const ua = defaultUA();
  const headers = commonHeaders(accountInfo.cookie, ua);

  const url = `${LIFE_BASE}/api/1.0/${app}/1.0/calendar/setoption`;
  const data = new URLSearchParams({
    locus: "1",
    open_life: "1",
  }).toString();

  console.log(`[115Life] lifeShow POST ${url}`);

  const resp = await axios.post(url, data, { headers, timeout: 10000, validateStatus: () => true });

  console.log(`[115Life] lifeShow 响应:`, JSON.stringify(resp.data).slice(0, 300));

  if (resp.status >= 200 && resp.status < 300) {
    return { state: true, ...resp.data };
  }

  return { state: false, error: `HTTP ${resp.status}`, ...resp.data };
}

// 拉取生活事件列表（参照 p115client 的 life_behavior_detail）
// web:  GET https://webapi.115.com/behavior/detail
// app:  GET https://proapi.115.com/{app}/behavior/detail
// 参数: type(事件类型名,空=全部), limit, offset, date(YYYY-MM-DD)
// 响应: data.data.list 为事件数组, data.data.count 为总数
export async function lifeBehaviorDetail(
  accountInfo: AccountInfo,
  options: {
    type?: string;
    limit?: number;
    offset?: number;
    date?: string;
    app?: string;
  }
): Promise<LifeEvent[]> {
  if (!accountInfo?.cookie) throw new Error("accountInfo.cookie is required");

  const { type = "", limit = 1000, offset = 0, date = "", app = "ios" } = options;

  const ua = defaultUA();
  const headers = commonHeaders(accountInfo.cookie, ua);

  // 调试：检查 Cookie 长度和前缀
  const cookieKeys = accountInfo.cookie.split(";").map(s => s.trim().split("=")[0]).filter(Boolean);
  console.log(`[115Life] Cookie 长度: ${accountInfo.cookie.length}, keys: [${cookieKeys.join(", ")}]`);

  // 判断是 web 还是 app（ios/android）
  const isWebApp = app === "web" || app === "desktop" || app === "chrome" || app === "aps" || app === "";
  const baseUrl = isWebApp ? WEB_BASE : PRO_BASE;
  const path = isWebApp ? "/behavior/detail" : `/${app}/behavior/detail`;
  const url = `${baseUrl}${path}`;

  const params: Record<string, string | number> = {
    type,
    limit,
    offset,
  };
  if (date) params.date = date;

  console.log(`[115Life] lifeBehaviorDetail GET ${url} params:`, params);

  const resp = await axios.get(url, { headers, params, timeout: 10000, validateStatus: () => true });

  console.log(`[115Life] lifeBehaviorDetail 响应:`, JSON.stringify(resp.data).slice(0, 500));

  if (resp.status === 404) {
    throw new Error(`HTTP 404: ${url}`);
  }
  if (resp.status === 405) {
    throw new Error(`HTTP 405 Method Not Allowed: ${url}`);
  }

  const data = resp.data;
  if (!data || typeof data !== "object") {
    throw new Error("响应为空或格式错误");
  }

  // 检查业务错误
  if (data.state === false) {
    // errno=99 或 990001 表示登录失效
    if (data.errno === 99 || data.errno === 990001) {
      // 如果是 proapi 失败，尝试 webapi fallback
      if (!isWebApp) {
        console.log(`[115Life] proapi 登录失效(errno=${data.errno})，尝试 webapi fallback...`);
        return lifeBehaviorDetail(accountInfo, { ...options, app: "web" });
      }
      throw new Error(`Cookie 已过期，请重新获取 115 Cookie (errno=${data.errno}: ${data.error})`);
    }
    throw new Error(`API错误: ${data.error || "state=false"}`);
  }
  if (data.errno && data.errno !== 0) {
    throw new Error(`业务错误 errno=${data.errno}: ${data.error || ""}`);
  }

  // 事件列表在 data.data.list 中
  const events = data?.data?.list;
  if (Array.isArray(events)) {
    return events as LifeEvent[];
  }

  // 如果 data.data 是数组
  if (Array.isArray(data?.data)) {
    return data.data as LifeEvent[];
  }

  // 空列表情况
  if (data?.data?.count === 0 || (data?.data && !data.data.list)) {
    return [];
  }

  throw new Error(`未找到事件列表字段，响应: ${JSON.stringify(data).slice(0, 200)}`);
}

// 遍历拉取所有生活事件（处理分页，参照 p115client 的 iter_life_behavior_once）
export async function* iterLifeBehaviorOnce(
  accountInfo: AccountInfo,
  options: {
    from_time?: number;
    from_id?: number;
    app?: string;
    pageSize?: number;
  }
): AsyncGenerator<LifeEvent> {
  const { from_time = 0, from_id = 0, app = "ios", pageSize = 1000 } = options;

  let offset = 0;
  let hasMore = true;

  while (hasMore) {
    const events = await lifeBehaviorDetail(accountInfo, {
      type: "",
      limit: pageSize,
      offset,
      app,
    });

    if (events.length === 0) {
      break;
    }

    for (const event of events) {
      // 如果指定了 from_id，跳过 <= from_id 的事件
      if (from_id && Number(event.id) <= from_id) {
        hasMore = false;
        break;
      }
      // 如果指定了 from_time > 0，跳过 < from_time 的事件
      if (from_time > 0 && event.update_time && Number(event.update_time) < from_time) {
        hasMore = false;
        break;
      }
      yield event;
    }

    offset += events.length;

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
  app: string = "ios"
): Promise<{ events: LifeEvent[]; next_time: number; next_id: number }> {
  const events: LifeEvent[] = [];
  let next_time = from_time;
  let next_id = from_id;

  for await (const event of iterLifeBehaviorOnce(accountInfo, {
    from_time,
    from_id,
    app,
    pageSize: 1000,
  })) {
    events.push(event);
    next_time = event.update_time;
    next_id = event.id;
  }

  return { events, next_time, next_id };
}