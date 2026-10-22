import { useEffect } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import { Checkbox } from "@/components/ui/checkbox";
import {
  ShieldAlert,
  CheckCircle,
  AlertCircle,
  Bell,
  RefreshCw,
} from "lucide-react";
import { useAccountAlertsConfig } from "@/hooks/useAccountAlertsConfig";

/**
 * 通知中心 › 账号告警：账号状态异常/恢复的 Telegram 通知开关。
 * 配置读写与账号页健康面板共用 useAccountAlertsConfig，保存时只提交本页字段，
 * 不会覆盖账号页维护的 Cookie 巡检开关。
 */
export default function AccountAlertsPage({
  embedded = false,
}: {
  embedded?: boolean;
} = {}) {
  const { config, loaded, saving, error, success, reload, save, update, clearFeedback } =
    useAccountAlertsConfig();

  useEffect(() => {
    void reload();
  }, [reload]);

  const handleSave = async () => {
    await save(
      { enabled: config.enabled, onError: config.onError, onRecover: config.onRecover },
      "账号告警设置保存成功"
    );
  };

  return (
    <div className={`${embedded ? "" : "mx-auto max-w-5xl"} space-y-6`}>
      {!embedded && (
        <div className="min-w-0 break-words">
          <h1 className="text-xl sm:text-2xl font-semibold flex items-center gap-2">
            <ShieldAlert className="h-6 w-6 shrink-0" />
            <span>账号告警</span>
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            账号状态异常或恢复时的通知开关
          </p>
        </div>
      )}

      <section className="border rounded-md p-4 sm:p-5 space-y-5">
        <div>
          <div className="flex items-center gap-2">
            <ShieldAlert className="h-5 w-5" />
            <h2 className="text-base font-medium">通知配置</h2>
            <Badge variant={config.enabled ? "default" : "outline"}>
              {config.enabled ? "已启用" : "未启用"}
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
              checked={config.enabled}
              disabled={!loaded || saving}
              onCheckedChange={(checked) => {
                clearFeedback();
                update({ enabled: checked === true });
              }}
            />
            <label htmlFor="alertsEnabled" className="text-sm font-medium leading-none cursor-pointer">
              启用账号状态通知
            </label>
          </div>

          <Separator />

          <div className="grid gap-3 md:grid-cols-2">
            <div className="flex items-center space-x-2">
              <Checkbox
                id="onError"
                checked={config.onError}
                disabled={!config.enabled || saving}
                onCheckedChange={(checked) => {
                  clearFeedback();
                  update({ onError: checked === true });
                }}
              />
              <label
                htmlFor="onError"
                className={`text-sm leading-none ${
                  !config.enabled ? "text-muted-foreground" : "cursor-pointer"
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
                checked={config.onRecover}
                disabled={!config.enabled || saving}
                onCheckedChange={(checked) => {
                  clearFeedback();
                  update({ onRecover: checked === true });
                }}
              />
              <label
                htmlFor="onRecover"
                className={`text-sm leading-none ${
                  !config.enabled ? "text-muted-foreground" : "cursor-pointer"
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
              • 账号状态从“正常”变为“异常”时发送异常通知
              <br />
              • 账号状态从“异常”恢复为“正常”时发送恢复通知
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
            <Button onClick={() => void handleSave()} disabled={!loaded || saving} size="sm">
              {saving ? "保存中..." : "保存账号告警设置"}
            </Button>
            <Button onClick={() => void reload()} disabled={saving} variant="outline" size="sm">
              <RefreshCw className="h-4 w-4 mr-1" />
              重新加载
            </Button>
            {success && (
              <span className="text-xs text-green-500 flex items-center gap-1">
                <CheckCircle className="h-3.5 w-3.5" />
                {success}
              </span>
            )}
          </div>
        </div>
      </section>

      <section className="border rounded-md p-4 sm:p-5 bg-muted/30">
        <h2 className="text-sm font-medium mb-2">前置条件</h2>
        <ul className="text-xs text-muted-foreground space-y-1 list-disc list-inside">
          <li>
            先在
            <Link to="/notify?tab=telegram" className="mx-1 text-primary hover:underline">
              通知中心 › Telegram
            </Link>
            配置 Bot 并启动轮询或 Webhook
          </li>
          <li>
            账号状态由生活事件监控更新，也可在
            <Link to="/account" className="mx-1 text-primary hover:underline">
              网盘账号
            </Link>
            页主动巡检
          </li>
        </ul>
      </section>
    </div>
  );
}
