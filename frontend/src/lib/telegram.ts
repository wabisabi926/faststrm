// Telegram Bot API 集成
import axios from "axios";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";

export interface TelegramConfig {
  botToken: string;
  chatId?: string; // 可选，用于发送消息到特定聊天
}

export interface TelegramMessage {
  chat_id: string;
  text: string;
  parse_mode?: 'HTML' | 'Markdown' | 'MarkdownV2';
  reply_markup?: {
    inline_keyboard?: Array<Array<{ text: string; callback_data: string }>>;
  };
}

export interface TelegramUpdate {
  update_id: number;
  message?: {
    message_id: number;
    from: {
      id: number;
      is_bot: boolean;
      first_name: string;
      username?: string;
    };
    chat: {
      id: number;
      type: string;
    };
    date: number;
    text?: string;
  };
  callback_query?: {
    id: string;
    from: {
      id: number;
      is_bot: boolean;
      first_name: string;
      username?: string;
    };
    message?: {
      message_id: number;
      chat: { id: number; type: string };
      text?: string;
    };
    data?: string;
  };
}

export interface TelegramResponse {
  ok: boolean;
  result?: unknown;
  error_code?: number;
  description?: string;
  error?: string;
}

export interface TelegramBotInfo {
  id: number;
  is_bot: boolean;
  first_name: string;
  username: string;
  can_join_groups: boolean;
  can_read_all_group_messages: boolean;
  supports_inline_queries: boolean;
}

export class TelegramBot {
  private botToken: string;
  private baseUrl: string;

  constructor(botToken: string) {
    this.botToken = botToken;
    this.baseUrl = `https://api.telegram.org/bot${botToken}`;
  }

