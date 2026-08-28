// 表单字段标签 + 帮助提示 tooltip。
// 抽出 AddTaskDialog 中 3 处重复的 HelpCircle 提示，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { HelpCircle } from "lucide-react";
import { FormLabel } from "@/components/ui/form";
import type { ReactNode } from "react";

export interface HelpLabelProps {
  children: ReactNode;
  help: string;
}

export function HelpLabel({ children, help }: HelpLabelProps) {
  return (
    <FormLabel className="flex items-center gap-1">
      {children}
      <div className="group relative">
        <HelpCircle className="w-4 h-4 text-gray-400 hover:text-gray-600 cursor-help" />
        <div className="absolute bottom-full left-1/2 transform -translate-x-1/2 mb-2 px-3 py-2 bg-gray-800 text-white text-sm rounded-md opacity-0 group-hover:opacity-100 transition-opacity duration-200 pointer-events-none whitespace-nowrap z-10">
          {help}
          <div className="absolute top-full left-1/2 transform -translate-x-1/2 border-4 border-transparent border-t-gray-800"></div>
        </div>
      </div>
    </FormLabel>
  );
}
