/**
 * 删除同步历史持久化
 * 存储到 ./data/syncDelHistory.json，最多保留 200 条
 */

import * as fs from "fs";
import * as path from "path";

const HISTORY_FILE = path.join(process.cwd(), "../data", "syncDelHistory.json");
const MAX_RECORDS = 200;

export interface SyncDelRecord {
  itemPath: string;
  itemName: string;
  itemType: string;
  deletedAt: string;  // ISO 时间
  deletedFiles: number;
  cloudPath: string;
  dryRun?: boolean;
}

function ensureDataDir(): void {
  const dir = path.dirname(HISTORY_FILE);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

function readAll(): SyncDelRecord[] {
  if (!fs.existsSync(HISTORY_FILE)) return [];
  try {
    const data = fs.readFileSync(HISTORY_FILE, "utf-8");
    const parsed = JSON.parse(data);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeAll(records: SyncDelRecord[]): void {
  ensureDataDir();
  fs.writeFileSync(HISTORY_FILE, JSON.stringify(records, null, 2), "utf-8");
}

/** 追加一条删除记录 */
export function addSyncDelRecord(record: SyncDelRecord): void {
  try {
    const records = readAll();
    records.push(record);
    // 超出上限时只保留最近的 MAX_RECORDS 条
    if (records.length > MAX_RECORDS) {
      records.splice(0, records.length - MAX_RECORDS);
    }
    writeAll(records);
  } catch (e) {
    console.error("[SyncDelHistory] 写入失败:", e);
  }
}

/** 读取最近 N 条记录（默认 50） */
export function getSyncDelRecords(limit = 50): SyncDelRecord[] {
  const records = readAll();
  if (limit <= 0) return records;
  return records.slice(-limit).reverse();
}

/** 清空所有历史 */
export function clearSyncDelHistory(): void {
  writeAll([]);
}
