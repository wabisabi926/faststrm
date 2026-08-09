import * as fs from "fs";
import * as path from "path";
import {
  AccountInfo,
  exportDirParse,
  fs_dir_getid,
} from "./115";
import {
  buildTree,
  collectFilesAndTopEmptyDirs,
  getLocalTree,
  readAccounts,
  readSettings,
  removeExtraFiles,
} from "./serverUtils";

export interface MappingScanRequest {
  account: string;
  cloudPath: string;
  localPath: string;
}

export interface StaleStrm {
  relPath: string;
  fullPath: string;
  strmContent?: string;
}

export interface MissingStrm {
  relPath: string;
  mediaExtension: string;
}

export interface MappingScanResult {
  account: string;
  cloudPath: string;
  localPath: string;
  remoteFileCount: number;
  localStrmCount: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  error?: string;
}

export interface ScanResult {
  mappings: MappingScanResult[];
  totalRemoteFiles: number;
  totalLocalStrms: number;
  totalStale: number;
  totalMissing: number;
  durationMs: number;
}

export interface ExecuteRequest {
  entries: Array<{
    localPath: string;
    staleRelPaths: string[];
  }>;
  dryRun?: boolean;
}

export interface ExecuteResult {
  deletedCount: number;
  failedCount: number;
  errors: Array<{ path: string; error: string }>;
  removedEmptyDirs: string[];
  dryRun: boolean;
  durationMs: number;
}

const MEDIA_EXT_SET = new Set([
  ".mkv", ".mp4", ".avi", ".mov", ".rmvb", ".flv", ".webm",
  ".ts", ".mpg", ".mpeg", ".wmv", ".m4v", ".3gp", ".f4v",
  ".iso", ".strm", ".m2ts", ".mts", ".tp", ".trp", ".vob",
]);

function mediaToStrm(filePath: string): string {
  const ext = path.extname(filePath);
  if (ext.toLowerCase() === ".strm") return filePath;
  return filePath.substring(0, filePath.length - ext.length) + ".strm";
}

function isMediaExtension(filePath: string): boolean {
  return MEDIA_EXT_SET.has(path.extname(filePath).toLowerCase());
}

async function scanSingleMapping(
  req: MappingScanRequest,
  accountInfo: AccountInfo
): Promise<MappingScanResult> {
  const result: MappingScanResult = {
    account: req.account,
    cloudPath: req.cloudPath,
    localPath: req.localPath,
    remoteFileCount: 0,
    localStrmCount: 0,
    staleStrms: [],
    missingStrms: [],
  };

  const saveDir = path.resolve(process.cwd(), `../data/${req.localPath}`);
  try {
    const idRes = await fs_dir_getid(req.cloudPath, { accountInfo });

    const data = await exportDirParse({
      exportFileIds: idRes.id,
      targetPid: 0,
      layerLimit: 0,
      deleteAfter: true,
      timeoutMs: 300000,
      checkIntervalMs: 1000,
      accountInfo,
    });

    const tree = buildTree(data);
    const remoteFiles: string[] = [];
    for (const node of tree) {
      if (node.children && node.children.length > 0) {
        remoteFiles.push(...collectFilesAndTopEmptyDirs(node.children));
      } else if (/\.[a-z0-9]+$/i.test(node.name)) {
        remoteFiles.push(node.name);
      }
    }
    result.remoteFileCount = remoteFiles.filter(isMediaExtension).length;

    const remoteMediaStrms = new Set(
      remoteFiles
        .filter(isMediaExtension)
        .map((p) => mediaToStrm(p).replace(/\\/g, "/").toLowerCase())
    );

    if (!fs.existsSync(saveDir)) {
      result.localStrmCount = 0;
      return result;
    }

    const localAllFiles = collectFilesAndTopEmptyDirs(getLocalTree(saveDir));
    const localStrmFiles = localAllFiles.filter((p) =>
      p.toLowerCase().endsWith(".strm")
    );
    result.localStrmCount = localStrmFiles.length;

    for (const relPath of localStrmFiles) {
      const norm = relPath.replace(/\\/g, "/").toLowerCase();
      if (!remoteMediaStrms.has(norm)) {
        const fullPath = path.join(saveDir, relPath);
        let strmContent: string | undefined;
        try {
          strmContent = fs.readFileSync(fullPath, "utf-8").trim();
        } catch {
          // ignore
        }
        result.staleStrms.push({ relPath, fullPath, strmContent });
      }
    }

    const localStrmNormSet = new Set(
      localStrmFiles.map((p) => p.replace(/\\/g, "/").toLowerCase())
    );
    for (const remoteFile of remoteFiles) {
      if (!isMediaExtension(remoteFile)) continue;
      const asStrm = mediaToStrm(remoteFile).replace(/\\/g, "/").toLowerCase();
      if (!localStrmNormSet.has(asStrm)) {
        result.missingStrms.push({
          relPath: remoteFile,
          mediaExtension: path.extname(remoteFile),
        });
      }
    }
  } catch (err: unknown) {
    result.error = err instanceof Error ? err.message : String(err);
    console.error(`[strmCleanup] Mapping failed: ${req.cloudPath} -> ${req.localPath}`, err);
  }
  return result;
}

