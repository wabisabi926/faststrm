import * as fs from "fs";
import * as path from "path";

export type LifeEventType = "create" | "delete" | "move" | "rename" | "folder-sync";

export interface LifeEventLog {
  id: string;
  timestamp: number;
  account: string;
  eventType: LifeEventType;
  success: boolean;
  filePath?: string;
  localPath?: string;
  message: string;
}

const LOG_DIR = path.join(process.cwd(), "../logs");
const LOG_FILE = path.join(LOG_DIR, "life-events.json");

const MAX_ENTRIES = 5000;
const MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;

function ensureDir() {
  if (!fs.existsSync(LOG_DIR)) {
    fs.mkdirSync(LOG_DIR, { recursive: true });
  }
}

function readAll(): LifeEventLog[] {
  ensureDir();
  if (!fs.existsSync(LOG_FILE)) return [];
  try {
    const raw = fs.readFileSync(LOG_FILE, "utf-8");
    if (!raw.trim()) return [];
    return JSON.parse(raw) as LifeEventLog[];
  } catch {
    return [];
  }
}

function writeAll(entries: LifeEventLog[]) {
  ensureDir();
  fs.writeFileSync(LOG_FILE, JSON.stringify(entries, null, 2), "utf-8");
}

function applyCleanup(entries: LifeEventLog[]): LifeEventLog[] {
  const now = Date.now();
  let filtered = entries.filter((e) => now - e.timestamp <= MAX_AGE_MS);
  if (filtered.length > MAX_ENTRIES) {
    filtered = filtered.slice(-MAX_ENTRIES);
  }
  return filtered;
}

export function appendLifeEventLog(
  account: string,
  eventType: LifeEventType | number,
  success: boolean,
  filePath: string | undefined,
  localPath: string | undefined,
  message: string
) {
  ensureDir();
  const typeStr =
    typeof eventType === "number" ? typeNumberToString(eventType) : eventType;

  const entry: LifeEventLog = {
    id: `${typeStr}_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
    timestamp: Date.now(),
    account,
    eventType: typeStr,
    success,
    filePath,
    localPath,
    message,
  };

  const all = readAll();
  all.push(entry);
  writeAll(applyCleanup(all));
}

function typeNumberToString(n: number): LifeEventType {
  switch (n) {
    case 1:
    case 2:
    case 14:
    case 17:
    case 18:
    case 23:
      return "create";
    case 3:
    case 4:
      return "delete";
    case 5:
    case 6:
      return "move";
    case 7:
    case 8:
      return "rename";
    default:
      return "folder-sync";
  }
}

export interface ListOptions {
  account?: string;
  eventType?: LifeEventType;
  success?: boolean;
  since?: number;
  until?: number;
  limit?: number;
}

export function listLifeEventLogs(options: ListOptions = {}): LifeEventLog[] {
  let entries = readAll();

  if (options.account) {
    entries = entries.filter((e) => e.account === options.account);
  }
  if (options.eventType) {
    entries = entries.filter((e) => e.eventType === options.eventType);
  }
  if (typeof options.success === "boolean") {
    entries = entries.filter((e) => e.success === options.success);
  }
  if (options.since) {
    entries = entries.filter((e) => e.timestamp >= options.since!);
  }
  if (options.until) {
    entries = entries.filter((e) => e.timestamp <= options.until!);
  }

  entries.sort((a, b) => b.timestamp - a.timestamp);

  if (options.limit && options.limit > 0) {
    entries = entries.slice(0, options.limit);
  }

  return entries;
}

export function deleteLifeEventLogs(id?: string): number {
  if (id) {
    const all = readAll();
    const before = all.length;
    const filtered = all.filter((e) => e.id !== id);
    writeAll(filtered);
    return before - filtered.length;
  }
  writeAll([]);
  return readAll().length;
}

export function cleanupLifeEventLogs(): number {
  const all = readAll();
  const cleaned = applyCleanup(all);
  writeAll(cleaned);
  return all.length - cleaned.length;
}
