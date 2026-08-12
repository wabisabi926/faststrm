import { NextRequest, NextResponse } from "next/server";
import { readAccounts, readSettings } from "@/lib/serverUtils";
import { getDownloadUrlWebFull, type AccountInfo, type DownloadUrlMeta } from "@/lib/115";

const URL_CACHE_TTL = 5 * 60 * 1000; // 5 分钟
const REACHABLE_CACHE_TTL = 4 * 60 * 1000; // 4 分钟（略小于 URL_CACHE_TTL）
const REACHABLE_CACHE_MAX = 256; // 简单 LRU 上限（够用就行，避免内存膨胀）

// 可配置化的默认值（settings 读取失败时兜底）
const DEFAULT_FORCE_PROXY_UA_TOKENS = Object.freeze(["Infuse", "VidHub", "SenPlayer", "SenPlayerHD"]);
const DEFAULT_ACCOUNT_PROXY_CONCURRENCY_LIMIT = 8;
const DEFAULT_REDIRECT_CHECK_TIMEOUT_MS = 5000;

// 读取 strm 路由策略配置（带兜底默认值）
function getStrmRouteConfig() {
  const s = readSettings();
  const st = s.strm || {};
  return {
    forceProxyUaTokens: (st.forceProxyUaTokens && st.forceProxyUaTokens.length > 0)
      ? st.forceProxyUaTokens
      : DEFAULT_FORCE_PROXY_UA_TOKENS,
    accountProxyConcurrencyLimit: (st.accountProxyConcurrencyLimit && st.accountProxyConcurrencyLimit > 0)
      ? st.accountProxyConcurrencyLimit
      : DEFAULT_ACCOUNT_PROXY_CONCURRENCY_LIMIT,
    redirectCheckTimeoutMs: (st.redirectCheckTimeoutMs && st.redirectCheckTimeoutMs > 0)
      ? st.redirectCheckTimeoutMs
      : DEFAULT_REDIRECT_CHECK_TIMEOUT_MS,
  };
}

const accountProxyCount = new Map<string, number>();

