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
    <section className="w-full min-w-0 border rounded-md p-3 sm:p-5 space-y-5">
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

        {/* 路径映射表（严格对齐用户"移动端 4 行规格"文字图，1:1；与 MonitorSettingsTab 同构仅方向相反）
           ============================================================
           移动端（<640px）每行 4 行堆叠：
             Line 1 :  [Emby 本地路径输入 .........................]  [📂]
             Line 2 :                    ↓  (单独一行，居中)
             Line 3 :  [账号下拉 ▼]  [网盘路径输入 ...............]  [💾]
             Line 4 :  [删除 / 添加]  (整行宽按钮，不挤右缘)
           sm+（>=640px，桌面横排）传统一行：
             [Emby📂]  →  [账号▼ 网盘💾]  [删除/添加]
           ============================================================
           min-w-0 + Input/SelectValue.truncate 贯穿；账号 mobile 1/3、sm 140px，网盘 flex-1。
        */}
        <div className="space-y-4 w-full min-w-0">
          <Label>路径映射（Emby 路径 → 115 网盘路径）</Label>

          {(settings.syncDeletePathMappings || []).map((mapping, index) => (
            /* 行容器：mobile 堆叠 gap-2；sm 切横排 items-center */
            <div
              key={index}
              className="w-full min-w-0 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2"
            >
              {/* ============ Line 1 / sm 左段：Emby 输入 + 📂 ============ */}
              <div className="flex-1 flex gap-1 items-center min-w-0 w-full sm:w-auto sm:mr-2">
                <Input
                  className="flex-1 min-w-0 w-full truncate break-all"
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

              {/* ============ Line 2：↓ 居中（mobile only；sm 内联 →） ============ */}
              <span className="text-muted-foreground hidden sm:inline self-center shrink-0 px-1">→</span>
              <span
                className="sm:hidden w-full text-center shrink-0 select-none text-muted-foreground"
                aria-hidden
              >
                ↓
              </span>

              {/* ============ Line 3 / sm 中段+右段：账号▼(左) + 网盘输入(右) + 💾 + [删除 sm 内联]
                             mobile：账号 1/3 左、网盘 2/3 右，同一行；sm 账号 140px 网盘 flex-1
              ============ */}
              <div className="w-full sm:w-auto sm:flex-1 min-w-0 flex flex-row gap-1 sm:gap-0 items-center">
                {/* 账号下拉 */}
                <div className="w-1/3 sm:w-[140px] sm:shrink-0 min-w-0 sm:mr-2">
                  <Select
                    value={mapping.account || "__all__"}
                    onValueChange={(v) =>
                      updatePathMapping(index, "account", v === "__all__" ? "" : v)
                    }
                  >
                    <SelectTrigger className="h-9 w-full min-w-0">
                      <SelectValue placeholder="账号（可选）" className="truncate" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__all__" className="truncate">遍历全部账号</SelectItem>
                      {accounts.map((acc) => (
                        <SelectItem key={acc} value={acc} className="truncate">
                          {acc}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                {/* 网盘路径输入 + 💾 */}
                <div className="flex-1 flex gap-1 items-center min-w-0">
                  <Input
                    className="flex-1 min-w-0 w-full truncate break-all"
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

                {/* [删除] sm 内联在行尾 */}
                <div className="hidden sm:flex sm:shrink-0 sm:w-auto sm:ml-2">
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

              {/* ============ Line 4（mobile only）：删除按钮整行宽 ============ */}
              <div className="sm:hidden w-full min-w-0">
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => removePathMapping(index)}
                  className="w-full shrink-0"
                >
                  删除
                </Button>
              </div>
            </div>
          ))}

          {/* ============ 新建行：与已有映射行严格同构 ============ */}
          <div className="w-full min-w-0 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2 pt-1 border-t border-dashed">
            {/* Line 1：Emby 输入 + 📂 */}
            <div className="flex-1 flex gap-1 items-center min-w-0 w-full sm:w-auto sm:mr-2">
              <Input
                className="flex-1 min-w-0 w-full truncate break-all"
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

            {/* Line 2：↓ 居中 */}
            <span className="text-muted-foreground hidden sm:inline self-center shrink-0 px-1">→</span>
            <span className="sm:hidden w-full text-center shrink-0 select-none text-muted-foreground" aria-hidden>↓</span>

            {/* Line 3：账号▼ + 网盘路径 + 💾 + [添加 sm 内联] */}
            <div className="w-full sm:w-auto sm:flex-1 min-w-0 flex flex-row gap-1 sm:gap-0 items-center">
              <div className="w-1/3 sm:w-[140px] sm:shrink-0 min-w-0 sm:mr-2">
                <Select
                  value={newMappingAccount || "__all__"}
                  onValueChange={(v) => setNewMappingAccount(v === "__all__" ? "" : v)}
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue placeholder="账号（可选）" className="truncate" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__" className="truncate">遍历全部账号</SelectItem>
                    {accounts.map((acc) => (
                      <SelectItem key={acc} value={acc} className="truncate">
                        {acc}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              <div className="flex-1 flex gap-1 items-center min-w-0">
                <Input
                  className="flex-1 min-w-0 w-full truncate break-all"
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

              {/* [添加] sm 内联在行尾 */}
              <div className="hidden sm:flex sm:shrink-0 sm:w-auto sm:ml-2">
                <Button size="sm" onClick={addPathMapping} className="shrink-0">添加</Button>
              </div>
            </div>

            {/* Line 4（mobile only）：添加按钮整行宽 */}
            <div className="sm:hidden w-full min-w-0">
              <Button size="sm" onClick={addPathMapping} className="w-full shrink-0">添加</Button>
            </div>
          </div>

          <p className="text-xs text-muted-foreground break-words">
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
