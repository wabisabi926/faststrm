import { useSearchParams } from "react-router-dom";
import { ScrollText, SlidersHorizontal } from "lucide-react";
import { PageTabs, type PageTabItem } from "@/components/page-tabs";
import LifeEventsPage from "@/pages/life-events";
import SettingsPage from "@/pages/settings";

type LifeEventsTab = "logs" | "config";

const TABS: readonly PageTabItem<LifeEventsTab>[] = [
  { id: "logs", label: "事件日志", icon: ScrollText },
  { id: "config", label: "监控设置", icon: SlidersHorizontal },
];

export default function LifeEventsHubPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab: LifeEventsTab = searchParams.get("tab") === "config" ? "config" : "logs";

  const setTab = (tab: LifeEventsTab) => {
    const next = new URLSearchParams(searchParams);
    if (tab === "logs") {
      next.delete("tab");
    } else {
      next.set("tab", tab);
    }
    setSearchParams(next, { replace: true });
  };

  return (
    <div className="mx-auto max-w-5xl space-y-5">
      <div className="min-w-0">
        <h1 className="text-xl sm:text-2xl font-semibold break-words">生活事件</h1>
        <p className="text-sm text-muted-foreground mt-1 break-words">
          监控设置与 115 文件事件处理日志
        </p>
      </div>

      <PageTabs
        tabs={TABS}
        value={activeTab}
        onChange={setTab}
        ariaLabel="生活事件分区"
      />

      {activeTab === "logs" ? (
        <LifeEventsPage embedded />
      ) : (
        <SettingsPage embedded forceTab="monitor" />
      )}
    </div>
  );
}
