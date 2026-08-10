// Telegram 发送消息 API
import { NextRequest, NextResponse } from "next/server";
import { createTelegramBot, formatTaskStatusMessage, formatDownloadCompleteMessage } from "@/lib/telegram";
import { readSettings } from "@/lib/serverUtils";

export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const { message, type, data } = body;

    const settings = readSettings();
    const telegram = settings.telegram;
    
    if (!telegram || !telegram.botToken || !telegram.chatId) {
      return NextResponse.json({ 
        error: "Telegram not configured (missing botToken or chatId)" 
      }, { status: 400 });
    }

    if (telegram.enabled === false) {
      return NextResponse.json({ 
        error: "Telegram notifications are disabled" 
      }, { status: 400 });
    }

    const bot = createTelegramBot(telegram.botToken);

    let messageText = message;

    if (type === 'task_status' && data) {
      messageText = formatTaskStatusMessage(data);
    } else if (type === 'download_complete' && data) {
      messageText = formatDownloadCompleteMessage(data);
    } else if (type === 'error' && data) {
      messageText = `❌ <b>Error</b>\n\n${data.message || data}\n\n<b>Time:</b> ${new Date().toLocaleString()}`;
    } else if (type === 'info' && data) {
      messageText = `ℹ️ <b>Info</b>\n\n${data.message || data}\n\n<b>Time:</b> ${new Date().toLocaleString()}`;
    }

    const result = await bot.sendNotification(messageText, telegram.chatId);

    return NextResponse.json({ 
      success: true, 
      messageId: (result.result as { message_id?: number })?.message_id,
      result 
    });
  } catch (error) {
    console.error("Telegram send error:", error);
    return NextResponse.json({ 
      error: "Failed to send Telegram message", 
      details: error instanceof Error ? error.message : String(error) 
    }, { status: 500 });
  }
}
