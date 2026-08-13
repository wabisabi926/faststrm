/**
 * filePathDb.ts — SQLite 后端的文件路径数据库
 *
 * 替代原 eventMonitor.ts 中的 JSON 版 lifeFilePathDb.json。
 * 提供单条 CRUD + 批量路径前缀更新 + 幽灵记录清理。
 *
 * 参考项目：p115strmhelper db_manager/file_oper.py + models/file.py
 */

import Database from "better-sqlite3";
import type { Database as DatabaseType } from "better-sqlite3";
import fs from "fs";
import path from "path";

// ==================== 类型定义 ====================

/**
 * file_id 类型：number（精度丢失） 或 string（原始精度）
 *
 * 115 life API 返回的 file_id 是 19 位字符串（精度无损），但 LifeEvent 接口
 * 误声明为 number 导致运行时 TS 编译不报错但精度丢失。
 * SQLite 用 INTEGER 列存储能保留精度，但 better-sqlite3 接收 JS Number 时
 * 转换行为不稳定（同一 Number 多次插入可能得到不同 int64）。
 *
 * 解决方案：所有 file_id 在绑定到 SQLite 时统一 String() 转换，SQLite 按
 * INTEGER 亲和性把字符串转 int64（精度无损），查询时也能精确匹配。
 */
export type FileId = number | string;

export interface FilePathEntry {
  fileId: FileId;
  path: string;
  fileName: string;
  parentId: FileId;
  pickCode: string;
  updateTime: number;
}

// ==================== 常量 ====================

const CONFIG_DIR = path.join(process.cwd(), "../config");
const DB_FILE = path.join(CONFIG_DIR, "filePathDb.sqlite");
const OLD_JSON_FILE = path.join(CONFIG_DIR, "lifeFilePathDb.json");

/** SQLite 单次绑定变量上限（默认 999），分块删除时使用 */
const SQLITE_CHUNK_SIZE = 900;

// ==================== 路径规范化（统一入口） ====================

/**
 * P1修复：统一 DB 层面的路径规范化，解决读写不一致问题
 * 规则：去掉前导的 '/'。
 * 原因：生活事件写入时经常不带前导斜杠，而全量扫描有时会带，
 * 导致 "电影/xxx" 和 "/电影/xxx" 被当成两个不同路径。
 */
function normalizeDbPath(p: string): string {
  return p.replace(/^\/+/, "");
}

// ==================== 数据库初始化 ====================

let db: DatabaseType | null = null;

function ensureConfigDir(): void {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }
}

function getDb(): DatabaseType {
  if (db) return db;

  ensureConfigDir();
  db = new Database(DB_FILE);

  // WAL 模式：并发读 + 单写，性能更好
  db.pragma("journal_mode = WAL");
  db.pragma("synchronous = NORMAL");
  db.pragma("busy_timeout = 5000");

  // 建表
  db.exec(`
    CREATE TABLE IF NOT EXISTS files (
      account    TEXT    NOT NULL,
      file_id    INTEGER NOT NULL,
      path       TEXT    NOT NULL,
      file_name  TEXT    NOT NULL,
      parent_id  INTEGER NOT NULL DEFAULT 0,
      pickcode   TEXT    NOT NULL DEFAULT '',
      update_time INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (account, file_id)
    );
    CREATE INDEX IF NOT EXISTS idx_files_path ON files(account, path);
    CREATE INDEX IF NOT EXISTS idx_files_parent ON files(account, parent_id);
  `);

  // 从旧 JSON 迁移数据（仅首次创建 SQLite 时执行）
  migrateFromJsonIfNeeded(db);

  return db;
}

// ==================== JSON → SQLite 迁移 ====================

