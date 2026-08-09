import * as crypto from "crypto";
import * as fs from "fs";
import * as path from "path";

const CONFIG_DIR = path.join(process.cwd(), "../config");
const CONFIG_FILE = path.join(CONFIG_DIR, "config.json");
const SALT_FILE = path.join(CONFIG_DIR, ".salt");

// 哈希格式前缀，用于区分明文和哈希
const HASH_PREFIX = "$sha256$";
// 加密格式前缀，用于区分明文和密文
const CIPHER_PREFIX = "$aes256gcm$";

/**
 * 读取或生成持久化 salt（每个部署唯一）
 */
function getSalt(): string {
  if (!fs.existsSync(SALT_FILE)) {
    const salt = crypto.randomBytes(32).toString("hex");
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
    fs.writeFileSync(SALT_FILE, salt, "utf-8");
    return salt;
  }
  return fs.readFileSync(SALT_FILE, "utf-8").trim();
}

/**
 * 对密码进行 SHA-256 + salt 哈希
 */
export function hashPassword(password: string): string {
  const salt = getSalt();
  const hash = crypto
    .createHash("sha256")
    .update(salt + password)
    .digest("hex");
  return `${HASH_PREFIX}${hash}`;
}

/**
 * 验证密码是否匹配
 * 支持明文（旧格式）和哈希（新格式）两种存储方式
 */
export function verifyPassword(password: string, stored: string): boolean {
  if (stored.startsWith(HASH_PREFIX)) {
    const hash = stored.substring(HASH_PREFIX.length);
    const salt = getSalt();
    const inputHash = crypto
      .createHash("sha256")
      .update(salt + password)
      .digest("hex");
    return hash === inputHash;
  }
  // 明文兼容（旧格式）
  return password === stored;
}

/**
 * 判断存储的密码是否已是哈希格式
 */
export function isHashed(stored: string): boolean {
  return stored.startsWith(HASH_PREFIX);
}

/**
 * 读取 config.json
 */
export function readConfig(): { username: string; password: string } {
  const raw = fs.readFileSync(CONFIG_FILE, "utf-8");
  return JSON.parse(raw);
}

/**
 * 写入 config.json
 */
export function writeConfig(config: { username: string; password: string }): void {
  fs.writeFileSync(CONFIG_FILE, JSON.stringify(config, null, 2), "utf-8");
}

/**
 * 迁移：如果密码是明文，自动转为哈希
 * 返回 true 表示执行了迁移
 */
export function migratePlaintextPassword(): boolean {
  try {
    const config = readConfig();
    if (config.password && !isHashed(config.password)) {
      config.password = hashPassword(config.password);
      writeConfig(config);
      console.log("[Auth] Password migrated from plaintext to hash");
      return true;
    }
  } catch (err) {
    console.error("[Auth] Failed to migrate password:", err);
  }
  return false;
}

// ==================== 凭据可逆加密（AES-256-GCM） ====================

/**
 * 从 salt 派生 AES-256 主密钥（32 字节）
 */
function getAesKey(): Buffer {
  const salt = getSalt();
  return crypto.createHash("sha256").update(salt + ":aes-key").digest();
}

/**
 * 加密单个字符串（AES-256-GCM）
 * 返回格式：$aes256gcm$<iv_hex>$<authTag_hex>$<ciphertext_hex>
 */
export function encryptCredential(plaintext: string): string {
  if (!plaintext || plaintext.startsWith(CIPHER_PREFIX)) return plaintext;
  const key = getAesKey();
  const iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv("aes-256-gcm", key, iv);
  const encrypted = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  const authTag = cipher.getAuthTag();
  return `${CIPHER_PREFIX}${iv.toString("hex")}$${authTag.toString("hex")}$${encrypted.toString("hex")}`;
}

/**
 * 解密单个字符串
 * 若不是加密格式，原样返回（兼容明文）
 */
export function decryptCredential(stored: string): string {
  if (!stored || !stored.startsWith(CIPHER_PREFIX)) return stored;
  try {
    const parts = stored.substring(CIPHER_PREFIX.length).split("$");
    if (parts.length !== 3) return stored;
    const iv = Buffer.from(parts[0], "hex");
    const authTag = Buffer.from(parts[1], "hex");
    const ciphertext = Buffer.from(parts[2], "hex");
    const key = getAesKey();
    const decipher = crypto.createDecipheriv("aes-256-gcm", key, iv);
    decipher.setAuthTag(authTag);
    const decrypted = Buffer.concat([decipher.update(ciphertext), decipher.final()]);
    return decrypted.toString("utf8");
  } catch (err) {
    console.error("[Crypto] Failed to decrypt credential:", err);
    return stored;
  }
}

