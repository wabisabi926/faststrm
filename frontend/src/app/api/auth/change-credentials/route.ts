import { NextResponse, NextRequest } from "next/server";
import { readConfig, writeConfig, verifyPassword } from "@/lib/passwordCrypto";

const USERNAME_MIN_LEN = 3;
const USERNAME_MAX_LEN = 32;
const USERNAME_REGEX = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

export async function POST(req: NextRequest) {
  try {
    const { currentPassword, newUsername, newPassword, confirmPassword } =
      await req.json();

    if (!currentPassword) {
      return NextResponse.json({ error: "请输入当前密码" }, { status: 400 });
    }

    const config = readConfig();

    if (!verifyPassword(currentPassword, config.password)) {
      return NextResponse.json({ error: "当前密码错误" }, { status: 401 });
    }

    const usernameInput =
      typeof newUsername === "string" ? newUsername.trim() : "";
    const passwordInput =
      typeof newPassword === "string" ? newPassword : "";
    const confirmInput =
      typeof confirmPassword === "string" ? confirmPassword : "";

    const changes: string[] = [];

    // 处理用户名修改
    if (usernameInput) {
      if (
        usernameInput.length < USERNAME_MIN_LEN ||
        usernameInput.length > USERNAME_MAX_LEN
      ) {
        return NextResponse.json(
          {
            error: `用户名长度需在 ${USERNAME_MIN_LEN}-${USERNAME_MAX_LEN} 位之间`,
          },
          { status: 400 }
        );
      }
      if (!USERNAME_REGEX.test(usernameInput)) {
        return NextResponse.json(
          {
            error:
              "用户名只能包含字母、数字和下划线，且以字母或下划线开头",
          },
          { status: 400 }
        );
      }
      if (/^\d+$/.test(usernameInput)) {
        return NextResponse.json(
          { error: "用户名不能为纯数字" },
          { status: 400 }
        );
      }
      if (usernameInput === config.username) {
        return NextResponse.json(
          { error: "新用户名不能与当前用户名相同" },
          { status: 400 }
        );
      }
      config.username = usernameInput;
      changes.push("用户名");
    }

    // 处理密码修改
    if (passwordInput) {
      if (passwordInput.length < 6) {
        return NextResponse.json(
          { error: "密码长度不能少于 6 位" },
          { status: 400 }
        );
      }
      if (passwordInput !== confirmInput) {
        return NextResponse.json(
          { error: "两次输入的新密码不一致" },
          { status: 400 }
        );
      }
      config.password = passwordInput;
      changes.push("密码");
    }

    if (changes.length === 0) {
      return NextResponse.json(
        { error: "未填写任何修改项" },
        { status: 400 }
      );
    }

    writeConfig(config);

    return NextResponse.json({
      message: `${changes.join("、")}修改成功`,
    });
  } catch (err) {
    console.error("[Auth] change-credentials failed:", err);
    return NextResponse.json({ error: "服务器内部错误" }, { status: 500 });
  }
}
