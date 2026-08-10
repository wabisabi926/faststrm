import { exec } from "child_process";
import * as fs from "fs";
import {
  AppSettings,
  LifeMonitorSettings,
  readAccounts,
  readSettings,
  writeSettings,
  readTasks,
} from "./serverUtils";
import { resolveStrmSettings } from "./strmUtils";

export type MediaMountSourceTag = "global_302" | "task" | "life_monitor";

export interface MediaMountEntry {
  /** 标准化后的前缀（末尾不含 /） */
  prefix: string;
  /** 来源 tag（用于 UI 展示和日志追溯） */
  source: MediaMountSourceTag;
  /** 关联账号名（可选） */
  account?: string;
  /** 关联任务 ID（仅 task 来源） */
  taskId?: string;
}

export interface SyncResult {
  changed: boolean;
  added: string[];
  removed: string[];
  kept: string[];
  final: string[];
  entriesWithSource: MediaMountEntry[];
  nginx: {
    attempted: boolean;
    available: boolean;
    ok: boolean;
    message: string;
  };
  error?: string;
}

/** 标准化前缀：去末尾斜杠、trim */
function normalizePrefix(p: string): string {
  return (p || "").trim().replace(/\/+$/, "");
}

/** 校验是否是合法的 http/https URL 前缀（只要域名+端口部分，空的也直接放行但不入列） */
function isValidHttpPrefix(p: string): boolean {
  if (!p) return false;
  return /^https?:\/\/[^\s/$.?#].[^\s]*$/i.test(p);
}

/**
 * 检查当前环境 nginx 是否可用。
 * - Windows 上通过 where.exe nginx 探测（PATH 里能找到就算"可用"）
 * - Linux/macOS 上通过 command -v nginx
 */
async function isNginxAvailable(): Promise<boolean> {
  const cmd =
    process.platform === "win32"
      ? "where.exe nginx 2>nul"
      : "command -v nginx >/dev/null 2>&1";
  return new Promise<boolean>((resolve) => {
    try {
      exec(cmd, { timeout: 3000 }, (err) => resolve(!err));
    } catch {
      resolve(false);
    }
  });
}

/** 执行 nginx -s reload（仅在 nginx 可用时尝试，异步等待退出码） */
async function reloadNginxIfAvailable(): Promise<{
  attempted: boolean;
  available: boolean;
  ok: boolean;
  message: string;
}> {
  const available = await isNginxAvailable();
  if (!available) {
    return {
      attempted: false,
      available: false,
      ok: true,
      message: "nginx not found in PATH, skipped reload",
    };
  }
  return new Promise((resolve) => {
    try {
      exec("nginx -s reload", { timeout: 5000 }, (err, _stdout, stderr) => {
        if (err) {
          resolve({
            attempted: true,
            available: true,
            ok: false,
            message: (stderr || err.message || String(err)).trim() || "nginx reload failed (unknown)",
          });
        } else {
          resolve({
            attempted: true,
            available: true,
            ok: true,
            message: "nginx reloaded successfully",
          });
        }
      });
    } catch (e) {
      resolve({
        attempted: true,
        available: true,
        ok: false,
        message: e instanceof Error ? e.message : String(e),
      });
    }
  });
}

/** 收集来源条目，写入 entries（带 source tag）；同时把标准化 prefix 加入 resultSet（去重） */
function collect(
  entries: MediaMountEntry[],
  resultSet: Set<string>,
  prefix: string,
  source: MediaMountSourceTag,
  extra?: { account?: string; taskId?: string }
) {
  const p = normalizePrefix(prefix);
  if (!isValidHttpPrefix(p)) return;
  if (resultSet.has(p)) {
    // 已存在的前缀保留第一个 source（优先级：global_302 先收集）
    return;
  }
  resultSet.add(p);
  entries.push({ prefix: p, source, ...extra });
}

export interface ComputeInput {
  settings: AppSettings;
  accounts: { name: string }[];
  tasks: Array<{
    id?: string;
    account?: string;
    strmPrefix?: string;
    enablePathEncoding?: boolean;
    enable302?: boolean;
  }>;
}

export interface ComputeResult {
  entries: MediaMountEntry[];
  finalSet: Set<string>;
  finalPaths: string[];
}

/**
 * 全量重算 mediaMountPath 条目集合（纯计算，不落盘、不做 nginx）。
 * 这是 SSOT 的"视图层"——用于 UI 展示来源 tag、做 diff 预览、以及作为 sync 落盘的数据源。
 */
export function computeMediaMountEntries(input: ComputeInput): ComputeResult {
  const { settings, accounts, tasks } = input;
  const lifeMonitor: LifeMonitorSettings | undefined = settings.lifeMonitor;

  const entries: MediaMountEntry[] = [];
  const resultSet = new Set<string>();

  // ========== 场景 1：全局 302 × 所有账号 ==========
  if (settings.enable302 && settings.strmPrefix) {
    for (const acc of accounts) {
      if (!acc?.name) continue;
      const resolved = resolveStrmSettings(acc.name, null, settings);
      collect(entries, resultSet, resolved.strmPrefix, "global_302", {
        account: acc.name,
      });
    }
  }

  // ========== 场景 2：所有任务 ==========
  for (const task of tasks) {
    const taskHasStrmConfig =
      (task.strmPrefix != null && task.strmPrefix !== "") ||
      task.enable302 === true;
    if (!taskHasStrmConfig) continue;
    const resolved = resolveStrmSettings(task.account || undefined, task, settings);
    collect(entries, resultSet, resolved.strmPrefix, "task", {
      taskId: task.id,
      account: task.account,
    });
  }

  // ========== 场景 3：生活事件监控（若有独立的 strmPrefix/enable302）==========
  if (lifeMonitor && Array.isArray(lifeMonitor.accounts)) {
    // 生活事件配置的优先级：lifeMonitor 自己的 strmPrefix / enable302 > 全局
    const lifeOverride = {
      strmPrefix: lifeMonitor.strmPrefix ?? settings.strmPrefix,
      enablePathEncoding: lifeMonitor.enablePathEncoding ?? settings.enablePathEncoding,
      enable302:
        (lifeMonitor as LifeMonitorSettings & { enable302?: boolean }).enable302 ??
        settings.enable302,
    };
    for (const accName of lifeMonitor.accounts) {
      if (!accName) continue;
      const resolved = resolveStrmSettings(accName, null, lifeOverride);
      collect(entries, resultSet, resolved.strmPrefix, "life_monitor", {
        account: accName,
      });
    }
  }

  const finalPaths = [...resultSet];
  return { entries, finalSet: resultSet, finalPaths };
}

/**
 * 媒体挂载路径唯一事实来源（SSOT）同步入口。
 *
 * 基于当前系统状态全量重算 mediaMountPath 最终集合，
 * 自动收敛所有"只增不减/孤儿条目/改前缀后残留"问题。
 *
 * 扫描范围：
 *   1. 全局 enable302 × 所有账号
 *   2. 所有任务的自定义 strmPrefix / enable302
 *   3. 生活事件监控 × 其账号集
 *
 * @param opts.skipNginxReload 为 true 时不执行 nginx reload（连续调用时避免重复 reload）
 * @param opts.existingSettings 传入已读取的 settings，避免重复磁盘 IO
 */
export async function syncMediaMountPaths(opts: {
  skipNginxReload?: boolean;
  existingSettings?: AppSettings;
} = {}): Promise<SyncResult> {
  try {
    const settings = opts.existingSettings ?? readSettings();
    const accounts = readAccounts() as unknown as { name: string }[];
    const tasks = readTasks() as Array<{
      id?: string;
      account?: string;
      strmPrefix?: string;
      enablePathEncoding?: boolean;
      enable302?: boolean;
    }>;

    const { entries, finalPaths } = computeMediaMountEntries({
      settings,
      accounts,
      tasks,
    });

    const previous: string[] = Array.isArray(settings.mediaMountPath)
      ? settings.mediaMountPath.map(normalizePrefix).filter(isValidHttpPrefix)
      : [];
    const prevSet = new Set(previous);

    const added = finalPaths.filter((p) => !prevSet.has(p));
    const removed = previous.filter((p) => !finalPaths.includes(p));
    const kept = finalPaths.filter((p) => prevSet.has(p));
    const changed = added.length > 0 || removed.length > 0;

    // entriesWithSource 也需要按 final 顺序，稳定排序
    const entriesWithSource = finalPaths
      .map((p) => entries.find((e) => e.prefix === p) || { prefix: p, source: "global_302" as MediaMountSourceTag })
      .sort((a, b) => a.prefix.localeCompare(b.prefix));

    if (changed) {
      const nextSettings: AppSettings = { ...settings, mediaMountPath: finalPaths };
      writeSettings(nextSettings);
    }

    const nginx = opts.skipNginxReload
      ? { attempted: false, available: false, ok: true, message: "skipped (skipNginxReload=true)" }
      : await reloadNginxIfAvailable();

    const logParts: string[] = [`最终 ${finalPaths.length} 条`];
    if (added.length) logParts.push(`+${added.length} 新增`);
    if (removed.length) logParts.push(`-${removed.length} 删除`);
    if (!changed) logParts.push("（无变化）");
    const nginxTag = nginx.attempted
      ? nginx.ok
        ? "nginx: ok"
        : `nginx: FAIL ${nginx.message}`
      : nginx.available
        ? "nginx: skipped"
        : "nginx: n/a";
    console.info(
      `[mediaMount] 同步完成: ${logParts.join("，")} | ${nginxTag}` +
        (added.length
          ? `\n  新增: ${added.join(", ")}`
          : "") +
        (removed.length
          ? `\n  删除: ${removed.join(", ")}`
          : "")
    );

    return {
      changed,
      added,
      removed,
      kept,
      final: finalPaths,
      entriesWithSource,
      nginx,
    };
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    console.error("[mediaMount] syncMediaMountPaths failed:", e);
    return {
      changed: false,
      added: [],
      removed: [],
      kept: [],
      final: [],
      entriesWithSource: [],
      nginx: { attempted: false, available: false, ok: false, message: "sync aborted" },
      error: message,
    };
  }
}

/** 便于前端展示的来源 tag 颜色映射（语义名，前端自行决定配色） */
export function sourceTagLabel(s: MediaMountSourceTag): string {
  switch (s) {
    case "global_302":
      return "全局 302";
    case "task":
      return "任务";
    case "life_monitor":
      return "生活事件";
    default:
      return s;
  }
}

// 防止 fs 未被使用告警（被 serverUtils.readSettings/writeSettings 间接使用，这里声明以防 tree-shake 误报）
void fs;
