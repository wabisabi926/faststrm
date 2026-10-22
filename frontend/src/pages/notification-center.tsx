import { useSearchParams } from "react-router-dom";
import { Send, Server, ShieldAlert } from "lucide-react";
import { PageTabs, type PageTabItem } from "@/components/page-tabs";
import TelegramNotifyPage from "@/pages/tg-notify";
import EmbyNotifyPage from "@/pages/emby-notify";
import AccountAlertsPage from "@/pages/account-alerts";

type NotifyTab = "telegram" | "emby" | "alerts";

const TABS: readonly PageTabItem<NotifyTab>[] = [
  { id: "telegram", label: "Telegram", icon: Send },
  { id: "emby", label: "Emby", icon: Server },
  { id: "alerts", label: "账号告警", icon: ShieldAlert },
];

function normalizeTab(value: string | null): NotifyTab {
  return value === "emby" || value === "alerts" ? value : "telegram";
}

export default function NotificationCenterPage({ embedded = false }: { embedded?: boolean } = {}) {
  const [searchParams, setSearchParams] = useSearchParams();
  // 独立页面用 ?tab=，嵌入设置页时用 ?sub=，避免和设置页签的 tab 冲突。
  const activeTab = normalizeTab(searchParams.get(embedded ? "sub" : "tab"));

  const setTab = (tab: NotifyTab) => {
    const next = new URLSearchParams(searchParams);
    next.set(embedded ? "sub" : "tab", tab);
    setSearchParams(next, { replace: true });
  };

  return (
    <div className={embedded ? "space-y-5" : "mx-auto max-w-5xl space-y-5"}>
      {!embedded && (
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">通知中心</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            统一管理 Telegram、Emby 与账号告警。
          </p>
        </div>
      )}

      <PageTabs
        tabs={TABS}
        value={activeTab}
        onChange={setTab}
        ariaLabel="通知渠道"
      />

      {activeTab === "telegram" && <TelegramNotifyPage embedded />}
      {activeTab === "emby" && <EmbyNotifyPage embedded />}
      {activeTab === "alerts" && <AccountAlertsPage embedded />}
    </div>
  );
}
