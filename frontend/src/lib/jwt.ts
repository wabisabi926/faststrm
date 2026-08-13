import { SignJWT, jwtVerify } from 'jose';
import { logger } from "@/lib/logger";

let _secret: Uint8Array | null = null;

// 启动时注入密钥（由 layout.tsx 等服务端入口调用）
export function setSecret(secret: Uint8Array): void {
  _secret = secret;
  // 同时持久化到 globalThis，防止 HMR 热更新或 Edge Runtime 丢失
  (globalThis as any).__jwtSecret = secret;
}

function getSecret(): Uint8Array {
  if (_secret) {
    return _secret;
  }

  // 从 globalThis 恢复（HMR 后模块变量重置，但 globalThis 持久化）
  const cached = (globalThis as any).__jwtSecret;
  if (cached instanceof Uint8Array && cached.length > 0) {
    _secret = cached;
    return _secret;
  }

  // 兜底：尝试从环境变量加载（Edge Runtime 可用 process.env）
  try {
    const envSecret = process.env.JWT_SECRET;
    if (envSecret && envSecret.length >= 16) {
      _secret = new TextEncoder().encode(envSecret);
      (globalThis as any).__jwtSecret = _secret;
      return _secret;
    }
  } catch {
    // Edge Runtime 可能不支持 process.env
  }

  throw new Error("JWT 密钥未初始化，请在服务端启动时调用 setSecret() 或设置 JWT_SECRET 环境变量");
}

export interface TokenPayload {
  username: string;
  iat: number;
  exp: number;
}

// 生成JWT token
export async function generateToken(username: string): Promise<string> {
  const token = await new SignJWT({ username })
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuedAt()
    .setExpirationTime('24h')
    .sign(getSecret());
  
  return token;
}

// 验证JWT token
export async function verifyToken(token: string): Promise<TokenPayload | null> {
  try {
    const { payload } = await jwtVerify(token, getSecret());
    
    if (typeof payload.username !== 'string' || 
        typeof payload.iat !== 'number' || 
        typeof payload.exp !== 'number') {
      return null;
    }
    
    return {
      username: payload.username,
      iat: payload.iat,
      exp: payload.exp
    };
  } catch (error) {
    logger.error('Token verification failed:', error);
    return null;
  }
}

// 从请求头中提取token
export function extractTokenFromHeader(authHeader: string | null): string | null {
  if (!authHeader || !authHeader.startsWith('Bearer ')) {
    return null;
  }
  return authHeader.substring(7);
}