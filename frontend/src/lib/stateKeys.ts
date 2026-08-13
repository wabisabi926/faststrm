/**
 * globalThis 状态 Key 常量
 *
 * 这些 Key 用于在 HMR (Hot Module Replacement) 期间持久化服务端状态。
 * Next.js dev 模式下每次模块更新会重置模块级变量，
 * 通过挂载到 globalThis 可以保持跨 HMR 的状态连续性。
 *
 * 使用的模块：
 *   - __telegramPolling: Telegram 轮询管理器 (lib/telegramPolling.ts)
 *   - __accountRuntimeInitialized: 账号运行时状态 (lib/accountRuntimeState.ts)
 *   - __taskSchedulerJobs__: 任务调度器 CronJob 注册表 (lib/taskScheduler.ts)
 *   - __taskSchedulerInitialized: 任务调度器初始化标志 (lib/taskScheduler.ts)
 */

export const STATE_KEYS = {
  TELEGRAM_POLLING: '__telegramPolling',
  ACCOUNT_RUNTIME_INITIALIZED: '__accountRuntimeInitialized',
  TASK_SCHEDULER_JOBS: '__taskSchedulerJobs__',
  TASK_SCHEDULER_INITIALIZED: '__taskSchedulerInitialized',
} as const;

export type StateKey = (typeof STATE_KEYS)[keyof typeof STATE_KEYS];
