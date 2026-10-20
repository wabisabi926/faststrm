import type { LucideIcon } from "lucide-react";

export interface PageTabItem<T extends string> {
  id: T;
  label: string;
  icon?: LucideIcon;
}

export function PageTabs<T extends string>({
  tabs,
  value,
  onChange,
  ariaLabel = "页面分区",
}: {
  tabs: readonly PageTabItem<T>[];
  value: T;
  onChange: (id: T) => void;
  ariaLabel?: string;
}) {
  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      className="flex flex-nowrap gap-1 border-b border-border overflow-x-auto scrollbar-none -mx-1 px-1"
    >
      {tabs.map((tab) => {
        const active = tab.id === value;
        const Icon = tab.icon;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onChange(tab.id)}
            className={`shrink-0 px-3 py-2 text-sm font-medium transition-colors relative whitespace-nowrap ${
              active ? "text-foreground" : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {Icon && <Icon className="inline-block h-4 w-4 mr-1" />}
            {tab.label}
            {active && (
              <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-foreground" />
            )}
          </button>
        );
      })}
    </div>
  );
}
