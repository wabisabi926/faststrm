// Telegram Bot 管理 API
import { NextRequest, NextResponse } from "next/server";
import { createTelegramBot } from "@/lib/telegram";
import { readSettings, writeSettings } from "@/lib/serverUtils";

function maskToken(token: string): string {
  if (!token) return '';
  if (token.length <= 8) return '***';
  const separator = token.indexOf(':');
  if (separator === -1) return '***';
  const idPart = token.slice(0, separator);
  const secretPart = token.slice(separator + 1);
  return `${idPart}:${'*'.repeat(Math.max(secretPart.length - 4, 0))}${secretPart.slice(-4)}`;
}

// 3 秒超时兜底：避免 Telegram 网络不通时阻塞整个页面
const withTimeout = <T>(promise: Promise<T>, ms: number, fallback: T): Promise<T> =>
  Promise.race([
    promise,
    new Promise<T>((resolve) => setTimeout(() => resolve(fallback), ms)),
  ]);

// 获取机器人信息
export async function GET() {
  try {
    const settings = readSettings();
    const telegram = settings.telegram;

    if (!telegram || !telegram.botToken) {
      return NextResponse.json({ error: "Telegram not configured" }, { status: 400 });
    }

    // 本地配置立刻可用，不需要等 Telegram 返回
    const baseResponse = {
      configured: true,
      chatId: telegram.chatId || '',
      enabled: telegram.enabled ?? true,
      botToken: maskToken(telegram.botToken || ''),
      webhookUrl: telegram.webhookUrl || '',
    };

    // 并行 + 3 秒兜底请求 Telegram 实时信息
    // 失败/超时不影响基础配置返回，只是右侧机器人状态卡信息会缺失
    const bot = createTelegramBot(telegram.botToken);
    const [botInfoResult, webhookInfoResult] = await Promise.allSettled([
      withTimeout(bot.getMe(), 3000, { ok: false as const }),
      withTimeout(bot.getWebhookInfo(), 3000, { ok: false as const }),
    ]);

    const botInfo =
      botInfoResult.status === "fulfilled" && botInfoResult.value?.ok
        ? botInfoResult.value
        : undefined;
    const webhookInfo =
      webhookInfoResult.status === "fulfilled" && webhookInfoResult.value?.ok
        ? webhookInfoResult.value
        : undefined;

    return NextResponse.json({
      ...baseResponse,
      bot: botInfo,
      webhook: webhookInfo,
    });
  } catch (error) {
    console.error("Telegram bot info error:", error);
    return NextResponse.json(
      {
        error: "Failed to get bot info",
        details: error instanceof Error ? error.message : String(error),
      },
      { status: 500 },
    );
  }
}

// 配置机器人
export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const { botToken, chatId, webhookUrl, enabled } = body;

    if (!botToken) {
      return NextResponse.json({ error: "Bot token is required" }, { status: 400 });
    }

    // 验证 Bot Token 格式
    const tokenPattern = /^\d+:[A-Za-z0-9_-]{35}$/;
    if (!tokenPattern.test(botToken)) {
      return NextResponse.json({ 
        error: "Invalid bot token format", 
        details: "Bot token should be in format: 123456789:ABCdefGHIjklMNOpqrsTUVwxyz" 
      }, { status: 400 });
    }

    // 验证机器人 token
    const bot = createTelegramBot(botToken);
    let botInfo;
    
    try {
      botInfo = await bot.getMe();
      
      if (!botInfo.ok) {
        return NextResponse.json({ 
          error: "Invalid bot token", 
          details: botInfo.description || "Bot token validation failed" 
        }, { status: 400 });
      }
    } catch (error: unknown) {
      console.error("Bot token validation error:", error);
      
      const axiosError = error as { response?: { status?: number }; message?: string };
      if (axiosError.response?.status === 404) {
        return NextResponse.json({ 
          error: "Invalid bot token", 
          details: "Bot token not found. Please check your token format and ensure it's correct." 
        }, { status: 400 });
      } else if (axiosError.response?.status === 401) {
        return NextResponse.json({ 
          error: "Unauthorized", 
          details: "Bot token is invalid or expired." 
        }, { status: 400 });
      } else {
        return NextResponse.json({ 
          error: "Bot validation failed", 
          details: axiosError.message || "Unknown error occurred" 
        }, { status: 400 });
      }
    }

    // 读取当前设置
    const settings = readSettings();

    // 更新 Telegram 配置（保留 accountAlerts 子配置）
    settings.telegram = {
      botToken,
      chatId: chatId || settings.telegram?.chatId,
      webhookUrl: webhookUrl || settings.telegram?.webhookUrl,
      enabled: enabled !== undefined ? enabled : (settings.telegram?.enabled ?? true),
      accountAlerts: settings.telegram?.accountAlerts,  // 保留账户状态通知配置
    };

    // 保存设置
    writeSettings(settings);

    // 设置 webhook（如果提供了 URL）
    if (webhookUrl) {
      try {
        await bot.setWebhook(webhookUrl);
      } catch (webhookError) {
        console.error("Failed to set webhook:", webhookError);
        // 不返回错误，因为配置本身是成功的
      }
    }

    return NextResponse.json({
      success: true,
      bot: botInfo,
      chatId: chatId || '',
      enabled: settings.telegram?.enabled ?? true,
      message: "Telegram bot configured successfully"
    });
  } catch (error) {
    console.error("Telegram bot config error:", error);
    return NextResponse.json({ 
      error: "Failed to configure bot", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}

// 删除机器人配置
export async function DELETE() {
  try {
    // 读取当前设置
    const settings = readSettings();

    // 如果有 webhook，先删除
    if (settings.telegram?.botToken) {
      try {
        const bot = createTelegramBot(settings.telegram.botToken);
        await bot.deleteWebhook();
      } catch (error) {
        console.error("Failed to delete webhook:", error);
      }
    }

    // 删除 Telegram 配置
    delete settings.telegram;

    // 保存设置
    writeSettings(settings);

    return NextResponse.json({
      success: true,
      message: "Telegram bot configuration removed"
    });
  } catch (error) {
    console.error("Telegram bot delete error:", error);
    return NextResponse.json({ 
      error: "Failed to remove bot configuration", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}
