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

// ==================== 回收站支持 ====================

const TRASH_DIR = path.join(process.cwd(), "../data/.trash");
const TRASH_RETENTION_MS = 7 * 24 * 60 * 60 * 1000; // 7 天保留

function ensureTrashDir(): string {
  if (!fs.existsSync(TRASH_DIR)) {
    fs.mkdirSync(TRASH_DIR, { recursive: true });
  }
  // 清理超过保留期的文件（懒清理，每次调用最多清理 20 个避免卡顿）
  try {
    const now = Date.now();
    const entries = fs.readdirSync(TRASH_DIR, { withFileTypes: true });
    let cleaned = 0;
    for (const entry of entries) {
      if (entry.isFile()) {
        const p = path.join(TRASH_DIR, entry.name);
        try {
          const stat = fs.statSync(p);
          if (now - stat.mtimeMs > TRASH_RETENTION_MS) {
            fs.unlinkSync(p);
            cleaned++;
            if (cleaned >= 20) break;
          }
        } catch {}
      }
    }
  } catch {}
  return TRASH_DIR;
}

/**
 * 将文件移动到回收站（保留目录结构和文件名），返回回收站中的新路径；若移动失败则 fallback 到永久删除。
 */
function moveToTrash(filePath: string, tag: string): string | null {
  try {
    const trashDir = ensureTrashDir();
    // 生成唯一回收站文件名：时间戳_原路径hash_文件名
    const timestamp = Date.now();
    const pathHash = Buffer.from(filePath).toString("base64").replace(/[^a-zA-Z0-9]/g, "").slice(0, 12);
    const trashName = `${timestamp}_${pathHash}_${path.basename(filePath)}`;
    const trashPath = path.join(trashDir, trashName);
    fs.renameSync(filePath, trashPath);
    console.log(`[${tag}] 已移入回收站: ${filePath} -> ${trashPath}`);
    return trashPath;
  } catch (e) {
    console.warn(`[${tag}] 移入回收站失败，降级为永久删除: ${filePath}: ${e instanceof Error ? e.message : String(e)}`);
    try {
      fs.unlinkSync(filePath);
      return null;
    } catch {
      return null;
    }
  }
}

// ==================== 类型 ====================

export interface RemoveEmptyParentsOptions {
  /** 不允许超越的根目录集合（这些目录不会被删除） */
  rootDirs: Set<string>;
  /** 调用方标签，用于日志 */
  tag?: string;
}

export interface DeleteStrmFileOptions {
  /** 删除后清理空父目录的根目录集合 */
  rootDirs?: Set<string>;
  /** 删除 STRM 的同时清理同名关联文件 (.nfo/.jpg 等) */
  cleanRelated?: boolean;
  /** 调用方标签 */
  tag?: string;
  /** 是否启用回收站（默认 true） */
  enableTrash?: boolean;
}

export interface SyncStrmTextResult {
  /** 操作是否成功 */
  ok: boolean;
  /** 是否实际写入了文件（内容一致时为 false） */
  wrote: boolean;
  /** 错误信息 */
  error?: string;
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
  const tag = opts.tag || "strmFileOps";
  let currentDir = path.dirname(startPath);