function migrateFromJsonIfNeeded(database: DatabaseType): void {
  // 检查表是否已有数据
  const count = database.prepare("SELECT COUNT(*) as n FROM files").get() as { n: number };
  if (count.n > 0) return;

  if (!fs.existsSync(OLD_JSON_FILE)) return;

  try {
    const oldDb = JSON.parse(fs.readFileSync(OLD_JSON_FILE, "utf-8")) as Record<string, FilePathEntry>;
    const entries = Object.entries(oldDb);
    if (entries.length === 0) return;

    // 解析 key "account:fileId" → account + fileId（保留字符串避免精度丢失）
    const insert = database.prepare(`
      INSERT OR REPLACE INTO files (account, file_id, path, file_name, parent_id, pickcode, update_time)
      VALUES (?, ?, ?, ?, ?, ?, ?)
    `);

    const migrateAll = database.transaction((rows: [string, FilePathEntry][]) => {
      for (const [key, entry] of rows) {
        const lastColon = key.lastIndexOf(":");
        if (lastColon <= 0) continue;
        const account = key.substring(0, lastColon);
        const fileIdStr = key.substring(lastColon + 1);
        if (!fileIdStr || !/^\d+$/.test(fileIdStr)) continue;
        insert.run(
          account,
          fileIdStr,
          normalizeDbPath(entry.path),
          entry.fileName,
          String(entry.parentId || 0),
          entry.pickCode || "",
          entry.updateTime || 0
        );
      }
    });

    migrateAll(entries);
    // 迁移成功后备份旧 JSON（不删除，留作回退）
    const backupPath = OLD_JSON_FILE + ".migrated";
    if (!fs.existsSync(backupPath)) {
      fs.copyFileSync(OLD_JSON_FILE, backupPath);
    }
    console.log(`[filePathDb] 从 JSON 迁移了 ${entries.length} 条记录到 SQLite`);
  } catch (e) {
    console.error("[filePathDb] JSON 迁移失败:", e);
  }
}

// ==================== 单条 CRUD ====================

/** 获取单条记录 */
export function getFilePathEntry(account: string, fileId: FileId): FilePathEntry | undefined {
  // 关键修复：始终用字符串绑定，避免 JS Number 精度丢失导致查询 miss
  // SQLite 接收字符串后按 INTEGER 亲和性转换，能精确匹配 DB 中存储的 int64 值
  const row = getDb().prepare(
    "SELECT file_id, path, file_name, parent_id, pickcode, update_time FROM files WHERE account = ? AND file_id = ?"
  ).get(account, String(fileId)) as
    | { file_id: number; path: string; file_name: string; parent_id: number; pickcode: string; update_time: number }
    | undefined;

  if (!row) return undefined;
  return {
    fileId: row.file_id,
    path: row.path,
    fileName: row.file_name,
    parentId: row.parent_id,
    pickCode: row.pickcode,
    updateTime: row.update_time,
  };
}

/** 按路径查找记录（用于 302 模式下从路径反查 pickcode） */
export function getFilePathEntryByPath(account: string, filePath: string): FilePathEntry | undefined {
  const normalizedPath = normalizeDbPath(filePath);
  const row = getDb().prepare(
    "SELECT file_id, path, file_name, parent_id, pickcode, update_time FROM files WHERE account = ? AND path = ?"
  ).get(account, normalizedPath) as
    | { file_id: number; path: string; file_name: string; parent_id: number; pickcode: string; update_time: number }
    | undefined;

  if (!row) return undefined;
  return {
    fileId: row.file_id,
    path: row.path,
    fileName: row.file_name,
    parentId: row.parent_id,
    pickCode: row.pickcode,
    updateTime: row.update_time,
  };
}

/** 插入或更新单条记录 */
export function upsertFilePathEntry(account: string, entry: FilePathEntry): void {
  getDb().prepare(`
    INSERT INTO files (account, file_id, path, file_name, parent_id, pickcode, update_time)
    VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(account, file_id) DO UPDATE SET
      path = excluded.path,
      file_name = excluded.file_name,
      parent_id = excluded.parent_id,
      pickcode = excluded.pickcode,
      update_time = excluded.update_time
  `).run(
    account,
    String(entry.fileId),
    normalizeDbPath(entry.path),
    entry.fileName,
    String(entry.parentId),
    entry.pickCode,
    entry.updateTime
  );
}

