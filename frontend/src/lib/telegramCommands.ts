import { createTelegramBot } from "./telegram";
import { readSettings, isTelegramUserAllowed, readAccounts } from "./serverUtils";
import { getMonitorStatus } from "./eventMonitor";
import { logger } from "@/lib/logger";
import {
  runScan,
  runReconcile,
  getDefaultScanRequestsFromSettings,
} from "./strmCleanup";

export const BOT_COMMANDS = [
  { command: "status", description: "📊 系统状态" },
  { command: "scan", description: "🔍 全量对账" },
  { command: "cleanup", description: "🧹 清理孤儿" },
  { command: "accounts", description: "👥 账号列表" },
  { command: "help", description: "❓ 帮助" },
];

export const BASE_URL = process.env.NEXTAUTH_URL || process.env.NEXT_PUBLIC_APP_URL || `http://localhost:${process.env.PORT || 3000}`;

type Bot = ReturnType<typeof createTelegramBot>;

interface MessageLike {
  chat: { id: number };
  text?: string;
  from: { username?: string; first_name: string; id: number };
}

interface CallbackQueryLike {
  message?: { chat: { id: number }; message_id: number };
  data?: string;
  id: string;
}

async function handleStart(bot: Bot, chatId: string, username: string) {
  const text = `👋 <b>你好 ${username}！</b>\n\nFastStrm Bot 让你在 Telegram 上管理网盘同步。\n\n请选择操作：`;

  const buttons = [
    [
      { text: "🔍 全量对账", callback_data: "scan" },
      { text: "🧹 清理孤儿", callback_data: "cleanup" },
    ],
    [
      { text: "📊 系统状态", callback_data: "status" },
      { text: "👥 账号列表", callback_data: "accounts" },
    ],
  ];

  await bot.sendMessageWithButtons(chatId, text, buttons);
}

