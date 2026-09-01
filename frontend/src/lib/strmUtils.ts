// STRM 相关工具函数
// 方案 A（v1.2.2+）：统一使用 /api/strm 端点，enable302 已废弃

export interface ResolvedStrmSettings {
  strmPrefix: string;
  enablePathEncoding: boolean;
}

interface StrmSettingsLike {
  strmPrefix?: string;
  enablePathEncoding?: boolean;
}

/**
 * 解析 STRM 设置（全局 + 任务级覆盖）。
 * v1.2.2 起 enable302 已删除，始终统一走 /api/strm 端点。
 */
export function resolveStrmSettings(
  account?: string,
  task?: { strmPrefix?: string; enablePathEncoding?: boolean } | null,
  settings?: StrmSettingsLike
): ResolvedStrmSettings {
  const g = settings || {};

  let strmPrefix = g.strmPrefix || "";
  let enablePathEncoding = !!g.enablePathEncoding;

  if (task) {
    if (task.strmPrefix !== undefined && task.strmPrefix !== "") {
      strmPrefix = task.strmPrefix;
    }
    if (task.enablePathEncoding !== undefined) {
      enablePathEncoding = task.enablePathEncoding;
    }
  }

  // 统一硬编码 /api/strm（后端 enable302 已废弃，所有 STRM 都走智能路由）
  const trimmed = strmPrefix.replace(/\/+$/, "");
  if (!trimmed.endsWith("/api/strm")) {
    strmPrefix = trimmed + "/api/strm";
  }

  return { strmPrefix, enablePathEncoding };
}

export function getStrmFileName(fileName: string): string {
  const lastDot = fileName.lastIndexOf(".");
  if (lastDot === -1) return fileName + ".strm";
  return fileName.substring(0, lastDot) + ".strm";
}

/**
 * 生成 STRM 文件内容。
 * v1.2.2 起统一生成 /api/strm query URL（后端智能路由决定 proxy/redirect）。
 */
export function generateStrmContent(
  cloudPath: string,
  strmPrefix: string,
  enablePathEncoding: boolean,
  opts?: {
    account?: string;
    pickcode?: string;
    fileName?: string;
  }
): string {
  const prefix = (strmPrefix || "").replace(/\/+$/, "");

  // 统一 query URL 模式
  if (opts?.pickcode) {
    const params = new URLSearchParams();
    if (opts.account) params.set("account", opts.account);
    params.set("pickcode", opts.pickcode);
    if (opts.fileName) params.set("file_name", opts.fileName);
    return `${prefix}?${params.toString()}`;
  }

  // 兜底：pickcode 缺失时返回空（无法生成有效 STRM）
  console.warn(
    `[STRM] pickcode 缺失，跳过生成: cloudPath=${cloudPath}, account=${opts?.account || "-"}`
  );
  return "";
}
