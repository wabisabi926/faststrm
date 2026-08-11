/**
 * strmFileOps.ts — STRM 文件操作统一工具层
 *
 * 消除 eventMonitor / strmCleanup / serverUtils 三处重复的：
 *   - 空目录清理 (removeEmptyParents)
 *   - STRM 文件删除 (deleteStrmFile)
 *   - 目录递归删除 (deleteStrmDir)
 *   - 按文件名递归搜索 STRM (findStrmRecursive)
 *   - 关联文件清理 (cleanRelatedFiles)  ← 移植自参考项目 PathRemoveUtils
 *   - STRM 内容比较写入 (syncStrmText)  ← 移植自参考项目 _sync_strm_text_with_event
 *
 * 参考项目：p115strmhelper utils/path.py PathRemoveUtils + helper/life/client.py
 */

import * as fs from "fs";
import * as path from "path";

// ==================== 内部工具 ====================

/**
 * 原子写入文件：先写临时文件再 rename，避免进程崩溃导致文件内容写一半损坏。
 * 临时文件与目标文件同目录（保证同分区，rename 是原子的）。
 */
function atomicWriteFileSync(filePath: string, content: string): void {
  const tmpPath = `${filePath}.tmp.${process.pid}`;
  fs.writeFileSync(tmpPath, content, "utf-8");
  fs.renameSync(tmpPath, filePath);
}

// ==================== 类型 ====================

export interface RemoveEmptyParentsOptions {
  /** 不允许超越的根目录集合（这些目录不会被删除） */
  rootDirs: Set<string>;
  /** 调用方标签，用于日志 */
  tag?: string;
  /** 账号名，用于日志上下文（符合 project_memory 硬约束） */
  account?: string;
}

export interface DeleteStrmFileOptions {
  /** 删除后清理空父目录的根目录集合 */
  rootDirs?: Set<string>;
  /** 删除 STRM 的同时清理同名关联文件 (.nfo/.jpg 等) */
  cleanRelated?: boolean;
  /** 调用方标签 */
  tag?: string;
  /** 账号名，用于日志上下文 */
  account?: string;
}

export interface SyncStrmTextResult {
  /** 操作是否成功 */
  ok: boolean;
  /** 是否实际写入了文件（内容一致时为 false） */
  wrote: boolean;
  /** 错误信息 */
  error?: string;
}

/** 构建带账号上下文的日志前缀，符合 project_memory 硬约束 */
function buildLogPrefix(tag?: string, account?: string): string {
  const t = tag || "strmFileOps";
  return account ? `[${t}] account=${account}` : `[${t}]`;
}

// ==================== 1. 空目录清理 ====================

/**
 * 从指定路径向上递归删除空目录，直到遇到 rootDirs 或非空目录。
 *
 * 统一实现，替代：
 *   - eventMonitor.ts removeEmptyParentDirs (L641)
 *   - strmCleanup.ts removeEmptyParents (L288 内联)
 *   - serverUtils.ts removeExtraFiles 中的 removeEmptyParents (L68 内联)
 *
 * 参考项目：p115strmhelper utils/path.py PathRemoveUtils.remove_parent_dir()
 */
export function removeEmptyParents(
  startPath: string,
  opts: RemoveEmptyParentsOptions
): string[] {
  const removed: string[] = [];
  const prefix = buildLogPrefix(opts.tag, opts.account);
  // resolve 为绝对路径，确保与 rootDirs（已是 resolved 绝对路径）能正确匹配，
  // 否则用户把 localPath 配成相对路径时根目录保护会失效
  let currentDir = path.resolve(path.dirname(startPath));

  while (currentDir) {
    // 命中根目录边界 → 停止
    if (opts.rootDirs.has(currentDir)) break;

    try {
      const entries = fs.readdirSync(currentDir);
      if (entries.length > 0) break; // 非空 → 停止

      fs.rmdirSync(currentDir);
      removed.push(currentDir);
      console.log(`${prefix} 清理空目录: ${currentDir}`);
      currentDir = path.dirname(currentDir);
    } catch {
      break; // 读取/删除失败 → 停止
    }
  }

  return removed;
}

// ==================== 2. STRM 文件删除 ====================

/**
 * 删除单个 STRM 文件，可选清理关联文件和空父目录。
 *
 * 参考项目：p115strmhelper helper/life/client.py __apply_remove_unless_strm_path()
 */
