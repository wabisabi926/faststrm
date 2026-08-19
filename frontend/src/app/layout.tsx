import type { Metadata } from "next";
import  LayoutWrapper  from "@/components/LayoutWrapper";
import ClientAuthProvider from "@/components/ClientAuthProvider";
import { Toaster } from "@/components/ui/sonner"
import "./globals.css";
import { initTaskScheduler } from "@/lib/taskScheduler";
import { initTelegramPolling } from "@/lib/telegramPolling";
import { setSecret } from "@/lib/jwt";
import { getSecretBytes, isDefaultSecret } from "@/lib/jwtSecret";
import { logger } from "@/lib/logger";

// 使用 globalThis 防止 HMR 重复初始化
const _lg = globalThis as unknown as { __servicesInitialized?: boolean };
if (!_lg.__servicesInitialized) {
  _lg.__servicesInitialized = true;

  // JWT 密钥初始化 + 安全检查
  try {
    const secretBytes = getSecretBytes();
    setSecret(secretBytes);
    // 同步到 process.env，使 Edge Runtime (middleware) 也能获取
    process.env.JWT_SECRET = new TextDecoder().decode(secretBytes);
    if (isDefaultSecret()) {
      const isProd = process.env.NODE_ENV === "production";
      if (isProd) {
        throw new Error("JWT_SECRET 未配置且无自动生成的密钥文件，生产环境拒绝启动。");
      } else {
        logger.warn("[JWT] 警告：正在使用默认密钥，生产环境请务必设置 JWT_SECRET 环境变量！");
      }
    }
  } catch (err) {
    logger.error("[Init] JWT 密钥初始化失败:", err);
  }

  // 初始化后台服务
  initTaskScheduler().catch((err) =>
    logger.error("[Init] 任务调度器初始化失败:", err)
  );
  initTelegramPolling().catch((err) =>
    logger.error("[Init] Telegram 轮询初始化失败:", err)
  );

  // 启动账号状态后台监控（预热缓存，避免首次页面加载慢）
  import("@/app/api/account/status/route")
    .then(({ startAccountStatusBackgroundMonitor }) => {
      startAccountStatusBackgroundMonitor();
    })
    .catch((err) => {
      logger.warn("[Init] 账号状态监控启动失败:", err);
    });

  // 延迟重试：确保配置文件就绪后再次检查并启动轮询
  const retryInitTelegram = (delayMs: number) => {
    setTimeout(() => {
      initTelegramPolling().catch((err) =>
        logger.error("[Init] Telegram 轮询延迟初始化失败:", err)
      );
    }, delayMs);
  };
  retryInitTelegram(5000);
  retryInitTelegram(30000);
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
    <html lang="zh-CN" className="light">
      <body className="antialiased bg-background text-foreground">
        <ClientAuthProvider>
          <LayoutWrapper>{children}</LayoutWrapper>
        </ClientAuthProvider>
        <Toaster />
      </body>
    </html>
  );
}
