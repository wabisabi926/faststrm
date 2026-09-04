// 删除同步设置区块：开关 + 路径映射表 + 本地/云盘目录选择器 + 工作流程提示。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

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
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { FolderOpen, HardDrive, XCircle } from "lucide-react";
import type { EmbySettings } from "./types";

export interface SyncDeleteSectionProps {
  settings: EmbySettings;
  loading: boolean;
  accounts: string[];
  updateSetting: <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => void;
  onSave: () => void;
  // 路径映射管理
  newMappingEmbyPath: string;
  newMappingCloudPath: string;
  newMappingAccount: string;
  setNewMappingEmbyPath: (v: string) => void;
  setNewMappingCloudPath: (v: string) => void;
  setNewMappingAccount: (v: string) => void;
  updatePathMapping: (index: number, field: "embyPath" | "cloudPath" | "account", value: string) => void;
  addPathMapping: () => void;
  removePathMapping: (index: number) => void;
  // 目录选择器触发
  openFolderPickerForNew: () => void;
  openFolderPickerForMapping: (index: number) => void;
  openCloudPickerForNew: () => void;
  openCloudPickerForMapping: (index: number) => void;
}

export function SyncDeleteSection({
  settings,
  loading,
  accounts,
  updateSetting,
  onSave,
  newMappingEmbyPath,
  newMappingCloudPath,
  newMappingAccount,
  setNewMappingEmbyPath,
  setNewMappingCloudPath,
  setNewMappingAccount,
  updatePathMapping,
  addPathMapping,
  removePathMapping,
  openFolderPickerForNew,
  openFolderPickerForMapping,
  openCloudPickerForNew,
  openCloudPickerForMapping,
}: SyncDeleteSectionProps) {
  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-center gap-2">
        <XCircle className="h-5 w-5" />
        <h2 className="text-base font-medium">删除同步</h2>
      </div>
      <p className="text-xs text-muted-foreground mt-1">
        监听 Emby 删除事件，自动删除本地 STRM 文件 + 关联字幕/图片 + 清理 DB 记录
      </p>
      <div className="space-y-3">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="syncDeleteEnabled"
              checked={!!settings.syncDeleteEnabled}
              onCheckedChange={(checked) =>
                updateSetting("syncDeleteEnabled", checked === true)
              }
            />
            <div className="flex-1">
              <Label htmlFor="syncDeleteEnabled" className="cursor-pointer">
                启用删除同步
              </Label>
              <p className="text-xs text-muted-foreground">
                Emby 删除媒体时自动清理 STRM
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="syncDeleteDryRun"
              checked={!!settings.syncDeleteDryRun}
              onCheckedChange={(checked) =>
                updateSetting("syncDeleteDryRun", checked === true)
              }
              disabled={!settings.syncDeleteEnabled}
            />
            <div className="flex-1">
              <Label
                htmlFor="syncDeleteDryRun"
                className={`cursor-pointer ${!settings.syncDeleteEnabled ? "text-muted-foreground" : ""}`}
              >
                试运行模式
              </Label>
              <p className="text-xs text-muted-foreground">
                只记日志不实际删除（首次验证用）
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-2 p-3 rounded-lg border">
            <Checkbox
              id="syncDeleteNotify"
              checked={!!settings.syncDeleteNotify}
              onCheckedChange={(checked) =>
                updateSetting("syncDeleteNotify", checked === true)
              }
              disabled={!settings.syncDeleteEnabled}
            />
            <div className="flex-1">
              <Label
                htmlFor="syncDeleteNotify"
                className={`cursor-pointer ${!settings.syncDeleteEnabled ? "text-muted-foreground" : ""}`}
              >
                删除通知
              </Label>
              <p className="text-xs text-muted-foreground">
                删除时发送 TG 通知（替代上方删除通知）
              </p>
            </div>
          </div>
        </div>

        {/* 路径映射表
           行结构：[Emby输入 📂] → [网盘输入 💾] [账号下拉] [操作按钮]
           账号 & 操作按钮在右侧，所有行垂直对齐 */}
        <div className="space-y-2">
          <Label>路径映射（Emby 路径 → 115 网盘路径）</Label>

          {(settings.syncDeletePathMappings || []).map((mapping, index) => (
            <div
              key={index}
              className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2"
            >
              {/* 左段：Emby 输入 + 目录选择图标 */}
              <div className="flex-1 flex gap-1 items-center">
                <Input
                  className="flex-1 min-w-0"
                  placeholder="Emby 路径前缀，如 /app/data/strm/电影"
                  value={mapping.embyPath}
                  onChange={(e) => updatePathMapping(index, "embyPath", e.target.value)}
                />
                <TooltipProvider>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="shrink-0 inline-flex">
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          className="shrink-0"
                          onClick={() => openFolderPickerForMapping(index)}
                        >
                          <FolderOpen className="h-4 w-4" />
                        </Button>
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>选择包含 STRM 文件的本地目录</TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              </div>

              {/* 箭头：sm 横排 → / mobile 竖排 ↓ */}
              <span className="text-muted-foreground hidden sm:inline self-center shrink-0 px-1">→</span>
              <span className="text-muted-foreground sm:hidden w-full text-center shrink-0">↓</span>

              {/* 中段：115 网盘输入 + 网盘选择图标（💾 账号为空则灰）*/}
              <div className="flex-1 flex gap-1 items-center">
                <Input
                  className="flex-1 min-w-0"
                  placeholder="网盘路径前缀，如 /电影"
                  value={mapping.cloudPath}
                  onChange={(e) => updatePathMapping(index, "cloudPath", e.target.value)}
                />
                <TooltipProvider>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="shrink-0 inline-flex">
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          className="shrink-0"
                          disabled={!mapping.account}
                          onClick={() => openCloudPickerForMapping(index)}
                        >
                          <HardDrive className="h-4 w-4" />
                        </Button>
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>
                      {mapping.account ? "选择网盘目录" : "请先选择具体账号"}
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              </div>

              {/* 右段：账号下拉 + 删除按钮，两排对齐 */}
              <div className="flex gap-2 items-center shrink-0 w-full sm:w-auto sm:justify-end justify-end">
                <Select
                  value={mapping.account || "__all__"}
                  onValueChange={(v) =>
                    updatePathMapping(index, "account", v === "__all__" ? "" : v)
                  }
                >
                  <SelectTrigger className="h-9 w-full sm:w-[120px] shrink-0">
                    <SelectValue placeholder="账号（可选）" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__">遍历全部账号</SelectItem>
                    {accounts.map((acc) => (
                      <SelectItem key={acc} value={acc}>
                        {acc}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => removePathMapping(index)}
                  className="shrink-0"
                >
                  删除
                </Button>
              </div>
            </div>
          ))}

          {/* 新建一行，与上面映射行结构完全一致：[Emby 📂] → [网盘 💾] [账号] [添加] */}
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2">
            <div className="flex-1 flex gap-1 items-center">
              <Input
                className="flex-1 min-w-0"
                placeholder="Emby 路径前缀"
                value={newMappingEmbyPath}
                onChange={(e) => setNewMappingEmbyPath(e.target.value)}
              />
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className="shrink-0 inline-flex">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="shrink-0"
                        onClick={openFolderPickerForNew}
                      >
                        <FolderOpen className="h-4 w-4" />
                      </Button>
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>选择包含 STRM 文件的本地目录</TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </div>

            <span className="text-muted-foreground hidden sm:inline self-center shrink-0 px-1">→</span>
            <span className="text-muted-foreground sm:hidden w-full text-center shrink-0">↓</span>

            <div className="flex-1 flex gap-1 items-center">
              <Input
                className="flex-1 min-w-0"
                placeholder="网盘路径前缀"
                value={newMappingCloudPath}
                onChange={(e) => setNewMappingCloudPath(e.target.value)}
              />
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className="shrink-0 inline-flex">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="shrink-0"
                        disabled={!newMappingAccount}
                        onClick={openCloudPickerForNew}
                      >
                        <HardDrive className="h-4 w-4" />
                      </Button>
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>
                    {newMappingAccount ? "选择网盘目录" : "请先选择具体账号"}
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </div>

            <div className="flex gap-2 items-center shrink-0 w-full sm:w-auto sm:justify-end justify-end">
              <Select
                value={newMappingAccount || "__all__"}
                onValueChange={(v) => setNewMappingAccount(v === "__all__" ? "" : v)}
              >
                <SelectTrigger className="h-9 w-full sm:w-[120px] shrink-0">
                  <SelectValue placeholder="账号（可选）" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__all__">遍历全部账号</SelectItem>
                  {accounts.map((acc) => (
                    <SelectItem key={acc} value={acc}>
                      {acc}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              <Button
                size="sm"
                onClick={addPathMapping}
                className="shrink-0"
              >
                添加
              </Button>
            </div>
          </div>

          <p className="text-xs text-muted-foreground">
            只有匹配到 Emby 路径前缀的删除事件才会被处理。账号留空时遍历所有 115 账号删除 DB 记录。
          </p>
        </div>

        <div className="flex flex-wrap gap-2">
          <Button onClick={onSave} disabled={loading}>
            {loading ? "保存中..." : "保存设置"}
          </Button>
        </div>

        <div className="p-3 bg-muted/50 rounded-lg text-sm">
          <p className="font-semibold mb-1">工作流程</p>
          <p className="text-xs text-muted-foreground">
            Emby 删除媒体 → 匹配路径映射 → 去重检查（60s） → 防误删（STRM 存在 + 标题匹配 + 目录文件数 ≤100） → 删 STRM + 字幕 + nfo → 清空目录 → 更新 DB → TG 通知
          </p>
        </div>
      </div>
    </section>
  );
}
