import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import axiosInstance from "@/lib/axios";
import type { Settings as SettingsType } from "@/pages/settings/types";
import { CleanupPolicyCard } from "@/pages/strm-cleanup/CleanupPolicyCard";
import { StrmCleanupCard } from "@/pages/StrmCleanupCard";

const AUTO_CLEANUP_LABELS: Record<string, string> = {
  off: "关闭",
  mark_only: "仅标记",
  auto_clean: "自动清理",
};

const CLEANUP_CONFIRM_LABELS: Record<string, string> = {
  none: "关闭",
  plugin_ui: "Web UI",
  telegram: "Telegram",
};

export default function CleanupPage() {
  const [data, setData] = useState<SettingsType>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      const response = await axiosInstance.get("/api/settings");
      setData(response.data || {});
    } catch (err) {
      console.error("加载清理策略失败:", err);
      setError("加载清理策略失败，请检查服务状态后重试。");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const save = async (): Promise<boolean> => {
    setSaving(true);
    try {
      await axiosInstance.post("/api/settings", data);
      toast.success("清理策略已保存");
      return true;
    } catch (err) {
      const axiosErr = err as { response?: { data?: { error?: string; message?: string } } };
      toast.error(axiosErr.response?.data?.error || axiosErr.response?.data?.message || "保存清理策略失败");
      return false;
    } finally {
      setSaving(false);
    }
  };

  const cleanup = data.cleanup || {};
  const autoMode = data.download?.fullSyncCleanupOrphans ?? "auto_clean";
  const policySummary = [
    `任务同步自动清理：${AUTO_CLEANUP_LABELS[autoMode] ?? autoMode}`,
    `阈值 ${cleanup.maxThreshold ?? 1000} / ${Math.round((cleanup.maxRatio ?? 0.1) * 100)}% / ${cleanup.maxDirs ?? 20}`,
    `确认：${CLEANUP_CONFIRM_LABELS[cleanup.confirmMode ?? "none"] ?? cleanup.confirmMode}`,
  ].join(" · ");

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">STRM 清理</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            策略、扫描、待确认批次和回收站集中在这里。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading || saving}>
          <RefreshCw className={`mr-1 h-4 w-4 ${loading ? "animate-spin" : ""}`} />
          刷新
        </Button>
      </div>

      {loading ? (
        <div className="flex items-center justify-center h-48 text-sm text-muted-foreground">
          正在加载清理策略...
        </div>
      ) : error ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
          <p>{error}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => void load()}>
            重试
          </Button>
        </div>
      ) : (
        <>
          <CleanupPolicyCard data={data} setData={setData} saving={saving} onSave={save} />
          <section className="border rounded-md p-3 sm:p-5 space-y-5">
            <div>
              <h2 className="text-base font-medium">扫描与执行</h2>
              <p className="text-xs text-muted-foreground mt-1">
                扫描本地与网盘的一致性，清理失效 STRM 或补生成漏项。
              </p>
            </div>
            <StrmCleanupCard policySummary={policySummary} />
          </section>
        </>
      )}
    </div>
  );
}
