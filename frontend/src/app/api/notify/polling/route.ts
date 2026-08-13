// Telegram 轮询 API
import { NextResponse } from "next/server";
import { createTelegramBot } from "@/lib/telegram";
import { readSettings } from "@/lib/serverUtils";
import { stopPolling, getPollingStatus, forceCleanup, safeStartPolling } from "@/lib/telegramPolling";
import { logger } from "@/lib/logger";

// 启动轮询
export async function POST() {
  try {
    // 读取设置
    const settings = readSettings();
    const telegram = settings.telegram;
    
    // 检查 Telegram 配置是否完整
    if (!telegram || !telegram.botToken) {
      logger.info("Telegram not configured (missing botToken), cannot start polling");
      return NextResponse.json({ error: "Telegram 未配置" }, { status: 400 });
    }

    // 创建机器人实例
    const bot = createTelegramBot(telegram.botToken);

    // 删除现有的 webhook（如果存在）
    try {
      await bot.deleteWebhook();
      logger.info("Deleted existing webhook for polling mode");
    } catch (error) {
      logger.info("No webhook to delete or error deleting webhook:", error);
    }

    // 使用安全启动轮询
    await safeStartPolling();

    return NextResponse.json({ 
      success: true, 
      message: "轮询已启动" 
    });
  } catch (error) {
    logger.error("Telegram polling start error:", error);
    return NextResponse.json({ 
      error: "启动轮询失败", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}

// 停止轮询
export async function DELETE() {
  try {
    // 读取设置
    const settings = readSettings();
    const telegram = settings.telegram;
    
    // 检查 Telegram 配置是否完整
    if (!telegram || !telegram.botToken) {
      logger.info("Telegram not configured (missing botToken), cannot stop polling");
      return NextResponse.json({ error: "Telegram 未配置" }, { status: 400 });
    }

    // 停止轮询
    stopPolling();

    // 如果有 webhook URL，设置 webhook
    if (telegram.webhookUrl) {
      const bot = createTelegramBot(telegram.botToken);
      await bot.setWebhook(telegram.webhookUrl);
      logger.info("Stopped polling, webhook enabled");
    }

    return NextResponse.json({ 
      success: true, 
      message: "轮询已停止" 
    });
  } catch (error) {
    logger.error("Telegram polling stop error:", error);
    return NextResponse.json({ 
      error: "停止轮询失败", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}

// 轮询状态
export async function GET() {
  try {
    // 读取设置
    const settings = readSettings();
    const telegram = settings.telegram;
    
    // 检查 Telegram 配置是否完整
    if (!telegram || !telegram.botToken) {
      logger.info("Telegram not configured (missing botToken), cannot get polling status");
      return NextResponse.json({ error: "Telegram 未配置" }, { status: 400 });
    }

    // 获取轮询状态
    const pollingStatus = getPollingStatus();
    
    // 创建机器人实例获取 webhook 信息
    const bot = createTelegramBot(telegram.botToken);
    const webhookInfo = await bot.getWebhookInfo();

    return NextResponse.json({ 
      polling: pollingStatus.active,
      webhook: webhookInfo.result,
      message: pollingStatus.message
    });
  } catch (error) {
    logger.error("Telegram polling status error:", error);
    return NextResponse.json({ 
      error: "获取轮询状态失败", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}

// 强制清理 webhook 和轮询状态
export async function PUT() {
  try {
    const success = await forceCleanup();
    
    if (success) {
      return NextResponse.json({ 
        success: true, 
        message: "强制清理完成，轮询将自动重启" 
      });
    } else {
      return NextResponse.json({ 
        error: "强制清理失败" 
      }, { status: 500 });
    }
  } catch (error) {
    logger.error("Telegram force cleanup error:", error);
    return NextResponse.json({ 
      error: "强制清理失败", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}

