// Telegram 轮询管理器
import { createTelegramBot } from "./telegram";
import { readSettings, isTelegramUserAllowed, readTasks, addTelegramUser, getTelegramUsers, removeTelegramUser } from "./serverUtils";

let pollingInterval: NodeJS.Timeout | null = null;
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
    message: isPollingActive ? "轮询中" : "轮询未启动"
  };
}

// 强制清理 webhook 和轮询状态
export async function forceCleanup(): Promise<boolean> {
  try {
    const settings = readSettings();
    const telegram = settings.telegram;
    
    if (!telegram || !telegram.botToken) {
      console.error("Telegram not configured for cleanup");
      return false;
    }

    const bot = createTelegramBot(telegram.botToken);
    
    // 停止轮询
    stopPolling();
    
    // 强制删除 webhook
    try {
      await bot.deleteWebhook();
      console.log("Force deleted webhook");
    } catch (error) {
      console.log("Error force deleting webhook:", error);
    }
    
    // 等待 5 秒后重新启动轮询，给 Telegram 服务器更多时间
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

// 安全启动轮询（处理冲突）
export async function safeStartPolling(): Promise<boolean> {
  try {
    const settings = readSettings();
    const telegram = settings.telegram;
    
    if (!telegram || !telegram.botToken) {
      console.error("Telegram not configured for polling");
      return false;
    }

    const bot = createTelegramBot(telegram.botToken);
    
    // 停止现有轮询
    stopPolling();
    
    // 多次尝试删除 webhook
    for (let i = 0; i < 3; i++) {
      try {
        await bot.deleteWebhook();
        console.log(`Deleted webhook (attempt ${i + 1})`);
        await new Promise(resolve => setTimeout(resolve, 2000));
        
        // 验证 webhook 是否已删除
        const webhookInfo = await bot.getWebhookInfo();
        if (!(webhookInfo.result as { url?: string })?.url) {
          console.log("Webhook successfully deleted");
          break;
        }
      } catch (error) {
        console.log(`Error deleting webhook (attempt ${i + 1}):`, error);
      }
    }
    
    // 等待更长时间确保状态同步
    console.log("Waiting for Telegram server to sync...");
    await new Promise(resolve => setTimeout(resolve, 5000));
    
    // 启动轮询
    return await startPolling();
  } catch (error) {
    console.error("Failed to safely start polling:", error);
    return false;
  }
}

// 处理消息
async function handleMessage(bot: ReturnType<typeof createTelegramBot>, message: unknown) {
  const msg = message as { chat: { id: number }; text?: string; from: { username?: string; first_name: string; id: number } };
  const chatId = msg.chat.id.toString();
  const text = msg.text;
  const username = msg.from.username || msg.from.first_name;
  const userId = msg.from.id;

  console.log(`[Telegram Polling] Message from ${username} (${userId}): ${text}`);
  console.log(`[Telegram Polling] Chat ID: ${chatId}, User ID: ${userId}`);

  // 处理命令
  if (text?.startsWith('/')) {
    await handleCommand(bot, chatId, text, username, userId);
  } else {
    // 处理普通消息
    await bot.sendMessage({
      chat_id: chatId,
      text: `Hello ${username}! 👋\n\nUse /help to see available commands.`,
      parse_mode: 'HTML'
    });
  }
}

// 处理命令
async function handleCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string, command: string, username: string, userId: number) {
  const [cmd, ...args] = command.split(' ');

  // 检查用户权限
  if (!isTelegramUserAllowed(userId)) {
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ <b>Access Denied</b>\n\n` +
            `You are not authorized to use this bot.\n\n` +
            `Contact the administrator to get access.\n\n` +
            `Your User ID: <code>${userId}</code>`,
      parse_mode: 'HTML'
    });
    return;
  }

  switch (cmd) {
    case '/start':
      await handleStartCommand(bot, chatId, username);
      break;

    case '/help':
      await bot.sendMessage({
        chat_id: chatId,
        text: `<b>🤖 FreeStrm Bot Commands</b>\n\n` +
              `<b>Available Commands:</b>\n` +
              `<b>/start</b> - Start the bot\n` +
              `<b>/help</b> - Show this help message\n` +
              `<b>/ping</b> - Test bot connectivity\n` +
              `<b>/status</b> - Show system status\n` +
              `<b>/tasks</b> - List current tasks\n` +
              `<b>/settings</b> - Show current settings\n` +
              `<b>/users</b> - List authorized users\n` +
              `<b>/adduser &lt;user_id&gt;</b> - Add new user\n` +
              `<b>/removeuser &lt;user_id&gt;</b> - Remove user\n\n` +
              `✅ You are authorized to use all commands.`,
        parse_mode: 'HTML'
      });
      break;

    case '/ping':
      await bot.sendMessage({
        chat_id: chatId,
        text: `🏓 Pong! Bot is working fine. (Polling mode)`,
        parse_mode: 'HTML'
      });
      break;

    case '/status':
      await bot.sendMessage({
        chat_id: chatId,
        text: `<b>📊 System Status</b>\n\n` +
              `<b>Bot:</b> ✅ Online (Polling)\n` +
              `<b>Time:</b> ${new Date().toLocaleString()}\n` +
              `<b>Uptime:</b> ${process.uptime().toFixed(0)}s`,
        parse_mode: 'HTML'
      });
      break;

    case '/tasks':
      await handleTasksCommand(bot, chatId);
      break;

    case '/settings':
      await handleSettingsCommand(bot, chatId);
      break;

    case '/users':
      await handleUsersCommand(bot, chatId);
      break;

    case '/adduser':
      await handleAddUserCommand(bot, chatId, args);
      break;

    case '/removeuser':
      await handleRemoveUserCommand(bot, chatId, args);
      break;

    default:
      await bot.sendMessage({
        chat_id: chatId,
        text: `❓ Unknown command: ${cmd}\n\nUse /help to see available commands.`,
        parse_mode: 'HTML'
      });
  }
}

// 处理任务命令（返回真实任务列表）
async function handleTasksCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
  try {
    const tasks = readTasks();

    if (tasks.length === 0) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `<b>📋 Current Tasks</b>\n\n` +
              `No active tasks found.\n\n` +
              `Use the web interface to start new download tasks.`,
        parse_mode: 'HTML'
      });
      return;
    }

    let message = `<b>📋 Current Tasks (${tasks.length})</b>\n\n`;
    for (let i = 0; i < tasks.length; i++) {
      const task = tasks[i] as { originPath?: string; targetPath?: string; account?: string; strmType?: string; id?: string };
      const taskName = `${task.originPath ?? ''} → ${task.targetPath ?? ''}`;
      message += `${i + 1}. <b>${taskName || 'Unnamed'}</b>\n` +
                 `   Account: ${task.account ?? '-'}\n` +
                 `   Type: ${task.strmType ?? '-'}\n` +
                 `   ID: <code>${task.id ?? '-'}</code>\n\n`;
    }

    await bot.sendMessage({
      chat_id: chatId,
      text: message,
      parse_mode: 'HTML'
    });
  } catch (error) {
    console.error("Error handling tasks command (polling):", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error retrieving tasks: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: 'HTML'
    });
  }
}

// 处理设置命令
async function handleSettingsCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
  try {
    const settings = readSettings();

    let settingsText = `<b>⚙️ Current Settings</b>\n\n`;

    if (settings.emby) {
      settingsText += `<b>Emby:</b>\n`;
      settingsText += `• URL: ${settings.emby.url || 'Not set'}\n`;
      settingsText += `• API Key: ${settings.emby.apiKey ? '***' + settings.emby.apiKey.slice(-4) : 'Not set'}\n\n`;
    }

    if (settings.telegram) {
      settingsText += `<b>Telegram:</b>\n`;
      settingsText += `• Bot Token: ${settings.telegram.botToken ? '***' + settings.telegram.botToken.slice(-4) : 'Not set'}\n`;
      settingsText += `• Chat ID: ${settings.telegram.chatId || 'Not set'}\n`;
      settingsText += `• Enabled: ${settings.telegram.enabled === false ? '❌ Off' : '✅ On'}\n`;
      settingsText += `• Allowed Users: ${settings.telegram.allowedUsers?.length || 0}\n\n`;
    }

    settingsText += `<b>User Agent:</b> ${settings['user-agent'] || 'Default'}`;

    await bot.sendMessage({
      chat_id: chatId,
      text: settingsText,
      parse_mode: 'HTML'
    });
  } catch (error) {
    console.error("Error handling settings command (polling):", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error retrieving settings: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: 'HTML'
    });
  }
}

// 处理用户列表命令
async function handleUsersCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string) {
  try {
    const users = getTelegramUsers();

    if (users.length === 0) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `<b>👥 Authorized Users</b>\n\nNo authorized users found.`,
        parse_mode: 'HTML'
      });
      return;
    }

    let usersText = `<b>👥 Authorized Users</b>\n\n`;
    users.forEach((uId, index) => {
      usersText += `<b>${index + 1}.</b> User ID: <code>${uId}</code>\n`;
    });

    await bot.sendMessage({
      chat_id: chatId,
      text: usersText,
      parse_mode: 'HTML'
    });
  } catch (error) {
    console.error("Error handling users command (polling):", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error retrieving users: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: 'HTML'
    });
  }
}

// 处理添加用户命令
async function handleAddUserCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string, args: string[]) {
  try {
    if (args.length < 1) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `<b>❌ Usage:</b> /adduser &lt;user_id&gt;\n\n` +
              `<b>Example:</b> /adduser 123456789`,
        parse_mode: 'HTML'
      });
      return;
    }

    const userId = parseInt(args[0]);

    if (isNaN(userId)) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ Invalid user ID: ${args[0]}`,
        parse_mode: 'HTML'
      });
      return;
    }

    const success = addTelegramUser(userId);

    if (success) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `✅ User added successfully!\n\n` +
              `User ID: <code>${userId}</code>`,
        parse_mode: 'HTML'
      });
    } else {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ Failed to add user. User might already exist.`,
        parse_mode: 'HTML'
      });
    }
  } catch (error) {
    console.error("Error handling add user command (polling):", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error adding user: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: 'HTML'
    });
  }
}

// 处理删除用户命令
async function handleRemoveUserCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string, args: string[]) {
  try {
    if (args.length < 1) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `<b>❌ Usage:</b> /removeuser &lt;user_id&gt;\n\n` +
              `<b>Example:</b> /removeuser 123456`,
        parse_mode: 'HTML'
      });
      return;
    }

    const userId = parseInt(args[0]);

    if (isNaN(userId)) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ Invalid user ID: ${args[0]}`,
        parse_mode: 'HTML'
      });
      return;
    }

    const success = removeTelegramUser(userId);

    if (success) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `✅ User removed successfully!\n\nID: ${userId}`,
        parse_mode: 'HTML'
      });
    } else {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ User not found or failed to remove.`,
        parse_mode: 'HTML'
      });
    }
  } catch (error) {
    console.error("Error handling remove user command (polling):", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error removing user: ${error instanceof Error ? error.message : String(error)}`,
      parse_mode: 'HTML'
    });
  }
}

