// Telegram 轮询管理器
import { createTelegramBot } from "./telegram";
import { readSettings } from "./serverUtils";
import { BOT_COMMANDS, handleMessage, handleCallbackQuery, BASE_URL } from "./telegramCommands";
import { logger } from "./logger";

// 使用 globalThis 持久化轮询状态，防止 HMR 重置导致状态丢失
const _pg = globalThis as unknown as {
  __telegramPolling?: {
    interval: NodeJS.Timeout | null;
    startTimer: NodeJS.Timeout | null;
    lastUpdateId: number;
    isActive: boolean;
    isRequestInFlight: boolean;
    currentBotToken: string | null;
    menuSetup: boolean;
  };
};

if (!_pg.__telegramPolling) {
  _pg.__telegramPolling = {
    interval: null,
    startTimer: null,
    lastUpdateId: 0,
    isActive: false,
    isRequestInFlight: false,
    currentBotToken: null,
    menuSetup: false,
  };
}

let pollingInterval = _pg.__telegramPolling.interval;
let pollingStartTimer = _pg.__telegramPolling.startTimer;
let lastUpdateId = _pg.__telegramPolling.lastUpdateId;
let isPollingActive = _pg.__telegramPolling.isActive;
let isRequestInFlight = _pg.__telegramPolling.isRequestInFlight;
let currentBotToken = _pg.__telegramPolling.currentBotToken;
let menuSetup = _pg.__telegramPolling.menuSetup;
let currentBot: ReturnType<typeof createTelegramBot> | null = null;

function syncPollingState() {
  if (_pg.__telegramPolling) {
    _pg.__telegramPolling.interval = pollingInterval;
    _pg.__telegramPolling.startTimer = pollingStartTimer;
    _pg.__telegramPolling.lastUpdateId = lastUpdateId;
    _pg.__telegramPolling.isActive = isPollingActive;
    _pg.__telegramPolling.isRequestInFlight = isRequestInFlight;
    _pg.__telegramPolling.currentBotToken = currentBotToken;
    _pg.__telegramPolling.menuSetup = menuSetup;
  }
}

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
  if (menuSetup) return;
  menuSetup = true;
  syncPollingState();
  try {
    const result = await bot.setMyCommands(BOT_COMMANDS);
    if (result.ok) {
      logger.info("[Telegram] Bot menu commands set successfully");
    } else {
      logger.warn("[Telegram] Failed to set bot menu:", result.description);
    }
  } catch (error) {
    logger.warn("[Telegram] setMyCommands error:", error);
    menuSetup = false;
    syncPollingState();
  }
}

async function pollingTick() {
  if (isRequestInFlight) return;

  const settings = readSettings();
  const telegram = settings.telegram;

  if (!telegram?.botToken || telegram.enabled === false) {
    stopPolling();
    return;
  }

  // 运行时自愈：如果标记为 active 但定时器已失效（进程崩溃/HMR 导致），自动重启
  if (isPollingActive && !pollingInterval && !pollingStartTimer) {
    logger.warn("[Telegram] Polling active but timer missing, restarting...");
    isPollingActive = false;
    syncPollingState();
    await startPolling();
    return;
  }

  const bot = recreateBotIfNeeded();
  if (!bot) return;

  isRequestInFlight = true;
  syncPollingState();
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
      logger.warn("Polling error:", axiosError.message || error);
    }
  } finally {
    isRequestInFlight = false;
    syncPollingState();
  }
}

export async function startPolling(): Promise<boolean> {
  if (isPollingActive) {
    return false;
  }

  const settings = readSettings();
  const telegram = settings.telegram;

  if (!telegram?.botToken) {
    logger.error("Telegram not configured for polling");
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
  syncPollingState();

  pollingStartTimer = setTimeout(() => {
    if (!isPollingActive) return;
    pollingInterval = setInterval(pollingTick, 5000);
    syncPollingState();
  }, 3000);
  syncPollingState();

  return true;
}

export async function initTelegramPolling(): Promise<void> {
  const settings = readSettings();
  const telegram = settings.telegram;

  const hasToken = !!telegram?.botToken;
  const isEnabled = telegram?.enabled !== false;
  const noWebhook = !telegram?.webhookUrl || telegram.webhookUrl.trim() === "";
  const autoPolling = telegram?.autoPolling !== false;

  // 如果没有 autoPolling 或条件不满足，停止轮询（如果正在运行）
  if (!autoPolling || !hasToken || !isEnabled || !noWebhook) {
    if (isPollingActive) {
      logger.info("[Telegram] Polling auto-start disabled or conditions not met, stopping...");
      stopPolling();
    }
    return;
  }

  if (!isPollingActive) {
    logger.info("[Telegram] Auto-starting polling (autoPolling=true, enabled, no webhook)...");
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
  syncPollingState();
  logger.info("Telegram polling stopped");
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
    logger.error("Failed to force cleanup:", error);
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
    logger.error("Failed to safely start polling:", error);
    return false;
  }
}

export { BASE_URL };
