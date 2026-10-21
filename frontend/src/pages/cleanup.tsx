import { useCallback, useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import axiosInstance from "@/lib/axios";
import type { Settings as SettingsType } from "@/pages/settings/types";
import { CleanupPolicyCard } from "@/pages/strm-cleanup/CleanupPolicyCard";
import { StrmCleanupCard } from "@/pages/StrmCleanupCard";

export default function CleanupPage() {
  const [data, setData] = useState<SettingsType>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  // 页头「刷新」不只刷策略：扫描结果统计、待确认批次、回收站一起刷。
  // 递增 token 让子组件重新拉取，避免三个刷新按钮语义不一致（P2-3）。
  const [refreshToken, setRefreshToken] = useState(0);

  const load = useCallback(async () => {
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
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

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

  const refreshAll = () => {
    void load();
    setRefreshToken((n) => n + 1);
  };

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">STRM 清理</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            策略、扫描、待确认批次和回收站集中在这里。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={refreshAll} disabled={loading || saving}>
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
          {/* 标题层级收敛：页头 h1 + 卡片 CardTitle 已经够了，
              中间的「扫描与执行」section 标题只是重复（P2-1）。 */}
          <StrmCleanupCard
            refreshToken={refreshToken}
            removeStrm={data.cleanup?.removeStrm !== false}
          />
        </>
      )}
    </div>
  );
}
