import { NextRequest, NextResponse } from "next/server";
import { runExecute, ExecuteRequest } from "@/lib/strmCleanup";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  try {
    const body: ExecuteRequest = await req.json();

    if (!body.entries || !Array.isArray(body.entries) || body.entries.length === 0) {
      return NextResponse.json(
        { message: "entries 不能为空", error: "请指定要清理的条目" },
        { status: 400 }
      );
    }

    for (const entry of body.entries) {
      if (!entry.localPath || !Array.isArray(entry.staleRelPaths)) {
        return NextResponse.json(
          { message: "entries 格式错误", error: "每项需包含 localPath 和 staleRelPaths 数组" },
          { status: 400 }
        );
      }
    }

    const totalRequested = body.entries.reduce((s, e) => s + e.staleRelPaths.length, 0);
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
