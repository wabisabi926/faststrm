import * as fs from "fs";
import * as path from "path";
import axios from "axios";
import { decryptAccounts, decryptSettings, encryptSettings } from "./passwordCrypto";

const accountFile = path.join(process.cwd(), "../config", "account.json");

export function readAccounts() {
  if (!fs.existsSync(accountFile)) return [];
  const accounts = JSON.parse(fs.readFileSync(accountFile, "utf-8"));
  return decryptAccounts(accounts);
}

type Node = {
  key: number;
  name: string;
  parent_key: number;
  depth: number;
  children?: Node[];
};

export function getLocalTree(
  dirPath: string,
  parentKey = 0,
  depth = 0,
  keySeed = { value: 1 }
): Node[] {
  if (!fs.existsSync(dirPath)) return [];
  const nodes: Node[] = [];
  const items = fs.readdirSync(dirPath);

  for (const name of items) {
    const fullPath = path.join(dirPath, name);
    const stat = fs.statSync(fullPath);
    const node: Node = {
      key: keySeed.value++,
      name,
      parent_key: parentKey,
      depth,
      children: [],
    };
    if (stat.isDirectory()) {
      node.children = getLocalTree(fullPath, node.key, depth + 1, keySeed);
    }
    nodes.push(node);
  }
  return nodes;
}

const tasksFile = path.join(process.cwd(), "../config/tasks.json");
const settingsFile = path.join(process.cwd(), "../config/settings.json");

// 工具函数：读取任务
export function readTasks() {
  if (!fs.existsSync(tasksFile)) return [];
  const data = fs.readFileSync(tasksFile, "utf-8");
  return JSON.parse(data);
}

// 工具函数：保存任务
export function saveTasks(tasks: unknown[]) {
  fs.writeFileSync(tasksFile, JSON.stringify(tasks, null, 2), "utf-8");
}

// 删除多余文件，并清理空父目录
export function removeExtraFiles(extraLocally: string[], saveDir: string) {
  const removeEmptyParents = (dir: string) => {
    if (!dir.startsWith(saveDir)) return; // 防止越界误删
    if (dir === saveDir) return; // 根目录不删
    try {
      const files = fs.readdirSync(dir);
      if (files.length === 0) {
        fs.rmdirSync(dir);
        console.log("删除空目录:", dir);
        removeEmptyParents(path.dirname(dir)); // 递归往上删
      }
    } catch (err) {
      console.error("清理空目录失败:", dir, err);
    }
  };

  extraLocally.forEach((relPath) => {
    const filePath = path.join(saveDir, relPath);
    try {
      if (fs.existsSync(filePath)) {
        const stat = fs.statSync(filePath);
        if (stat.isFile()) {
          fs.unlinkSync(filePath);
          console.log("删除文件:", filePath);
        } else if (stat.isDirectory()) {
          fs.rmSync(filePath, { recursive: true, force: true });
          console.log("删除文件夹:", filePath);
        }
        // 删除完成后检查父目录是否为空
        removeEmptyParents(path.dirname(filePath));
      }
    } catch (err) {
      console.error("删除失败:", filePath, err);
    }
  });
}


// 构建树
export function buildTree(list: Node[]): Node[] {
  const map = new Map<number, Node>();
  const roots: Node[] = [];

  list.forEach((node) => map.set(node.key, { ...node, children: [] }));
  list.forEach((node) => {
    if (node.parent_key === 0) roots.push(map.get(node.key)!);
    else map.get(node.parent_key)?.children!.push(map.get(node.key)!);
  });

  return roots;
}

export function collectFilesAndTopEmptyDirs(
  nodes: Node[],
  parentPath = ""
): string[] {
  const result: string[] = [];

  function dfs(nodeList: Node[], basePath: string): boolean {
    let hasFileInTree = false;

    for (const node of nodeList) {
      const currentPath = basePath ? `${basePath}/${node.name}` : node.name;

      if (
        (!node.children || node.children.length === 0) &&
        /\.[a-z0-9]+$/i.test(node.name)
      ) {
        // 文件
        result.push(currentPath);
        hasFileInTree = true;
      } else if (node.children) {
        if (node.children.length > 0) {
          const subHasFile = dfs(node.children, currentPath);

          if (subHasFile) {
            hasFileInTree = true;
          }
        } else {
          // 真正空目录
          // 先标记它为空目录，但不立即加入结果
        }
      }
    }

    // 遍历完当前层
    if (!hasFileInTree && basePath) {
      // 整个子树没有文件 → 收集最上层目录
      result.push(basePath);
      return true; // 返回 true，防止父目录再收集它
    }

    return hasFileInTree;
  }

  dfs(nodes, parentPath);
  return result;
}

