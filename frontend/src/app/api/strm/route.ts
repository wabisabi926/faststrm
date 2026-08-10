import { NextRequest, NextResponse } from "next/server";
import { readAccounts, readSettings } from "@/lib/serverUtils";
import { getDownloadUrlWeb, type AccountInfo } from "@/lib/115";

const URL_CACHE_TTL = 5 * 60 * 1000; // 5 分钟
const REACHABLE_CACHE_TTL = 4 * 60 * 1000; // 4 分钟（略小于 URL_CACHE_TTL）
const REACHABLE_CACHE_MAX = 256; // 简单 LRU 上限（够用就行，避免内存膨胀）

type RouteDecision = "proxy" | "redirect";
interface DecisionResult {
  decision: RouteDecision;
  reason: string;
}
interface ReachableResult {
  ok: boolean;
  status: number;
}

/* ---------------------------- 缓存（简单 LRU） ---------------------------- */

interface CacheEntry<V> {
  value: V;
  expires: number;
}

class SimpleLRU<K, V> {
  private map = new Map<K, CacheEntry<V>>();
  constructor(private maxSize: number, private ttlMs: number) {}

  get(key: K): V | undefined {
    const entry = this.map.get(key);
    if (!entry) return undefined;
    if (entry.expires <= Date.now()) {
      this.map.delete(key);
      return undefined;
    }
    // LRU touch: 移到末尾
    this.map.delete(key);
    this.map.set(key, entry);
    return entry.value;
  }

  set(key: K, value: V): void {
    if (this.map.has(key)) this.map.delete(key);
    while (this.map.size >= this.maxSize) {
      // 删除最老（map 最前）的一条
      const firstKey = this.map.keys().next().value;
      if (firstKey === undefined) break;
      this.map.delete(firstKey);
    }
    this.map.set(key, { value, expires: Date.now() + this.ttlMs });
  }
}

const urlCache = new SimpleLRU<string, string>(512, URL_CACHE_TTL);
const reachableCache = new SimpleLRU<string, ReachableResult>(
  REACHABLE_CACHE_MAX,
  REACHABLE_CACHE_TTL
);

/* -------------------------------- 工具函数 -------------------------------- */

function isValidPickcode(code: string): boolean {
  return code.length === 17 && /^[a-zA-Z0-9]+$/.test(code);
}

function findAccount(accountName: string): AccountInfo | undefined {
  const accounts = readAccounts() as unknown as AccountInfo[];
  return accounts.find((a) => a.name === accountName && a.accountType === "115");
}

function buildContentDisposition(fileName: string): string {
  const asciiOnly = /^[\x00-\x7F]+$/.test(fileName);
  if (asciiOnly) {
    return `attachment; filename="${fileName}"`;
  }
  const encoded = encodeURIComponent(fileName);
  return `attachment; filename*=UTF-8''${encoded}`;
}

function resolveFileName(raw: string | undefined): string | undefined {
  if (!raw) return undefined;
  return /%/.test(raw) ? decodeURIComponent(raw) : raw;
}

function estimateFileSizeBytesFromName(fileName: string | undefined): number | undefined {
  if (!fileName) return undefined;
  // 优先匹配明确的 size 标记，如 .20GB. .45.3G. .12000MB. 等
  const m = fileName.match(/[._-](\d+(?:\.\d+)?)\s*(GB|G|MB|M|KB|K)\b/i);
  if (!m) return undefined;
  const num = Number(m[1]);
  if (!Number.isFinite(num) || num <= 0) return undefined;
  const unit = m[2].toUpperCase();
  switch (unit) {
    case "GB":
    case "G":
      return Math.round(num * 1024 ** 3);
    case "MB":
    case "M":
      return Math.round(num * 1024 ** 2);
    case "KB":
    case "K":
      return Math.round(num * 1024);
    default:
      return undefined;
  }
}

/* ----------------------- 规则引擎：proxy vs redirect ----------------------- */

/**
 * 已知 seek/302 兼容性差的客户端 UA 关键字（参考 emby2Alist 的 clientSelfAlistRule）。
 * 这些客户端强制代理，避免 seek 时 Range 跟 302 Location 对不上导致进度条拖动异常。
 */
const FORCE_PROXY_UA_TOKENS = Object.freeze([
  "Infuse",
  "VidHub",
  "SenPlayer",
  "SenPlayerHD",
]);

const LARGE_FILE_BYTES = 20 * 1024 ** 3; // 20GB

function isPrivateNetworkIp(ip: string | null): boolean {
  if (!ip) return false;
  // IPv4 私有地址段
  if (ip.startsWith("192.168.")) return true;
  if (ip.startsWith("10.")) return true;
  if (/^172\.(1[6-9]|2\d|3[0-1])\./.test(ip)) return true;
  if (ip === "127.0.0.1" || ip === "::1" || ip === "localhost") return true;
  if (ip.startsWith("[")) return true; // IPv6 本地链路基本当本地处理
  return false;
}

