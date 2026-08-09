import { CronJob } from "cron";
import { readTasks, saveTasks } from "@/lib/serverUtils";
import { executeTask, updateTaskScheduleState } from "./taskExecutor";
import { initAccountRuntimeState } from "./accountRuntimeState";

export type ScheduleMode = "interval" | "daily" | "weekly";

export interface TaskSchedule {
  enabled: boolean;
  mode: ScheduleMode;
  intervalMinutes?: number;
  time?: string;
  weekdays?: number[];
  lastRunAt?: number;
  nextRunAt?: number;
  lastRunStatus?: "success" | "failed" | "blocked" | "catchup";
  lastRunMessage?: string;
  lastRunDurationMs?: number;
}

const TIMEZONE = "Asia/Shanghai";
const MIN_INTERVAL_MINUTES = 5;
const WEEKDAY_MAP: Record<string, number> = {
  日: 0, 日: 0, Sun: 0, sun: 0, Sunday: 0, sunday: 0,
  一: 1, Mon: 1, mon: 1, Monday: 1, monday: 1,
  二: 2, Tue: 2, tue: 2, Tuesday: 2, tuesday: 2,
  三: 3, Wed: 3, wed: 3, Wednesday: 3, wednesday: 3,
  四: 4, Thu: 4, thu: 4, Thursday: 4, thursday: 4,
  五: 5, Fri: 5, fri: 5, Friday: 5, friday: 5,
  六: 6, Sat: 6, sat: 6, Saturday: 6, saturday: 6,
};

export function scheduleToCron(schedule: TaskSchedule): string {
  if (!schedule?.enabled) return "";

  if (schedule.mode === "interval") {
    const minutes = Math.max(MIN_INTERVAL_MINUTES, schedule.intervalMinutes || MIN_INTERVAL_MINUTES);
    return `*/${minutes} * * * *`;
  }

  const [hh, mm] = (schedule.time || "03:00").split(":").map((v) => parseInt(v, 10) || 0);

  if (schedule.mode === "daily") {
    return `${mm} ${hh} * * *`;
  }

  if (schedule.mode === "weekly") {
    const dow = (schedule.weekdays && schedule.weekdays.length > 0)
      ? schedule.weekdays.join(",")
      : "1";
    return `${mm} ${hh} * * ${dow}`;
  }

  return "";
}

export function computeNextRun(schedule: TaskSchedule): number | null {
  const expr = scheduleToCron(schedule);
  if (!expr) return null;
  try {
    const job = new CronJob(expr, () => {}, null, false, TIMEZONE);
    const next = job.nextDate();
    return next?.toMillis() || null;
  } catch {
    return null;
  }
}

function getSchedulerMap(): Map<string, CronJob> {
  const g = globalThis as unknown as { __taskSchedulerJobs__?: Map<string, CronJob> };
  if (!g.__taskSchedulerJobs__) {
    g.__taskSchedulerJobs__ = new Map();
  }
  return g.__taskSchedulerJobs__;
}

let schedulerInitialized = false;
const executingIds = new Set<string>();

export function getSchedulerStatus(): {
  initialized: boolean;
  registeredCount: number;
  tasks: { taskId: string; cron: string; nextRunAt: number | null }[];
} {
  const jobs = getSchedulerMap();
  const tasks: { taskId: string; cron: string; nextRunAt: number | null }[] = [];
  for (const [taskId, job] of jobs) {
    const next = job.nextDate();
    tasks.push({ taskId, cron: job.cronTime?.toString() || "", nextRunAt: next?.toMillis() || null });
  }
  return { initialized: schedulerInitialized, registeredCount: jobs.size, tasks };
}

export async function initTaskScheduler(): Promise<void> {
  if (schedulerInitialized) return;
  schedulerInitialized = true;

  initAccountRuntimeState();

  const tasks = (readTasks() as any[]).filter(
    (t) => t.schedule?.enabled && t.schedule.mode
  );

  const now = Date.now();

  for (const task of tasks) {
    const schedule: TaskSchedule = task.schedule;
    const cronExpr = scheduleToCron(schedule);
    if (!cronExpr) continue;

    const nextRun = computeNextRun(schedule);

    const lastRunAt = schedule.lastRunAt || 0;
    const isCatchupEligible =
      lastRunAt > 0 &&
      nextRun !== null &&
      lastRunAt + _intervalMs(cronExpr) < now - 60_000;

    if (isCatchupEligible) {
      console.log(
        `[TaskScheduler] catchup for task ${task.id} (lastRun=${new Date(lastRunAt).toISOString()}, missed)`
      );
      fireTask(task.id, "catchup");
    }

    _registerJob(task.id, schedule);
  }

  console.log(
    `[TaskScheduler] initialized, ${getSchedulerMap().size} task(s) registered`
  );
}

function _intervalMs(cronExpr: string): number {
  try {
    const now = Date.now();
    const job = new CronJob(cronExpr, () => {}, null, false, TIMEZONE);
    const next = job.nextDate();
    const nextMs = next?.toMillis() || now + 3600_000;
    return nextMs - now;
  } catch {
    return 3600_000;
  }
}