/** 删除单条记录 */
export function removeFilePathEntry(account: string, fileId: FileId): void {
  getDb().prepare("DELETE FROM files WHERE account = ? AND file_id = ?").run(account, String(fileId));
}

// ==================== 批量操作（参考项目移植） ====================

/**
 * 批量更新路径前缀（文件夹 rename/move 时同步所有子记录）
 *
 * 参考项目：p115strmhelper db_manager/models/file.py update_path_prefix()
 *
 * 单条 SQL 完成所有更新，10000 条记录毫秒级：
 *   UPDATE files SET path = ? || substr(path, ?)
 *   WHERE account = ? AND (path = ? OR path LIKE ?)
 *
 * @param account 账号名
 * @param oldPrefix 旧路径前缀（如 /电影/老名字）
 * @param newPrefix 新路径前缀（如 /电影/新名字）
 * @returns 更新的行数
 */
export function updatePathPrefixBatch(
  account: string,
  oldPrefix: string,
  newPrefix: string
): number {
  // P1修复：统一 normalizeDbPath（去前导 /）+ 去末尾 /，与其他入口保持一致
  const oldP = normalizeDbPath(oldPrefix).replace(/\/+$/, "") || "/";
  const newP = normalizeDbPath(newPrefix).replace(/\/+$/, "") || "/";

  // 根目录短路（避免全表误改）
  if (oldP === "/") return 0;

  const oldPrefixLen = oldP.length + 1; // +1 是因为 substr 从 1 开始计数

  const result = getDb().prepare(`
    UPDATE files
    SET path = ? || substr(path, ?)
    WHERE account = ? AND (path = ? OR path LIKE ?)
  `).run(newP, oldPrefixLen, account, oldP, `${oldP}/%`);

  return result.changes;
}

export interface RemoveGhostOptions {
  /** 绝对数量阈值：超过此数量的删除会被拒绝（默认 1000） */
  maxAbsoluteCount?: number;
  /** 比例阈值：删除比例超过此值会被拒绝（默认 0.3 = 30%），范围 (0, 1] */
  maxRatio?: number;
  /** 是否在拒绝时打印告警（默认 true） */
  warnOnBlock?: boolean;
}

/**
 * 清理幽灵记录（全量扫描后，DB 有但网盘已不存在的记录）
 *
 * 参考项目：p115strmhelper db_manager/models/file.py remove_by_path_prefix_not_in_ids()
 *
 * 逻辑：
 *   1. 查出 account + pathPrefix 下所有 file_id
 *   2. 计算差集：DB 有但本次扫描没看到的 = 幽灵
 *   3. 分块删除（每块 900 条，规避 SQLite 999 变量限制）
 *
 * @param account 账号名
 * @param pathPrefix 路径前缀（如 /电影/）
 * @param seenFileIds 本次扫描看到的 file_id 集合
 * @param opts 安全阈值选项
 * @returns 删除的记录数
 */
