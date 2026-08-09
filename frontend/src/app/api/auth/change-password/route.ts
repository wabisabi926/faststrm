import { NextResponse, NextRequest } from "next/server";
import { readConfig, writeConfig, verifyPassword, hashPassword } from "@/lib/passwordCrypto";

export async function POST(req: NextRequest) {
  const { currentPassword, newPassword } = await req.json();

  if (!currentPassword || !newPassword) {
    return NextResponse.json({ error: "请填写当前密码和新密码" }, { status: 400 });
  }

  if (newPassword.length < 6) {
    return NextResponse.json({ error: "新密码至少 6 位" }, { status: 400 });
  }

  const config = readConfig();

  // 验证当前密码
  if (!verifyPassword(currentPassword, config.password)) {
    return NextResponse.json({ error: "当前密码错误" }, { status: 401 });
  }

  // 更新为哈希密码
  config.password = hashPassword(newPassword);
  writeConfig(config);

  return NextResponse.json({ message: "密码修改成功" });
}