function _registerJob(taskId: string, schedule: TaskSchedule): void {
  const jobs = getSchedulerMap();

  if (jobs.has(taskId)) {
    const existing = jobs.get(taskId)!;
    try { existing.stop(); } catch {}
    jobs.delete(taskId);
  }

  const cronExpr = scheduleToCron(schedule);
  if (!cronExpr) return;

  const job = new CronJob(
    cronExpr,
    () => fireTask(taskId, "schedule"),
    null,
    false,
    TIMEZONE
  );

  job.start();
  jobs.set(taskId, job);

  const next = computeNextRun(schedule);
  if (next) {
    const tasks = readTasks() as any[];
    const idx = tasks.findIndex((t) => t.id === taskId);
    if (idx !== -1) {
      if (!tasks[idx].schedule) tasks[idx].schedule = {};
      tasks[idx].schedule.nextRunAt = next;
      saveTasks(tasks);
    }
  }

  console.log(
    `[TaskScheduler] registered task ${taskId} cron=${cronExpr} nextRun=${next ? new Date(next).toISOString() : "?"}`
  );
}

function _unregisterJob(taskId: string): void {
  const jobs = getSchedulerMap();
  const job = jobs.get(taskId);
  if (job) {
    try { job.stop(); } catch {}
    jobs.delete(taskId);
    console.log(`[TaskScheduler] unregistered task ${taskId}`);
  }
}

async function fireTask(taskId: string, trigger: "schedule" | "catchup"): Promise<void> {
  if (executingIds.has(taskId)) {
    console.log(`[TaskScheduler] task ${taskId} is already executing, skip`);
    return;
  }
  executingIds.add(taskId);

  const startMs = Date.now();
  const existing = (readTasks() as any[]).find((t) => t.id === taskId);
  if (!existing?.schedule?.enabled) {
    executingIds.delete(taskId);
    return;
  }

  try {
    const result = await executeTask(taskId, { trigger });
    const durationMs = Date.now() - startMs;

    const next = computeNextRun(existing.schedule);
    const lastRunStatus: TaskSchedule["lastRunStatus"] =
      result.blocked
        ? "blocked"
        : result.success
        ? "success"
        : trigger === "catchup" && result.success
        ? "catchup"
        : "failed";

    updateTaskScheduleState(taskId, {
      lastRunAt: Date.now(),
      lastRunStatus,
      lastRunMessage: result.message,
      lastRunDurationMs: durationMs,
      nextRunAt: next || undefined,
    });

    const label = trigger === "catchup" ? "[catchup]" : "[schedule]";
    if (result.blocked) {
      console.log(
        `[TaskScheduler] ${label} task ${taskId} blocked (account busy). nextRun=${next ? new Date(next).toISOString() : "?"}`
      );
    } else {
      console.log(
        `[TaskScheduler] ${label} task ${taskId} ${result.success ? "OK" : "FAIL"} ${durationMs}ms. nextRun=${next ? new Date(next).toISOString() : "?"}`
      );
    }
  } catch (err) {
    const durationMs = Date.now() - startMs;
    const next = computeNextRun(existing.schedule);
    updateTaskScheduleState(taskId, {
      lastRunAt: Date.now(),
      lastRunStatus: "failed",
      lastRunMessage: err instanceof Error ? err.message : String(err),
      lastRunDurationMs: durationMs,
      nextRunAt: next || undefined,
    });
    console.error(`[TaskScheduler] ${trigger} task ${taskId} crashed:`, err);
  } finally {
    executingIds.delete(taskId);
  }
}

export function registerTaskSchedule(taskId: string): void {
  if (!schedulerInitialized) return;
  const tasks = (readTasks() as any[]).filter((t) => t.id === taskId);
  if (tasks.length === 0) return;
  const task = tasks[0];
  if (!task.schedule?.enabled || !task.schedule.mode) {
    _unregisterJob(taskId);
    return;
  }
  _registerJob(taskId, task.schedule);
}

export function unregisterTaskSchedule(taskId: string): void {
  if (!schedulerInitialized) return;
  _unregisterJob(taskId);
}

export function refreshAllSchedules(): void {
  if (!schedulerInitialized) return;
  const jobs = getSchedulerMap();
  const tasks = readTasks() as any[];

  for (const [taskId] of jobs) {
    if (!tasks.find((t) => t.id === taskId && t.schedule?.enabled)) {
      _unregisterJob(taskId);
    }
  }

  for (const task of tasks) {
    if (task.schedule?.enabled && task.schedule.mode) {
      _registerJob(task.id, task.schedule);
    }
  }
}

export function isSchedulerRunning(): boolean {
  return schedulerInitialized;
}

export function getNextRunPreview(schedule: TaskSchedule): {
  nextRunAt: number | null;
  cron: string;
} {
  const cron = scheduleToCron(schedule);
  return { cron, nextRunAt: computeNextRun(schedule) };
}