  // 发送消息
  async sendMessage(message: TelegramMessage): Promise<TelegramResponse> {
    try {
      const response = await axios.post(`${this.baseUrl}/sendMessage`, message);
      return response.data;
    } catch (error) {
      console.error('Telegram sendMessage error:', error);
      // 不抛出错误，避免影响主流程，只记录日志
      return { ok: false, error: `Telegram API error: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 发送通知消息（简化版本）
  async sendNotification(text: string, chatId?: string): Promise<TelegramResponse> {
    if (!chatId) {
      console.warn('Chat ID is required for sending notifications, skipping...');
      return { ok: false, error: 'Chat ID is required for sending notifications' };
    }

    try {
      return await this.sendMessage({
        chat_id: chatId,
        text,
        parse_mode: 'HTML'
      });
    } catch (error) {
      console.error('Failed to send Telegram notification:', error);
      return { ok: false, error: `Failed to send notification: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 获取机器人信息
  async getMe(): Promise<TelegramResponse> {
    try {
      const response = await axios.get(`${this.baseUrl}/getMe`);
      return response.data;
    } catch (error) {
      console.error('Telegram getMe error:', error);
      return { ok: false, error: `Failed to get bot info: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 获取更新（用于 webhook 或轮询）
  async getUpdates(offset?: number, limit?: number, timeout?: number): Promise<TelegramUpdate[]> {
    try {
      const params = new URLSearchParams();
      if (offset) params.append('offset', offset.toString());
      if (limit) params.append('limit', limit.toString());
      if (timeout) params.append('timeout', timeout.toString());

      const response = await axios.get(`${this.baseUrl}/getUpdates?${params}`, {
        timeout: (timeout || 30) * 1000 + 5000, // 给额外的5秒缓冲时间
      });
      return response.data.result || [];
    } catch (error: unknown) {
      // 409 错误表示没有新消息，这是正常的，不需要记录
      if (error && typeof error === 'object' && 'response' in error && 
          (error as { response?: { status?: number } }).response?.status === 409) {
        console.log('Telegram getUpdates: No new messages (409)');
        return []; // 返回空数组而不是抛出错误
      }
      console.error('Telegram getUpdates error:', error);
      return []; // 返回空数组而不是抛出错误
    }
  }

  // 设置 webhook
  async setWebhook(url: string, secretToken?: string): Promise<TelegramResponse> {
    try {
      const data: { url: string; secret_token?: string } = { url };
      if (secretToken) data.secret_token = secretToken;

      const response = await axios.post(`${this.baseUrl}/setWebhook`, data);
      return response.data;
    } catch (error) {
      console.error('Telegram setWebhook error:', error);
      return { ok: false, error: `Failed to set webhook: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 删除 webhook
  async deleteWebhook(): Promise<TelegramResponse> {
    try {
      const response = await axios.post(`${this.baseUrl}/deleteWebhook`);
      return response.data;
    } catch (error) {
      console.error('Telegram deleteWebhook error:', error);
      return { ok: false, error: `Failed to delete webhook: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 获取 webhook 信息
  async getWebhookInfo(): Promise<TelegramResponse> {
    try {
      const response = await axios.get(`${this.baseUrl}/getWebhookInfo`);
      return response.data;
    } catch (error) {
      console.error('Telegram getWebhookInfo error:', error);
      return { ok: false, error: `Failed to get webhook info: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 发送图片
  async sendPhoto(message: TelegramMessage & { photo: string }): Promise<TelegramResponse> {
    try {
      const response = await axios.post(`${this.baseUrl}/sendPhoto`, message);
      return response.data;
    } catch (error) {
      console.error('Telegram sendPhoto error:', error);
      return { ok: false, error: `Telegram API error: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 发送本地文件图片（multipart/form-data，适配内网 Emby 海报无法被 TG 服务器访问的场景）
  async sendPhotoFromFile(
    chatId: string,
    filePath: string,
    caption?: string
  ): Promise<TelegramResponse> {
    try {
      const basename = path.basename(filePath);
      const fileBuffer = fs.readFileSync(filePath);
      const blob = new Blob([fileBuffer]);
      const form = new FormData();
      form.append("chat_id", chatId);
      if (caption) form.append("caption", caption);
      form.append("parse_mode", "HTML");
      form.append("photo", blob, basename);

      const response = await axios.post(`${this.baseUrl}/sendPhoto`, form);
      return response.data;
    } catch (error) {
      console.error('Telegram sendPhotoFromFile error:', error);
      return { ok: false, error: `Telegram API error: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  /** 返回临时海报文件路径（用于 Emby 海报下载后上传 TG） */
  static makeTempPosterPath(itemId: string, suffix = ""): string {
    return path.join(os.tmpdir(), `emby_${itemId}${suffix}.jpg`);
  }

  // 发送带按钮的消息
  async sendMessageWithButtons(
    chatId: string, 
    text: string, 
    buttons: Array<Array<{ text: string; callback_data: string }>>
  ): Promise<TelegramResponse> {
    return this.sendMessage({
      chat_id: chatId,
      text,
      parse_mode: 'HTML',
      reply_markup: {
        inline_keyboard: buttons
      }
    });
  }

  // 编辑消息
  async editMessageText(
    chatId: string,
    messageId: number,
    text: string,
    replyMarkup?: { inline_keyboard?: Array<Array<{ text: string; callback_data: string }>> }
  ): Promise<TelegramResponse> {
    try {
      const data: {
        chat_id: string;
        message_id: number;
        text: string;
        parse_mode: string;
        reply_markup?: { inline_keyboard?: Array<Array<{ text: string; callback_data: string }>> };
      } = {
        chat_id: chatId,
        message_id: messageId,
        text,
        parse_mode: 'HTML'
      };
      if (replyMarkup) data.reply_markup = replyMarkup;

      const response = await axios.post(`${this.baseUrl}/editMessageText`, data);
      return response.data;
    } catch (error) {
      console.error('Telegram editMessageText error:', error);
      return { ok: false, error: `Failed to edit message: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 回答回调查询
  async answerCallbackQuery(callbackQueryId: string, text?: string): Promise<TelegramResponse> {
    try {
      const data: { callback_query_id: string; text?: string } = { callback_query_id: callbackQueryId };
      if (text) data.text = text;

      const response = await axios.post(`${this.baseUrl}/answerCallbackQuery`, data);
      return response.data;
    } catch (error) {
      console.error('Telegram answerCallbackQuery error:', error);
      return { ok: false, error: `Failed to answer callback query: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 设置 Bot 菜单命令
  async setMyCommands(commands: Array<{ command: string; description: string }>): Promise<TelegramResponse> {
    try {
      const response = await axios.post(`${this.baseUrl}/setMyCommands`, {
        commands,
        scope: { type: 'all_private_chats' }
      });
      return response.data;
    } catch (error) {
      console.error('Telegram setMyCommands error:', error);
      return { ok: false, error: `Failed to set bot commands: ${error instanceof Error ? error.message : String(error)}` };
    }
  }

  // 删除 Bot 菜单命令
  async deleteMyCommands(): Promise<TelegramResponse> {
    try {
      const response = await axios.post(`${this.baseUrl}/deleteMyCommands`);
      return response.data;
    } catch (error) {
      console.error('Telegram deleteMyCommands error:', error);
      return { ok: false, error: `Failed to delete bot commands: ${error instanceof Error ? error.message : String(error)}` };
    }
  }
}

// 创建机器人实例的工厂函数
export function createTelegramBot(botToken: string): TelegramBot {
  return new TelegramBot(botToken);
}

// 验证 Telegram 配置
export function validateTelegramConfig(config: unknown): config is TelegramConfig {
  return config !== null && typeof config === 'object' && 
         'botToken' in config && 
         typeof (config as { botToken: unknown }).botToken === 'string' && 
         (config as { botToken: string }).botToken.length > 0;
}

// 格式化任务状态消息
export function formatTaskStatusMessage(task: { name?: string; progress?: number; status?: string; [key: string]: unknown }): string {
  const status = task.status || 'unknown';
  const name = task.name || 'Unknown Task';
  const progress = task.progress || 0;
  
  let statusEmoji = '⏳';
  switch (status) {
    case 'completed':
      statusEmoji = '✅';
      break;
    case 'failed':
      statusEmoji = '❌';
      break;
    case 'running':
      statusEmoji = '🔄';
      break;
    case 'paused':
      statusEmoji = '⏸️';
      break;
  }

  return `<b>${statusEmoji} Task Update</b>\n\n` +
         `<b>Name:</b> ${name}\n` +
         `<b>Status:</b> ${status}\n` +
         `<b>Progress:</b> ${progress}%\n` +
         `<b>Time:</b> ${new Date().toLocaleString()}`;
}

// 格式化下载完成消息
export function formatDownloadCompleteMessage(task: { name?: string; size?: number; [key: string]: unknown }): string {
  const name = task.name || 'Unknown File';
  const size = task.size ? formatFileSize(task.size) : 'Unknown size';
  
  return `<b>🎉 Download Complete!</b>\n\n` +
         `<b>File:</b> ${name}\n` +
         `<b>Size:</b> ${size}\n` +
         `<b>Completed:</b> ${new Date().toLocaleString()}`;
}

// 格式化文件大小
function formatFileSize(bytes: number): string {
  if (bytes === 0) return '0 Bytes';
  
  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

// 发送 Telegram 通知的公共方法
export async function sendTelegramNotification(message: string, type: 'start' | 'complete' | 'error' = 'start') {
  try {
    // 动态导入 readSettings 避免循环依赖
    const { readSettings } = await import('./serverUtils');
    const settings = readSettings();
    const telegram = settings.telegram;
    
    // 检查 Telegram 配置是否完整
    if (!telegram || !telegram.botToken || !telegram.chatId) {
      console.log('Telegram not configured (missing botToken or chatId), skipping notification');
      return;
    }
    if (telegram.enabled === false) {
      return;
    }

    // 确保 Polling 已自恢复（不会循环，动态 import）
    try {
      const { initTelegramPolling } = await import("./telegramPolling");
      await initTelegramPolling();
    } catch {
      // 忽略，Polling 不是必须的
    }

    const bot = createTelegramBot(telegram.botToken);
    
    let emoji = 'ℹ️';
    let prefix = '';
    
    switch (type) {
      case 'start':
        emoji = '🚀';
        prefix = 'Task Started';
        break;
      case 'complete':
        emoji = '✅';
        prefix = 'Task Completed';
        break;
      case 'error':
        emoji = '❌';
        prefix = 'Task Error';
        break;
    }
    
    const formattedMessage = `${emoji} <b>${prefix}</b>\n\n${message}\n\n<b>Time:</b> ${new Date().toLocaleString()}`;
    
    await bot.sendNotification(formattedMessage, telegram.chatId);
    console.log(`Telegram notification sent: ${type}`);
  } catch (error) {
    console.error('Failed to send Telegram notification:', error);
    // 不抛出错误，避免影响主流程
  }
}

// 发送带图片的 Telegram 通知
export async function sendTelegramPhoto(chatId: string, photoUrl: string, caption: string) {
  try {
    // 动态导入 readSettings 避免循环依赖
    const { readSettings } = await import('./serverUtils');
    const settings = readSettings();
    const telegram = settings.telegram;
    
    // 检查 Telegram 配置是否完整
    if (!telegram || !telegram.botToken) {
      console.log('Telegram not configured (missing botToken), skipping photo send');
      return;
    }
    if (telegram.enabled === false) {
      return;
    }

    const bot = createTelegramBot(telegram.botToken);
    
    await bot.sendPhoto({
      chat_id: chatId,
      photo: photoUrl,
      text: caption,
      parse_mode: 'HTML'
    });
    console.log('Telegram photo sent');
  } catch (error) {
    console.error('Failed to send Telegram photo:', error);
  }
}
