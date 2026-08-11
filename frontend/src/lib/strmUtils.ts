import * as path from "path";

export interface ResolvedStrmSettings {
  strmPrefix: string;
  enablePathEncoding: boolean;
  enable302: boolean;
}

interface StrmSettingsLike {
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  enable302?: boolean;
}

export function resolveStrmSettings(
  account?: string,
  task?: { strmPrefix?: string; enablePathEncoding?: boolean } | null,
  settings?: StrmSettingsLike
): ResolvedStrmSettings {
  const g = settings || {};

  let strmPrefix = g.strmPrefix || "";
  let enablePathEncoding = !!g.enablePathEncoding;
  // 302 策略统一由全局控制，任务级不再覆盖
  const enable302 = !!g.enable302;

  if (task) {
    // 任务级覆盖：strmPrefix 仅在非空时覆盖（空串视为"未设置"，
    // 与 enablePathEncoding 的 undefined 判断保持一致语义）
    if (task.strmPrefix !== undefined && task.strmPrefix !== "") {
      strmPrefix = task.strmPrefix;
    }
    if (task.enablePathEncoding !== undefined) {
      enablePathEncoding = task.enablePathEncoding;
    }
  }

  if (enable302) {
    const trimmed = strmPrefix.replace(/\/+$/, "");
    // 302 模式：strmPrefix 指向 /api/strm handler
    if (!trimmed.endsWith("/api/strm")) {
      strmPrefix = trimmed + "/api/strm";
    }
  } else if (account) {
    // 非 302 模式：旧版行为，追加账号名
    const trimmed = strmPrefix.replace(/\/+$/, "");
    if (!trimmed.endsWith("/" + account)) {
      strmPrefix = trimmed + "/" + account;
    }
  }

  return { strmPrefix, enablePathEncoding, enable302 };
}

export function getStrmFileName(fileName: string): string {
  const ext = path.extname(fileName);
  if (!ext) return fileName + ".strm";
  return fileName.replace(new RegExp(ext + "$", "i"), ".strm");
}

export function generateStrmContent(
  cloudPath: string,
  strmPrefix: string,
  enablePathEncoding: boolean,
  opts?: {
    enable302?: boolean;
    account?: string;
    pickcode?: string;
    fileName?: string;
  }
): string {
  const prefix = (strmPrefix || "").replace(/\/+$/, "");

  if (opts?.enable302 && opts.pickcode) {
    // 302 模式：生成带 pickcode 的 query URL
    const params = new URLSearchParams();
    if (opts.account) params.set("account", opts.account);
    params.set("pickcode", opts.pickcode);
    if (opts.fileName) params.set("file_name", opts.fileName);
    return `${prefix}?${params.toString()}`;
  }

  // 非 302 模式：旧版路径拼接
  const normalized = cloudPath.startsWith("/") ? cloudPath : "/" + cloudPath;
  const content = `${prefix}${normalized}`;
  return enablePathEncoding ? encodeURI(content) : content;
}
