/**
 * 后端 API 响应类型定义
 * 集中管理，避免各处使用 any
 */

// ================ Task / 任务相关 ================

export interface TaskRuntimeApi {
  status?: string;
  startedAt?: number;
  endedAt?: number;
  error?: string;
  totalFiles?: number;
  downloadedFiles?: number;
  deletedFiles?: number;
  stage?: string;
  stageDetail?: string;
  [key: string]: unknown;
}

export interface TaskScheduleApi {
  enabled?: boolean;
  mode?: "interval" | "cron" | string;
  intervalMinutes?: number;
  time?: string;
  weekdays?: number[];
  [key: string]: unknown;
}

/**
 * GET /api/tasks 返回的任务对象（与后端 model.Tasks / runtime 对齐）
 * 所有字段均可选，避免后端加减字段时前端编译报错
 */
export interface TaskApiResponse {
  id?: string | number;
  name?: string;
  account?: string;
  accountType?: string;
  originPath?: string;
  targetPath?: string;
  strmType?: string;
  strmPrefix?: string;
  removeExtraFiles?: boolean;
  enable302?: boolean;
  status?: string;
  runtime?: TaskRuntimeApi;
  schedule?: TaskScheduleApi;
  scheduleNext?: { nextRunAt?: number; [key: string]: unknown };
  error?: string | null;
  [key: string]: unknown;
}

// ================ Directory / 目录树相关 ================

/**
 * GET /api/directory/remote/list & POST /api/directory/local/list 返回的节点
 * id: 本地一般是 string（路径），远程一般是 number（cid）
 * isDir: 本地一定有，远程有时通过 fid/cid 是否为 0 判断
 */
export interface DirectoryNodeApi {
  id: number | string;
  name: string;
  isDir?: boolean;
  cid?: number | string;
  pid?: string;
  pickcode?: string;
  fileSize?: number;
  fid?: number | string;
  sha1?: string;
  [key: string]: unknown;
}

// ================ 通用 API 包装响应 ================

/**
 * 后端目录接口通用返回格式：{ code: 200, data: [...] }
 */
export interface ApiListResponse<T> {
  code: number;
  data?: T[];
  message?: string;
}

export interface ApiObjectResponse<T> {
  code: number;
  data?: T;
  message?: string;
  success?: boolean;
  [key: string]: unknown;
}