function getClientIp(req: NextRequest): string | null {
  const xf = req.headers.get("x-forwarded-for");
  if (xf) {
    const first = xf.split(",")[0]?.trim();
    if (first) return first;
  }
  const xr = req.headers.get("x-real-ip");
  if (xr) return xr.trim();
  try {
    // Next.js Edge runtime 下 req.ip 可能存在；兜底 null
    return (req as unknown as { ip?: string }).ip ?? null;
  } catch {
    return null;
  }
}

function decideRoute(
  req: NextRequest,
  explicitMode: string | undefined,
  fileName: string | undefined
): DecisionResult {
  // 1) 手动指定优先级最高（调试用）
  if (explicitMode === "redirect") return { decision: "redirect", reason: "explicit_mode_redirect" };
  if (explicitMode === "proxy") return { decision: "proxy", reason: "explicit_mode_proxy" };

  const ua = req.headers.get("user-agent") || "";

  // 2) seek 坑客户端 → 强制代理
  for (const token of FORCE_PROXY_UA_TOKENS) {
    if (ua.includes(token)) {
      return { decision: "proxy", reason: `force_proxy_ua:${token}` };
    }
  }

  // 3) 局域网用户 → 代理（家里上行通常够，稳定性优先）
  if (isPrivateNetworkIp(getClientIp(req))) {
    return { decision: "proxy", reason: "private_network_prefers_proxy" };
  }

  // 4) 大文件（≥20GB）→ 走 redirect 省服务器上行；后面会做 redirectCheck
  const estSize = estimateFileSizeBytesFromName(fileName);
  if (estSize !== undefined && estSize >= LARGE_FILE_BYTES) {
    return {
      decision: "redirect",
      reason: `large_file_ge_20GB(${Math.round(estSize / 1024 ** 3)}GB)`,
    };
  }

  // 5) 默认策略：代理优先，保证所有浏览器/客户端开箱即用
  return { decision: "proxy", reason: "default_proxy_fallback" };
}

/* -------------------- redirectCheck：后端 HEAD 预检直链 -------------------- */

function build115SafeHeaders(
  account: AccountInfo,
  userAgent: string | undefined,
  extras?: Record<string, string>
): Headers {
  const headers = new Headers();
  headers.set("User-Agent", userAgent || (readSettings()["user-agent"] as string) || "Mozilla/5.0");
  headers.set("Referer", "https://115.com/");
  headers.set("Origin", "https://115.com");
  if (account.cookie) headers.set("Cookie", account.cookie);
  if (extras) {
    for (const [k, v] of Object.entries(extras)) headers.set(k, v);
  }
  return headers;
}

async function redirectCheck(
  cdnUrl: string,
  account: AccountInfo,
  userAgent: string | undefined
): Promise<ReachableResult> {
  const cacheKey = `${account.name}|${cdnUrl}`;
  const cached = reachableCache.get(cacheKey);
  if (cached) return cached;

  const controller = new AbortController();
  const timeoutMs = 5000;
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  let status = 0;
  try {
    const resp = await fetch(cdnUrl, {
      method: "HEAD",
      headers: build115SafeHeaders(account, userAgent),
      redirect: "follow",
      cache: "no-store",
      signal: controller.signal,
    });
    status = resp.status;
  } catch (err) {
    const msg = err instanceof Error ? err.name : String(err);
    console.warn(`[STRM][redirectCheck] HEAD failed: ${msg}`);
    status = 0;
  } finally {
    clearTimeout(timer);
  }

  // 2xx/3xx 都视为可达；302/304 等在 follow 下会是最终 2xx
  const ok = status >= 200 && status < 400;
  const result: ReachableResult = { ok, status };
  if (ok) reachableCache.set(cacheKey, result); // 只缓存成功，失败让它下次重试
  return result;
}

/* ----------------------------- URL 解析缓存 ----------------------------- */

async function resolveDownloadUrl(
  pickcode: string,
  account: AccountInfo,
  userAgent: string | undefined
): Promise<string> {
  const cacheKey = `${account.name}:${pickcode}:${userAgent || ""}`;
  const cached = urlCache.get(cacheKey);
  if (cached) return cached;

  const url = await getDownloadUrlWeb(pickcode, {
    userAgent,
    accountInfo: account,
  });
  urlCache.set(cacheKey, url);
  return url;
}

/* ------------------------------- 核心处理器 ------------------------------- */

