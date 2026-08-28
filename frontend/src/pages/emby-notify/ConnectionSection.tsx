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
        <div className="flex flex-wrap gap-2">
          <Button onClick={onSave} disabled={loading}>
            {loading ? "保存中..." : "保存配置"}
          </Button>
          <Button variant="outline" onClick={onTest} disabled={loading}>
            <RefreshCw className="h-4 w-4 mr-2" />
            测试连接
          </Button>
        </div>
      </div>
    </section>
  );
}
