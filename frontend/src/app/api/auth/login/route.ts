import { NextResponse, NextRequest } from "next/server";
import { generateToken } from "@/lib/jwt";
import { readConfig, verifyPassword, migrateAllCredentials } from "@/lib/passwordCrypto";

export async function POST(req: NextRequest) {
  const { username, password } = await req.json();

  // 自动迁移所有明文凭据（登录密码 + 115 cookie + openlist + emby apiKey）
  migrateAllCredentials();

  const configData = readConfig();

  if (username === configData.username && verifyPassword(password, configData.password)) {
    // 生成JWT token
    const token = await generateToken(username);

    console.log("Login successful, token generated for user:", username);

    return NextResponse.json({
      message: "登录成功",
      token,
      user: { username }
    });
  }

  return NextResponse.json({ error: "账号或密码错误" }, { status: 401 });
}
