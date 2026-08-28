// 任务表单高级设置：可折叠面板，包含 STRM 前缀覆盖 + 预览路径。
// 从 AddTaskDialog.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { ChevronDown, ChevronRight } from "lucide-react";
import {
  FormControl,
  FormField,
  FormItem,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { HelpLabel } from "./HelpLabel";
import type { Control } from "react-hook-form";
import type { TaskFormValues } from "./AddTaskDialog";

export interface AdvancedStrmSettingsProps {
  open: boolean;
  onToggle: () => void;
  control: Control<TaskFormValues>;
  previewPath: string;
}

export function AdvancedStrmSettings({
  open,
  onToggle,
  control,
  previewPath,
}: AdvancedStrmSettingsProps) {
  return (
    <div className="border rounded-md p-3 space-y-3">
      <button
        type="button"
        onClick={onToggle}
        className="flex items-center gap-2 text-sm font-medium w-full text-left"
      >
        {open ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
        高级设置（覆盖全局默认）
      </button>
      {open && (
        <div className="space-y-4 pt-1">
          {/* Strm 前缀 */}
          <FormField
            control={control}
            name="strmPrefix"
            render={({ field }) => (
              <FormItem>
                <HelpLabel help="覆盖全局 Strm 前缀设置，留空使用全局默认">
                  Strm 前缀
                </HelpLabel>
                <FormControl>
                  <Input {...field} placeholder="留空使用全局默认前缀" className="flex-1" />
                </FormControl>
                <p className="text-xs text-muted-foreground">
                  全局默认值在「设置 → STRM 生成设置」中配置
                </p>
                <FormMessage />
              </FormItem>
            )}
          />

          {/* 预览路径 */}
          <div className="text-sm text-amber-600 font-medium bg-amber-50 dark:bg-amber-900/20 px-3 py-2 rounded">
            预览：{previewPath}
          </div>
        </div>
      )}
    </div>
  );
}
