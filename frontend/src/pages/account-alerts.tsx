import type { ReactNode } from "react";
import { useState, useEffect, useCallback } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import {
  ShieldAlert,
  CheckCircle,
  AlertCircle,
  AlertTriangle,
  HelpCircle,
  Bell,
  RefreshCw,
  Loader2,
} from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import axiosInstance from "@/lib/axios";
import { toast } from "sonner";

interface AccountAlertsConfig {
  enabled: boolean;
  onError: boolean;
  onRecover: boolean;
}

interface AccountStatusInfo {
  name: string;
  accountType: string;
  status: string;
  lastChecked?: string;
  message?: string;
}

type AccountStatusResult = {
  name: string;
  status: "ok" | "error" | "unknown";
  message?: string;
};

export default function AccountAlertsPage() {
  const [accountAlerts, setAccountAlerts] = useState<AccountAlertsConfig>({
    enabled: false,
    onError: true,
    onRecover: true,
  });
  const [alertsLoading, setAlertsLoading] = useState(false);
  const [alertsSuccess, setAlertsSuccess] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [accounts, setAccounts] = useState<AccountStatusInfo[]>([]);
  const [statusMap, setStatusMap] = useState<Record<string, AccountStatusResult>>({});
  const [isCheckingStatus, setIsCheckingStatus] = useState(false);

  const loadAccountAlerts = async () => {
    try {
      const response = await axiosInstance.get("/api/notify/alerts");
      if (response.data) {
        setAccountAlerts({
          enabled: response.data.enabled ?? false,
          onError: response.data.onError ?? true,
          onRecover: response.data.onRecover ?? true,
        });
      }
    } catch (error) {
      console.error("加载账户状态通知配置失败:", error);
    }
  };

  const loadAccounts = async () => {
    try {
      const response = await axiosInstance.get("/api/account");
      setAccounts(response.data?.accounts || response.data || []);
    } catch (error) {
      console.error("加载账号列表失败:", error);
    }
  };

  const checkStatus = useCallback(async () => {
    try {
      setIsCheckingStatus(true);
      const res = await axiosInstance.get("/api/account/status");
      const map: Record<string, AccountStatusResult> = {};
      for (const s of res.data.results) {
        map[s.name] = s;
      }
      setStatusMap(map);
    } catch {
      toast.error("检测账户状态失败");
    } finally {
      setIsCheckingStatus(false);
    }
  }, []);

  useEffect(() => {
    void loadAccountAlerts();
    void loadAccounts();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (accounts.length > 0) {
      checkStatus();
    }
  }, [accounts.length, checkStatus]);

  const saveAccountAlerts = async () => {
    try {
      setAlertsLoading(true);
      setAlertsSuccess(null);
      setError(null);

      const response = await axiosInstance.post("/api/notify/alerts", accountAlerts);

      if (response.data.success) {
        setAlertsSuccess("账户状态通知配置保存成功！");
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "保存账户状态通知配置失败");
    } finally {
      setAlertsLoading(false);
    }
  };

  const renderStatusBadge = (accountName: string) => {
    const status = statusMap[accountName];

    if (!status) {
      return (
        <div className="flex items-center gap-1.5 text-muted-foreground">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          <span className="text-xs">检测中...</span>
        </div>
      );
    }

    const config: Record<string, { icon: ReactNode; label: string; cls: string }> = {
      ok: {
        icon: <CheckCircle className="w-3.5 h-3.5 text-green-500" />,
        label: "正常",
        cls: "text-green-500",
      },
      error: {
        icon: <AlertTriangle className="w-3.5 h-3.5 text-red-500" />,
        label: "异常",
        cls: "text-red-500",
      },
      unknown: {
        icon: <HelpCircle className="w-3.5 h-3.5 text-muted-foreground" />,
        label: "未知",
        cls: "text-muted-foreground",
      },
    };

    const c = config[status.status] || config.unknown;
    const displayMessage =
      status.message && status.message.length > 30
        ? status.message.slice(0, 30) + "..."
        : status.message;

    return (
      <div className="flex flex-col gap-0.5">
        <div className="flex items-center gap-1.5" title={status.message || c.label}>
          {c.icon}
          <span className={`text-xs font-medium ${c.cls}`}>{c.label}</span>
        </div>
        {displayMessage && (
          <span
            className="text-xs text-muted-foreground truncate max-w-[200px]"
            title={status.message}
          >
            {displayMessage}
          </span>
        )}
      </div>
    );
  };

  return (
    <div className="mx-auto max-w-4xl lg:max-w-5xl space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 min-w-0">
        <div className="min-w-0 break-words">
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <ShieldAlert className="h-6 w-6 shrink-0" />
            <span className="break-words">账户状态通知</span>
          </h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            监听 115 账号 Cookie 状态，异常或恢复时自动推送通知
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => checkStatus()}
          disabled={isCheckingStatus}
          size="sm"
          className="shrink-0"
        >
          <RefreshCw className={`w-4 h-4 mr-2 ${isCheckingStatus ? "animate-spin" : ""}`} />
          {isCheckingStatus ? "检测中..." : "检测状态"}
        </Button>
      </div>

      <section className="border rounded-md p-4 sm:p-5 space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-medium">账号状态总览</h2>
          <span className="text-xs text-muted-foreground">
            共 {accounts.length} 个账号
          </span>
        </div>
        <div className="flex flex-col gap-3">
          {accounts.map((acc) => (
            <div
              key={acc.name}
              className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between p-3 border rounded"
            >
              <div className="flex items-center gap-2 min-w-0">
                <span className="font-medium break-words">{acc.name}</span>
                <span className="text-xs text-muted-foreground shrink-0">
                  {acc.accountType}
                </span>
              </div>
              {renderStatusBadge(acc.name)}
            </div>
          ))}
          {accounts.length === 0 && (
            <p className="text-sm text-muted-foreground">
              暂无账号，请先在「账户」页面添加
            </p>
          )}
        </div>
      </section>

      <section className="border rounded-md p-4 sm:p-5 space-y-5">
        <div>
          <div className="flex items-center gap-2">
            <ShieldAlert className="h-5 w-5" />
            <h2 className="text-base font-medium">通知配置</h2>
            <Badge variant={accountAlerts.enabled ? "default" : "outline"}>
              {accountAlerts.enabled ? "已启用" : "未启用"}
            </Badge>
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            当账号状态异常或恢复时，自动发送 Telegram 通知
          </p>
        </div>
        <div className="space-y-3">
          <div className="flex items-center space-x-2">
            <Checkbox
              id="alertsEnabled"
              checked={accountAlerts.enabled}
              onCheckedChange={(checked) =>
                setAccountAlerts({ ...accountAlerts, enabled: checked === true })
              }
            />
            <label
              htmlFor="alertsEnabled"
              className="text-sm font-medium leading-none cursor-pointer"
            >
              启用账户状态通知
            </label>
          </div>

          <Separator />

          <div className="grid gap-3 md:grid-cols-2">
            <div className="flex items-center space-x-2">
              <Checkbox
                id="onError"
                checked={accountAlerts.onError}
                disabled={!accountAlerts.enabled}
                onCheckedChange={(checked) =>
                  setAccountAlerts({ ...accountAlerts, onError: checked === true })
                }
              />
              <label
                htmlFor="onError"
                className={`text-sm leading-none ${
                  !accountAlerts.enabled ? "text-muted-foreground" : "cursor-pointer"
                }`}
              >
                <span className="flex items-center gap-1">
                  <AlertCircle className="h-3.5 w-3.5 text-red-500" />
                  账号异常时通知
                </span>
              </label>
            </div>

            <div className="flex items-center space-x-2">
              <Checkbox
                id="onRecover"
                checked={accountAlerts.onRecover}
                disabled={!accountAlerts.enabled}
                onCheckedChange={(checked) =>
                  setAccountAlerts({ ...accountAlerts, onRecover: checked === true })
                }
              />
              <label
                htmlFor="onRecover"
                className={`text-sm leading-none ${
                  !accountAlerts.enabled ? "text-muted-foreground" : "cursor-pointer"
                }`}
              >
                <span className="flex items-center gap-1">
                  <CheckCircle className="h-3.5 w-3.5 text-green-500" />
                  账号恢复正常时通知
                </span>
              </label>
            </div>
          </div>

          <Alert className="bg-blue-500/10 border-blue-500/20">
            <Bell className="h-4 w-4 text-blue-400" />
            <AlertDescription className="text-xs text-blue-300">
              <strong>通知触发条件：</strong>
              <br />
              • 账号状态从"正常"变为"异常"时发送异常通知
              <br />
              • 账号状态从"异常"恢复为"正常"时发送恢复通知
              <br />
              • 每个状态变化只会通知一次，避免重复打扰
            </AlertDescription>
          </Alert>

          {error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription className="text-xs">{error}</AlertDescription>
            </Alert>
          )}

          <div className="flex flex-wrap items-center gap-2">
            <Button
              onClick={saveAccountAlerts}
              disabled={alertsLoading}
              size="sm"
            >
              {alertsLoading ? "保存中..." : "保存通知设置"}
            </Button>
            <Button
              onClick={() => loadAccountAlerts()}
              disabled={alertsLoading}
              variant="outline"
              size="sm"
            >
              <RefreshCw className="h-4 w-4 mr-1" />
              重新加载
            </Button>
            {alertsSuccess && (
              <span className="text-xs text-green-500 flex items-center gap-1">
                <CheckCircle className="h-3.5 w-3.5" />
                {alertsSuccess}
              </span>
            )}
          </div>
        </div>
      </section>

      <section className="border rounded-md p-4 sm:p-5 bg-muted/30">
        <h2 className="text-sm font-medium mb-2">前置条件</h2>
        <ul className="text-xs text-muted-foreground space-y-1 list-disc list-inside">
          <li>需要在「TG 通知」页面配置 Bot Token 和 Chat ID</li>
          <li>需要在「TG 通知」页面启动轮询模式</li>
          <li>账号状态由生活事件监控自动检测</li>
        </ul>
      </section>
    </div>
  );
}