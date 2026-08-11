// Emby 连接测试：请求 Emby System/Info 端点验证 URL + ApiKey 的正确性
import { NextRequest, NextResponse } from "next/server";
import axios, { AxiosError } from "axios";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const TIMEOUT_MS = 8_000;

export async function GET(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const url = (searchParams.get("url") || "").trim();
  const apiKey = (searchParams.get("apiKey") || "").trim();

  if (!url || !apiKey) {
    return NextResponse.json(
      { success: false, message: "请先填写 Emby URL 和 API Key" },
      { status: 200 },
    );
  }

  const base = url.replace(/\/$/, "");
  const probeUrl = `${base}/emby/System/Info`;

  try {
    const resp = await axios.get(probeUrl, {
      params: { api_key: apiKey },
      timeout: TIMEOUT_MS,
      headers: { Accept: "application/json" },
      // 防止跟随鉴权跳转把 401 变成 2xx 登录页
      maxRedirects: 0,
      validateStatus: (s) => s >= 200 && s < 300,
    });

    const data = (resp.data ?? {}) as Record<string, unknown>;
    const name = typeof data.ServerName === "string" ? data.ServerName : undefined;
    const version = typeof data.Version === "string" ? data.Version : undefined;
    const id = typeof data.Id === "string" ? data.Id : undefined;
    const operatingSystem =
      typeof data.OperatingSystem === "string" ? data.OperatingSystem : undefined;

    const bits: string[] = [];
    if (name) bits.push(`服务器：${name}`);
    if (version) bits.push(`版本：v${version}`);
    if (operatingSystem) bits.push(`系统：${operatingSystem}`);
    if (id) bits.push(`ID：${id.slice(0, 8)}…`);

    return NextResponse.json({
      success: true,
      message: bits.length > 0 ? `连接成功：${bits.join("，")}` : "Emby 连接成功",
      detail: { name, version, operatingSystem, idPrefix: id ? id.slice(0, 8) : undefined },
    });
  } catch (raw) {
    const err = raw as AxiosError<{ Message?: string }>;
    const status = err.response?.status;
    const statusText = err.response?.statusText;
    const embyMsg = typeof err.response?.data?.Message === "string" ? err.response.data.Message : undefined;

    const hint = classifyEmbyConnectError({
      status,
      code: err.code,
      url: probeUrl,
      embyMsg,
    });

    const lines = [] as string[];
    if (status) lines.push(`HTTP ${status}${statusText ? ` ${statusText}` : ""}`);
    if (embyMsg) lines.push(`Emby 返回：${embyMsg}`);
    if (hint.primary) lines.push(hint.primary);
    if (hint.secondary) lines.push(hint.secondary);

    const message = lines.length > 0 ? lines.join("；") : `连接失败：${err.message || "未知错误"}`;

    return NextResponse.json(
      {
        success: false,
        message,
        debug: {
          url: probeUrl,
          status: status ?? null,
          code: err.code ?? null,
          hint: hint.tag,
        },
      },
      { status: 200 },
    );
  }
}

function classifyEmbyConnectError(ctx: {
  status?: number;
  code?: string;
  url: string;
  embyMsg?: string;
}) {
  const { status, code } = ctx;

  if (status === 401 || status === 403) {
    return {
      tag: "UNAUTHORIZED",
      primary: "API Key 无效或未授权",
      secondary: "请确认在 Emby 后台「设置 → 高级设置 → API Key」生成的 key 已完整粘贴，且无多余空格。",
    };
  }

  if (status === 404) {
    return {
      tag: "NOT_FOUND",
      primary: "访问路径 404",
      secondary:
        "请确认 Emby URL 的协议/端口/IP 是否正确，以及末尾未带多余路径（如 /web）。正确示例：http://192.168.1.10:8096",
    };
  }

  if (status && status >= 500) {
    return {
      tag: "SERVER_5XX",
      primary: "Emby 服务端返回 5xx 错误",
      secondary: "请检查 Emby 服务本身是否启动正常，或查看 Emby 服务端日志。",
    };
  }

  if (code === "ECONNREFUSED") {
    return {
      tag: "ECONNREFUSED",
      primary: "目标端口没有服务在监听",
      secondary:
        "请确认 Emby 已启动、端口正确、faststrm 所在网络能访问该 IP:Port（Docker 部署注意容器网络 ↔ Emby 宿主机互通）。",
    };
  }

  if (code === "ENOTFOUND") {
    return {
      tag: "DNS_FAIL",
      primary: "域名解析失败",
      secondary: "若 Emby URL 使用了域名，请确认 faststrm 所在容器/机器的 DNS 能正确解析。局域网场景建议直接填 IP。",
    };
  }

  if (code === "ETIMEDOUT" || code === "ECONNRESET") {
    return {
      tag: "TIMEOUT_OR_RESET",
      primary: "连接超时或被重置",
      secondary: "请检查网络连通性、防火墙，以及 Emby 是否在运行（8 秒内未响应）。",
    };
  }

  if (code === "ECONNABORTED") {
    return {
      tag: "ABORTED",
      primary: "请求被中止（可能 HTTPS/证书不匹配）",
      secondary:
        "若使用 https:// 连接，请确认证书可信；内网自签证书建议改用 http://，或在部署时让 Emby 使用受信任证书。",
    };
  }

  if (status && status >= 300 && status < 400) {
    return {
      tag: "REDIRECT_BLOCKED",
      primary: `服务端返回重定向（HTTP ${status}），未跟随`,
      secondary: "常见于 Emby 后台开启了 HTTPS 强制跳转。请使用重定向后的地址，或改为 http://。",
    };
  }

  return {
    tag: "UNKNOWN",
    primary: "",
    secondary: "",
  };
}