export function deleteStrmFile(
  strmPath: string,
  opts?: DeleteStrmFileOptions
): { deleted: boolean; removedDirs: string[]; relatedDeleted: string[] } {
  const prefix = buildLogPrefix(opts?.tag, opts?.account);
  const removedDirs: string[] = [];
  const relatedDeleted: string[] = [];
  let deleted = false;

  try {
    if (fs.existsSync(strmPath)) {
      fs.unlinkSync(strmPath);
      deleted = true;
      console.log(`${prefix} 删除 STRM: ${strmPath}`);
    }
  } catch (e) {
    console.error(`${prefix} 删除 STRM 失败 ${strmPath}: ${e instanceof Error ? e.message : String(e)}`);
    return { deleted: false, removedDirs, relatedDeleted };
  }

  // 清理关联文件 (.nfo/.jpg/.srt 等)
  if (deleted && opts?.cleanRelated) {
    const related = cleanRelatedFiles(strmPath, { tag: opts?.tag, account: opts?.account });
    relatedDeleted.push(...related);
  }

  // 清理空父目录
  if (deleted && opts?.rootDirs) {
    const dirs = removeEmptyParents(strmPath, { rootDirs: opts.rootDirs, tag: opts?.tag, account: opts?.account });
    removedDirs.push(...dirs);
  }

  return { deleted, removedDirs, relatedDeleted };
}

// ==================== 3. 目录递归删除 ====================

/**
 * 递归删除整个目录及其内容（用于文件夹删除事件）。
 * 如果目录不存在则静默跳过。
 *
 * 参考项目：p115strmhelper helper/life/client.py rmtree()
 */
export function deleteStrmDir(
  dirPath: string,
  opts?: { tag?: string; account?: string }
): { deleted: boolean; error?: string } {
  const prefix = buildLogPrefix(opts?.tag, opts?.account);

  try {
    if (!fs.existsSync(dirPath)) {
      return { deleted: false };
    }

    fs.rmSync(dirPath, { recursive: true, force: true });
    console.log(`${prefix} 删除目录: ${dirPath}`);
    return { deleted: true };
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    console.error(`${prefix} 删除目录失败 ${dirPath}: ${msg}`);

    // 尝试逐个清理
    try {
      const entries = fs.readdirSync(dirPath);
      for (const entry of entries) {
        const full = path.join(dirPath, entry);
        const stat = fs.statSync(full);
        if (stat.isDirectory()) {
          fs.rmSync(full, { recursive: true, force: true });
        } else {
          fs.unlinkSync(full);
        }
      }
      fs.rmdirSync(dirPath);
      console.log(`${prefix} 逐个清理后删除目录: ${dirPath}`);
      return { deleted: true };
    } catch (e2) {
      return { deleted: false, error: e2 instanceof Error ? e2.message : String(e2) };
    }
  }
}

// ==================== 4. 按文件名递归搜索 STRM ====================

/**
 * 在指定目录下递归查找精确匹配文件名的 STRM 文件。
 *
 * 用于 move-outside / rename-cross-mapping 兜底场景。
 * 单个目录读取失败不中断整体搜索。
 *
 * 参考项目无直接对应，为本项目独创兜底机制。
 */
export function findStrmRecursive(dir: string, targetStrmName: string): string[] {
  const results: string[] = [];
  try {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        results.push(...findStrmRecursive(full, targetStrmName));
      } else if (entry.isFile() && entry.name === targetStrmName) {
        results.push(full);
      }
    }
  } catch (e) {
    console.warn(
      `${buildLogPrefix()} findStrmRecursive 跳过 ${dir}: ${e instanceof Error ? e.message : String(e)}`
    );
  }
  return results;
}

// ==================== 5. 按目录名递归搜索目录 ====================

/**
 * 在指定目录下递归查找精确匹配名称的子目录。
 *
 * 用于 move-outside / rename 兜底场景中文件夹的定位与清理。
 * 单个目录读取失败不中断整体搜索。
 *
 * @param dir 搜索起始目录
 * @param targetDirName 目标目录名（精确匹配 entry.name）
 * @returns 匹配到的目录绝对路径列表
 */
export function findDirRecursive(dir: string, targetDirName: string): string[] {
  const results: string[] = [];
  try {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === targetDirName) {
          results.push(full);
        }
        // 无论是否命中，都继续向子目录递归（同名目录可能嵌套存在）
        results.push(...findDirRecursive(full, targetDirName));
      }
    }
  } catch (e) {
    console.warn(
      `${buildLogPrefix()} findDirRecursive 跳过 ${dir}: ${e instanceof Error ? e.message : String(e)}`
    );
  }
  return results;
}

// ==================== 6. 关联文件清理 ====================

