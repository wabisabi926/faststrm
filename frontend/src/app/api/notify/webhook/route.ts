// Telegram Bot Webhook 处理
import { NextRequest, NextResponse } from "next/server";
import { createTelegramBot, TelegramUpdate } from "@/lib/telegram";
import { readSettings } from "@/lib/serverUtils";
import { handleMessage, handleCallbackQuery } from "@/lib/telegramCommands";

export async function POST(request: NextRequest) {
  try {
    const settings = readSettings();
    const telegram = settings.telegram;

    if (!telegram?.botToken) {
      return NextResponse.json({ error: "Telegram not configured" }, { status: 400 });
    }

    const bot = createTelegramBot(telegram.botToken);
    const update: TelegramUpdate = await request.json();

    if (update.message) {
      await handleMessage(bot, update.message, "Webhook");
    }

    if (update.callback_query) {
      await handleCallbackQuery(bot, update.callback_query, "Webhook");
    }

    return NextResponse.json({ ok: true });
  } catch (error) {
    console.error("Telegram webhook error:", error);
    return NextResponse.json({
      error: "Internal server error",
      details: error instanceof Error ? error.message : String(error),
    }, { status: 500 });
  }
}
