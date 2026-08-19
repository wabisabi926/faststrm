/**
 * Lightweight browser-compatible logger.
 * Replaces server-side console logging with simple prefixed output.
 */

function timestamp(): string {
  const d = new Date();
  const pad = (n: number) => n.toString().padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export const logger = {
  debug(...args: unknown[]): void {
    const prefix = `%c[DEBUG ${timestamp()}]`;
    console.log(prefix, "color: #888", ...args);
  },

  info(...args: unknown[]): void {
    const prefix = `[INFO ${timestamp()}]`;
    console.log(prefix, ...args);
  },

  warn(...args: unknown[]): void {
    const prefix = `[WARN ${timestamp()}]`;
    console.warn(prefix, ...args);
  },

  error(...args: unknown[]): void {
    const prefix = `[ERROR ${timestamp()}]`;
    console.error(prefix, ...args);
  },

  always(...args: unknown[]): void {
    const prefix = `[${timestamp()}]`;
    console.log(prefix, ...args);
  },
};

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

export function createTraceId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