export function normalizeToStrm(path: string): string {
  return path.replace(/\.(mp4|mp3|mkv)$/i, ".strm");
}

// 监控路径映射
export interface PathMapping {
  account?: string;
  cloudPath: string;
  localPath: string;
}

// 生活事件监控配置
export type FirstPullMode = "latest" | "all" | "last";
export type MoveMediaMode = "recreate" | "local_move";

export interface LifeMonitorSettings {
  enabled: boolean;
  accounts: string[];
  pollInterval: number;
  pathMappings: PathMapping[];
  removeEmptyDirs: boolean;
  eventTypes: {
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  };
  /** STRM 前缀（运行时由 resolveStrmSettings 填充，通常为全局设置值） */
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  /** STRM 前缀是否在末尾拼账号名（与全局 enable302 语义一致；留空时 fallback 到全局 AppSettings.enable302） */
  enable302?: boolean;
  /** 最小文件大小（字节），小于此值的文件跳过 STRM 生成。0 表示不过滤 */
  minFileSize?: number;
  /** 首次拉取模式：latest=从当前时间 / all=拉取全部历史 / last=从上次断点继续 */
  firstPullMode?: FirstPullMode;
  /** 移动事件处理模式：recreate=删除旧 STRM 并重新生成 / local_move=本地直接移动 STRM 文件 */
  moveMediaMode?: MoveMediaMode;
}

// Settings helpers
export type AppSettings = {
  "user-agent"?: string;
  internalToken?: string;  // 内部 API 验证 token，首次启动时自动生成
  strmExtensions?: string[];  // strm文件扩展名配置
  downloadExtensions?: string[];  // 需要下载的文件扩展名配置
  /**
   * 媒体挂载路径列表（STRM 生成后 nginx 转发到本地的 URL 前缀集合）。
   * 不建议手工修改，应由 SSOT 函数 syncMediaMountPaths() 基于以下数据源自动同步：
   *   1) 全局 enable302 × 所有账号
   *   2) 每个任务的自定义 strmPrefix / enable302
   *   3) 生活事件监控 × 其账号集
   * 每项格式：https?://host[:port][/account]，末尾不含斜杠。
   */
  mediaMountPath?: string[];
  // 全局 STRM 生成设置
  strmPrefix?: string;  // STRM 前缀（如 http://localhost:3000），不含账号名
  enablePathEncoding?: boolean;  // 是否启用 URL 路径编码
  enable302?: boolean;  // 是否在 strmPrefix 后自动拼接账号名（用于 Emby 302 重定向）
  removeExtraFiles?: boolean;  // 是否自动删除远程已不存在的本地 STRM 文件
  // STRM 路由策略（route.ts 配置化常量）
  strm?: {
    /** 强制走代理的 UA 关键字（seek/302 兼容性差的客户端），默认 ["Infuse","VidHub","SenPlayer","SenPlayerHD"] */
    forceProxyUaTokens?: string[];
    /** 单账号并发代理上限，默认 8（115 限流，超阈值自动降级 redirect） */
    accountProxyConcurrencyLimit?: number;
    /** HEAD 可达性预检超时(ms)，默认 5000 */
    redirectCheckTimeoutMs?: number;
  };
  download?: {
    linkMaxPerSecond?: number;
    linkMaxConcurrent?: number;
    downloadMaxConcurrent?: number;
  };
  emby?: {
    url?: string;
    apiKey?: string;
    notifyMediaAdded?: boolean;
    notifyMediaRemoved?: boolean;
    notifyPlayback?: boolean;
    playbackShowProgress?: boolean;
    playbackShowOverview?: boolean;
    webhookAuth?: string;
    libraryId?: string;
    /** 删除同步：监听 Emby library.deleted 事件，自动删除本地 STRM + 关联文件 */
    syncDeleteEnabled?: boolean;
    /** 删除同步路径映射：Emby 路径（=STRM 本地保存路径）→ 115 网盘路径 */
    syncDeletePathMappings?: Array<{ embyPath: string; cloudPath: string; account?: string }>;
    /** 删除同步时发送 TG 通知 */
    syncDeleteNotify?: boolean;
    /** 试运行模式：只记日志不实际删除（首次配置验证用） */
    syncDeleteDryRun?: boolean;
  };
  telegram?: {
    botToken?: string;
    chatId?: string;
    webhookUrl?: string;
    enabled?: boolean;
    allowedUsers?: number[];
    /** 账户状态通知配置 */
    accountAlerts?: {
      /** 是否开启账户状态通知 */
      enabled?: boolean;
      /** 账号异常时通知（Cookie 过期等） */
      onError?: boolean;
      /** 账号恢复正常时通知 */
      onRecover?: boolean;
      /** Cookie 即将过期预警天数 */
      expiryWarningDays?: number;
    };
  };
  lifeMonitor?: LifeMonitorSettings;
} & Record<string, unknown>;