  while (currentDir) {
    // 命中根目录边界 → 停止
    if (opts.rootDirs.has(currentDir)) break;

    try {
      const entries = fs.readdirSync(currentDir);
      if (entries.length > 0) break; // 非空 → 停止

      fs.rmdirSync(currentDir);
      removed.push(currentDir);
      console.log(`[${tag}] 清理空目录: ${currentDir}`);
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
): { deleted: boolean; removedDirs: string[]; relatedDeleted: string[]; trashPath?: string } {
  const tag = opts?.tag || "strmFileOps";
  const enableTrash = opts?.enableTrash ?? true;
  const removedDirs: string[] = [];
  const relatedDeleted: string[] = [];
  let deleted = false;
  let trashPath: string | undefined;

  try {
    if (fs.existsSync(strmPath)) {
      if (enableTrash) {
        const moved = moveToTrash(strmPath, tag);
        if (moved) {
          trashPath = moved;
        }
        deleted = true;
      } else {
        fs.unlinkSync(strmPath);
        deleted = true;
        console.log(`[${tag}] 删除 STRM: ${strmPath}`);
      }
    }
  } catch (e) {
    console.error(`[${tag}] 删除 STRM 失败 ${strmPath}: ${e instanceof Error ? e.message : String(e)}`);
    return { deleted: false, removedDirs, relatedDeleted };
  }

  // 清理关联文件 (.nfo/.jpg/.srt 等)
  if (deleted && opts?.cleanRelated) {
    const related = cleanRelatedFiles(strmPath, { tag });
    relatedDeleted.push(...related);
  }

  // 清理空父目录
  if (deleted && opts?.rootDirs) {
    const dirs = removeEmptyParents(strmPath, { rootDirs: opts.rootDirs, tag });
    removedDirs.push(...dirs);
  }

  return { deleted, removedDirs, relatedDeleted, trashPath };
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
  opts?: { tag?: string; enableTrash?: boolean }
): { deleted: boolean; error?: string } {
  const tag = opts?.tag || "strmFileOps";
  const enableTrash = opts?.enableTrash ?? true;

  try {
    if (!fs.existsSync(dirPath)) {
      return { deleted: false };
    }

    if (enableTrash) {
      // 启用回收站：遍历目录树，每个文件逐个 moveToTrash，然后尝试 rmdir 空目录
      try {
        const entries = fs.readdirSync(dirPath, { withFileTypes: true });
        for (const entry of entries) {
          const full = path.join(dirPath, entry.name);
          if (entry.isDirectory()) {
            // 递归处理子目录
            deleteStrmDir(full, { ...opts, tag: `${tag}/sub` });
          } else {
            // 文件：移入回收站（若是关联文件也回收）
            moveToTrash(full, tag);
          }
        }
        // 清理空目录（子目录已被递归清理，此处应为空）
        try {
          fs.rmdirSync(dirPath);
        } catch {
          // 若仍非空，fallback 到递归强制删除
          fs.rmSync(dirPath, { recursive: true, force: true });
        }
        console.log(`[${tag}] 删除目录(已回收): ${dirPath}`);
        return { deleted: true };
      } catch (e) {
        console.warn(`[${tag}] 目录回收失败，降级为强制删除: ${e instanceof Error ? e.message : String(e)}`);
        fs.rmSync(dirPath, { recursive: true, force: true });
        return { deleted: true };
      }
    } else {
      fs.rmSync(dirPath, { recursive: true, force: true });
      console.log(`[${tag}] 删除目录: ${dirPath}`);
      return { deleted: true };
    }
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    console.error(`[${tag}] 删除目录失败 ${dirPath}: ${msg}`);

    // 尝试逐个清理（参考项目 mixed 模式思路）
    try {
      const entries = fs.readdirSync(dirPath);
      for (const entry of entries) {
        const full = path.join(dirPath, entry);
        const stat = fs.statSync(full);
        if (stat.isDirectory()) {
          fs.rmSync(full, { recursive: true, force: true });
        } else {
          if (enableTrash) {
            moveToTrash(full, tag);
          } else {
            fs.unlinkSync(full);
          }
        }
      }
      fs.rmdirSync(dirPath);
      console.log(`[${tag}] 逐个清理后删除目录: ${dirPath}`);
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
      `[strmFileOps] findStrmRecursive 跳过 ${dir}: ${e instanceof Error ? e.message : String(e)}`
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
      `[strmFileOps] findDirRecursive 跳过 ${dir}: ${e instanceof Error ? e.message : String(e)}`
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
  opts?: { tag?: string }
): string[] {
  const tag = opts?.tag || "strmFileOps";
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
          console.warn(`[${tag}] 清理关联文件: ${entryPath}`);
        } catch (e) {
          // missing_ok 容忍并发删除
          if (!(e instanceof Error && "code" in e && e.code === "ENOENT")) {
            console.error(`[${tag}] 删除关联文件失败 ${entryPath}: ${e instanceof Error ? e.message : String(e)}`);
          }
        }
      }
    }
  } catch (e) {
    console.warn(`[${tag}] cleanRelatedFiles 扫描目录失败 ${dir}: ${e instanceof Error ? e.message : String(e)}`);
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
  opts?: { createIfMissing?: boolean; tag?: string }
): SyncStrmTextResult {
  const tag = opts?.tag || "strmFileOps";
  const createIfMissing = opts?.createIfMissing ?? true;

  // 文件不存在
  if (!fs.existsSync(strmPath)) {
    if (!createIfMissing) {
      return { ok: false, wrote: false, error: `文件不存在: ${strmPath}` };
    }
    try {
      fs.mkdirSync(path.dirname(strmPath), { recursive: true });
      fs.writeFileSync(strmPath, expectedContent, "utf-8");
      console.log(`[${tag}] 创建 STRM: ${strmPath}`);
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
    fs.writeFileSync(strmPath, expectedContent, "utf-8");
    console.log(`[${tag}] 更新 STRM 内容: ${strmPath}`);
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
