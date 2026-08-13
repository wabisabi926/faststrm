import { SignJWT, jwtVerify } from 'jose';

let _secret: Uint8Array | null = null;

// 启动时注入密钥（由 layout.tsx 等服务端入口调用）
export function setSecret(secret: Uint8Array): void {
  _secret = secret;
}

function getSecret(): Uint8Array {
  if (!_secret) {
    throw new Error("JWT 密钥未初始化，请在服务端启动时调用 setSecret()");
  }
  return _secret;
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
    console.error('Token verification failed:', error);
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