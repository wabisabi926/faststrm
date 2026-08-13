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

  if (opts?.enable302 && !opts.pickcode) {
    // P2-13: enable302=true 但 pickcode 缺失时，记录警告并返回空字符串
    // 调用方应检查返回值，空字符串表示该文件无法生成有效的 302 STRM
    console.warn(
      `[STRM] enable302=true 但 pickcode 缺失，跳过生成: cloudPath=${cloudPath}, account=${opts?.account || "-"}`
    );
    return "";
  }

  // 非 302 模式：旧版路径拼接（直接访问 OpenList/直链）
  const normalized = cloudPath.startsWith("/") ? cloudPath : "/" + cloudPath;
  const content = `${prefix}${normalized}`;
  if (!enablePathEncoding) return content;

  // encodeURI 不处理 # 和 ?，这两个字符在 URL 中是分隔符（fragment / query-string）
  // 文件名里只要出现它们，播放器请求就会被截断或变成查询参数
  // 这里改用 encodeURIComponent 对整段路径做完整转义
  return encodeURIComponent(content);
}