/**
 * 判断字符串是否已是加密格式
 */
export function isEncrypted(stored: string): boolean {
  return !!stored && stored.startsWith(CIPHER_PREFIX);
}

/**
 * 需要加密的 account 字段
 */
const ACCOUNT_ENCRYPTED_FIELDS = ["cookie", "password"] as const;

/**
 * 解密 account 数组中的敏感字段（返回新数组，不修改原数组）
 */
export function decryptAccounts<T extends Record<string, unknown>>(accounts: T[]): T[] {
  return accounts.map((acc) => {
    const decrypted = { ...acc };
    for (const field of ACCOUNT_ENCRYPTED_FIELDS) {
      const val = (decrypted as Record<string, unknown>)[field];
      if (typeof val === "string" && isEncrypted(val)) {
        (decrypted as Record<string, unknown>)[field] = decryptCredential(val);
      }
    }
    return decrypted;
  });
}

/**
 * 加密 account 数组中的敏感字段（原地修改并返回）
 */
export function encryptAccounts<T extends Record<string, unknown>>(accounts: T[]): T[] {
  for (const acc of accounts) {
    for (const field of ACCOUNT_ENCRYPTED_FIELDS) {
      const val = (acc as Record<string, unknown>)[field];
      if (typeof val === "string" && val && !isEncrypted(val)) {
        (acc as Record<string, unknown>)[field] = encryptCredential(val);
      }
    }
  }
  return accounts;
}

/**
 * 迁移 account.json 中的明文凭据为加密格式
 * 返回 true 表示执行了迁移
 */
export function migrateAccountCredentials(): boolean {
  try {
    const accountPath = path.join(CONFIG_DIR, "account.json");
    if (!fs.existsSync(accountPath)) return false;
    const accounts = JSON.parse(fs.readFileSync(accountPath, "utf-8"));
    if (!Array.isArray(accounts)) return false;

    let changed = false;
    for (const acc of accounts) {
      for (const field of ACCOUNT_ENCRYPTED_FIELDS) {
        const val = acc[field];
        if (typeof val === "string" && val && !isEncrypted(val)) {
          acc[field] = encryptCredential(val);
          changed = true;
        }
      }
    }
    if (changed) {
      fs.writeFileSync(accountPath, JSON.stringify(accounts, null, 2), "utf-8");
      console.log("[Crypto] Account credentials migrated to encrypted format");
    }
    return changed;
  } catch (err) {
    console.error("[Crypto] Failed to migrate account credentials:", err);
    return false;
  }
}

/**
 * 迁移 settings.json 中的明文凭据为加密格式
 * 返回 true 表示执行了迁移
 */
export function migrateSettingsCredentials(): boolean {
  try {
    const settingsPath = path.join(CONFIG_DIR, "settings.json");
    if (!fs.existsSync(settingsPath)) return false;
    const settings = JSON.parse(fs.readFileSync(settingsPath, "utf-8"));

    let changed = false;
    if (settings.emby?.apiKey && !isEncrypted(settings.emby.apiKey)) {
      settings.emby.apiKey = encryptCredential(settings.emby.apiKey);
      changed = true;
    }

    if (changed) {
      fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 2), "utf-8");
      console.log("[Crypto] Settings credentials migrated to encrypted format");
    }
    return changed;
  } catch (err) {
    console.error("[Crypto] Failed to migrate settings credentials:", err);
    return false;
  }
}

/**
 * 解密 settings 对象中的 emby.apiKey
 */
export function decryptSettings<T extends Record<string, unknown>>(settings: T): T {
  if (!settings) return settings;
  const decrypted = { ...settings };
  const emby = (decrypted as Record<string, unknown>).emby as Record<string, unknown> | undefined;
  if (emby?.apiKey && typeof emby.apiKey === "string" && isEncrypted(emby.apiKey)) {
    decrypted.emby = { ...emby, apiKey: decryptCredential(emby.apiKey) };
  }
  return decrypted;
}

/**
 * 加密 settings 对象中的 emby.apiKey（原地修改）
 */
export function encryptSettings<T extends Record<string, unknown>>(settings: T): T {
  const emby = (settings as Record<string, unknown>).emby as Record<string, unknown> | undefined;
  if (emby?.apiKey && typeof emby.apiKey === "string" && !isEncrypted(emby.apiKey)) {
    emby.apiKey = encryptCredential(emby.apiKey);
  }
  return settings;
}

/**
 * 执行所有凭据迁移（登录时调用一次）
 */
export function migrateAllCredentials(): void {
  migratePlaintextPassword();
  migrateAccountCredentials();
  migrateSettingsCredentials();
}
