import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { extractTokenFromHeader } from "@/lib/jwt";
import { logger, createTraceId } from "@/lib/logger";

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;
  const traceId = createTraceId();
  const startTime = performance.now();

  if (!pathname.startsWith("/api")) {
    return NextResponse.next();
  }

  logger.debug(`请求开始`, { traceId, method: req.method, path: pathname });

  // 登录相关API直接放行
  if (pathname.startsWith("/api/auth")) {
    const res = NextResponse.next();
    res.headers.set('x-trace-id', traceId);
    return res;
  }

  // 健康检查端点放行
  if (pathname === "/api/health") {
    const res = NextResponse.next();
    res.headers.set('x-trace-id', traceId);
    return res;
  }

  // STRM 302 跳转接口放行（播放器无 token）
  if (pathname === "/api/strm") {
    const res = NextResponse.next();
    res.headers.set('x-trace-id', traceId);
    return res;
  }

  // Emby Webhook 回调放行
  if (pathname.startsWith("/api/emby/webhook")) {
    const res = NextResponse.next();
    res.headers.set('x-trace-id', traceId);
    return res;
  }

  // Telegram Webhook 回调放行
  if (pathname.startsWith("/api/notify/webhook")) {
    const res = NextResponse.next();
    res.headers.set('x-trace-id', traceId);
    return res;
  }

  // alist 兼容接口使用内部 API Token 验证
  if (pathname.startsWith("/api/fs")) {
    const authHeader = req.headers.get('authorization') || '';
    const internalToken = process.env.ALIST_API_TOKEN || '';
    if (internalToken && authHeader === internalToken) {
      const res = NextResponse.next();
      res.headers.set('x-trace-id', traceId);
      return res;
    }
    logger.warn(`未授权的 fs 接口访问`, { traceId, path: pathname });
    return NextResponse.json({ code: 401, message: "unauthorized" }, { status: 401 });
  }

  // 其他 API 路由：放行，让路由自身处理鉴权
  // 注：Edge Runtime 无法访问 Node.js Runtime 中动态设置的 JWT 密钥
  const token = extractTokenFromHeader(req.headers.get('authorization'));
  
  // 如果有 token，尝试验证（容错处理）
  if (token) {
    try {
      // 注意：不在此处强制验证，因为 Edge Runtime 可能无法获取密钥
      // 真正的鉴权在各个 API 路由中处理
      logger.debug(`请求带 token`, { traceId, path: pathname });
    } catch {
      // 静默处理
    }
  } else {
    logger.debug(`请求无 token`, { traceId, path: pathname });
  }

  const response = NextResponse.next();
  response.headers.set('x-trace-id', traceId);

  const durationMs = Math.round(performance.now() - startTime);
  logger.debug(`请求完成`, { traceId, path: pathname, durationMs });
  
  return response;
}

export const config = {
  matcher: ["/((?!_next|static|favicon.ico).*)"],
};