// hop-by-hop 头部，模块级常量避免每次请求重建
const HOP_BY_HOP_HEADERS = new Set([
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

const urlCache = new SimpleLRU<string, DownloadUrlMeta>(512, URL_CACHE_TTL);
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

/* ----------------------- 规则引擎：proxy vs redirect ----------------------- */

/**
 * 已知 seek/302 兼容性差的客户端 UA 关键字（参考 emby2Alist 的 clientSelfAlistRule）。
 * 这些客户端强制代理，避免 seek 时 Range 跟 302 Location 对不上导致进度条拖动异常。
 * P2-16: 配置化 — 从 settings.strm.forceProxyUaTokens 读取，默认值见 DEFAULT_FORCE_PROXY_UA_TOKENS
 */

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
  forceProxyUaTokens: readonly string[]
): DecisionResult {
  // 1) 手动指定优先级最高（调试用，仅私网生效，见 handleStrm）
  if (explicitMode === "redirect") return { decision: "redirect", reason: "explicit_mode_redirect" };
  if (explicitMode === "proxy") return { decision: "proxy", reason: "explicit_mode_proxy" };

  const ua = req.headers.get("user-agent") || "";

  // 2) seek 坑客户端 → 强制代理（Infuse/VidHub/SenPlayer 对 115 的 302 seek 有 bug）
  for (const token of forceProxyUaTokens) {
    if (ua.includes(token)) {
      return { decision: "proxy", reason: `force_proxy_ua:${token}` };
    }
  }

  // 3) 默认策略：redirect 优先
  // 部署设备（faststrm + Emby Server 同机）场景下，proxy 会导致双重中转：
  //   115 -> faststrm -> Emby Server -> 客户端
  // redirect 让 Emby Server / Kodi / 浏览器直连 115，faststrm 零中转。
  // force-proxy UA 已在规则 2 处理，其余客户端默认信任 302 兼容性。
  return { decision: "redirect", reason: "default_redirect" };
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
  userAgent: string | undefined,
  timeoutMs: number
): Promise<ReachableResult> {
  const cacheKey = `${account.name}|${cdnUrl}`;
  const cached = reachableCache.get(cacheKey);
  if (cached) return cached;

  const controller = new AbortController();
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
): Promise<DownloadUrlMeta> {
  // Phase 1.1: 去掉 UA 维度，115 直链解析不依赖 UA
  const cacheKey = `${account.name}:${pickcode}`;
  const cached = urlCache.get(cacheKey);
  if (cached) return cached;

  const meta = await getDownloadUrlWebFull(pickcode, {
    userAgent,
    accountInfo: account,
  });
  urlCache.set(cacheKey, meta);
  return meta;
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

  // 建连超时控制 + 客户端断连传播
  // 防止 CDN 慢响应导致 proxy 并发名额被永久占用（accountProxyCount 不释放）
  const controller = new AbortController();
  const CONNECT_TIMEOUT_MS = 30_000;
  const timer = setTimeout(() => controller.abort(), CONNECT_TIMEOUT_MS);
  // 客户端断连时取消上游 fetch，释放连接
  if (req.signal) {
    if (req.signal.aborted) controller.abort();
    else req.signal.addEventListener("abort", () => controller.abort(), { once: true });
  }

  let upstream: Response;
  try {
    upstream = await fetch(cdnUrl, {
      method: req.method,
      headers: forwardedHeaders,
      // @ts-expect-error Next.js fetch supports duplex for streaming bodies
      duplex: "half",
      cache: "no-store",
      redirect: "follow",
      signal: controller.signal,
    });
  } catch (err) {
    clearTimeout(timer);
    const msg = err instanceof Error ? err.name : String(err);
    console.warn(`[STRM][handleProxy] upstream fetch failed account=${account.name}: ${msg}`);
    return new NextResponse(`Upstream fetch failed: ${msg}`, { status: 502 });
  }

  // 建连成功，清除建连超时（传输阶段靠 req.signal 控制客户端断连）
  clearTimeout(timer);

  const respHeaders = new Headers();

  for (const [k, v] of upstream.headers.entries()) {
    if (HOP_BY_HOP_HEADERS.has(k.toLowerCase())) continue;
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
  const rawMode = searchParams.get("mode")?.toLowerCase();

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

  // P2-16: 读取可配置化的路由策略
  const routeCfg = getStrmRouteConfig();

  // Phase 3.2: 解析直链 + 真实文件元数据
  let meta: DownloadUrlMeta;
  try {
    meta = await resolveDownloadUrl(pickcode, account, userAgent);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error(`[STRM] account=${accountName} failed to get download URL:`, message);
    return NextResponse.json(
      { error: `Failed to get download URL: ${message}` },
      { status: 502 }
    );
  }
  const cdnUrl = meta.url;

  // Phase 1.4: explicit mode 仅在私网生效，防止公网用户绕过 force-proxy UA 保护
  const isPrivate = isPrivateNetworkIp(getClientIp(req));
  const explicitMode = isPrivate ? rawMode : undefined;

  // 规则引擎：决策 proxy vs redirect
  const { decision, reason } = decideRoute(req, explicitMode, routeCfg.forceProxyUaTokens);

  let finalDecision: RouteDecision = decision;
  let finalReason = reason;
  let redirectCheckStatus: number | undefined;

  if (decision === "redirect") {
    const check = await redirectCheck(cdnUrl, account, userAgent, routeCfg.redirectCheckTimeoutMs);
    redirectCheckStatus = check.status;
    if (!check.ok) {
      // 直链不可达 → 降级 proxy，保证用户体验不炸
      finalDecision = "proxy";
      finalReason = `${reason} -> redirect_check_failed(${check.status}) fallback_proxy`;
    }
  }

  // Phase 2.3: 115 单账号并发 proxy 限流（配置化）
  if (finalDecision === "proxy") {
    const current = accountProxyCount.get(accountName) || 0;
    if (current >= routeCfg.accountProxyConcurrencyLimit) {
      finalDecision = "redirect";
      finalReason = `${reason} -> proxy_concurrency_limit(${current}/${routeCfg.accountProxyConcurrencyLimit}) fallback_redirect`;
      console.warn(
        `[STRM] account=${accountName} proxy concurrency hit limit (${current}/${routeCfg.accountProxyConcurrencyLimit}), fallback to redirect`
      );
    }
  }

  const elapsed = Date.now() - t0;
  const shortPc = `${pickcode.slice(0, 4)}…${pickcode.slice(-3)}`;
  const sizeLog = meta.fileSize !== undefined ? ` size=${meta.fileSize}` : "";
  console.log(
    `[STRM] account=${accountName} pickcode=${shortPc} decision=${finalDecision} reason=${finalReason} ` +
      `redirect_check=${redirectCheckStatus ?? "skipped"}${sizeLog} elapsed=${elapsed}ms`
  );

  // proxy 模式：计数 + finally 释放
  if (finalDecision === "proxy") {
    accountProxyCount.set(accountName, (accountProxyCount.get(accountName) || 0) + 1);
    try {
      return await handleProxy(req, cdnUrl, account, userAgent, fileName);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      console.error(`[STRM][proxy] account=${accountName} error:`, message);
      return NextResponse.json(
        { error: `proxy failed: ${message}` },
        { status: 502 }
      );
    } finally {
      const after = (accountProxyCount.get(accountName) || 1) - 1;
      if (after <= 0) accountProxyCount.delete(accountName);
      else accountProxyCount.set(accountName, after);
    }
  }

  try {
    return doRedirect(cdnUrl, fileName);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error(`[STRM][redirect] account=${accountName} error:`, message);
    return NextResponse.json(
      { error: `redirect failed: ${message}` },
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