async function handleProxy(
  req: NextRequest,
  cdnUrl: string,
  account: AccountInfo,
  userAgent: string | undefined,
  fileName: string | undefined
): Promise<NextResponse> {
  const forwardedHeaders = build115SafeHeaders(account, userAgent);

  const range = req.headers.get("range");
  if (range) forwardedHeaders.set("Range", range);
  const ifRange = req.headers.get("if-range");
  if (ifRange) forwardedHeaders.set("If-Range", ifRange);

  // 禁用上游压缩，否则没法做 passthrough 的 content-length
  forwardedHeaders.set("Accept-Encoding", "identity");

  const upstream = await fetch(cdnUrl, {
    method: req.method,
    headers: forwardedHeaders,
    // @ts-expect-error Next.js fetch supports duplex for streaming bodies
    duplex: "half",
    cache: "no-store",
    redirect: "follow",
  });

  const respHeaders = new Headers();
  const hopByHop = new Set([
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailers",
    "transfer-encoding",
    "upgrade",
    "content-encoding",
  ]);

  for (const [k, v] of upstream.headers.entries()) {
    if (hopByHop.has(k.toLowerCase())) continue;
    respHeaders.set(k, v);
  }

  // 避免泄漏 115 Cookie
  respHeaders.delete("set-cookie");

  // 强制覆盖安全/展示头部
  respHeaders.set("Accept-Ranges", "bytes");
  const decodedFileName = resolveFileName(fileName);
  if (decodedFileName) {
    respHeaders.set("Content-Disposition", buildContentDisposition(decodedFileName));
  }
  respHeaders.set(
    "Access-Control-Allow-Origin",
    req.headers.get("origin") || "*"
  );
  respHeaders.set(
    "Access-Control-Expose-Headers",
    "Content-Disposition, Content-Length, Content-Type, Accept-Ranges, Content-Range"
  );

  return new NextResponse(upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: respHeaders,
  });
}

function doRedirect(
  cdnUrl: string,
  fileName: string | undefined
): NextResponse {
  const headers: Record<string, string> = { Location: cdnUrl };
  const decodedFileName = resolveFileName(fileName);
  if (decodedFileName) {
    headers["Content-Disposition"] = buildContentDisposition(decodedFileName);
  }
  return new NextResponse(null, { status: 302, headers });
}

async function handleStrm(req: NextRequest): Promise<NextResponse> {
  const t0 = Date.now();
  const { searchParams } = new URL(req.url);
  const accountName = searchParams.get("account") || "";
  const pickcode = searchParams.get("pickcode") || "";
  const fileName = searchParams.get("file_name") || undefined;
  const explicitMode = searchParams.get("mode")?.toLowerCase();

  if (!accountName) {
    return NextResponse.json({ error: "Missing account" }, { status: 400 });
  }
  if (!pickcode) {
    return NextResponse.json({ error: "Missing pickcode" }, { status: 400 });
  }
  if (!isValidPickcode(pickcode)) {
    return NextResponse.json(
      { error: `Bad pickcode: ${pickcode}` },
      { status: 400 }
    );
  }

  const account = findAccount(accountName);
  if (!account) {
    return NextResponse.json(
      { error: `Account not found: ${accountName}` },
      { status: 404 }
    );
  }

  const userAgent =
    req.headers.get("user-agent") ||
    (readSettings()["user-agent"] as string) ||
    undefined;

  let cdnUrl: string;
  try {
    cdnUrl = await resolveDownloadUrl(pickcode, account, userAgent);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error("[STRM] Failed to get download URL:", message);
    return NextResponse.json(
      { error: `Failed to get download URL: ${message}` },
      { status: 502 }
    );
  }

  // 规则引擎：决策 proxy vs redirect
  const { decision, reason } = decideRoute(req, explicitMode, fileName);

  let finalDecision: RouteDecision = decision;
  let finalReason = reason;
  let redirectCheckStatus: number | undefined;

  if (decision === "redirect") {
    const check = await redirectCheck(cdnUrl, account, userAgent);
    redirectCheckStatus = check.status;
    if (!check.ok) {
      // 直链不可达 → 降级 proxy，保证用户体验不炸
      finalDecision = "proxy";
      finalReason = `${reason} -> redirect_check_failed(${check.status}) fallback_proxy`;
    }
  }

  const elapsed = Date.now() - t0;
  const shortPc = `${pickcode.slice(0, 4)}…${pickcode.slice(-3)}`;
  console.log(
    `[STRM] pickcode=${shortPc} decision=${finalDecision} reason=${finalReason} ` +
      `redirect_check=${redirectCheckStatus ?? "skipped"} elapsed=${elapsed}ms`
  );

  try {
    if (finalDecision === "redirect") {
      return doRedirect(cdnUrl, fileName);
    }
    return await handleProxy(req, cdnUrl, account, userAgent, fileName);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error(`[STRM][${finalDecision}] error:`, message);
    return NextResponse.json(
      { error: `${finalDecision} failed: ${message}` },
      { status: 502 }
    );
  }
}

export async function GET(req: NextRequest) {
  return handleStrm(req);
}

export async function HEAD(req: NextRequest) {
  return handleStrm(req);
}

export const dynamic = "force-dynamic";
export const runtime = "nodejs";