/**
 * 清理与 STRM 文件同名的关联文件（.nfo/.jpg/.srt 等）。
 *
 * 参考项目：p115strmhelper utils/path.py PathRemoveUtils.clean_related_files()
 *
 * 匹配规则（与参考项目一致）：
 *   1. 仅扫描同目录文件（不递归）
 *   2. 基准文件的 stem 是其他文件 stem 的子串
 *   3. 排除 .strm 后缀（保护 STRM 文件本身）
 *   4. 排除基准文件自身
 *
 * @param baseFilePath 基准文件路径（如 /data/电影/A.mkv.strm）
 * @returns 被删除的文件路径列表
 */
export function cleanRelatedFiles(
  baseFilePath: string,
  opts?: { tag?: string; account?: string }
): string[] {
  const prefix = buildLogPrefix(opts?.tag, opts?.account);
  const deleted: string[] = [];

  const dir = path.dirname(baseFilePath);
  const baseStem = path.basename(baseFilePath, path.extname(baseFilePath));

  try {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      if (!entry.isFile()) continue;

      const entryPath = path.join(dir, entry.name);
      if (entryPath === baseFilePath) continue; // 排除自身
      if (entry.name.toLowerCase().endsWith(".strm")) continue; // 保护 .strm

      const entryStem = path.basename(entry.name, path.extname(entry.name));
      if (entryStem.includes(baseStem)) {
        try {
          fs.unlinkSync(entryPath);
          deleted.push(entryPath);
          console.warn(`${prefix} 清理关联文件: ${entryPath}`);
        } catch (e) {
          // missing_ok 容忍并发删除
          if (!(e instanceof Error && "code" in e && e.code === "ENOENT")) {
            console.error(`${prefix} 删除关联文件失败 ${entryPath}: ${e instanceof Error ? e.message : String(e)}`);
          }
        }
      }
    }
  } catch (e) {
    console.warn(`${prefix} cleanRelatedFiles 扫描目录失败 ${dir}: ${e instanceof Error ? e.message : String(e)}`);
  }

  return deleted;
}

// ==================== 7. STRM 内容比较写入 ====================

/**
 * 比较 STRM 文件当前内容与期望内容，不同才写入。
 *
 * 参考项目：p115strmhelper helper/life/client.py _sync_strm_text_with_event()
 *
 * 避免无谓的磁盘写入（重命名后 URL 不变则跳过 IO）。
 *
 * @param strmPath STRM 文件路径
 * @param expectedContent 期望的内容（URL 字符串）
 * @param createIfMissing 文件不存在时是否创建（默认 true）
 * @returns { ok, wrote } wrote=false 表示内容一致无需写入
 */
export function syncStrmText(
  strmPath: string,
  expectedContent: string,
  opts?: { createIfMissing?: boolean; tag?: string; account?: string }
): SyncStrmTextResult {
  const prefix = buildLogPrefix(opts?.tag, opts?.account);
  const createIfMissing = opts?.createIfMissing ?? true;

  // 文件不存在
  if (!fs.existsSync(strmPath)) {
    if (!createIfMissing) {
      return { ok: false, wrote: false, error: `文件不存在: ${strmPath}` };
    }
    try {
      fs.mkdirSync(path.dirname(strmPath), { recursive: true });
      atomicWriteFileSync(strmPath, expectedContent);
      console.log(`${prefix} 创建 STRM: ${strmPath}`);
      return { ok: true, wrote: true };
    } catch (e) {
      return { ok: false, wrote: false, error: e instanceof Error ? e.message : String(e) };
    }
  }

  // 文件存在 → 比较内容
  try {
    const current = fs.readFileSync(strmPath, "utf-8").trim();
    const expected = expectedContent.trim();

    if (current === expected) {
      // 内容一致，无需写入
      return { ok: true, wrote: false };
    }

    // 内容不同 → 重写
    atomicWriteFileSync(strmPath, expectedContent);
    console.log(`${prefix} 更新 STRM 内容: ${strmPath}`);
    return { ok: true, wrote: true };
  } catch (e) {
    return { ok: false, wrote: false, error: e instanceof Error ? e.message : String(e) };
  }
}

// ==================== 8. 批量获取根目录集合 ====================

/**
 * 从路径映射列表中提取根目录集合（用于 removeEmptyParents 的边界保护）。
 *
 * 替代 eventMonitor.ts getRootDirs()
 */
export function getRootDirsFromMappings(
  pathMappings: Array<{ localPath: string }>
): Set<string> {
  const roots = new Set<string>();
  for (const mapping of pathMappings) {
    roots.add(path.resolve(mapping.localPath));
  }
  return roots;
}