export function removeGhostRecords(
  account: string,
  pathPrefix: string,
  seenFileIds: Set<FileId>,
  opts?: RemoveGhostOptions
): number {
  // ==================== 安全防护（避免误删全表） ====================
  // 防止因 API 异常返回空数组导致 DB 被误清空。
  // 防护策略：
  //   1) seenFileIds 为空 → 拒绝（疑似 API 异常）
  //   2) 幽灵数量超过 maxAbsoluteCount → 拒绝
  //   3) 幽灵占比超过 maxRatio → 拒绝
  const maxAbsoluteCount = opts?.maxAbsoluteCount ?? 1000;
  const maxRatio = opts?.maxRatio ?? 0.3;
  const warnOnBlock = opts?.warnOnBlock ?? true;

  if (seenFileIds.size === 0) {
    if (warnOnBlock) {
      console.warn(`[filePathDb] 警告: seenFileIds 为空，疑似 API 返回异常，跳过 DB 清理以避免全表删除 (account=${account}, prefix=${pathPrefix})`);
    }
    return 0;
  }

  const db = getDb();
  // P1修复：使用 normalizeDbPath 规范化前缀，确保查询与写入一致
  const normalizedPrefix = normalizeDbPath(pathPrefix);
  const prefix = normalizedPrefix.endsWith("/") ? normalizedPrefix : normalizedPrefix + "/";

  // 查出该前缀下所有 file_id（字符串形式以匹配 Set 比较时归一化）
  const rows = db.prepare(
    "SELECT file_id FROM files WHERE account = ? AND (path = ? OR path LIKE ?)"
  ).all(account, prefix.replace(/\/+$/, ""), `${prefix}%`) as { file_id: number }[];

  // 统一 String() 归一化以避免 JS Number 精度丢失导致比较失败
  const seenStringSet = new Set(Array.from(seenFileIds).map((id) => String(id)));
  const allIds = new Set(rows.map((r) => String(r.file_id)));
  const totalCount = allIds.size;

  if (totalCount === 0) return 0;

  // 差集：DB 有但扫描没看到的
  // P0修复：保护正数ID（来自生活事件的真实file_id），永不被幽灵清理删除
  // 原因：占位符用负数ID（-1,-2,...），生活事件用真实正数ID（19位），主键不同导致共存。
  // 正数ID是文件存在的真实凭据，绝不能因全量扫描而删除。
  const candidatesNotSeen: string[] = [];
  for (const id of allIds) {
    if (!seenStringSet.has(id)) {
      candidatesNotSeen.push(id);
    }
  }
  const ghostIds = candidatesNotSeen.filter((id) => Number(id) < 0);
  const positiveGhostCount = candidatesNotSeen.length - ghostIds.length;
  if (positiveGhostCount > 0) {
    console.info(
      `[filePathDb] 跳过 ${positiveGhostCount} 条正数ID（生活事件真实file_id）的幽灵清理 (account=${account}, prefix=${pathPrefix})`
    );
  }

  const ghostCount = ghostIds.length;
  if (ghostCount === 0) {
    if (positiveGhostCount > 0) {
      console.info(
        `[filePathDb] 幽灵记录 ${positiveGhostCount} 条均为正数ID，无负数占位符可清理 (account=${account}, prefix=${pathPrefix})`
      );
    }
    return 0;
  }

  // 绝对数量阈值检查
  if (ghostCount > maxAbsoluteCount) {
    if (warnOnBlock) {
      console.warn(
        `[filePathDb] 警告: 幽灵记录数(${ghostCount})超过绝对阈值(${maxAbsoluteCount})，` +
        `拒绝删除以避免误删 (account=${account}, prefix=${pathPrefix}, total=${totalCount})`
      );
    }
    return 0;
  }

  // 比例阈值检查
  const ratio = ghostCount / totalCount;
  if (ratio > maxRatio) {
    if (warnOnBlock) {
      console.warn(
        `[filePathDb] 警告: 幽灵记录比例(${(ratio * 100).toFixed(1)}%)超过阈值(${maxRatio * 100}%)，` +
        `拒绝删除以避免误删 (account=${account}, prefix=${pathPrefix}, ghost=${ghostCount}, total=${totalCount})`
      );
    }
    return 0;
  }

  // 分块删除
  const deleteStmt = db.prepare("DELETE FROM files WHERE account = ? AND file_id = ?");
  const deleteBatch = db.transaction((ids: string[]) => {
    for (const id of ids) {
      deleteStmt.run(account, id);
    }
  });

  let deleted = 0;
  for (let i = 0; i < ghostIds.length; i += SQLITE_CHUNK_SIZE) {
    const chunk = ghostIds.slice(i, i + SQLITE_CHUNK_SIZE);
    deleteBatch(chunk);
    deleted += chunk.length;
  }

  return deleted;
}

/**
 * 按路径前缀查询所有记录（用于批量清理操作）
 */
