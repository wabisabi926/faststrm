import * as fs from 'fs';
import * as path from 'path';
import * as crypto from 'crypto';
import { logger } from "@/lib/logger";

const DEFAULT_SECRET = 'your-super-secret-jwt-key-change-in-production';
const jwtSecretFile = path.join(process.cwd(), "../config", ".jwt_secret");

let cachedSecret: string | null = null;

export function loadJwtSecret(): string {
  if (cachedSecret) return cachedSecret;

  // 1. 环境变量优先
  const envSecret = process.env.JWT_SECRET;
  if (envSecret && envSecret.length >= 16) {
    cachedSecret = envSecret;
    return cachedSecret;
  }

  // 2. 持久化密钥文件
  try {
    if (fs.existsSync(jwtSecretFile)) {
      const fileSecret = fs.readFileSync(jwtSecretFile, "utf-8").trim();
      if (fileSecret.length >= 16) {
        cachedSecret = fileSecret;
        return cachedSecret;
      }
    }
  } catch {}

  // 3. 生成随机密钥并持久化
  const randomSecret = crypto.randomBytes(48).toString("base64");
  try {
    const dir = path.dirname(jwtSecretFile);
    if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(jwtSecretFile, randomSecret, "utf-8", { mode: 0o600 });
    logger.info(`[JWT] 已自动生成随机密钥并保存到 ${jwtSecretFile}`);
  } catch (err) {
    logger.warn("[JWT] 无法写入密钥文件，使用临时密钥（重启后失效）:", err);
  }

  cachedSecret = randomSecret;
  return cachedSecret;
}

export function isDefaultSecret(): boolean {
  const secret = loadJwtSecret();
  return secret === DEFAULT_SECRET;
}

export function getSecretBytes(): Uint8Array {
  return new TextEncoder().encode(loadJwtSecret());
}