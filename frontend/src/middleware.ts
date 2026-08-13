import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { verifyToken, extractTokenFromHeader } from "@/lib/jwt";

// 注意：middleware 只能用 edge runtime，iron-session 支持
export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  // 只对API路由进行token验证，页面路由交给客户端处理
  if (!pathname.startsWith("/api")) {
    return NextResponse.next();
  }

  // 登录相关API直接放行
  if (pathname.startsWith("/api/auth")) {
    return NextResponse.next();
  }

  // STRM 302 跳转接口放行（播放器无 token）
  if (pathname === "/api/strm") {
    return NextResponse.next();
  }

  // Emby Webhook 回调放行（Emby 服务器无法携带登录 token）
  if (pathname.startsWith("/api/emby/webhook")) {
    return NextResponse.next();
  }

  // Telegram Webhook 回调放行（Telegram 服务器无法携带登录 token）
  if (pathname.startsWith("/api/notify/webhook")) {
    return NextResponse.next();
  }

  // alist 兼容接口使用内部 API Token 验证 (/api/fs/*)
  if (pathname.startsWith("/api/fs")) {
    const authHeader = req.headers.get('authorization') || '';
    const internalToken = process.env.ALIST_API_TOKEN || '';
    if (internalToken && authHeader === internalToken) {
      return NextResponse.next();
    }
    return NextResponse.json({ code: 401, message: "unauthorized" }, { status: 401 });
  }

  // 从Authorization头部获取token
  const authHeader = req.headers.get('authorization');
  const token = extractTokenFromHeader(authHeader);

  if (!token) {
    return NextResponse.json({ error: "未登录" }, { status: 401 });
  }

  // 验证token
  const payload = await verifyToken(token);
  if (!payload) {
    return NextResponse.json({ error: "登录已过期" }, { status: 401 });
  }
  
  // 将用户信息添加到请求头中，供后续API使用
  const response = NextResponse.next();
  response.headers.set('x-user', payload.username);
  
  return response;
}

export const config = {
  matcher: ["/((?!_next|static|favicon.ico).*)"],
};
