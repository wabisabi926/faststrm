// 统计卡片子组件：展示单项数值与可选操作插槽。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

import * as React from "react";

export interface StatCardProps {
  label: string;
  value: number;
  icon: React.ReactNode;
  tone?: "default" | "success" | "destructive" | "warning";
  hint?: string;
  children?: React.ReactNode;
}

export function StatCard({
  label,
  value,
  icon,
  tone = "default",
  hint,
  children,
}: StatCardProps) {
  const toneClass =
    tone === "destructive"
      ? "text-destructive"
      : tone === "success"
      ? "text-green-600"
      : tone === "warning"
      ? "text-amber-600"
      : "text-foreground";
  return (
    <div className="rounded-md border p-3 space-y-1">
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <span className={toneClass}>{icon}</span>
        {label}
      </div>
      <div className={`text-2xl font-semibold ${toneClass}`}>{value}</div>
      {hint && <div className="text-[11px] text-muted-foreground">{hint}</div>}
      {children || null}
    </div>
  );
}