export function getEntriesByPathPrefix(
  account: string,
  pathPrefix: string
): FilePathEntry[] {
  // P1修复：使用 normalizeDbPath 规范化前缀
  const normalizedPrefix = normalizeDbPath(pathPrefix);
  const prefix = normalizedPrefix.endsWith("/") ? normalizedPrefix : normalizedPrefix + "/";
  const rows = getDb().prepare(
    "SELECT file_id, path, file_name, parent_id, pickcode, update_time FROM files WHERE account = ? AND (path = ? OR path LIKE ?)"
  ).all(account, prefix.replace(/\/+$/, ""), `${prefix}%`) as
    { file_id: number; path: string; file_name: string; parent_id: number; pickcode: string; update_time: number }[];

  return rows.map((row) => ({
    fileId: row.file_id,
    path: row.path,
    fileName: row.file_name,
    parentId: row.parent_id,
    pickCode: row.pickcode,
    updateTime: row.update_time,
  }));
}

/**
 * 按精确路径删除记录（Emby 删除同步用）
 * 删除 path 完全匹配的记录。
 * @returns 删除的行数
 */
export function deleteByPath(account: string, filePath: string): number {
  // P1修复：使用 normalizeDbPath 规范化路径
  const result = getDb().prepare(
    "DELETE FROM files WHERE account = ? AND path = ?"
  ).run(account, normalizeDbPath(filePath));
  return result.changes;
}

/**
 * 按路径前缀批量删除记录（Emby 整季/整剧删除同步用）
 * 删除 path = prefix 或 path 以 prefix/ 开头的所有记录。
 * @returns 删除的行数
 */
export function deleteByPathPrefix(account: string, pathPrefix: string): number {
  // P1修复：统一 normalizeDbPath（去前导 /）+ 去末尾 /
  const prefix = normalizeDbPath(pathPrefix).replace(/\/+$/, "") || "/";
  if (prefix === "/") return 0; // 根目录短路，防全表误删
  const result = getDb().prepare(
    "DELETE FROM files WHERE account = ? AND (path = ? OR path LIKE ?)"
  ).run(account, prefix, `${prefix}/%`);
  return result.changes;
}

/**
 * 获取指定账号下的记录总数
 */
export function getEntryCount(account?: string): number {
  if (account) {
    const row = getDb().prepare("SELECT COUNT(*) as n FROM files WHERE account = ?").get(account) as { n: number };
    return row.n;
  }
  const row = getDb().prepare("SELECT COUNT(*) as n FROM files").get() as { n: number };
  return row.n;
}

/**
 * 批量 upsert（用于全量扫描后批量写入）
 */
export function upsertFilePathEntryBatch(account: string, entries: FilePathEntry[]): void {
  if (entries.length === 0) return;

  const stmt = getDb().prepare(`
    INSERT INTO files (account, file_id, path, file_name, parent_id, pickcode, update_time)
    VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(account, file_id) DO UPDATE SET
      path = excluded.path,
      file_name = excluded.file_name,
      parent_id = excluded.parent_id,
      pickcode = excluded.pickcode,
      update_time = excluded.update_time
  `);

  const batch = getDb().transaction((rows: FilePathEntry[]) => {
    for (const entry of rows) {
      stmt.run(
        account,
        String(entry.fileId),
        normalizeDbPath(entry.path),
        entry.fileName,
        String(entry.parentId),
        entry.pickCode,
        entry.updateTime
      );
    }
  });

  // 分块处理，避免单次事务过大
  for (let i = 0; i < entries.length; i += SQLITE_CHUNK_SIZE) {
    const chunk = entries.slice(i, i + SQLITE_CHUNK_SIZE);
    batch(chunk);
  }
}

/**
 * 关闭数据库连接（进程退出时调用）
 */
export function closeDb(): void {
  if (db) {
    db.close();
    db = null;
  }
}

/**
 * 获取数据库文件路径（用于诊断）
 */
export function getDbPath(): string {
  return DB_FILE;
}