// 处理回调查询（简化版本）
async function handleCallbackQuery(bot: ReturnType<typeof createTelegramBot>, callbackQuery: unknown) {
  const query = callbackQuery as { message?: { chat: { id: number } }; data?: string; id: string };
  if (!query.message) {
    console.error("Callback query has no message");
    return;
  }

  const chatId = query.message.chat.id.toString();
  const data = query.data;
  const queryId = query.id;

  console.log(`[Telegram Polling] Callback query: ${data}`);

  // 回答回调查询
  await bot.answerCallbackQuery(queryId, "Processing...");

  // 处理任务开始回调
  if (data && data.startsWith('start_task_')) {
    const taskId = data.replace('start_task_', '');
    await handleTaskStartCallback(bot, chatId, taskId);
    return;
  }

  // 其他回调查询处理
  await bot.sendMessage({
    chat_id: chatId,
    text: `✅ Callback processed: ${data}`,
    parse_mode: 'HTML'
  });
}

// 处理 /start 命令 - 显示任务列表
async function handleStartCommand(bot: ReturnType<typeof createTelegramBot>, chatId: string, username: string) {
  try {
    const tasks = readTasks();
    
    if (tasks.length === 0) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `Welcome to FreeStrm Bot! 🤖\n\n` +
              `Hello ${username}! You are authorized to use this bot.\n\n` +
              `📋 <b>Current Tasks:</b> No tasks available\n\n` +
              `Use /help to see all available commands.`,
        parse_mode: 'HTML'
      });
      return;
    }

    // 构建任务列表消息
    let message = `Welcome to FreeStrm Bot! 🤖\n\n` +
                  `Hello ${username}! You are authorized to use this bot.\n\n` +
                  `📋 <b>Current Tasks (${tasks.length}):</b>\n\n`;

    // 为每个任务创建按钮
    const buttons = [];
    
    for (let i = 0; i < tasks.length; i++) {
      const task = tasks[i];
      const taskName = `${task.originPath} → ${task.targetPath}`;
      const taskInfo = `${i + 1}. <b>${taskName}</b>\n` +
                      `   Account: ${task.account}\n` +
                      `   Type: ${task.strmType}\n\n`;
      
      message += taskInfo;
      
      // 添加开始按钮
      buttons.push([{
        text: `▶️ Start Task ${i + 1}`,
        callback_data: `start_task_${task.id}`
      }]);
    }

    message += `Use the buttons below to start tasks, or /help for more commands.`;

    await bot.sendMessageWithButtons(chatId, message, buttons);
  } catch (error) {
    console.error("Error handling start command:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error loading tasks: ${error}`,
      parse_mode: 'HTML'
    });
  }
}

