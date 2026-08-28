// 115 账号 fixture：与 pages/account/index.tsx 的 Account 类型对齐。
// 详见 v1.1.1 改进任务清单 T2。

export interface AccountFixture {
  accountType: string;
  name: string;
  cookie?: string;
  account?: string;
  password?: string;
  url?: string;
  token?: string;
  expiresAt?: number;
  lastCookieCheck?: number;
  cookieValid?: boolean;
}

// 单个 115 账号（cookie 有效）
export const valid115Account: AccountFixture = {
  accountType: "115",
  name: "main-115",
  cookie: "UID=123456&CID=abc&SEID=def&KID=xyz",
  expiresAt: 1735689600000, // 2025-01-01
  lastCookieCheck: 1735603200000,
  cookieValid: true,
};

// 单个 115 账号（cookie 已失效）
export const invalid115Account: AccountFixture = {
  accountType: "115",
  name: "expired-115",
  cookie: "UID=expired&CID=old",
  expiresAt: 1704067200000, // 2024-01-01
  lastCookieCheck: 1704067200000,
  cookieValid: false,
};

// openlist 类型账号
export const openlistAccount: AccountFixture = {
  accountType: "openlist",
  name: "openlist-main",
  account: "admin",
  password: "secret",
  url: "http://openlist.local:5244",
};

// 账号列表（GET /api/account 返回）
export const accountList: AccountFixture[] = [
  valid115Account,
  invalid115Account,
  openlistAccount,
];

// 仅 115 类型账号（emby-notify.tsx 的 loadAccounts 过滤后结果）
export const only115Accounts: string[] = ["main-115", "expired-115"];

// 账号状态（GET /api/account/status 返回的 results）
export interface AccountStatusFixture {
  name: string;
  status: "ok" | "error" | "unknown";
  message?: string;
}

export const accountStatusList: AccountStatusFixture[] = [
  { name: "main-115", status: "ok" },
  { name: "expired-115", status: "error", message: "Cookie 已失效" },
  { name: "openlist-main", status: "unknown" },
];

// 扫码登录 Step 1 二维码响应
export const qrCodeStep1Response = {
  code: 200,
  data: {
    uid: "qrcode-uid-12345",
    qrcode: "https://qrcodeapi.115.com/api/1.0/web/1.2/qrcode?uid=qrcode-uid-12345",
    signature: "sig-abc",
  },
};

// 扫码登录 Step 2 状态轮询：等待扫描
export const qrCodeStep2Waiting = {
  code: 200,
  data: { status: 2, msg: "等待扫码" },
};

// 扫码登录 Step 2 状态轮询：已扫描，等待确认
export const qrCodeStep2Scanned = {
  code: 200,
  data: { status: 4, msg: "已扫描，等待确认" },
};

// 扫码登录 Step 2 状态轮询：已确认
export const qrCodeStep2Confirmed = {
  code: 200,
  data: { status: 7, msg: "已确认", uid: "115-user-uid" },
};

// 扫码登录 Step 3 换取 Cookie 成功响应
export const qrCodeStep3Success = {
  code: 200,
  data: {
    cookie: "UID=115-user-uid&CID=newcid&SEID=newseid&KID=newkid",
    expiresAt: 1765132800000, // 2026-01-01
  },
};

// 保存账号失败响应（后端返回具体错误）
export const saveAccountError = {
  code: 400,
  message: "账号名称已存在",
};
