// 通知设置区块：入库/删除/播放通知开关 + 播放进度显示。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Bell } from "lucide-react";
import type { EmbySettings } from "./types";

export interface NotifySettingsSectionProps {
  settings: EmbySettings;
  loading: boolean;
  updateSetting: <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => void;
  onSave: () => void;
}

export function NotifySettingsSection({
  settings,
  loading,
  updateSetting,
  onSave,
}: NotifySettingsSectionProps) {
  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-center gap-2">
        <Bell className="h-5 w-5" />
        <h2 className="text-base font-medium">通知设置</h2>
      </div>
      <p className="text-xs text-muted-foreground mt-1">选择需要启用的通知类型</p>
      <div className="space-y-3">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="notifyMediaAdded"
              checked={!!settings.notifyMediaAdded}
              onCheckedChange={(checked) =>
                updateSetting("notifyMediaAdded", checked === true)
              }
            />
            <div className="flex-1">
              <Label htmlFor="notifyMediaAdded" className="cursor-pointer">
                入库通知
              </Label>
              <p className="text-xs text-muted-foreground">
                电影/剧集入库时发送通知
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="notifyMediaRemoved"
              checked={!!settings.notifyMediaRemoved}
              onCheckedChange={(checked) =>
                updateSetting("notifyMediaRemoved", checked === true)
              }
            />
            <div className="flex-1">
              <Label htmlFor="notifyMediaRemoved" className="cursor-pointer">
                删除通知
              </Label>
              <p className="text-xs text-muted-foreground">
                媒体删除时发送通知
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="notifyPlayback"
              checked={!!settings.notifyPlayback}
              onCheckedChange={(checked) =>
                updateSetting("notifyPlayback", checked === true)
              }
            />
            <div className="flex-1">
              <Label htmlFor="notifyPlayback" className="cursor-pointer">
                播放通知
              </Label>
              <p className="text-xs text-muted-foreground">
                播放开始/暂停/结束时发送通知
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="playbackShowProgress"
              checked={!!settings.playbackShowProgress}
              onCheckedChange={(checked) =>
                updateSetting("playbackShowProgress", checked === true)
              }
              disabled={!settings.notifyPlayback}
            />
            <div className="flex-1">
              <Label
                htmlFor="playbackShowProgress"
                className={`cursor-pointer ${!settings.notifyPlayback ? "text-muted-foreground" : ""}`}
              >
                显示播放进度
              </Label>
              <p className="text-xs text-muted-foreground">
                在播放通知中显示进度百分比
              </p>
            </div>
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