export async function runScan(reqs: MappingScanRequest[]): Promise<ScanResult> {
  const started = Date.now();
  const accounts = readAccounts();
  const results: MappingScanResult[] = [];

  for (const req of reqs) {
    const accountInfo = accounts.find(
      (a: { name: string }) => a.name === req.account
    );
    if (!accountInfo?.cookie) {
      results.push({
        account: req.account,
        cloudPath: req.cloudPath,
        localPath: req.localPath,
        remoteFileCount: 0,
        localStrmCount: 0,
        staleStrms: [],
        missingStrms: [],
        error: `未找到账号或缺少 cookie: ${req.account}`,
      });
      continue;
    }
    results.push(await scanSingleMapping(req, accountInfo as AccountInfo));
  }

  const totalRemoteFiles = results.reduce((s, r) => s + r.remoteFileCount, 0);
  const totalLocalStrms = results.reduce((s, r) => s + r.localStrmCount, 0);
  const totalStale = results.reduce((s, r) => s + r.staleStrms.length, 0);
  const totalMissing = results.reduce((s, r) => s + r.missingStrms.length, 0);

  return {
    mappings: results,
    totalRemoteFiles,
    totalLocalStrms,
    totalStale,
    totalMissing,
    durationMs: Date.now() - started,
  };
}

export function runExecute(req: ExecuteRequest): ExecuteResult {
  const started = Date.now();
  const dryRun = !!req.dryRun;
  const errors: ExecuteResult["errors"] = [];
  const removedEmptyDirs: string[] = [];
  let deletedCount = 0;

  for (const entry of req.entries) {
    const saveDir = path.resolve(process.cwd(), `../data/${entry.localPath}`);
    if (!dryRun) {
      const removedFiles: string[] = [];
      for (const relPath of entry.staleRelPaths) {
        const fullPath = path.join(saveDir, relPath);
        try {
          if (fs.existsSync(fullPath)) {
            const stat = fs.statSync(fullPath);
            if (stat.isFile()) {
              fs.unlinkSync(fullPath);
              removedFiles.push(relPath);
              deletedCount++;
            }
          }
        } catch (err: unknown) {
          errors.push({
            path: relPath,
            error: err instanceof Error ? err.message : String(err),
          });
        }
      }
      if (removedFiles.length > 0) {
        const removeEmptyParents = (dir: string) => {
          if (!dir.startsWith(saveDir)) return;
          if (dir === saveDir) return;
          try {
            const files = fs.readdirSync(dir);
            if (files.length === 0) {
              fs.rmdirSync(dir);
              removedEmptyDirs.push(dir);
              removeEmptyParents(path.dirname(dir));
            }
          } catch {
            // ignore
          }
        };
        for (const relPath of removedFiles) {
          removeEmptyParents(path.dirname(path.join(saveDir, relPath)));
        }
      }
    } else {
      deletedCount = entry.staleRelPaths.length;
    }
  }

  return {
    deletedCount,
    failedCount: errors.length,
    errors,
    removedEmptyDirs,
    dryRun,
    durationMs: Date.now() - started,
  };
}

export function resolveDataDir(localPath: string): string {
  return path.resolve(process.cwd(), `../data/${localPath}`);
}

export function getDefaultScanRequestsFromSettings(): MappingScanRequest[] {
  const settings = readSettings();
  const lifeMonitor =
    (settings.lifeMonitor as { accounts?: string[]; pathMappings?: Array<{ cloudPath: string; localPath: string }> } | undefined);
  if (!lifeMonitor?.pathMappings || lifeMonitor.pathMappings.length === 0) return [];
  const account = lifeMonitor.accounts?.[0];
  if (!account) return [];
  return lifeMonitor.pathMappings.map((pm) => ({
    account,
    cloudPath: pm.cloudPath,
    localPath: pm.localPath,
  }));
}
