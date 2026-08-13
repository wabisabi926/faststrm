import {
  sanitizeDirectoryPath,
  _getLifeMonitorConfig,
  ensureConfigDir,
  readIdPathCache,
  writeIdPathCache,
  ID_PATH_MEM_CACHE_MAX,
} from "./eventMonitorConfig";
import {
  AccountInfo,
  fs_dir_getid,
  fs_files,
  getFileInfoById,
} from "./115";
import { LifeEvent } from "./115Life";
import { waitFor115ApiToken } from "./rateLimiter";
import { readSettings } from "./serverUtils";

function evictIdPathCacheIfNeeded(cache: Map<string, string>): void {
  while (cache.size >= ID_PATH_MEM_CACHE_MAX) {
    const oldestKey = cache.keys().next().value;
    if (oldestKey === undefined) break;
    cache.delete(oldestKey);
  }
}

function getIdPath(account: string, cid: number | string, idPathMemoryCache: Map<string, string>): string | undefined {
  const cacheKey = `${account}:${cid}`;
  const memCached = idPathMemoryCache.get(cacheKey);
  if (memCached) {
    idPathMemoryCache.delete(cacheKey);
    idPathMemoryCache.set(cacheKey, memCached);
    return sanitizeDirectoryPath(memCached, `getIdPath(mem ${account}:${cid})`);
  }

  const diskCache = readIdPathCache();
  const diskCached = diskCache[cacheKey];
  if (diskCached) {
    const sane = sanitizeDirectoryPath(diskCached, `getIdPath(disk ${account}:${cid})`);
    evictIdPathCacheIfNeeded(idPathMemoryCache);
    idPathMemoryCache.set(cacheKey, sane);
    return sane;
  }
  return undefined;
}

function setIdPath(account: string, cid: number | string, pathStr: string, idPathMemoryCache: Map<string, string>) {
  const sane = sanitizeDirectoryPath(pathStr, `setIdPath(${account}:${cid})`);
  const cacheKey = `${account}:${cid}`;
  if (idPathMemoryCache.has(cacheKey)) idPathMemoryCache.delete(cacheKey);
  evictIdPathCacheIfNeeded(idPathMemoryCache);
  idPathMemoryCache.set(cacheKey, sane);
  const diskCache = readIdPathCache();
  diskCache[cacheKey] = sane;
  writeIdPathCache(diskCache);
}

async function resolvePathByCid(
  accountInfo: AccountInfo,
  cid: number | string,
  idPathMemoryCache: Map<string, string>
): Promise<string> {
  if (Number(cid) === 0) return "/";

  const account = accountInfo.name;

  const memCached = getIdPath(account, cid, idPathMemoryCache);
  if (memCached) return memCached;

  const config = _getLifeMonitorConfig();
  for (const mapping of config.pathMappings) {
    if (mapping.account && mapping.account !== account) continue;
    try {
      await waitFor115ApiToken();
      const mappedCid = await fs_dir_getid(mapping.cloudPath, {
        userAgent: readSettings()["user-agent"],
        accountInfo: accountInfo as AccountInfo,
      });
      if (mappedCid.id === cid) {
        setIdPath(account, cid, mapping.cloudPath, idPathMemoryCache);
        return sanitizeDirectoryPath(mapping.cloudPath, `resolvePathByCid(tier2 ${account}:${cid})`);
      }
    } catch {
      // Ignore errors for individual mappings
    }
  }

  try {
    const { default: axios } = await import("axios");
    const userAgent = readSettings()["user-agent"] || "Mozilla/5.0";

    await waitFor115ApiToken();
    const fileInfo = await getFileInfoById(cid, {
      userAgent,
      accountInfo: accountInfo as AccountInfo,
    });

    if (fileInfo && typeof fileInfo === "object") {
      const info = fileInfo as Record<string, unknown>;
      const pathVal = info.path as string | undefined;
      if (pathVal) {
        setIdPath(account, cid, pathVal, idPathMemoryCache);
        return sanitizeDirectoryPath(pathVal, `resolvePathByCid(tier3-fileInfo ${account}:${cid})`);
      }
    }

    await waitFor115ApiToken();
    await fs_files(cid, {
      userAgent,
      limit: 1,
      accountInfo: accountInfo as AccountInfo,
    });

    const exportUrl = "https://proapi.115.com/android/2.0/ufile/export_dir";
    const formData = new URLSearchParams();
    formData.append("file_ids", String(cid));
    formData.append("target", "U_1_0");
    formData.append("layer_limit", "1");

    const exportResp = await axios.post(exportUrl, formData, {
      headers: {
        "User-Agent": userAgent,
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        Cookie: accountInfo.cookie,
      },
      timeout: 15000,
    });

    const exportId = exportResp.data?.data?.export_id;
    if (exportId) {
      for (let i = 0; i < 10; i++) {
        await new Promise((r) => setTimeout(r, 500));
        const statusUrl = `https://webapi.115.com/files/export_dir?export_id=${exportId}`;
        const statusResp = await axios.get(statusUrl, {
          headers: {
            "User-Agent": userAgent,
            Cookie: accountInfo.cookie,
          },
          timeout: 10000,
        });

        const exportData = statusResp.data?.data;
        if (exportData) {
          const pathStr = extractPathFromExportData(exportData, cid);
          if (pathStr) {
            setIdPath(account, cid, pathStr, idPathMemoryCache);
            return sanitizeDirectoryPath(pathStr, `resolvePathByCid(tier3-exportDir ${account}:${cid})`);
          }
        }
      }
    }

    console.warn(`[LifeMonitor] Cannot resolve path for cid ${cid}`);
    return "";
  } catch (error) {
    console.error(`[LifeMonitor] Error resolving path for cid ${cid}:`, error);
    return "";
  }
}

function extractPathFromExportData(data: unknown, targetCid: number | string): string {
  if (!data || typeof data !== "object") return "";
  const obj = data as Record<string, unknown>;
  const targetStr = String(targetCid);

  const innerData = obj.data as Record<string, unknown> | undefined;
  if (innerData) {
    const list = innerData.list as Array<Record<string, unknown>> | undefined;
    if (Array.isArray(list)) {
      for (const item of list) {
        if (String(item.cid) === targetStr || String(item.id) === targetStr) {
          return (item.path || item.file_path || "") as string;
        }
        if (item.children) {
          const found = extractPathFromExportData(item.children, targetCid);
          if (found) return found;
        }
      }
    }
  }

  if (Array.isArray(obj.list)) {
    for (const item of obj.list as Array<Record<string, unknown>>) {
      if (String(item.cid) === targetStr || String(item.id) === targetStr) {
        return (item.path || item.file_path || "") as string;
      }
    }
  }

  return "";
}

async function resolveEventPath(
  accountInfo: AccountInfo,
  event: LifeEvent,
  idPathMemoryCache: Map<string, string>,
  nameOverride?: string
): Promise<string> {
  let parentPath = "";
  if (Number(event.parent_id) > 0) {
    parentPath = await resolvePathByCid(accountInfo, event.parent_id, idPathMemoryCache);
  } else {
    parentPath = "/";
  }

  const fileName = nameOverride || event.file_name || "";
  if (parentPath === "/" || parentPath === "") {
    return "/" + fileName;
  }
  return parentPath.endsWith("/") ? parentPath + fileName : parentPath + "/" + fileName;
}

export {
  getIdPath,
  setIdPath,
  resolvePathByCid,
  resolveEventPath,
  extractPathFromExportData,
  evictIdPathCacheIfNeeded,
};