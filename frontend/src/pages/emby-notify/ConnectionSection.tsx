// Emby 连接配置区块：URL / API Key / 保存 / 测试连接。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RefreshCw, Server } from "lucide-react";
import axiosInstance from "@/lib/axios";
import type { EmbySettings } from "./types";

interface ProxyStatus {
  running: boolean;
  addr?: string;
  embyURL?: string;
  externalPlayerEnabled?: boolean;
}

export interface ConnectionSectionProps {
  settings: EmbySettings;
  loading: boolean;
  updateSetting: <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => void;
  onSave: () => void;
  onTest: () => void;
}

export function ConnectionSection({
  settings,
  loading,
  updateSetting,
  onSave,
  onTest,
}: ConnectionSectionProps) {
  // ✅ 直接从 settings.proxyPort 派生，与后端数据实时同步
  const proxyEnabled = (settings.proxyPort ?? 0) > 0;
  const [proxyStatus, setProxyStatus] = useState<ProxyStatus | null>(null);
  const [statusLoading, setStatusLoading] = useState(false);

  const refreshProxyStatus = async () => {
    setStatusLoading(true);
    try {
      const response = await axiosInstance.get("/api/emby/proxy/status");
      setProxyStatus(response.data);
    } catch {
      setProxyStatus(null);
    } finally {
      setStatusLoading(false);
    }
  };

  useEffect(() => {
    if (proxyEnabled) void refreshProxyStatus();
    else setProxyStatus(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proxyEnabled, settings.proxyPort]);

  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-center gap-2">
        <Server className="h-5 w-5" />
        <h2 className="text-base font-medium">Emby 连接配置</h2>
      </div>
      <p className="text-xs text-muted-foreground mt-1">配置 Emby 服务器连接信息</p>
      <div className="space-y-3">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="embyUrl">Emby URL</Label>
            <Input
              id="embyUrl"
              placeholder="http://192.168.1.100:8096"
              value={settings.url || ""}
              onChange={(e) => updateSetting("url", e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Emby 服务器地址，包括端口号
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="embyApiKey">Emby API Key</Label>
            <Input
              id="embyApiKey"
              type="password"
              placeholder="xxxxxxxxxxxxxxxxxxxxxxxx"
              value={settings.apiKey || ""}
              onChange={(e) => updateSetting("apiKey", e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              在 Emby 后台「设置 → 高级设置 → API Key」获取
            </p>
          </div>
          <div className="space-y-2 md:col-span-2">
            <Label htmlFor="embyWebhookAuth">
              Webhook 鉴权密钥 <span className="text-xs text-muted-foreground">（Webhook 必填）</span>
            </Label>
            <Input
              id="embyWebhookAuth"
              type="password"
              placeholder="留空不校验；设置后 Webhook URL 自动带 ?auth=..."
              value={settings.webhookAuth || ""}
              onChange={(e) => updateSetting("webhookAuth", e.target.value)}
              autoComplete="off"
            />
            <p className="text-xs text-muted-foreground">
              现在必须配置：未设置密钥时 Emby Webhook 会被直接拒绝。Emby 侧请使用下方“Webhook 配置指引”里复制出的带 auth 参数 URL。
            </p>
          </div>
        </div>

        {/* Emby 反向代理（PlaybackInfo 拦截） */}
        <div className="border-t pt-4 space-y-3">
          <label htmlFor="proxyToggle" className="flex items-start gap-3 min-h-[36px] cursor-pointer select-none">
            <span className="flex items-center justify-center shrink-0 pt-1">
              <input
                id="proxyToggle"
                type="checkbox"
                className="h-4 w-4"
                checked={proxyEnabled}
                onChange={(e) => {
                  if (!e.target.checked) {
                    updateSetting("proxyPort", 0);
                  } else {
                    updateSetting("proxyPort", (settings.proxyPort ?? 0) > 0 ? settings.proxyPort! : 8097);
                  }
                }}
              />
            </span>
            <span className="min-w-0 flex-1">
              <span className="text-sm font-medium">Emby 反向代理</span>
              <span className="block text-xs text-muted-foreground mt-0.5">
                开启后 Emby 网页端播放 STRM 会重定向到网盘直链：禁转码、流量不走 NAS。Emby for Kodi Next Gen 直接读 STRM，无需开启
              </span>
            </span>
          </label>
          {proxyEnabled && (
            <div className="space-y-3">
              <div className="space-y-2">
                <Label htmlFor="embyProxyPort">反代监听端口</Label>
                <Input
                  id="embyProxyPort"
                  type="number"
                  placeholder="8097"
                  value={settings.proxyPort || ""}
                  onChange={(e) =>
                    updateSetting("proxyPort", parseInt(e.target.value) || 0)
                  }
                />
                <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2.5 py-2 text-xs leading-relaxed space-y-1">
                  <div>
                    <span className="font-medium">客户端必须连 </span>
                    <code className="bg-amber-500/15 px-1.5 py-0.5 rounded break-all font-mono text-xs">
                      {`http://${settings.url?.replace(/^https?:\/\//, "")?.replace(/:\d+$/, "") || "你的Emby地址"}:${settings.proxyPort || 8097}`}
                    </code>
                  </div>
                  <div className="text-muted-foreground">
                    端口需与 Emby 本体不同（默认 8097）；连 Emby 本体端口时，ISO/BDMV 原盘会被判转码而失败。
                  </div>
                </div>
              </div>
              <label htmlFor="externalPlayerToggle" className="flex items-start gap-3 min-h-[36px] cursor-pointer select-none">
                <span className="flex items-center justify-center shrink-0 pt-1">
                  <input
                    id="externalPlayerToggle"
                    type="checkbox"
                    className="h-4 w-4"
                    checked={settings.externalPlayerEnabled ?? false}
                    onChange={(e) => updateSetting("externalPlayerEnabled", e.target.checked)}
                  />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="text-sm font-medium">外部播放器按钮</span>
                  <span className="block text-xs text-muted-foreground mt-0.5">
                    仅通过 FastStrm 反代端口访问 Emby 网页端时显示 PotPlayer / VLC / Infuse / MPV 按钮；保存后请刷新 Emby 页面
                  </span>
                </span>
              </label>

              <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-2.5 py-2 text-xs">
                <span
                  className={`h-2 w-2 shrink-0 rounded-full ${
                    proxyStatus?.running ? "bg-green-500" : "bg-red-500"
                  }`}
                />
                <span>
                  {proxyStatus?.running
                    ? `反代运行中 · ${proxyStatus.addr || ""}`
                    : "反代未运行"}
                </span>
                <span className="text-muted-foreground">
                  · 外部播放器 {proxyStatus?.externalPlayerEnabled ? "已启用" : "未启用"}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="ml-auto h-6 px-2 text-xs"
                  onClick={() => void refreshProxyStatus()}
                  disabled={statusLoading}
                >
                  <RefreshCw className={`mr-1 h-3 w-3 ${statusLoading ? "animate-spin" : ""}`} />
                  刷新状态
                </Button>
              </div>
              {!proxyStatus?.running && (
                <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2.5 py-2 text-xs leading-relaxed">
                  <span className="font-medium">反代未运行：ISO/BDMV 原盘会失败，普通格式不受影响。</span>
                  请检查端口是否被占用，处理后重启 FastStrm。
                </div>
              )}
            </div>
          )}
        </div>

        <div className="flex flex-col sm:flex-row flex-wrap gap-2">
          <Button onClick={onSave} disabled={loading} className="w-full sm:w-auto">
            {loading ? "保存中..." : "保存配置"}
          </Button>
          <Button variant="outline" onClick={onTest} disabled={loading} className="w-full sm:w-auto">
            <RefreshCw className="h-4 w-4 mr-2" />
            测试连接
          </Button>
        </div>
      </div>
    </section>
  );
}
