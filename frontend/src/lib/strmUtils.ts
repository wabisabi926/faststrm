import * as path from "path";

/**
 * 解析后的 STRM 设置
 */
export interface ResolvedStrmSettings {
  strmPrefix: string;
  enablePathEncoding: boolean;
}

/** 全局 STRM 设置的最小接口（避免客户端拉入 serverUtils 的 fs 依赖） */
interface StrmSettingsLike {
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  enable302?: boolean;
}

/**
 * 获取统一的 STRM 生成设置。
 *
 * 优先级：任务级覆盖 > 全局默认
 * - 全局 strmPrefix 不含账号名，enable302 在这里统一拼接
 * - 任务级 strmPrefix 如果已由旧版 302 逻辑写入账号后缀，仍可正常使用
 *
 * @param account 账号名（302 拼接用）
 * @param task 可选的任务级配置（覆盖全局默认）
 * @param settings 可选的已读取 settings（避免重复读磁盘；客户端不传时用空默认）
 */
export function resolveStrmSettings(
  account?: string,
  task?: { strmPrefix?: string; enablePathEncoding?: boolean; enable302?: boolean } | null,
  settings?: StrmSettingsLike
): ResolvedStrmSettings {
  const g = settings || {};

  let strmPrefix = g.strmPrefix || "";
  let enablePathEncoding = !!g.enablePathEncoding;
  let enable302 = !!g.enable302;

  // 任务级覆盖
  if (task) {
    if (task.strmPrefix !== undefined && task.strmPrefix !== "") {
      strmPrefix = task.strmPrefix;
    }
    if (task.enablePathEncoding !== undefined) {
      enablePathEncoding = task.enablePathEncoding;
    }
    if (task.enable302 !== undefined) {
      enable302 = task.enable302;
    }
  }

  // 302 拼接：如果 strmPrefix 末尾已有 /account 则不再重复拼接
  if (enable302 && account) {
    const trimmed = strmPrefix.replace(/\/+$/, "");
    if (!trimmed.endsWith("/" + account)) {
      strmPrefix = trimmed + "/" + account;
    }
  }

  return { strmPrefix, enablePathEncoding };
}

/**
 * 根据原始文件名生成 STRM 文件名
 *
 * 使用正则锚定结尾，避免文件名中间包含扩展名字符串时的错误替换。
 * 例：movie.mkv.2024.mkv → movie.mkv.2024.strm
 *
 * @param fileName 原始文件名
 * @returns STRM 文件名
 */
export function getStrmFileName(fileName: string): string {
  const ext = path.extname(fileName);
  if (!ext) return fileName + ".strm";
  return fileName.replace(new RegExp(ext + "$", "i"), ".strm");
}

/**
 * 生成 STRM 文件内容
 *
 * 拼接规则：strmPrefix + normalizedCloudPath
 * - strmPrefix 尾部斜杠会被去除，避免双斜杠
 * - cloudPath 确保以 / 开头
 * - enablePathEncoding 时对完整路径做 encodeURI
 *
 * @param cloudPath 网盘完整路径（如 /电影/叶问.mkv）
 * @param strmPrefix 前缀（如 http://192.168.1.1:5244，可为空）
 * @param enablePathEncoding 是否对路径做 URL 编码
 * @returns STRM 文件内容
 */
export function generateStrmContent(
  cloudPath: string,
  strmPrefix: string,
  enablePathEncoding: boolean
): string {
  const prefix = (strmPrefix || "").replace(/\/+$/, "");
  const normalized = cloudPath.startsWith("/") ? cloudPath : "/" + cloudPath;
  const content = `${prefix}${normalized}`;
  return enablePathEncoding ? encodeURI(content) : content;
}
