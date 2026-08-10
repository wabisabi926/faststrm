import { NextRequest, NextResponse } from "next/server";
import { runExecute, ExecuteRequest } from "@/lib/strmCleanup";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  try {
    const body: ExecuteRequest = await req.json();
    const action = body.action || "delete";

    // delete_all / delete_and_regenerate / regenerate 允许 entries 为空（内部会收集）
    if (action === "delete") {
      if (!body.entries || !Array.isArray(body.entries) || body.entries.length === 0) {
        return NextResponse.json(
          { message: "entries 不能为空", error: "请指定要清理的条目" },
          { status: 400 }
        );
      }
    }

    if (action === "delete" || action === "delete_all") {
      for (const entry of body.entries || []) {
        if (!entry.localPath || !Array.isArray(entry.staleRelPaths)) {
          return NextResponse.json(
            { message: "entries 格式错误", error: "每项需包含 localPath 和 staleRelPaths 数组" },
            { status: 400 }
          );
        }
      }
    }

    // delete_all 需要 scanSummary
    if (action === "delete_all" && !body.scanSummary) {
      return NextResponse.json(
        { message: "delete_all 需要 scanSummary", error: "请提供扫描结果以收集全量失效 STRM" },
        { status: 400 }
      );
    }

    // regenerate / delete_and_regenerate 需要 missingItems
    if ((action === "regenerate" || action === "delete_and_regenerate") &&
        (!body.missingItems || body.missingItems.length === 0)) {
      return NextResponse.json(
        { message: "missingItems 不能为空", error: "请指定要补生成的 STRM 条目" },
        { status: 400 }
      );
    }

    let totalRequested = (body.entries || []).reduce((s, e) => s + (e.staleRelPaths?.length || 0), 0);

    // P3.2f: delete_all / delete_and_regenerate: 也统计 scanSummary 中的 stale 数量（避免 entries 为空时绕过阈值）
    const actionType = body.action || "delete";
    if ((actionType === "delete_all" || actionType === "delete_and_regenerate") && body.scanSummary) {
      for (const m of body.scanSummary.mappings) {
        totalRequested += (m.staleStrms?.length || 0);
      }
    }

    if (totalRequested > 2000 && !body.dryRun) {
      return NextResponse.json(
        {
          message: `单次删除文件数过多 (${totalRequested})`,
          error: "单次最多允许删除 2000 个文件，建议先 dryRun 或分批执行",
        },
        { status: 413 }
      );
    }

    const result = runExecute(body);
    return NextResponse.json(result);
  } catch (err: unknown) {
    const errorMessage = err instanceof Error ? err.message : String(err);
    return NextResponse.json(
      { message: "执行失败", error: errorMessage },
      { status: 500 }
    );
  }
}
