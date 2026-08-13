// Telegram 轮询管理器
import { createTelegramBot } from "./telegram";
import { readSettings, isTelegramUserAllowed, readAccounts } from "./serverUtils";
import { getMonitorStatus } from "./eventMonitor";

let pollingInterval: NodeJS.Timeout | null = null;
let lastUpdateId = 0;
let isPollingActive = false;
let currentBotToken: string | null = null;
let currentBot: ReturnType<typeof createTelegramBot> | null = null;
let autoInitDone = false;

const BOT_COMMANDS = [
  { command: "status", description: "📊 系统状态" },
  { command: "scan", description: "🔍 全量对账" },
  { command: "cleanup", description: "🧹 清理孤儿" },
  { command: "accounts", description: "👥 账号列表" },
  { command: "help", description: "❓ 帮助" },
];

const BASE_URL = process.env.NEXTAUTH_URL || "http://localhost:3000";

function recreateBotIfNeeded(): ReturnType<typeof createTelegramBot> | null {
  const settings = readSettings();
  const token = settings.telegram?.botToken;
  if (!token) return null;
  if (token !== currentBotToken || !currentBot) {
    currentBotToken = token;
    currentBot = createTelegramBot(token);
  }
  return currentBot;
}

async function setupBotMenu(bot: ReturnType<typeof createTelegramBot>): Promise<void> {
  try {
    const result = await bot.setMyCommands(BOT_COMMANDS);
    if (result.ok) {
      console.log("[Telegram] Bot menu commands set successfully");
    } else {
      console.warn("[Telegram] Failed to set bot menu:", result.description);
    }
  } catch (error) {
    console.warn("[Telegram] setMyCommands error:", error);
  }
}

async function pollingTick() {
  const settings = readSettings();
  const telegram = settings.telegram;

  if (!telegram?.botToken || telegram.enabled === false) {
    stopPolling();
    return;
  }

  const bot = recreateBotIfNeeded();
  if (!bot) return;

  try {
    const updates = await bot.getUpdates(lastUpdateId + 1, 1, 30);
    if (!updates || updates.length === 0) return;

    for (const update of updates) {
      lastUpdateId = update.update_id;
      if (update.message) {
        await handleMessage(bot, update.message);
      }
      if (update.callback_query) {
        await handleCallbackQuery(bot, update.callback_query);
      }
    }
  } catch (error: unknown) {
    const axiosError = error as { response?: { status?: number }; message?: string };
    if (axiosError.response?.status !== 409) {
      console.warn("Polling error:", axiosError.message || error);
    }
  }
}

