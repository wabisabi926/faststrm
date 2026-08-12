import { NextRequest, NextResponse } from "next/server";
import { readAccounts, readSettings } from "@/lib/serverUtils";
import { fs_files, type AccountInfo } from "@/lib/115";
import { sendTelegramNotification } from "@/lib/telegram";
import axios from "axios";

type AccountStatus = {
  name: string;
  status: "ok" | "error" | "unknown";
  message?: string;
};

const STATUS_CACHE = new Map<string, { status: AccountStatus["status"]; message?: string; ts: number }>();
const CACHE_TTL_MS = 5 * 60 * 1000;

// 状态变化历史追踪：记录上一次的检测结果，用于判断状态是否发生变化
const STATUS_HISTORY = new Map<string, { status: AccountStatus["status"]; updatedAt: number }>();

/**
 * 发送账户状态变化通知
 */
async function sendStatusChangeNotification(
  accountName: string,
  newStatus: AccountStatus["status"],
  message?: string
): Promise<void> {
  try {
    const settings = readSettings();
    const alerts = settings.telegram?.accountAlerts;

    // 检查是否启用了账户状态通知
    if (!alerts?.enabled || settings.telegram?.enabled === false) {
      return;
    }

    let shouldNotify = false;
    let notificationType: "start" | "complete" | "error" = "start";
    let emoji = "";
    let prefix = "";

    switch (newStatus) {
      case "error":
        if (alerts.onError !== false) {
          shouldNotify = true;
          notificationType = "error";
          emoji = "❌";
          prefix = "账号异常";
        }
        break;
      case "ok":
        // 只有从 error 恢复到 ok 时才通知
        const prevStatus = STATUS_HISTORY.get(accountName)?.status;
        if (prevStatus === "error" && alerts.onRecover !== false) {
          shouldNotify = true;
          notificationType = "complete";
          emoji = "✅";
          prefix = "账号恢复";
        }
        break;
      case "unknown":
      default:
        break;
    }

    if (shouldNotify) {
      const notificationMessage = `${emoji} <b>${prefix}</b>\n\n` +
        `<b>账号：</b> ${accountName}\n` +
        `<b>状态：</b> ${newStatus === "error" ? "异常" : "正常"}\n` +
        (message ? `<b>原因：</b> ${message}\n` : "") +
        `<b>时间：</b> ${new Date().toLocaleString()}`;

      await sendTelegramNotification(notificationMessage, notificationType);
      console.log(`[ACCOUNT-STATUS] Sent ${prefix} notification for ${accountName}`);
    }
  } catch (err) {
    console.error(`[ACCOUNT-STATUS] Failed to send notification for ${accountName}:`, err);
  }
}

async function check115Account(account: AccountInfo): Promise<AccountStatus> {
  try {
    console.log(`[ACCOUNT-STATUS] Checking 115 account: ${account.name}, hasCookie: ${!!account.cookie}, cookieLen: ${account.cookie?.length || 0}`);
    if (!account.cookie) {
      return { name: account.name, status: "error", message: "Cookie 为空" };
    }
    // fs_files 内部会通过 ensureOk 检查错误，如果 cookie 无效会抛出异常
    const resp = await fs_files(0, { accountInfo: account, limit: 1 });
    console.log(`[ACCOUNT-STATUS] Response for ${account.name}: hasData=${!!resp?.data?.length}`);
    return { name: account.name, status: "ok", message: "Cookie 有效" };
  } catch (err: unknown) {
    console.error(`[ACCOUNT-STATUS] Error for ${account.name}:`, err);
    const msg = err instanceof Error ? err.message : String(err);
    if (msg.includes("401") || msg.includes("cookie") || msg.includes("errno") || msg.includes("API error")) {
      return { name: account.name, status: "error", message: "Cookie 已过期" };
    }
    return { name: account.name, status: "error", message: msg || "检测失败" };
  }
}

async function checkOpenListAccount(account: AccountInfo): Promise<AccountStatus> {
  try {
    const url = account.url || "";
    const resp = await axios.get(`${url}/api/v3/files`, {
      params: { username: account.account, password: account.password, limit: 1 },
      timeout: 10000,
    });
    if (resp.status === 200) {
      return { name: account.name, status: "ok", message: "连接正常" };
    }
    return { name: account.name, status: "error", message: `HTTP ${resp.status}` };
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    return { name: account.name, status: "error", message: msg || "连接失败" };
  }
}

export async function GET(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const namesParam = searchParams.get("names");

  const accounts = readAccounts() as unknown as AccountInfo[];

  let targetAccounts = accounts;
  if (namesParam) {
    const names = namesParam.split(",").map((n) => n.trim()).filter(Boolean);
    targetAccounts = accounts.filter((a) => names.includes(a.name));
  }

  const results: AccountStatus[] = [];
  const toCheck: { account: AccountInfo; force: boolean }[] = [];

  for (const account of targetAccounts) {
    const cached = STATUS_CACHE.get(account.name);
    if (cached && Date.now() - cached.ts < CACHE_TTL_MS) {
      results.push({ name: account.name, status: cached.status, message: cached.message });
    } else {
      toCheck.push({ account, force: true });
    }
  }

  if (toCheck.length > 0) {
    const checks = toCheck.map(async ({ account }) => {
      const result =
        account.accountType === "115"
          ? await check115Account(account)
          : account.accountType === "openlist"
          ? await checkOpenListAccount(account)
          : { name: account.name, status: "unknown" as const, message: "未知账户类型" };
      STATUS_CACHE.set(account.name, { status: result.status, message: result.message, ts: Date.now() });
      return result;
    });

    const freshResults = await Promise.allSettled(checks);
    for (let i = 0; i < freshResults.length; i++) {
      const r = freshResults[i];
      let result: AccountStatus;

      if (r.status === "fulfilled") {
        result = r.value;
      } else {
        result = {
          name: toCheck[i].account.name,
          status: "error",
          message: r.reason?.message || "检测异常",
        };
      }

      // 检查状态是否发生变化，如果变化则发送通知
      const prevHistory = STATUS_HISTORY.get(result.name);
      if (!prevHistory || prevHistory.status !== result.status) {
        // 状态发生变化，发送通知
        await sendStatusChangeNotification(result.name, result.status, result.message);
        // 更新状态历史
        STATUS_HISTORY.set(result.name, { status: result.status, updatedAt: Date.now() });
      }

      results.push(result);
    }
  }

  results.sort((a, b) => a.name.localeCompare(b.name));

  return NextResponse.json({ results, checkedAt: Date.now() });
}
