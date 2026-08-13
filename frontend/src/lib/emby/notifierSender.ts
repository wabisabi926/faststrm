import type { EmbyItemDetail } from "./types";
import { buildImageUrl } from "./client";
import { createTelegramBot, TelegramBot } from "../telegram";
import { readSettings } from "../serverUtils";
import axios from "axios";
import fs from "node:fs";
import { logger } from "@/lib/logger";

// ========== 下载 Emby 海报到本地临时文件（移植自 qmediasync helpers.DownloadFile） ==========
async function downloadPosterToTemp(imageUrl: string, tempPath: string, userAgent: string): Promise<boolean> {
  try {
    const resp = await axios.get(imageUrl, {
      responseType: "arraybuffer",
      timeout: 15_000,
      headers: { "User-Agent": userAgent },
    });
    if (resp.status !== 200 || !resp.data) return false;
    fs.writeFileSync(tempPath, Buffer.from(resp.data));
    return true;
  } catch (err) {
    logger.error("[Emby] 下载海报失败:", err);
    return false;
  }
}

// ========== 核心发送函数（完全裸发，不经过 sendTelegramNotification 二次包装） ==========

/** 检查 Telegram 是否配置完整，否则直接 return */
function getTgBotAndChat(): { bot: TelegramBot; chatId: string } | null {
  const s = readSettings();
  const tg = s.telegram;
  if (!tg?.enabled) {
    logger.warn("[Emby] Telegram 通知未启用");
    return null;
  }
  if (!tg.botToken) {
    logger.warn("[Emby] Telegram botToken 未配置");
    return null;
  }
  if (!tg.chatId) {
    logger.warn("[Emby] Telegram chatId 未配置");
    return null;
  }
  return { bot: createTelegramBot(tg.botToken), chatId: tg.chatId };
}

/** 裸发纯文本 Emby 通知（不加 Task Completed 等前缀） */
async function sendEmbyText(text: string): Promise<void> {
  const ctx = getTgBotAndChat();
  if (!ctx) return;
  try {
    const result = await ctx.bot.sendNotification(text, ctx.chatId);
    if (result?.ok) {
      logger.info(`[Emby] 文本通知发送成功 -> chatId=${ctx.chatId}`);
    } else {
      logger.warn(`[Emby] 文本通知发送失败: ${result?.error || result?.description || "未知错误"}`);
    }
  } catch (err) {
    logger.error("[Emby] 文本通知发送异常:", err);
  }
}

/** 带图片通知：先下载 Emby 海报到本地临时文件，再 multipart 上传 TG，失败降级纯文本 */
async function sendEmbyWithPoster(
  itemId: string,
  imageTags: EmbyItemDetail["ImageTags"],
  text: string,
  config: { url: string; apiKey: string; userAgent?: string }
): Promise<void> {
  const ctx = getTgBotAndChat();
  if (!ctx) return;
  if (!config.url || !config.apiKey) {
    // 拿不到 Emby URL 直接纯文字
    await sendEmbyText(text);
    return;
  }

  // 1) 构造图片 URL，优先 Backdrop（参考 qmediasync）
  let imageUrl: string | null = null;
  const bd = imageTags?.Backdrop || (imageTags as unknown as { backdrop?: string })?.backdrop;
  const primary = imageTags?.Primary || (imageTags as unknown as { Primary?: string })?.Primary;
  if (bd) {
    imageUrl = buildImageUrl(itemId, bd, "Backdrop", config);
  } else if (primary) {
    imageUrl = buildImageUrl(itemId, primary, "Primary", config);
  }

  if (!imageUrl) {
    await sendEmbyText(text);
    return;
  }

  // 2) 下载到临时文件
  const tempPath = TelegramBot.makeTempPosterPath(itemId);
  const ua = config.userAgent || "FastStrm/1.0";
  const ok = await downloadPosterToTemp(imageUrl, tempPath, ua);

  if (!ok) {
    await sendEmbyText(text);
    return;
  }

  // 3) multipart 上传
  try {
    const r = await ctx.bot.sendPhotoFromFile(ctx.chatId, tempPath, text);
    if (!r?.ok) {
      logger.warn("[Emby] 图片通知失败，降级纯文本:", r?.error);
      await sendEmbyText(text);
    }
  } catch (err) {
    logger.error("[Emby] sendPhotoFromFile 异常，降级纯文本:", err);
    await sendEmbyText(text);
  } finally {
    try { fs.unlinkSync(tempPath); } catch { /* 忽略 */ }
  }
}

export {
  downloadPosterToTemp,
  getTgBotAndChat,
  sendEmbyText,
  sendEmbyWithPoster,
};