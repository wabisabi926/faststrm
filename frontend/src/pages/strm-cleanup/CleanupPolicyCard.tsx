import { useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Settings as SettingsType } from "@/pages/settings/types";
import { AUTO_MODE_HINTS, CLEANUP_DEFAULTS } from "./cleanup-constants";

type CleanupSettings = NonNullable<SettingsType["cleanup"]>;

export interface CleanupPolicyCardProps {
  data: SettingsType;
  setData: Dispatch<SetStateAction<SettingsType>>;
  saving: boolean;
  onSave: () => Promise<boolean>;
}

export function CleanupPolicyCard({ data, setData, saving, onSave }: CleanupPolicyCardProps) {
  const [dirty, setDirty] = useState(false);
  const cleanup = data.cleanup || {};
  const cleanupMode = data.download?.fullSyncCleanupOrphans ?? "auto_clean";

  const updateCleanup = (patch: Partial<CleanupSettings>) => {
    setData({ ...data, cleanup: { ...(data.cleanup || {}), ...patch } });
    setDirty(true);
  };

  const updateAutoMode = (value: string) => {
    setData({
      ...data,
      download: { ...(data.download || {}), fullSyncCleanupOrphans: value },
    });
    setDirty(true);
  };

  const handleSave = async () => {
    if (await onSave()) {
      setDirty(false);
    }
  };

  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-start gap-2">
        <ShieldAlert className="mt-0.5 h-5 w-5 text-amber-500 shrink-0" />
        <div>
          <h2 className="text-base font-medium">清理策略</h2>
          <p className="text-xs text-muted-foreground mt-1">
            集中管理任务同步时的自动清理，以及自动/手动清理共用的保护阈值与动作。
          </p>
        </div>
      </div>

      {/* A. 自动清理触发 */}
      <div className="rounded-md border p-3 sm:p-4 space-y-3 bg-muted/20">
        <div>
          <h3 className="text-sm font-medium">任务同步时的自动清理</h3>
          <p className="text-xs text-muted-foreground mt-1">
            仅在全量/增量同步任务执行时生效；下方的“扫描路径映射”不会自动触发它。
          </p>
        </div>
        <div className="flex flex-col sm:flex-row sm:items-center gap-3">
          <Select value={cleanupMode} onValueChange={updateAutoMode}>
            <SelectTrigger className="w-full sm:w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="off">关闭</SelectItem>
              <SelectItem value="mark_only">仅标记（只写日志）</SelectItem>
              <SelectItem value="auto_clean">自动清理（按下方阈值）</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <p className="text-xs text-muted-foreground">
          {AUTO_MODE_HINTS[cleanupMode] ?? AUTO_MODE_HINTS.auto_clean}
        </p>
      </div>

      {/* B. 自动 + 手动共用 */}
      <div className="rounded-md border p-3 sm:p-4 space-y-4">
        <div>
          <h3 className="text-sm font-medium">保护阈值与清理动作（自动 + 手动共用）</h3>
          <p className="text-xs text-muted-foreground mt-1">
            这些参数同时作用于任务内自动清理和下方手动清理；保护阈值命中时只停删除、不停生成。
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <Label htmlFor="cleanup-max-threshold">单次删除绝对上限</Label>
            <Input
              id="cleanup-max-threshold"
              type="number"
              min="1"
              value={cleanup.maxThreshold ?? CLEANUP_DEFAULTS.maxThreshold}
              onChange={(e) =>
                updateCleanup({ maxThreshold: Math.max(1, parseInt(e.target.value) || CLEANUP_DEFAULTS.maxThreshold) })
              }
            />
            <p className="text-xs text-muted-foreground">默认 1000</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cleanup-max-ratio">比例上限（%）</Label>
            <Input
              id="cleanup-max-ratio"
              type="number"
              min="1"
              max="100"
              step="1"
              value={Math.round((cleanup.maxRatio ?? CLEANUP_DEFAULTS.maxRatio) * 100)}
              onChange={(e) => {
                const pct = Math.min(100, Math.max(1, parseInt(e.target.value) || 10));
                updateCleanup({ maxRatio: pct / 100 });
              }}
            />
            <p className="text-xs text-muted-foreground">默认 10%</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cleanup-max-dirs">涉及目录数上限</Label>
            <Input
              id="cleanup-max-dirs"
              type="number"
              min="1"
              value={cleanup.maxDirs ?? CLEANUP_DEFAULTS.maxDirs}
              onChange={(e) =>
                updateCleanup({ maxDirs: Math.max(1, parseInt(e.target.value) || CLEANUP_DEFAULTS.maxDirs) })
              }
            />
            <p className="text-xs text-muted-foreground">默认 20，防止分散目录的小批量删除绕过阈值</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cleanup-stable-threshold">稳定阈值</Label>
            <Input
              id="cleanup-stable-threshold"
              type="number"
              min="1"
              value={cleanup.stableThreshold ?? 5}
              onChange={(e) =>
                updateCleanup({ stableThreshold: Math.max(1, parseInt(e.target.value) || 5) })
              }
            />
            <p className="text-xs text-muted-foreground">超过该数量才进入二次确认，默认 5</p>
          </div>
          <div className="space-y-1.5 md:col-span-2">
            <Label htmlFor="cleanup-confirm-mode">二次确认模式</Label>
            <Select
              value={cleanup.confirmMode ?? "none"}
              onValueChange={(value) =>
                updateCleanup({ confirmMode: value as CleanupSettings["confirmMode"] })
              }
            >
              <SelectTrigger id="cleanup-confirm-mode" className="w-full md:w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">关闭（超过稳定阈值直接执行）</SelectItem>
                <SelectItem value="plugin_ui">Web UI 待确认批次</SelectItem>
                <SelectItem value="telegram">Telegram 按钮确认</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              超过稳定阈值时进入待确认队列；保护阈值始终强制拦截。
            </p>
          </div>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3 pt-1">
          <div className="flex items-start gap-2">
            <Checkbox
              id="cleanup-remove-strm"
              checked={cleanup.removeStrm ?? true}
              onCheckedChange={(checked) => updateCleanup({ removeStrm: checked === true })}
              className="mt-0.5"
            />
            <div className="space-y-1">
              <label htmlFor="cleanup-remove-strm" className="text-sm cursor-pointer">
                删除 STRM 文件本身
              </label>
              <p className="text-xs text-muted-foreground">
                关闭后只统计、不删除；会移入回收站，可在下方恢复
              </p>
            </div>
          </div>
          <div className="flex items-start gap-2">
            <Checkbox
              id="cleanup-remove-related"
              checked={cleanup.removeRelatedFiles ?? false}
              onCheckedChange={(checked) => updateCleanup({ removeRelatedFiles: checked === true })}
              className="mt-0.5"
            />
            <div className="space-y-1">
              <label htmlFor="cleanup-remove-related" className="text-sm cursor-pointer">
                连带删除同名 nfo / 海报 / 字幕
              </label>
              <p className="text-xs text-muted-foreground">
                只删 .nfo/.jpg/.png/.srt/.ass/.vtt 等，不碰视频
              </p>
            </div>
          </div>
          <div className="flex items-start gap-2">
            <Checkbox
              id="cleanup-remove-empty-dirs"
              checked={cleanup.removeEmptyDirs ?? false}
              onCheckedChange={(checked) => updateCleanup({ removeEmptyDirs: checked === true })}
              className="mt-0.5"
            />
            <div className="space-y-1">
              <label htmlFor="cleanup-remove-empty-dirs" className="text-sm cursor-pointer">
                清理空目录
              </label>
              <p className="text-xs text-muted-foreground">
                删除 STRM 后清理空父目录
              </p>
            </div>
          </div>
        </div>
      </div>

      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 pt-1">
        <p className={`text-xs ${dirty ? "text-amber-600" : "text-muted-foreground"}`}>
          {dirty ? "有未保存的修改；保存后才会影响任务和手动清理。" : "修改后请点击保存；未保存的改动不会生效。"}
        </p>
        <Button onClick={() => void handleSave()} disabled={saving} className="w-full sm:w-auto">
          {saving ? "保存中..." : "保存清理策略"}
        </Button>
      </div>
    </section>
  );
}
