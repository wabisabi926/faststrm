import { NextRequest, NextResponse } from "next/server";
import * as fs from "fs";
import * as path from "path";
import { encryptAccounts } from "@/lib/passwordCrypto";
import { readAccounts } from "@/lib/serverUtils";
import { syncMediaMountPaths } from "@/lib/mediaMountSync";
import type { AccountInfo } from "@/lib/115";

const accountFile = path.resolve(process.cwd(), "../config/account.json");

function writeAccounts(data: AccountInfo[]) {
  // 写入前加密敏感字段
  const encrypted = encryptAccounts(JSON.parse(JSON.stringify(data)));
  fs.writeFileSync(accountFile, JSON.stringify(encrypted, null, 2), "utf-8");
}

type AccountPayload = Partial<AccountInfo> & {
  accountType?: string;
  name?: string;
  cookie?: string;
  account?: string;
  password?: string;
  url?: string;
};

// GET: 获取所有账号（返回解密后的数据）
export async function GET() {
  return NextResponse.json(readAccounts());
}

// POST: 新建账号（基于 name 唯一）
export async function POST(req: NextRequest) {
  const body = (await req.json()) as AccountPayload;
  const { accountType, name } = body;

  if (!accountType || !name) {
    return NextResponse.json(
      { error: "accountType and name are required" },
      { status: 400 }
    );
  }

  // 根据账户类型验证必需字段
  if (accountType === "115") {
    const { cookie } = body;
    if (!cookie) {
      return NextResponse.json(
        { error: "cookie is required for 115 accounts" },
        { status: 400 }
      );
    }
  } else if (accountType === "openlist") {
    const { account, password, url } = body;
    if (!account || !password || !url) {
      return NextResponse.json(
        { error: "account, password, and url are required for openlist accounts" },
        { status: 400 }
      );
    }
  }

  const accounts = readAccounts() as AccountInfo[];
  if (accounts.find((a: AccountInfo) => a.name === name)) {
    return NextResponse.json({ error: "Account name already exists" }, { status: 400 });
  }

  // 创建新账户，只包含提供的字段
  const newAccount: AccountInfo = { accountType, name, ...body } as AccountInfo;
  delete (newAccount as unknown as AccountPayload).accountType; // 避免重复
  delete (newAccount as unknown as AccountPayload).name; // 避免重复
  const finalAccount: AccountInfo = { accountType, name, ...newAccount } as AccountInfo;

  accounts.push(finalAccount);
  writeAccounts(accounts);
  const mediaMountSync = await syncMediaMountPaths();

  return NextResponse.json({ ...finalAccount, mediaMountSync }, { status: 201 });
}

// PUT: 更新账号（基于 name）
export async function PUT(req: NextRequest) {
  const body = (await req.json()) as AccountPayload;
  const { name, accountType } = body;

  if (!name) {
    return NextResponse.json({ error: "name is required" }, { status: 400 });
  }

  // 根据账户类型验证必需字段（如果提供了 accountType）
  if (accountType) {
    if (accountType === "115") {
      const { cookie } = body;
      if (!cookie) {
        return NextResponse.json(
          { error: "cookie is required for 115 accounts" },
          { status: 400 }
        );
      }
    } else if (accountType === "openlist") {
      const { account, password, url } = body;
      if (!account || !password || !url) {
        return NextResponse.json(
          { error: "account, password, and url are required for openlist accounts" },
          { status: 400 }
        );
      }
    }
  }

  const accounts = readAccounts() as AccountInfo[];
  const idx = accounts.findIndex((a: AccountInfo) => a.name === name);

  if (idx === -1) {
    return NextResponse.json({ error: "Account not found" }, { status: 404 });
  }

  accounts[idx] = { ...accounts[idx], ...body };
  writeAccounts(accounts);
  const mediaMountSync = await syncMediaMountPaths();

  return NextResponse.json({ ...accounts[idx], mediaMountSync });
}

// DELETE: 删除账号（基于 name）
export async function DELETE(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const name = searchParams.get("name");

  if (!name) {
    return NextResponse.json({ error: "Missing name" }, { status: 400 });
  }

  const accounts = readAccounts() as AccountInfo[];
  const newAccounts = accounts.filter((a: AccountInfo) => a.name !== name);

  if (newAccounts.length === accounts.length) {
    return NextResponse.json({ error: "Account not found" }, { status: 404 });
  }

  writeAccounts(newAccounts);
  const mediaMountSync = await syncMediaMountPaths();
  return NextResponse.json({ message: "Account deleted", mediaMountSync });
}