/**
 * 类型清洗：防御 settings.json 被手工改成非 string[] / 混入字符串等情况。
 * 同时对 mediaMountPath 每项去空白 + 去尾斜杠，过滤掉非 http(s) 前缀。
 */
function sanitizeAppSettings(raw: unknown): AppSettings {
  if (!raw || typeof raw !== "object") return {} as AppSettings;
  const s = { ...(raw as Record<string, unknown>) };

  const normalize = (p: string) => (p || "").trim().replace(/\/+$/, "");
  const isValidHttp = (p: string) => /^https?:\/\/[^\s/$.?#].[^\s]*$/i.test(p);

  if (!Array.isArray(s.mediaMountPath)) {
    if (typeof s.mediaMountPath === "string" && s.mediaMountPath.length > 0) {
      // 兼容：用户手工写成单字符串 → 按逗号/分号/空格拆分
      s.mediaMountPath = String(s.mediaMountPath)
        .split(/[,;\s]+/)
        .map(normalize)
        .filter(isValidHttp);
    } else {
      s.mediaMountPath = [];
    }
  } else {
    s.mediaMountPath = (s.mediaMountPath as unknown[])
      .map((x) => (typeof x === "string" ? normalize(x) : ""))
      .filter(isValidHttp);
  }

  // 简单数组字段兜底（防御手工改成字符串）
  for (const key of ["strmExtensions", "downloadExtensions"] as const) {
    if (!Array.isArray(s[key])) {
      if (typeof s[key] === "string" && s[key]!.length > 0) {
        s[key] = String(s[key])
          .split(/[,;\s]+/)
          .filter(Boolean);
      } else {
        s[key] = undefined;
      }
    }
  }

  // strm 子配置类型清洗
  if (s.strm && typeof s.strm === "object") {
    const st = s.strm as Record<string, unknown>;
    if (st.forceProxyUaTokens !== undefined) {
      if (!Array.isArray(st.forceProxyUaTokens)) {
        st.forceProxyUaTokens = [];
      } else {
        st.forceProxyUaTokens = (st.forceProxyUaTokens as unknown[])
          .map((x) => (typeof x === "string" ? x : String(x)))
          .filter(Boolean);
      }
    }
    if (st.accountProxyConcurrencyLimit !== undefined) {
      const n = Number(st.accountProxyConcurrencyLimit);
      st.accountProxyConcurrencyLimit = Number.isFinite(n) && n > 0 ? Math.floor(n) : undefined;
    }
    if (st.redirectCheckTimeoutMs !== undefined) {
      const n = Number(st.redirectCheckTimeoutMs);
      st.redirectCheckTimeoutMs = Number.isFinite(n) && n > 0 ? Math.floor(n) : undefined;
    }
  }

  return s as AppSettings;
}

export function readSettings(): AppSettings {
  if (!fs.existsSync(settingsFile)) return {} as AppSettings;
  const raw = fs.readFileSync(settingsFile, "utf-8");
  try {
    const parsed = JSON.parse(raw || "{}");
    const decrypted = decryptSettings(parsed);
    return sanitizeAppSettings(decrypted);
  } catch {
    return {} as AppSettings;
  }
}

export function writeSettings(next: AppSettings) {
  // 写入前加密敏感字段（深拷贝避免修改入参）+ 类型清洗
  const sanitized = sanitizeAppSettings(next ?? {});
  const encrypted = encryptSettings(JSON.parse(JSON.stringify(sanitized)));
  const pretty = JSON.stringify(encrypted, null, 2);
  fs.writeFileSync(settingsFile, pretty, "utf-8");
}

// ==================== STRM Settings Resolution ====================
// resolveStrmSettings / ResolvedStrmSettings 已移至 strmUtils.ts（避免客户端拉入 fs 依赖）
export { resolveStrmSettings, type ResolvedStrmSettings } from "./strmUtils";

/**
 * 获取 STRM 扩展名列表（统一入口）
 */
export function getStrmExtensions(): string[] {
  try {
    const settings = readSettings();
    return (settings.strmExtensions || []).map((e: string) =>
      e.startsWith(".") ? e.toLowerCase() : "." + e.toLowerCase()
    );
  } catch {
    return [".mkv", ".mp4", ".avi", ".mov", ".rmvb", ".flv", ".webm"];
  }
}

// 通知 Emby 刷新媒体库（如果在 settings.json 配置了 emby）
export async function notifyEmbyRefresh(filePath?: string, account?: string) {
  try {
    const settings = readSettings();
    const emby = settings.emby;
    if (!emby || !emby.url || !emby.apiKey) return;

    const base = String(emby.url).replace(/\/$/, "");
    const url = `${base}/Library/Refresh?api_key=${encodeURIComponent(emby.apiKey)}`;

    let body: string | undefined = undefined;
    if (filePath) {
      // 路径级精准刷新：只刷新指定的文件或目录
      body = JSON.stringify({
        Path: filePath,
        Recursive: false,
      });
    }
    // fire-and-forget
    const res = await axios.post(url, body ? {
      data: body,
      headers: { 'Content-Type': 'application/json' }
    } : undefined);
    const acctCtx = account ? `account=${account} ` : "";
    console.log(`[Emby] ${acctCtx}刷新结果 ${res.status} ${filePath ? `(路径: ${filePath})` : "(全库)"}`);
  } catch(err){
    const acctCtx = account ? `account=${account} ` : "";
    console.log(`[Emby] ${acctCtx}刷新失败:`, err);
    // 忽略通知失败
  }
}



// Telegram 简单权限管理
export function isTelegramUserAllowed(userId: number): boolean {
  const settings = readSettings();
  const telegram = settings.telegram;
  
  if (!telegram) {
    return false;
  }

  // 检查是否在允许用户列表中
  return telegram.allowedUsers?.includes(userId) || false;
}

export function addTelegramUser(userId: number): boolean {
  try {
    const settings = readSettings();
    if (!settings.telegram) {
      settings.telegram = {};
    }
    if (!settings.telegram.allowedUsers) {
      settings.telegram.allowedUsers = [];
    }

    // 检查用户是否已存在
    if (settings.telegram.allowedUsers.includes(userId)) {
      return false; // 用户已存在
    }

    // 添加新用户
    settings.telegram.allowedUsers.push(userId);
    writeSettings(settings);
    return true;
  } catch (error) {
    console.error("Failed to add Telegram user:", error);
    return false;
  }
}

export function removeTelegramUser(userId: number): boolean {
  try {
    const settings = readSettings();
    if (!settings.telegram?.allowedUsers) {
      return false;
    }

    const initialLength = settings.telegram.allowedUsers.length;
    settings.telegram.allowedUsers = settings.telegram.allowedUsers.filter(id => id !== userId);
    
    if (settings.telegram.allowedUsers.length < initialLength) {
      writeSettings(settings);
      return true;
    }
    
    return false; // 用户不存在
  } catch (error) {
    console.error("Failed to remove Telegram user:", error);
    return false;
  }
}

export function getTelegramUsers(): number[] {
  const settings = readSettings();
  return settings.telegram?.allowedUsers || [];
}
