import { NextRequest, NextResponse } from "next/server";
import {
  getLifeMonitorConfig,
  saveLifeMonitorConfig,
  getMonitorStatus,
  startMonitor,
  stopMonitor,
  startAllMonitors,
  stopAllMonitors,
  verifyAccount,
  LifeMonitorConfig,
} from "@/lib/eventMonitor";
import { readAccounts } from "@/lib/serverUtils";

// GET: 获取监控状态和配置
export async function GET() {
  try {
    const { config, states } = getMonitorStatus();
    return NextResponse.json({
      config,
      states,
      accounts: readAccounts().map((acc: { name: string }) => acc.name),
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return NextResponse.json({ error: message }, { status: 500 });
  }
}

// POST: 执行监控操作
export async function POST(req: NextRequest) {
  try {
    const body = await req.json();
    const action = body.action as string;

    if (!action) {
      return NextResponse.json(
        { error: "action is required" },
        { status: 400 }
      );
    }

    switch (action) {
      case "start": {
        const account = body.account as string;
        if (!account) {
          return NextResponse.json(
            { error: "account is required" },
            { status: 400 }
          );
        }
        const success = startMonitor(account);
        return NextResponse.json({
          success,
          message: success ? `监控已启动: ${account}` : `启动失败: ${account}`,
        });
      }

      case "stop": {
        const account = body.account as string;
        if (!account) {
          return NextResponse.json(
            { error: "account is required" },
            { status: 400 }
          );
        }
        stopMonitor(account);
        return NextResponse.json({
          success: true,
          message: `监控已停止: ${account}`,
        });
      }

      case "startAll": {
        const started = startAllMonitors();
        return NextResponse.json({
          success: true,
          started,
          message:
            started.length > 0
              ? `已启动 ${started.length} 个账号的监控`
              : "没有可启动的账号",
        });
      }

      case "stopAll": {
        stopAllMonitors();
        return NextResponse.json({
          success: true,
          message: "所有监控已停止",
        });
      }

      case "saveConfig": {
        const newConfig = body.config as LifeMonitorConfig;
        if (!newConfig) {
          return NextResponse.json(
            { error: "config is required" },
            { status: 400 }
          );
        }
        saveLifeMonitorConfig(newConfig);
        return NextResponse.json({
          success: true,
          message: "配置已保存",
        });
      }

      case "verify": {
        const account = body.account as string;
        if (!account) {
          return NextResponse.json(
            { error: "account is required" },
            { status: 400 }
          );
        }
        const result = await verifyAccount(account);
        return NextResponse.json(result);
      }

      case "updateConfig": {
        const updates = body.updates as Partial<LifeMonitorConfig>;
        if (!updates) {
          return NextResponse.json(
            { error: "updates is required" },
            { status: 400 }
          );
        }
        const config = getLifeMonitorConfig();
        const newConfig: LifeMonitorConfig = { ...config, ...updates };
        
        // 如果监控正在运行且配置变更，需要重启监控
        // 先停止所有运行的监控
        stopAllMonitors();

        // 保存新配置
        saveLifeMonitorConfig(newConfig);

        // 如果新配置启用，自动重新启动
        if (newConfig.enabled && newConfig.accounts.length > 0) {
          const started = startAllMonitors();
          return NextResponse.json({
            success: true,
            message: "配置已更新，监控已重启",
            started,
          });
        }

        return NextResponse.json({
          success: true,
          message: "配置已更新",
        });
      }

      default:
        return NextResponse.json(
          { error: `Unknown action: ${action}` },
          { status: 400 }
        );
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return NextResponse.json({ error: message }, { status: 500 });
  }
}