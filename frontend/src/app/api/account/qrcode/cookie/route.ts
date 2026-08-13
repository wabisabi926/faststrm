// 115 扫码登录 - 换 cookie 并保存到指定账户
// 本项目独有：扫码成功后自动回填到 account.json 并触发状态检测
import { NextRequest, NextResponse } from "next/server";
import * as fs from "fs";
import * as path from "path";
import { encryptAccounts } from "@/lib/passwordCrypto";
import { readAccounts } from "@/lib/serverUtils";
import type { AccountInfo } from "@/lib/115";

const accountFile = path.resolve(process.cwd(), "../config/account.json");

function writeAccounts(data: AccountInfo[]) {
  const encrypted = encryptAccounts(JSON.parse(JSON.stringify(data)));
  fs.writeFileSync(accountFile, JSON.stringify(encrypted, null, 2), "utf-8");
}

// 强制触发指定账户的状态重检，让 STATUS_CACHE 失效并重新检测
// 这样异常恢复后能立即触发 TG 恢复通知
function triggerStatusRecheck(accountName: string) {
  try {
    const baseUrl = process.env.NEXTAUTH_URL || process.env.NEXT_PUBLIC_APP_URL || `http://localhost:${process.env.PORT || 3000}`;
    fetch(`${baseUrl}/api/account/status?names=${encodeURIComponent(accountName)}&_t=${Date.now()}`)
      .then(() => console.log(`[QRCODE-LOGIN] Triggered status recheck for ${accountName}`))
      .catch((err) => console.error(`[QRCODE-LOGIN] Status recheck failed:`, err));
  } catch (err) {
    console.error(`[QRCODE-LOGIN] Failed to trigger status recheck:`, err);
  }
}

// POST: 用 uid 换 cookie 并保存到指定账户
// Body: { uid: string, clientType: string, accountName?: string }
//   - accountName 提供：更新已有账户的 cookie（用于异常恢复）
//   - accountName 不提供：仅返回 cookie，由前端回填到表单（用于新增账户）
export async function POST(req: NextRequest) {
  try {
    const body = await req.json();
    const { uid, clientType, accountName } = body;

    if (!uid) {
      return NextResponse.json({ error: "uid 不能为空" }, { status: 400 });
    }

    const { getQrcodeResult, APP_TO_SSOENT } = await import("@/lib/115");
    const finalClientType = APP_TO_SSOENT[clientType] ? clientType : "alipaymini";

    // 换取 cookie
    const cookie = await getQrcodeResult(uid, finalClientType);

    // 如果提供了 accountName，则更新已有账户（异常恢复场景）
    if (accountName) {
      const accounts = readAccounts() as unknown as AccountInfo[];
      const idx = accounts.findIndex((a: AccountInfo) => a.name === accountName);

      if (idx === -1) {
        return NextResponse.json(
          { error: `账户 "${accountName}" 不存在` },
          { status: 404 }
        );
      }

      if (accounts[idx].accountType !== "115") {
        return NextResponse.json(
          { error: `账户 "${accountName}" 不是 115 类型，无法更新 Cookie` },
          { status: 400 }
        );
      }

      // 更新 Cookie
      accounts[idx].cookie = cookie;
      writeAccounts(accounts);
      console.log(`[QRCODE-LOGIN] Cookie updated for account: ${accountName}`);

      // 异步触发状态重检（不阻塞响应），让 TG 恢复通知能立即触发
      triggerStatusRecheck(accountName);

      return NextResponse.json({
        success: true,
        message: "Cookie 更新成功，正在重新检测状态",
        accountName,
        cookieLength: cookie.length,
      });
    }

    // 未提供 accountName：仅返回 cookie（用于新增账户场景，前端回填到表单）
    return NextResponse.json({
      success: true,
      message: "获取 Cookie 成功",
      cookie,
      cookieLength: cookie.length,
    });
  } catch (error) {
    console.error("[API/qrcode/cookie] Error:", error);
    return NextResponse.json(
      {
        error: "换取 Cookie 失败",
        details: error instanceof Error ? error.message : String(error),
      },
      { status: 500 }
    );
  }
}
