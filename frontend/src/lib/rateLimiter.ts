/**
 * rateLimiter.ts — 简易令牌桶限流器
 *
 * 用于限制 115 API 调用频率，避免触发封控。
 * 参考项目 p115strmhelper 的 RateLimiter 设计。
 */

/** 令牌桶限流器 */
export class RateLimiter {
  private tokens: number;
  private maxTokens: number;
  private refillRate: number; // 每秒补充的令牌数
  private lastRefillTime: number;
  private waitQueue: Array<() => void> = [];

  constructor(maxTokensPerMinute: number = 60) {
    this.maxTokens = maxTokensPerMinute;
    this.tokens = maxTokensPerMinute;
    this.refillRate = maxTokensPerMinute / 60; // 每秒令牌数
    this.lastRefillTime = Date.now();
  }

  private refill() {
    const now = Date.now();
    const elapsed = (now - this.lastRefillTime) / 1000;
    this.tokens = Math.min(this.maxTokens, this.tokens + elapsed * this.refillRate);
    this.lastRefillTime = now;
  }

  /** 获取一个令牌，若不足则等待 */
  async acquire(): Promise<void> {
    this.refill();
    if (this.tokens >= 1) {
      this.tokens -= 1;
      return;
    }
    // 等待令牌恢复
    return new Promise<void>((resolve) => {
      this.waitQueue.push(resolve);
      this.scheduleRefill();
    });
  }

  private scheduleRefill() {
    const check = () => {
      this.refill();
      while (this.tokens >= 1 && this.waitQueue.length > 0) {
        this.tokens -= 1;
        const resolve = this.waitQueue.shift()!;
        resolve();
      }
      if (this.waitQueue.length > 0) {
        setTimeout(check, 100);
      }
    };
    setTimeout(check, 100);
  }

  /** 获取当前可用令牌数 */
  get availableTokens(): number {
    this.refill();
    return Math.floor(this.tokens);
  }
}

// 全局实例：115 API 限流器（每分钟 60 次 = 每秒 1 次）
const api115RateLimiter = new RateLimiter(60);

/** 等待 115 API 令牌 */
export async function waitFor115ApiToken(): Promise<void> {
  await api115RateLimiter.acquire();
}

/** 获取全局 115 限流器实例 */
export function get115RateLimiter(): RateLimiter {
  return api115RateLimiter;
}
