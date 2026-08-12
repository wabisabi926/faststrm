// 账户状态通知配置 API
import { NextRequest, NextResponse } from "next/server";
import { readSettings, writeSettings } from "@/lib/serverUtils";

// 获取账户状态通知配置
export async function GET() {
  try {
    const settings = readSettings();
    const alerts = settings.telegram?.accountAlerts || {};

    return NextResponse.json({
      enabled: alerts.enabled ?? false,
      onError: alerts.onError ?? true,
      onRecover: alerts.onRecover ?? true,
    });
  } catch (error) {
    console.error("Get account alerts config error:", error);
    return NextResponse.json(
      { error: "Failed to get account alerts config" },
      { status: 500 }
    );
  }
}

// 保存账户状态通知配置
export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const { enabled, onError, onRecover } = body;

    const settings = readSettings();

    // 确保 telegram 对象存在
    if (!settings.telegram) {
      settings.telegram = {};
    }

    // 更新 accountAlerts 配置
    settings.telegram.accountAlerts = {
      enabled: enabled !== undefined ? enabled : false,
      onError: onError !== undefined ? onError : true,
      onRecover: onRecover !== undefined ? onRecover : true,
    };

    writeSettings(settings);

    return NextResponse.json({
      success: true,
      message: "账户状态通知配置保存成功",
      config: settings.telegram.accountAlerts,
    });
  } catch (error) {
    console.error("Save account alerts config error:", error);
    return NextResponse.json(
      {
        error: "Failed to save account alerts config",
        details: error instanceof Error ? error.message : String(error),
      },
      { status: 500 }
    );
  }
}
