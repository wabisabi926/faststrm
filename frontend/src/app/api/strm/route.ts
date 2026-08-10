import { NextRequest, NextResponse } from "next/server";
import { readAccounts, readSettings } from "@/lib/serverUtils";
import { getDownloadUrlWeb, type AccountInfo } from "@/lib/115";

const REDIRECT_CACHE_TTL = 5 * 60 * 1000; // 5 分钟
const redirectCache = new Map<string, { url: string; expires: number }>();

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

async function handleRedirect(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const accountName = searchParams.get("account") || "";
  const pickcode = searchParams.get("pickcode") || "";
  const fileName = searchParams.get("file_name") || "";

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

  const cacheKey = `${pickcode}:${userAgent || ""}`;
  const cached = redirectCache.get(cacheKey);
  const now = Date.now();
  if (cached && cached.expires > now) {
    const location = encodeURI(cached.url);
    const disposition = fileName
      ? buildContentDisposition(decodeURIComponent(fileName))
      : undefined;
    const headers: Record<string, string> = { Location: location };
    if (disposition) headers["Content-Disposition"] = disposition;
    return new NextResponse(null, { status: 302, headers });
  }

  try {
    const url = await getDownloadUrlWeb(pickcode, {
      userAgent,
      accountInfo: account,
    });

    redirectCache.set(cacheKey, {
      url,
      expires: now + REDIRECT_CACHE_TTL,
    });

    const location = encodeURI(url);
    const disposition = fileName
      ? buildContentDisposition(decodeURIComponent(fileName))
      : undefined;
    const headers: Record<string, string> = { Location: location };
    if (disposition) headers["Content-Disposition"] = disposition;
    return new NextResponse(null, { status: 302, headers });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error("[STRM 302] Failed to get download URL:", message);
    return NextResponse.json(
      { error: `Failed to get download URL: ${message}` },
      { status: 502 }
    );
  }
}

export async function GET(req: NextRequest) {
  return handleRedirect(req);
}

export async function HEAD(req: NextRequest) {
  return handleRedirect(req);
}