export async function startPolling(): Promise<boolean> {
  if (isPollingActive) {
    console.log("Polling already running");
    return false;
  }

  const settings = readSettings();
  const telegram = settings.telegram;

  if (!telegram?.botToken) {
    console.error("Telegram not configured for polling");
    return false;
  }

  if (telegram.enabled === false) {
    console.log("Telegram notifications disabled, skip polling start");
    return false;
  }

  const bot = recreateBotIfNeeded();
  if (!bot) return false;

  try {
    await bot.deleteWebhook();
    console.log("Deleted existing webhook for polling mode");
    await new Promise((resolve) => setTimeout(resolve, 2000));

    const webhookInfo = await bot.getWebhookInfo();
    if ((webhookInfo.result as { url?: string })?.url) {
      console.log("Warning: Webhook still exists, trying again...");
      await bot.deleteWebhook();
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  } catch (error) {
    console.log("No webhook to delete or error deleting webhook:", error);
  }

  await setupBotMenu(bot);

  console.log("Starting Telegram polling...");
  isPollingActive = true;

  setTimeout(() => {
    if (!isPollingActive) return;
    pollingInterval = setInterval(pollingTick, 5000);
  }, 3000);

  return true;
}

export async function initTelegramPolling(): Promise<void> {
  if (autoInitDone) return;
  autoInitDone = true;
  const settings = readSettings();
  const telegram = settings.telegram;
  if (telegram?.botToken && telegram.enabled !== false && !telegram.webhookUrl) {
    console.log("[Telegram] Auto-starting polling (enabled, no webhook)...");
    await startPolling();
  }
}

export function stopPolling(): boolean {
  if (!isPollingActive) {
    console.log("Polling not running");
    return false;
  }

  if (pollingInterval) {
    clearInterval(pollingInterval);
    pollingInterval = null;
  }

  isPollingActive = false;
  console.log("Telegram polling stopped");
  return true;
}

export function getPollingStatus(): { active: boolean; message: string } {
  return {
    active: isPollingActive,
    message: isPollingActive ? "轮询中" : "轮询未启动",
  };
}

export async function forceCleanup(): Promise<boolean> {
  try {
    const settings = readSettings();
    const telegram = settings.telegram;

    if (!telegram || !telegram.botToken) {
      console.error("Telegram not configured for cleanup");
      return false;
    }

    const bot = createTelegramBot(telegram.botToken);
    stopPolling();

    try {
      await bot.deleteWebhook();
      console.log("Force deleted webhook");
    } catch (error) {
      console.log("Error force deleting webhook:", error);
    }

    setTimeout(async () => {
      console.log("Restarting polling after cleanup...");
      await startPolling();
    }, 5000);

    return true;
  } catch (error) {
    console.error("Failed to force cleanup:", error);
    return false;
  }
}

export async function safeStartPolling(): Promise<boolean> {
  try {
    const settings = readSettings();
    const telegram = settings.telegram;

    if (!telegram || !telegram.botToken) {
      console.error("Telegram not configured for polling");
      return false;
    }

    const bot = createTelegramBot(telegram.botToken);
    stopPolling();

    for (let i = 0; i < 3; i++) {
      try {
        await bot.deleteWebhook();
        console.log(`Deleted webhook (attempt ${i + 1})`);
        await new Promise((resolve) => setTimeout(resolve, 2000));

        const webhookInfo = await bot.getWebhookInfo();
        if (!(webhookInfo.result as { url?: string })?.url) {
          console.log("Webhook successfully deleted");
          break;
        }
      } catch (error) {
        console.log(`Error deleting webhook (attempt ${i + 1}):`, error);
      }
    }

    console.log("Waiting for Telegram server to sync...");
    await new Promise((resolve) => setTimeout(resolve, 5000));

    return await startPolling();
  } catch (error) {
    console.error("Failed to safely start polling:", error);
    return false;
  }
}

async function handleMessage(bot: ReturnType<typeof createTelegramBot>, message: unknown) {
  const msg = message as { chat: { id: number }; text?: string; from: { username?: string; first_name: string; id: number } };
  const chatId = msg.chat.id.toString();
  const text = msg.text;
  const username = msg.from.username || msg.from.first_name;
  const userId = msg.from.id;

  console.log(`[Telegram Polling] Message from ${username} (${userId}): ${text}`);

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

async function handleCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string, command: string, username: string, userId: number) {
  const [cmd, ...args] = command.split(" ");

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

async function handleStart(bot: ReturnType<typeof createTelegramBot>, chatId: string, username: string) {
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

async function handleStatus(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
  try {
    const { config, states } = getMonitorStatus();
    const accounts = readAccounts() as Array<{ name: string; cookie?: string }>;

    let message = `<b>📊 系统状态</b>\n\n`;

    // 监控状态
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

    // 账号 Cookie 状态
    message += `<b>👥 账号状态</b>\n`;
    for (const account of accounts) {
      const hasCookie = account.cookie && account.cookie.length > 0;
      message += `  ${hasCookie ? "✅" : "⚠️"} <b>${account.name}</b> — Cookie: ${hasCookie ? "有效" : "未设置"}\n`;
    }

    message += `\n<b>⏰ 时间:</b> ${new Date().toLocaleString()}`;

    await bot.sendMessage({ chat_id: chatId, text: message, parse_mode: "HTML" });
  } catch (error) {
    console.error("Error handling status:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 获取状态失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleScan(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
  const msg = await bot.sendMessage({
    chat_id: chatId,
    text: `🔍 <b>正在执行全量对账...</b>\n\n这可能需要几分钟时间，请稍候。`,
    parse_mode: "HTML",
  });

  try {
    const response = await fetch(`${BASE_URL}/api/strmCleanup/scan`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "reconcile", useSettingsDefaults: true }),
    });

    const result = await response.json();

    if (response.ok) {
      const scanned = result.mappingsScanned?.length || 0;
      const created = result.createdCount || 0;
      const deleted = result.deletedCount || 0;
      const errors = result.errors || 0;

      let text = `✅ <b>全量对账完成</b>\n\n`;
      text += `• 扫描映射: ${scanned}\n`;
      text += `• 生成 STRM: ${created}\n`;
      text += `• 清理 STRM: ${deleted}\n`;
      if (errors > 0) text += `• 错误: ${errors}\n`;
      text += `\n⏰ ${new Date().toLocaleString()}`;

      await bot.sendMessage({ chat_id: chatId, text, parse_mode: "HTML" });
    } else {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ <b>全量对账失败</b>\n\n${result.message || result.error || "未知错误"}`,
        parse_mode: "HTML",
      });
    }
  } catch (error) {
    console.error("Error calling reconcile API:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 全量对账请求失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleCleanup(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
  const msg = await bot.sendMessage({
    chat_id: chatId,
    text: `🧹 <b>正在扫描孤儿 STRM...</b>\n\n这可能需要一些时间。`,
    parse_mode: "HTML",
  });

  try {
    const response = await fetch(`${BASE_URL}/api/strmCleanup/scan`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "scan", useSettingsDefaults: true }),
    });

    const result = await response.json();

    if (response.ok) {
      const stale = result.staleCount || 0;
      const missing = result.missingCount || 0;

      let text = `📋 <b>孤儿扫描完成</b>\n\n`;
      text += `• 孤儿 STRM: ${stale}\n`;
      text += `• 缺失 STRM: ${missing}\n`;

      if (stale > 0) {
        text += `\n⚠️ 发现 ${stale} 个孤儿 STRM，请在 Web UI 确认后执行清理。`;
      } else {
        text += `\n✅ 没有发现孤儿 STRM。`;
      }

      await bot.sendMessage({ chat_id: chatId, text, parse_mode: "HTML" });
    } else {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ <b>扫描失败</b>\n\n${result.message || result.error || "未知错误"}`,
        parse_mode: "HTML",
      });
    }
  } catch (error) {
    console.error("Error calling scan API:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 孤儿扫描请求失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleAccounts(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
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
    console.error("Error handling accounts:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ 获取账号列表失败: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: "HTML",
    });
  }
}

async function handleHelp(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
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

async function handleCallbackQuery(bot: ReturnType<typeof createTelegramBot>, callbackQuery: unknown) {
  const query = callbackQuery as { message?: { chat: { id: number }; message_id: number }; data?: string; id: string };
  if (!query.message) {
    console.error("Callback query has no message");
    return;
  }

  const chatId = query.message.chat.id.toString();
  const data = query.data;
  const queryId = query.id;

  console.log(`[Telegram Polling] Callback query: ${data}`);

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