// 处理任务开始回调
async function handleTaskStartCallback(bot: ReturnType<typeof createTelegramBot>, chatId: string, taskId: string) {
  try {
    const tasks = readTasks();
    const task = tasks.find((t: { id: string }) => t.id === taskId);
    
    if (!task) {
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ Task not found`,
        parse_mode: 'HTML'
      });
      return;
    }

    // 发送任务开始消息
    await bot.sendMessage({
      chat_id: chatId,
      text: `🚀 <b>Starting Task</b>\n\n` +
            `📁 <b>From:</b> ${task.originPath}\n` +
            `📁 <b>To:</b> ${task.targetPath}\n` +
            `👤 <b>Account:</b> ${task.account}\n` +
            `⚙️ <b>Type:</b> ${task.strmType}\n\n` +
            `⏳ Task is starting...`,
      parse_mode: 'HTML'
    });

    // 调用真正的 startTask API
    try {
      const response = await fetch(`${process.env.NEXTAUTH_URL || 'http://localhost:3000'}/api/startTask`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ id: taskId })
      });

      if (response.ok) {
        await bot.sendMessage({
          chat_id: chatId,
          text: `✅ <b>Task started successfully!</b>\n\n` +
                `Task ID: <code>${taskId}</code>\n` +
                `📁 From: ${task.originPath}\n` +
                `📁 To: ${task.targetPath}\n\n` +
                `You can check the progress in the web interface.`,
          parse_mode: 'HTML'
        });
      } else {
        const errorData = await response.text();
        await bot.sendMessage({
          chat_id: chatId,
          text: `❌ <b>Failed to start task</b>\n\n` +
                `Error: ${errorData}\n` +
                `Task ID: <code>${taskId}</code>`,
          parse_mode: 'HTML'
        });
      }
    } catch (apiError) {
      console.error("Error calling startTask API:", apiError);
      await bot.sendMessage({
        chat_id: chatId,
        text: `❌ <b>API Error</b>\n\n` +
              `Failed to call startTask API: ${apiError}\n` +
              `Task ID: <code>${taskId}</code>`,
        parse_mode: 'HTML'
      });
    }

  } catch (error) {
    console.error("Error starting task:", error);
    await bot.sendMessage({
      chat_id: chatId,
      text: `❌ Error starting task: ${error}`,
      parse_mode: 'HTML'
    });
  }
}
