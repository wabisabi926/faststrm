// Emby 通知/同步设置：局部 patch 保存（和 /api/notify/bot 对称设计）
import { NextRequest, NextResponse } from "next/server";
import { readSettings, writeSettings } from "@/lib/serverUtils";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

type EmbySettings = NonNullable<NonNullable<ReturnType<typeof readSettings>>["emby"]>;

export async function POST(req: NextRequest) {
  try {
    const body = (await req.json()) as Partial<EmbySettings>;
    if (!body || typeof body !== "object") {
      return NextResponse.json(
        { error: "请求体格式错误", details: "body 必须是 JSON 对象" },
        { status: 400 },
      );
    }

    // 严格只允许 emby 对象内部字段白名单，拒绝越权写 settings 的其他字段
    const ALLOWED_KEYS = new Set<keyof EmbySettings>([
      "url",
      "apiKey",
      "notifyMediaAdded",
      "notifyMediaRemoved",
      "notifyPlayback",
      "playbackShowProgress",
      "playbackShowOverview",
      "webhookAuth",
      "libraryId",
      "syncDeleteEnabled",
      "syncDeletePathMappings",
      "syncDeleteNotify",
      "syncDeleteDryRun",
    ]);

    const patch: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(body)) {
      if (ALLOWED_KEYS.has(k as keyof EmbySettings)) patch[k] = v;
    }

    const existing = readSettings();
    const mergedEmby = {
      ...(existing.emby ?? {}),
      ...patch,
    } as EmbySettings;

    // 对 syncDeletePathMappings 做防御式类型清洗
    if (!Array.isArray(mergedEmby.syncDeletePathMappings)) {
      mergedEmby.syncDeletePathMappings = [];
    }
    mergedEmby.syncDeletePathMappings = mergedEmby.syncDeletePathMappings
      .filter((m) => m && typeof m === "object")
      .map((m) => ({
        embyPath: String((m as { embyPath?: string }).embyPath ?? ""),
        cloudPath: String((m as { cloudPath?: string }).cloudPath ?? ""),
        account:
          (m as { account?: string }).account === "" ||
          (m as { account?: string }).account == null
            ? undefined
            : String((m as { account?: string }).account),
      }))
      .filter((m) => m.embyPath && m.cloudPath);

    const next = {
      ...existing,
      emby: mergedEmby,
    };

    writeSettings(next);

    return NextResponse.json({
      success: true,
      message: "Emby 通知设置已保存",
      saved: {
        ...mergedEmby,
        apiKey: mergedEmby.apiKey ? maskApiKey(mergedEmby.apiKey) : undefined,
      },
    });
  } catch (error) {
    console.error("[emby/settings] save failed:", error);
    return NextResponse.json(
      {
        error: "保存失败",
        details: error instanceof Error ? error.message : String(error),
      },
      { status: 500 },
    );
  }
}

function maskApiKey(key: string): string {
  if (!key) return "";
  if (key.length <= 8) return "***";
  return `${key.slice(0, 4)}${"*".repeat(Math.max(key.length - 8, 4))}${key.slice(-4)}`;
}
