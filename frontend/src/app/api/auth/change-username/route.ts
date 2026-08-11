import { NextResponse, NextRequest } from "next/server";
import { readConfig, writeConfig, verifyPassword } from "@/lib/passwordCrypto";

const USERNAME_MIN_LEN = 3;
const USERNAME_MAX_LEN = 32;
const USERNAME_REGEX = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

export async function POST(req: NextRequest) {
  try {
    const { currentPassword, newUsername } = await req.json();

    if (!currentPassword || !newUsername) {
      return NextResponse.json({ error: "请填写所有字段" }, { status: 400 });
    }

    const username = String(newUsername).trim();

    if (username.length < USERNAME_MIN_LEN || username.length > USERNAME_MAX_LEN) {
      return NextResponse.json(
        { error: `用户名长度需在 ${USERNAME_MIN_LEN}-${USERNAME_MAX_LEN} 位之间` },
        { status: 400 }
      );
    }

    if (!USERNAME_REGEX.test(username)) {
      return NextResponse.json(
        { error: "用户名只能包含字母、数字和下划线，且以字母或下划线开头" },
        { status: 400 }
      );
    }

    if (/^\d+$/.test(username)) {
      return NextResponse.json(
        { error: "用户名不能为纯数字" },
        { status: 400 }
      );
    }

    const config = readConfig();

    if (!verifyPassword(currentPassword, config.password)) {
      return NextResponse.json({ error: "当前密码错误" }, { status: 401 });
    }

    if (username === config.username) {
      return NextResponse.json(
        { error: "新用户名不能与当前用户名相同" },
        { status: 400 }
      );
    }

    config.username = username;
    writeConfig(config);

    return NextResponse.json({ message: "用户名修改成功，请重新登录" });
  } catch (err) {
    console.error("[Auth] change-username failed:", err);
    return NextResponse.json({ error: "服务器内部错误" }, { status: 500 });
  }
}
