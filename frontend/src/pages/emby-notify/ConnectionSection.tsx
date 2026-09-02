// Emby 连接配置区块：URL / API Key / 保存 / 测试连接。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RefreshCw, Server } from "lucide-react";
import type { EmbySettings } from "./types";

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
                拦截 PlaybackInfo，强制 STRM 直接播放（ISO 原盘必须启用）
              </span>
            </span>
          </label>
          {proxyEnabled && (
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
              <p className="text-xs text-muted-foreground leading-relaxed">
                与 Emby 本体端口（如 8096）不同，填一个空闲端口即可（默认 8097）。
                启用后，将 Emby for Kodi 的服务器地址改为：
                <code className="block bg-muted px-1.5 py-0.5 rounded mt-1 break-all font-mono text-[11px]">
                  http://{settings.url
                    ?.replace(/^https?:\/\//, "")
                    ?.replace(/:\d+$/, "")}:{settings.proxyPort || 8097}
                </code>
              </p>
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
