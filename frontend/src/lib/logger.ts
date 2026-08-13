/**
 * Lightweight structured logger for Next.js server-side code.
 *
 * Levels:
 *   debug  - 仅在 DEBUG 环境变量设置时输出（灰色）
 *   info   - dev 环境输出（白色），prod 静默
 *   warn   - dev + prod 都输出（黄色）
 *   error  - dev + prod 都输出（红色）
 *
 * Usage:
 *   import { logger } from '@/lib/logger';
 *   logger.info('Server started');
 *   logger.warn('Config missing');
 *   logger.error(err);
 *   logger.debug('Cache hit:', key);
 */

const IS_DEV = process.env.NODE_ENV !== 'production';
const IS_DEBUG = IS_DEV && process.env.DEBUG === '1';

function timestamp(): string {
  const d = new Date();
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export const logger = {
  debug(...args: unknown[]): void {
    if (!IS_DEBUG) return;
    const prefix = `\x1b[36m[DEBUG ${timestamp()}]\x1b[0m`;
    console.log(prefix, ...args);
  },

  info(...args: unknown[]): void {
    if (!IS_DEV) return;
    const prefix = `[INFO ${timestamp()}]`;
    console.log(prefix, ...args);
  },

  warn(...args: unknown[]): void {
    const prefix = `\x1b[33m[WARN ${timestamp()}]\x1b[0m`;
    console.warn(prefix, ...args);
  },

  error(...args: unknown[]): void {
    const prefix = `\x1b[31m[ERROR ${timestamp()}]\x1b[0m`;
    console.error(prefix, ...args);
  },

  /** 无论 dev/prod 都输出（用于关键业务日志，如发送通知、任务完成） */
  always(...args: unknown[]): void {
    const prefix = `[${timestamp()}]`;
    console.log(prefix, ...args);
  },
};

/** 高频路径的日志包装，内部做节流，避免刷屏 */
export function createThrottledLogger(intervalMs: number = 5000) {
  const lastCall = new Map<string, number>();
  return {
    log(key: string, ...args: unknown[]): void {
      const now = Date.now();
      const last = lastCall.get(key) ?? 0;
      if (now - last >= intervalMs) {
        lastCall.set(key, now);
        logger.info(...args);
      }
    },
  };
}

/** 生成唯一追踪 ID，用于请求链路追踪 */
export function createTraceId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