async function handleStatus(bot: Bot, chatId: string) {
  try {
    const { config, states } = getMonitorStatus();
    const accounts = readAccounts() as Array<{ name: string; cookie?: string }>;

    let message = `<b>📊 系统状态</b>\n\n`;

    message += `<b>🔔 生活监控</b>\n`;
    message += `• 启用: ${config.enabled ? "✅" : "❌"}\n`;
    message += `• 账号数: ${states.length}\n\n`;

    for (const state of states) {
      const status = state.running ? "🔄 运行中" : "⏸️ 已停止";
      message += `  ${status} <b>${state.account}</b>\n`;
      message += `    已处理事件: ${state.eventsProcessed}\n`;
      if (state.lastCheckTime) {
        const t = new Date(state.lastCheckTime).toLocaleString();
        message += `    上次检查: ${t}\n`;
      }
      message += "\n";
    }

    message += `<b>👥 账号状态</b>\n`;
    for (const account of accounts) {
      const hasCookie = account.cookie && account.cookie.length > 0;
      message += `  ${hasCookie ? "✅" : "⚠️"} <b>${account.name}</b> — Cookie: ${hasCookie ? "有效" : "未设置"}\n`;
    }

    message += `\n<b>⏰ 时间:</b> ${new Date().toLocaleString()}`;

    await bot.sendMessage({ chat_id: chatId, text: message, parse_mode: "HTML" });
  } catch (error) {
    logger.error("Error handling status:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 获取状态失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleScan(bot: Bot, chatId: string) {
  await bot.sendMessage({
    chat_id: chatId,
    text: `🔍 <b>正在执行全量对账...</b>\n\n这可能需要几分钟时间，请稍候。`,
    parse_mode: "HTML",
  });

  try {
    const mappings = getDefaultScanRequestsFromSettings();
    if (mappings.length === 0) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ <b>未找到扫描配置</b>\n\n请在 Web UI 的设置中先添加 115 生活事件监控的路径映射。`,
        parse_mode: "HTML",
      });
      return;
    }

    const result = await runReconcile(mappings);

    const totalRemote = result.results.reduce((s, r) => s + r.cloudFileCount, 0);
    const totalLocal = result.results.reduce((s, r) => s + r.localStrmCount, 0);
    const totalStale = result.results.reduce((s, r) => s + r.staleStrms.length, 0);
    const totalMissing = result.results.reduce((s, r) => s + r.missingStrms.length, 0);

    let text = `✅ <b>全量对账完成</b>\n\n`;
    text += `• 扫描映射: ${result.results.length}\n`;
    text += `• 远程文件: ${totalRemote}\n`;
    text += `• 本地 STRM: ${totalLocal}\n`;
    text += `• 孤儿 STRM: ${totalStale}\n`;
    text += `• 缺失 STRM: ${totalMissing}\n`;
    text += `• 耗时: ${(result.totalDurationMs / 1000).toFixed(1)}s\n`;
    text += `\n⏰ ${new Date().toLocaleString()}`;

    await bot.sendMessage({ chat_id: chatId, text, parse_mode: "HTML" });
  } catch (error) {
    logger.error("Error during reconcile:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 全量对账失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleCleanup(bot: Bot, chatId: string) {
  await bot.sendMessage({
    chat_id: chatId,
    text: `🧹 <b>正在扫描孤儿 STRM...</b>\n\n这可能需要一些时间。`,
    parse_mode: "HTML",
  });

  try {
    const mappings = getDefaultScanRequestsFromSettings();
    if (mappings.length === 0) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ <b>未找到扫描配置</b>\n\n请在 Web UI 的设置中先添加 115 生活事件监控的路径映射。`,
        parse_mode: "HTML",
      });
      return;
    }

    const result = await runScan(mappings);

    const stale = result.totalStale;
    const missing = result.totalMissing;

    let text = `📋 <b>孤儿扫描完成</b>\n\n`;
    text += `• 孤儿 STRM: ${stale}\n`;
    text += `• 缺失 STRM: ${missing}\n`;

    if (stale > 0) {
      text += `\n⚠️ 发现 ${stale} 个孤儿 STRM，请在 Web UI 确认后执行清理。`;
    } else {
      text += `\n✅ 没有发现孤儿 STRM。`;
    }

    await bot.sendMessage({ chat_id: chatId, text, parse_mode: "HTML" });
  } catch (error) {
    logger.error("Error during scan:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 孤儿扫描失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleAccounts(bot: Bot, chatId: string) {
  try {
    const accounts = readAccounts() as Array<{ name: string; cookie?: string }>;

    if (accounts.length === 0) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `👥 <b>账号列表</b>\n\n暂无账号，请在 Web UI 添加。`,
        parse_mode: "HTML",
      });
      return;
    }

    let message = `<b>👥 账号列表 (${accounts.length})</b>\n\n`;
    for (const account of accounts) {
      const hasCookie = account.cookie && account.cookie.length > 0;
      message += `${hasCookie ? "✅" : "⚠️"} <b>${account.name}</b>\n`;
      message += `   Cookie: ${hasCookie ? "已设置" : "未设置"}\n\n`;
    }

    message += `💡 Cookie 过期时可在 Web UI 账号管理页扫码刷新。`;

    await bot.sendMessage({ chat_id: chatId, text: message, parse_mode: "HTML" });
  } catch (error) {
    logger.error("Error handling accounts:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 获取账号列表失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleHelp(bot: Bot, chatId: string) {
  const text = `❓ <b>FastStrm Bot 命令</b>\n\n` +
    `<b>/start</b> — 打开操作菜单\n` +
    `<b>/status</b> — 查看监控状态、账号 Cookie 状态\n` +
    `<b>/scan</b> — 执行全量对账（扫描+清理+补生成）\n` +
    `<b>/cleanup</b> — 扫描孤儿 STRM\n` +
    `<b>/accounts</b> — 查看账号列表\n` +
    `<b>/help</b> — 显示此帮助\n\n` +
    `<b>说明：</b>\n` +
    `• 全量对账会暂停监控，完成后自动恢复\n` +
    `• 孤儿扫描不会自动删除，请在 Web UI 确认后清理\n` +
    `• Cookie 过期请在 Web UI 扫码刷新`;

  await bot.sendMessage({ chat_id: chatId, text, parse_mode: "HTML" });
}

export async function handleMessage(bot: Bot, message: MessageLike, source: string = "Polling") {
  const chatId = message.chat.id.toString();
  const text = message.text;
  const username = message.from.username || message.from.first_name;
  const userId = message.from.id;

  logger.debug(`[Telegram ${source}] Message from ${username} (${userId}): ${text}`);

  if (text?.startsWith("/")) {
    await handleCommand(bot, chatId, text, username, userId);
  } else {
    await bot.sendMessage({
      chat_id: chatId,
      text: `👋 你好 ${username}！\n\nFastStrm Bot 支持以下操作：\n\n• /status — 查看系统状态\n• /scan — 执行全量对账\n• /cleanup — 清理孤儿 STRM\n• /accounts — 查看账号列表\n\n输入 /start 打开操作菜单。`,
      parse_mode: "HTML",
    });
  }
}

async function handleCommand(bot: Bot, chatId: string, command: string, username: string, userId: number) {
  const [cmd] = command.split(" ");

  if (!isTelegramUserAllowed(userId)) {
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ <b>访问被拒绝</b>\n\n你没有使用此 Bot 的权限，请联系管理员。\n\n你的 User ID: <code>${userId}</code>`,
      parse_mode: "HTML",
    });
    return;
  }

  switch (cmd) {
    case "/start":
      await handleStart(bot, chatId, username);
      break;
    case "/status":
      await handleStatus(bot, chatId);
      break;
    case "/scan":
      await handleScan(bot, chatId);
      break;
    case "/cleanup":
      await handleCleanup(bot, chatId);
      break;
    case "/accounts":
      await handleAccounts(bot, chatId);
      break;
    case "/help":
      await handleHelp(bot, chatId);
      break;
    default:
      await bot.sendMessage({
        chat_id: chatId,
        text: `❓ 未知命令: ${cmd}\n\n输入 /help 查看所有可用命令。`,
        parse_mode: "HTML",
      });
  }
}

export async function handleCallbackQuery(bot: Bot, callbackQuery: CallbackQueryLike, source: string = "Polling") {
  if (!callbackQuery.message) {
    logger.error("Callback query has no message");
    return;
  }

  const chatId = callbackQuery.message.chat.id.toString();
  const data = callbackQuery.data;
  const queryId = callbackQuery.id;

  logger.debug(`[Telegram ${source}] Callback query: ${data}`);

  await bot.answerCallbackQuery(queryId, "处理中...");

  if (!data) return;

  switch (data) {
    case "scan":
      await handleScan(bot, chatId);
      break;
    case "cleanup":
      await handleCleanup(bot, chatId);
      break;
    case "status":
      await handleStatus(bot, chatId);
      break;
    case "accounts":
      await handleAccounts(bot, chatId);
      break;
    default:
      await bot.sendMessage({
        chat_id: chatId,
        text: `✅ 已处理: ${data}`,
        parse_mode: "HTML",
      });
  }
}