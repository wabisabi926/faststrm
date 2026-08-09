import { NextRequest, NextResponse } from "next/server";
import {
  MappingScanRequest,
  runScan,
  getDefaultScanRequestsFromSettings,
} from "@/lib/strmCleanup";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  try {
    let body: { mappings?: MappingScanRequest[]; useSettingsDefaults?: boolean } = {};
    try {
      body = await req.json();
    } catch {
      // 允许无 body，自动使用 settings 默认值
    }

    let mappings: MappingScanRequest[] = [];
    if (body.useSettingsDefaults || !body.mappings || body.mappings.length === 0) {
      mappings = getDefaultScanRequestsFromSettings();
      if (mappings.length === 0) {
        return NextResponse.json(
          {
            message: "未找到扫描配置",
            error: "请在设置中先添加 115 生活事件监控的路径映射",
          },
          { status: 400 }
        );
      }
    } else {
      mappings = body.mappings.filter(
        (m) => m?.account && m?.cloudPath && m?.localPath
      );
      if (mappings.length === 0) {
        return NextResponse.json(
          { message: "mappings 参数无效", error: "每个映射需包含 account、cloudPath、localPath" },
          { status: 400 }
        );
      }
    }

    const result = await runScan(mappings);
    return NextResponse.json({ ...result, mappingsScanned: mappings });
  } catch (err: unknown) {
    const errorMessage = err instanceof Error ? err.message : String(err);
    const status = errorMessage.includes("405") || errorMessage.includes("封控") ? 403 : 500;
    return NextResponse.json(
      { message: "扫描失败", error: errorMessage },
      { status }
    );
  }
}

export async function GET() {
  try {
    const defaults = getDefaultScanRequestsFromSettings();
    return NextResponse.json({ defaults });
  } catch (err: unknown) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : String(err) },
      { status: 500 }
    );
  }
}
