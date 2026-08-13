// Telegram 轮询管理器
import { createTelegramBot } from "./telegram";
import { readSettings } from "./serverUtils";
import { BOT_COMMANDS, handleMessage, handleCallbackQuery, BASE_URL } from "./telegramCommands";

let pollingInterval: NodeJS.Timeout | null = null;
let pollingStartTimer: NodeJS.Timeout | null = null;
let lastUpdateId = 0;
let isPollingActive = false;
let currentBotToken: string | null = null;
let currentBot: ReturnType<typeof createTelegramBot> | null = null;
let autoInitDone = false;

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
        await handleMessage(bot, update.message, "Polling");
      }
      if (update.callback_query) {
        await handleCallbackQuery(bot, update.callback_query, "Polling");
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
    return false;
  }

  const settings = readSettings();
  const telegram = settings.telegram;

  if (!telegram?.botToken) {
    console.error("Telegram not configured for polling");
    return false;
  }

  if (telegram.enabled === false) {
    return false;
  }

  const bot = recreateBotIfNeeded();
  if (!bot) return false;

  try {
    await bot.deleteWebhook();
    await new Promise((resolve) => setTimeout(resolve, 2000));

    const webhookInfo = await bot.getWebhookInfo();
    if ((webhookInfo.result as { url?: string })?.url) {
      await bot.deleteWebhook();
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  } catch (error) {
    // no webhook to delete or error deleting
  }

  await setupBotMenu(bot);

  isPollingActive = true;

  pollingStartTimer = setTimeout(() => {
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
    return false;
  }

  if (pollingStartTimer) {
    clearTimeout(pollingStartTimer);
    pollingStartTimer = null;
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
      return false;
    }

    const bot = createTelegramBot(telegram.botToken);
    stopPolling();

    try {
      await bot.deleteWebhook();
    } catch {
      // no webhook to delete
    }

    pollingStartTimer = setTimeout(async () => {
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
      return false;
    }

    const bot = createTelegramBot(telegram.botToken);
    stopPolling();

    for (let i = 0; i < 3; i++) {
      try {
        await bot.deleteWebhook();
        await new Promise((resolve) => setTimeout(resolve, 2000));

        const webhookInfo = await bot.getWebhookInfo();
        if (!(webhookInfo.result as { url?: string })?.url) {
          break;
        }
      } catch {
        // retry
      }
    }

    await new Promise((resolve) => setTimeout(resolve, 5000));

    return await startPolling();
  } catch (error) {
    console.error("Failed to safely start polling:", error);
    return false;
  }
}

export { BASE_URL };
