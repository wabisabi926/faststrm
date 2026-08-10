import { NextRequest, NextResponse } from "next/server";
import {
  MappingScanRequest,
  runScan,
  getDefaultScanRequestsFromSettings,
} from "@/lib/strmCleanup";
import { clearMonitorSuspend } from "@/lib/accountRuntimeState";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  let involvedAccounts: Set<string> | null = null;
  try {
    let body: { mappings?: MappingScanRequest[]; useSettingsDefaults?: boolean; action?: string } = {};
    try {
      body = await req.json();
    } catch {
      // 允许无 body，自动使用 settings 默认值
    }

    const action = body.action || "scan";

    // P4.2: 全量对账模式 — 三方对账（cloud + local + DB sync）
    if (action === "reconcile") {
      let reconcileMappings: MappingScanRequest[] = [];
      if (body.useSettingsDefaults || !body.mappings || body.mappings.length === 0) {
        reconcileMappings = getDefaultScanRequestsFromSettings();
        if (reconcileMappings.length === 0) {
          return NextResponse.json(
            {
              message: "未找到扫描配置",
              error: "请在设置中先添加 115 生活事件监控的路径映射",
            },
            { status: 400 }
          );
        }
      } else {
        reconcileMappings = body.mappings.filter(
          (m) => m?.account && m?.cloudPath && m?.localPath
        );
        if (reconcileMappings.length === 0) {
          return NextResponse.json(
            { message: "mappings 参数无效", error: "每个映射需包含 account、cloudPath、localPath" },
            { status: 400 }
          );
        }
      }
      involvedAccounts = new Set(reconcileMappings.map((m) => m.account));

      const { runReconcile } = await import("@/lib/strmCleanup");
      const result = await runReconcile(reconcileMappings);
      return NextResponse.json({ ...result, mappingsScanned: reconcileMappings });
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
    involvedAccounts = new Set(mappings.map((m) => m.account));

    const result = await runScan(mappings);
    return NextResponse.json({ ...result, mappingsScanned: mappings });
  } catch (err: unknown) {
    const errorMessage = err instanceof Error ? err.message : String(err);
    const status = errorMessage.includes("405") || errorMessage.includes("封控") ? 403 : 500;
    return NextResponse.json(
      { message: "扫描失败", error: errorMessage },
      { status }
    );
  } finally {
    // 路由层兜底：如果 strmCleanup 内部抛错导致其 finally 没执行到（理论上不会），
    // 此处强制恢复所有涉及账号的监控挂起状态，杜绝"对账后监控永久挂起"
    if (involvedAccounts && involvedAccounts.size > 0) {
      for (const account of involvedAccounts) {
        clearMonitorSuspend(account);
      }
    }
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
