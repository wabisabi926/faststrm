import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { HeartPulse } from "lucide-react";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";

interface CookieInspectConfig {
  enabled: boolean;
  intervalHours: number;
}

interface AccountAlertsConfig {
  enabled: boolean;
  onError: boolean;
  onRecover: boolean;
  cookieInspect: CookieInspectConfig;
}

interface AccountHealthPanelProps {
  accountNames: string[];
  statusMap: Record<string, { status: "ok" | "error" | "unknown" }>;
}

const DEFAULT_CONFIG: AccountAlertsConfig = {
  enabled: false,
  onError: true,
  onRecover: true,
  cookieInspect: { enabled: true, intervalHours: 6 },
};

export function AccountHealthPanel({ accountNames, statusMap }: AccountHealthPanelProps) {
  const [config, setConfig] = useState<AccountAlertsConfig>(DEFAULT_CONFIG);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const response = await axiosInstance.get("/api/notify/alerts");
        if (cancelled) return;
        const data = response.data || {};
        setConfig({
          enabled: data.enabled ?? false,
          onError: data.onError ?? true,
          onRecover: data.onRecover ?? true,
          cookieInspect: {
            enabled: data.cookieInspect?.enabled ?? true,
            intervalHours: data.cookieInspect?.intervalHours ?? 6,
          },
        });
        setLoaded(true);
      } catch {
        if (!cancelled) setError("加载巡检设置失败");
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await axiosInstance.post("/api/notify/alerts", config);
      toast.success("巡检设置已保存");
    } catch (err) {
      const axiosErr = err as { response?: { data?: { error?: string } } };
      setError(axiosErr.response?.data?.error || "保存巡检设置失败");
    } finally {
      setSaving(false);
    }
  };

  const counts = accountNames.reduce(
    (acc, name) => {
      const status = statusMap[name]?.status;
      if (status === "ok") acc.ok += 1;
      else if (status === "error") acc.error += 1;
      else acc.unknown += 1;
      return acc;
    },
    { ok: 0, error: 0, unknown: 0 }
  );

  return (
    <section className="border rounded-md p-3 sm:p-4 space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-2">
          <HeartPulse className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-base font-medium">账号健康与巡检</h2>
        </div>
        <div className="flex flex-wrap gap-1.5">
          <Badge variant="outline" className="text-[10px] text-green-600 dark:text-green-400">
            正常 {counts.ok}
          </Badge>
          <Badge variant="outline" className="text-[10px] text-red-600 dark:text-red-400">
            异常 {counts.error}
          </Badge>
          <Badge variant="outline" className="text-[10px] text-muted-foreground">
            未检测 {counts.unknown}
          </Badge>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className="flex items-center gap-2">
          <Checkbox
            id="cookieInspectEnabled"
            checked={config.cookieInspect.enabled}
            disabled={!loaded || saving}
            onCheckedChange={(checked) =>
              setConfig({
                ...config,
                cookieInspect: { ...config.cookieInspect, enabled: checked === true },
              })
            }
          />
          <label htmlFor="cookieInspectEnabled" className="cursor-pointer text-sm">
            Cookie 主动巡检
          </label>
        </div>
        <div className="flex items-center gap-1.5">
          <Input
            type="number"
            min="1"
            max="168"
            aria-label="巡检间隔（小时）"
            className="h-8 w-20"
            disabled={!loaded || saving || !config.cookieInspect.enabled}
            value={config.cookieInspect.intervalHours}
            onChange={(e) =>
              setConfig({
                ...config,
                cookieInspect: {
                  ...config.cookieInspect,
                  intervalHours: Math.min(168, Math.max(1, parseInt(e.target.value) || 6)),
                },
              })
            }
          />
          <span className="text-xs text-muted-foreground">小时</span>
        </div>
        <Button size="sm" variant="outline" onClick={() => void save()} disabled={!loaded || saving}>
          {saving ? "保存中..." : "保存巡检设置"}
        </Button>
        <Link to="/notify?tab=alerts" className="text-xs text-primary hover:underline">
          账号异常通知 →
        </Link>
      </div>

      <p className="text-xs text-muted-foreground">
        后台按间隔检查 Cookie 有效性；异常或恢复通知在「通知中心 › 账号告警」配置。
      </p>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </section>
  );
}
