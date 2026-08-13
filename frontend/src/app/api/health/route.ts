import { NextResponse } from "next/server";
import fs from "fs";
import path from "path";
import { logger } from "@/lib/logger";

export const dynamic = "force-dynamic";

interface HealthCheckResult {
  status: "ok" | "degraded" | "down";
  checks: {
    database: { status: "ok" | "warn" | "fail"; message?: string };
    diskSpace: { status: "ok" | "warn" | "fail"; message?: string };
    configValid: { status: "ok" | "warn" | "fail"; message?: string };
    monitorStatus: { status: "ok" | "warn" | "unknown"; message?: string };
    processInfo: {
      uptime: number;
      memoryUsage: { rss: number; heapTotal: number; heapUsed: number };
    };
  };
  timestamp: string;
  version?: string;
}

export async function GET() {
  const startTime = Date.now();
  const checks: HealthCheckResult["checks"] = {
    database: { status: "ok" },
    diskSpace: { status: "ok" },
    configValid: { status: "ok" },
    monitorStatus: { status: "unknown" },
    processInfo: {
      uptime: process.uptime(),
      memoryUsage: {
        rss: Math.round(process.memoryUsage().rss / 1024 / 1024),
        heapTotal: Math.round(process.memoryUsage().heapTotal / 1024 / 1024),
        heapUsed: Math.round(process.memoryUsage().heapUsed / 1024 / 1024),
      },
    },
  };

  // 1. Database check
  try {
    const dbPath = path.join(process.cwd(), "../config/filePathDb.sqlite");
    if (fs.existsSync(dbPath)) {
      const stats = fs.statSync(dbPath);
      if (stats.size < 100) {
        checks.database = { status: "warn", message: "数据库文件过小，可能未初始化" };
      }
    } else {
      checks.database = { status: "fail", message: "数据库文件不存在" };
    }
  } catch (err) {
    checks.database = { status: "fail", message: String(err) };
  }

  // 2. Disk space check (basic)
  try {
    const configDir = path.join(process.cwd(), "../config");
    if (fs.existsSync(configDir)) {
      fs.accessSync(configDir, fs.constants.W_OK);
      checks.diskSpace = { status: "ok", message: "配置目录可写" };
    } else {
      checks.diskSpace = { status: "fail", message: "配置目录不存在" };
    }
  } catch (err) {
    checks.diskSpace = { status: "fail", message: String(err) };
  }

  // 3. Config validation
  try {
    const settingsFile = path.join(process.cwd(), "../config/settings.json");
    const accountFile = path.join(process.cwd(), "../config/account.json");

    if (!fs.existsSync(settingsFile)) {
      checks.configValid = { status: "warn", message: "settings.json 不存在（首次启动可能正常）" };
    } else {
      const raw = fs.readFileSync(settingsFile, "utf-8");
      JSON.parse(raw);
    }

    if (!fs.existsSync(accountFile)) {
      checks.configValid = { status: "warn", message: "account.json 不存在（未配置账号）" };
    }
  } catch (err) {
    checks.configValid = { status: "fail", message: String(err) };
  }

  // 4. Monitor status
  try {
    const monitorStateFile = path.join(process.cwd(), "../config/lifeMonitorState.json");
    if (fs.existsSync(monitorStateFile)) {
      const state = JSON.parse(fs.readFileSync(monitorStateFile, "utf-8"));
      const activeMonitors = Object.entries(state).filter(
        ([, v]: [string, unknown]) => (v as { running?: boolean }).running
      ).length;
      checks.monitorStatus = {
        status: activeMonitors > 0 ? "ok" : "warn",
        message: activeMonitors > 0 ? `${activeMonitors} 个监控运行中` : "无活跃监控",
      };
    }
  } catch (err) {
    checks.monitorStatus = { status: "unknown", message: String(err) };
  }

  // Determine overall status
  const statuses = [checks.database.status, checks.diskSpace.status, checks.configValid.status];
  const hasFail = statuses.some((s) => s === "fail");
  const hasWarn = statuses.some((s) => s === "warn");
  const overallStatus: HealthCheckResult["status"] = hasFail ? "down" : hasWarn ? "degraded" : "ok";

  const result: HealthCheckResult = {
    status: overallStatus,
    checks,
    timestamp: new Date().toISOString(),
    version: process.env.npm_package_version,
  };

  const durationMs = Date.now() - startTime;
  if (durationMs > 100) {
    logger.info(`健康检查完成`, { durationMs, status: overallStatus });
  }

  return NextResponse.json(result, {
    status: overallStatus === "down" ? 503 : 200,
    headers: {
      "Cache-Control": "no-store",
      "x-response-time": `${durationMs}ms`,
    },
  });
}
