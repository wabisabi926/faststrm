import * as fs from "fs";
import * as path from "path";

export interface AccountRuntimeState {
  activeTaskId?: string;
  activeTaskStartAt?: number;
  monitorSuspendedUntil?: number;
  monitorSuspendedBy?: "fullscan";
}

const RUNTIME_FILE = path.join(process.cwd(), "../config/runtime.json");
const FULLSCAN_TIMEOUT_MS = 10 * 60 * 1000; // 10 分钟（之前 1 小时太长，对账后异常会导致监控挂起过久）
const MONITOR_RESUME_GRACE_MS = 5 * 60 * 1000;

const MEM_STATE: Map<string, AccountRuntimeState> = new Map();
let initialized = false;

function readRuntimeFile(): Record<string, AccountRuntimeState> {
  try {
    if (!fs.existsSync(RUNTIME_FILE)) return {};
    return JSON.parse(fs.readFileSync(RUNTIME_FILE, "utf-8"));
  } catch {
    return {};
  }
}

function writeRuntimeFile() {
  try {
    const obj = Object.fromEntries(MEM_STATE.entries());
    const dir = path.dirname(RUNTIME_FILE);
    if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(RUNTIME_FILE, JSON.stringify(obj, null, 2), "utf-8");
  } catch (err) {
    console.error("[AccountRuntime] write failed:", err);
  }
}

export function initAccountRuntimeState() {
  if (initialized) return;
  initialized = true;

  const persisted = readRuntimeFile();
  const now = Date.now();
  let needPersistUpdate = false;

  for (const [account, state] of Object.entries(persisted)) {
    const cleaned: AccountRuntimeState = {};

    if (
      state.activeTaskId &&
      state.activeTaskStartAt &&
      now - state.activeTaskStartAt < FULLSCAN_TIMEOUT_MS
    ) {
      cleaned.activeTaskId = state.activeTaskId;
      cleaned.activeTaskStartAt = state.activeTaskStartAt;
    } else if (state.activeTaskId) {
      console.log(
        `[AccountRuntime] cleanup stale fullscan lock for ${account} (activeTaskId=${state.activeTaskId})`
      );
      needPersistUpdate = true;
    }

    if (state.monitorSuspendedUntil && now < state.monitorSuspendedUntil) {
      cleaned.monitorSuspendedUntil = state.monitorSuspendedUntil;
      cleaned.monitorSuspendedBy = state.monitorSuspendedBy;
    }

    if (Object.keys(cleaned).length > 0) {
      MEM_STATE.set(account, cleaned);
    }
  }

  if (needPersistUpdate || Object.keys(persisted).length !== MEM_STATE.size) {
    writeRuntimeFile();
  }

  console.log(
    `[AccountRuntime] initialized, ${MEM_STATE.size} account(s) with runtime state`
  );
}

export function getAccountRuntimeState(account: string): AccountRuntimeState {
  initAccountRuntimeState();
  return MEM_STATE.get(account) || {};
}

export function getAllRuntimeStates(): Record<string, AccountRuntimeState> {
  initAccountRuntimeState();
  return Object.fromEntries(MEM_STATE.entries());
}

export function isAccountInFullScan(account: string): boolean {
  const s = getAccountRuntimeState(account);
  if (!s.activeTaskId || !s.activeTaskStartAt) return false;
  return Date.now() - s.activeTaskStartAt < FULLSCAN_TIMEOUT_MS;
}

export function isMonitorSuspended(account: string): boolean {
  const s = getAccountRuntimeState(account);
  return !!(s.monitorSuspendedUntil && Date.now() < s.monitorSuspendedUntil);
}

export interface EnterFullScanResult {
  ok: boolean;
  reason?: "task_running";
}

export function tryEnterFullScan(
  account: string,
  taskId: string
): EnterFullScanResult {
  initAccountRuntimeState();

  const existing = MEM_STATE.get(account);
  if (existing?.activeTaskId) {
    const elapsed = Date.now() - (existing.activeTaskStartAt || 0);
    if (elapsed < FULLSCAN_TIMEOUT_MS) {
      return { ok: false, reason: "task_running" };
    }
  }

  const state: AccountRuntimeState = {
    activeTaskId: taskId,
    activeTaskStartAt: Date.now(),
  };

  const prev = MEM_STATE.get(account);
  if (prev?.monitorSuspendedUntil && Date.now() < prev.monitorSuspendedUntil) {
    state.monitorSuspendedUntil = prev.monitorSuspendedUntil;
    state.monitorSuspendedBy = prev.monitorSuspendedBy;
  }

  MEM_STATE.set(account, state);
  writeRuntimeFile();

  console.log(`[AccountRuntime] fullscan enter: account=${account} taskId=${taskId}`);
  return { ok: true };
}

export function exitFullScan(account: string): void {
  initAccountRuntimeState();
  const current = MEM_STATE.get(account);
  if (!current?.activeTaskId) return;

  const state: AccountRuntimeState = {};

  if (current.monitorSuspendedUntil && Date.now() < current.monitorSuspendedUntil) {
    const resumeGrace = Date.now() + MONITOR_RESUME_GRACE_MS;
    state.monitorSuspendedUntil = Math.min(
      current.monitorSuspendedUntil,
      resumeGrace
    );
    state.monitorSuspendedBy = current.monitorSuspendedBy;
  }

  if (Object.keys(state).length === 0) {
    MEM_STATE.delete(account);
  } else {
    MEM_STATE.set(account, state);
  }
  writeRuntimeFile();

  console.log(`[AccountRuntime] fullscan exit: account=${account}`);
}

export function suspendMonitorForFullScan(account: string): void {
  initAccountRuntimeState();
  const state = MEM_STATE.get(account) || {};
  state.monitorSuspendedUntil = Date.now() + FULLSCAN_TIMEOUT_MS;
  state.monitorSuspendedBy = "fullscan";
  MEM_STATE.set(account, state);
  writeRuntimeFile();
  console.log(`[AccountRuntime] monitor suspended: account=${account}`);
}

export function tryPollMonitor(account: string): {
  ok: boolean;
  suspendedUntil?: number;
} {
  initAccountRuntimeState();
  const state = MEM_STATE.get(account);
  if (state?.monitorSuspendedUntil && Date.now() < state.monitorSuspendedUntil) {
    return { ok: false, suspendedUntil: state.monitorSuspendedUntil };
  }
  return { ok: true };
}

export function clearMonitorSuspend(account: string): void {
  initAccountRuntimeState();
  const current = MEM_STATE.get(account);
  if (!current?.monitorSuspendedUntil) return;
  const state: AccountRuntimeState = { ...current };
  delete state.monitorSuspendedUntil;
  delete state.monitorSuspendedBy;

  if (!state.activeTaskId) {
    MEM_STATE.delete(account);
  } else {
    MEM_STATE.set(account, state);
  }
  writeRuntimeFile();
}

export function touchFullScanHeartbeat(account: string): void {
  initAccountRuntimeState();
  const state = MEM_STATE.get(account);
  if (state?.activeTaskId) {
    state.activeTaskStartAt = Date.now();
    writeRuntimeFile();
  }
}
