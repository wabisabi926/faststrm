import type { Metadata } from "next";
import  LayoutWrapper  from "@/components/LayoutWrapper";
import ClientAuthProvider from "@/components/ClientAuthProvider";
import { Toaster } from "@/components/ui/sonner"
import "./globals.css";
import { initTaskScheduler } from "@/lib/taskScheduler";
import { initTelegramPolling } from "@/lib/telegramPolling";
import { setSecret } from "@/lib/jwt";
import { getSecretBytes, isDefaultSecret } from "@/lib/jwtSecret";

// 服务端启动时初始化后台服务（幂等，仅首次调用生效）
let servicesInitialized = false;
if (!servicesInitialized) {
  servicesInitialized = true;

  // JWT 密钥初始化 + 安全检查
  try {
    setSecret(getSecretBytes());
    if (isDefaultSecret()) {
      const isProd = process.env.NODE_ENV === "production";
      if (isProd) {
        throw new Error("JWT_SECRET 未配置且无自动生成的密钥文件，生产环境拒绝启动。");
      } else {
        console.warn("[JWT] 警告：正在使用默认密钥，生产环境请务必设置 JWT_SECRET 环境变量！");
      }
    }
  } catch (err) {
    console.error("[Init] JWT 密钥初始化失败:", err);
  }

  initTaskScheduler().catch((err) =>
    console.error("[Init] 任务调度器初始化失败:", err)
  );
  initTelegramPolling().catch((err) =>
    console.error("[Init] Telegram 轮询初始化失败:", err)
  );
}

export const metadata: Metadata = {
  title: "Fast Strm",
  description: "更快、更强、更硬",
  icons: {
    icon: "/favicon.ico",
    shortcut: "/favicon.ico",
    apple: "/logo.png",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className="antialiased">
        <ClientAuthProvider>
          <LayoutWrapper>{children}</LayoutWrapper>
        </ClientAuthProvider>
        <Toaster />
      </body>
    </html>
  );
}
