// 媒体库刷库配置区块：创建/删除后刷库 + 防抖秒数。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Database } from "lucide-react";
import type { EmbySettings } from "./types";

export interface RefreshSettingsSectionProps {
  settings: EmbySettings;
  loading: boolean;
  updateSetting: <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => void;
  onSave: () => void;
}

export function RefreshSettingsSection({
  settings,
  loading,
  updateSetting,
  onSave,
}: RefreshSettingsSectionProps) {
  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-center gap-2">
        <Database className="h-5 w-5" />
        <h2 className="text-base font-medium">媒体库刷库配置</h2>
      </div>
      <p className="text-xs text-muted-foreground mt-1">
        STRM 文件变动后自动刷新 Emby 媒体库
      </p>
      <div className="space-y-3">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="refreshOnCreate"
              checked={!!settings.refreshOnCreate}
              onCheckedChange={(checked) =>
                updateSetting("refreshOnCreate", checked === true)
              }
            />
            <div className="flex-1">
              <Label htmlFor="refreshOnCreate" className="cursor-pointer">
                创建后刷库
              </Label>
              <p className="text-xs text-muted-foreground">
                STRM 创建后自动刷新
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="refreshOnDelete"
              checked={!!settings.refreshOnDelete}
              onCheckedChange={(checked) =>
                updateSetting("refreshOnDelete", checked === true)
              }
            />
            <div className="flex-1">
              <Label htmlFor="refreshOnDelete" className="cursor-pointer">
                删除后刷库
              </Label>
              <p className="text-xs text-muted-foreground">
                STRM 删除后自动刷新
              </p>
            </div>
          </div>

          <div className="space-y-2 p-3 rounded-lg border">
            <Label htmlFor="debounceSeconds">刷库防抖（秒）</Label>
            <Input
              id="debounceSeconds"
              type="number"
              min="0"
              max="300"
              placeholder="10"
              value={settings.debounceSeconds ?? 10}
              onChange={(e) =>
                updateSetting("debounceSeconds", parseInt(e.target.value) || 10)
              }
            />
            <p className="text-xs text-muted-foreground">
              多次事件合并为一次刷库
            </p>
          </div>
        </div>

        <div className="flex flex-wrap gap-2">
          <Button onClick={onSave} disabled={loading}>
            {loading ? "保存中..." : "保存设置"}
          </Button>
        </div>
      </div>
    </section>
  );
}
