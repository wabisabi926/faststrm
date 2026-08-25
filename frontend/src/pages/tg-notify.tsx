import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Bot, MessageSquare, CheckCircle, XCircle, RefreshCw, Play, Square, AlertCircle } from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import axiosInstance from "@/lib/axios";
import { logger } from "@/lib/logger";

interface TelegramConfig {
  botToken?: string;
  chatId?: string;
  webhookUrl?: string;
  enabled?: boolean;
  autoPolling?: boolean;
  proxyUrl?: string;
}

interface BotInfo {
  id: number;
  first_name: string;
  username: string;
}

interface WebhookInfo {
  url: string;
  pending_update_count: number;
  last_error_message?: string;
}

function maskToken(token: string): string {
  if (!token) return "";
  if (token.length <= 8) return "***";
  const sep = token.indexOf(":");
  if (sep === -1) return "***";
  const idPart = token.slice(0, sep);
  const secretPart = token.slice(sep + 1);
  return `${idPart}:${"*".repeat(Math.max(secretPart.length - 4, 0))}${secretPart.slice(-4)}`;
}

export default function TelegramNotifyPage() {
  const [config, setConfig] = useState<TelegramConfig>({});
  const [botInfo, setBotInfo] = useState<BotInfo | null>(null);
  const [webhookInfo, setWebhookInfo] = useState<WebhookInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [pollingStatus, setPollingStatus] = useState<{ polling: boolean; message: string } | null>(null);
  const [originalTokenPlain, setOriginalTokenPlain] = useState<string>("");
  const [tokenModified, setTokenModified] = useState<boolean>(false);

  useEffect(() => {
    void loadBotInfo();
    void checkPollingStatus();
  }, []);

  const loadBotInfo = async () => {
    try {
      void (async () => {
        try {
          const settingsResp = await axiosInstance.get("/api/settings");
          const telegram = settingsResp.data?.telegram;
          if (telegram?.botToken) {
            setOriginalTokenPlain(telegram.botToken || "");
            setTokenModified(false);
            setConfig({
              botToken: maskToken(telegram.botToken || ""),
              chatId: telegram.chatId || "",
              webhookUrl: telegram.webhookUrl || "",
              enabled: telegram.enabled !== false,
              autoPolling: telegram.autoPolling !== false,
              proxyUrl: telegram.proxyUrl || "",
            });
          }
        } catch (e) {
          logger.error("加载本地 Telegram 配置失败:", e);
        }
      })();

      const botResp = await axiosInstance.get("/api/notify/bot");
      if (botResp.data.configured) {
        if (botResp.data.bot) setBotInfo(botResp.data.bot.result ?? botResp.data.bot);
        if (botResp.data.webhook) setWebhookInfo(botResp.data.webhook.result ?? botResp.data.webhook);
      }
    } catch (error) {
      logger.error("加载 Bot 信息失败:", error);
    }
  };

  const handleSave = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const effectiveBotToken = tokenModified ? config.botToken : originalTokenPlain;

      const response = await axiosInstance.post("/api/notify/bot", {
        botToken: effectiveBotToken,
        chatId: config.chatId,
        webhookUrl: config.webhookUrl,
        enabled: config.enabled !== false,
        autoPolling: config.autoPolling !== false,
        proxyUrl: config.proxyUrl,
      });

      if (response.data.success) {
        setSuccess("配置保存成功！");
        const savedToken = tokenModified && config.botToken ? config.botToken : originalTokenPlain;
        setOriginalTokenPlain(savedToken);
        setTokenModified(false);
        setConfig({
          botToken: maskToken(savedToken || ""),
          chatId: response.data.chatId || "",
          webhookUrl: response.data.webhook?.result?.url || "",
          enabled: config.enabled !== false,
          autoPolling: config.autoPolling !== false,
          proxyUrl: config.proxyUrl || "",
        });
        await loadBotInfo();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string; details?: string } }; message?: string };
      const errorMessage = axiosError.response?.data?.error || axiosError.message || "配置失败";
      const errorDetails = axiosError.response?.data?.details || "";
      setError(errorDetails ? `${errorMessage}: ${errorDetails}` : errorMessage);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!confirm("确定要删除 Telegram 机器人配置吗？")) return;
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      await axiosInstance.delete("/api/notify/bot");
      setSuccess("配置已删除！");
      setBotInfo(null);
      setWebhookInfo(null);
      setConfig({});
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "删除配置失败");
    } finally {
      setLoading(false);
    }
  };

  const checkPollingStatus = async () => {
    try {
      const response = await axiosInstance.get("/api/notify/polling");
      setPollingStatus(response.data);
    } catch (error) {
      logger.error("检查轮询状态失败:", error);
    }
  };

  const startPolling = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      const response = await axiosInstance.post("/api/notify/polling");
      if (response.data.success) {
        setSuccess("轮询已启动！");
        await checkPollingStatus();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "启动轮询失败");
    } finally {
      setLoading(false);
    }
  };

  const stopPolling = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      const response = await axiosInstance.delete("/api/notify/polling");
      if (response.data.success) {
        setSuccess("轮询已停止！");
        await checkPollingStatus();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "停止轮询失败");
    } finally {
      setLoading(false);
    }
  };

  const testBot = async () => {
    if (!config.chatId) {
      setError("请先设置 Chat ID");
      return;
    }
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      await axiosInstance.post("/api/notify/send", {
        message: "🤖 Fast Strm 测试消息！",
        type: "info",
      });
      setSuccess("测试消息发送成功！");
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "测试消息发送失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="mx-auto max-w-2xl space-y-5 min-w-0">
      <div className="break-words">
        <h1 className="text-2xl font-semibold">Telegram 通知</h1>
        <p className="text-sm text-muted-foreground mt-0.5 break-words">
          配置 Bot、启动轮询，即可在 Telegram 接收通知
        </p>
      </div>

      {error && (
        <Alert variant="destructive" className="py-2">
          <XCircle className="h-4 w-4" />
          <AlertDescription className="text-sm">{error}</AlertDescription>
        </Alert>
      )}

      {success && (
        <Alert className="py-2">
          <CheckCircle className="h-4 w-4" />
          <AlertDescription className="text-sm">{success}</AlertDescription>
        </Alert>
      )}

      {/* 机器人配置 */}
      <section className="border rounded-md p-3 sm:p-5 space-y-4">
        <div className="flex items-center gap-2">
          <Bot className="h-5 w-5" />
          <h2 className="text-base font-medium">机器人配置</h2>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5 min-w-0">
            <Label htmlFor="botToken">Bot Token</Label>
            <Input
              id="botToken"
              type="password"
              placeholder="123456789:ABCdef..."
              value={config.botToken || ""}
              onChange={(e) => {
                setConfig({ ...config, botToken: e.target.value });
                setTokenModified(true);
              }}
            />
            <p className="text-xs text-muted-foreground">从 @BotFather 获取</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="chatId">Chat ID</Label>
            <Input
              id="chatId"
              placeholder="你的聊天 ID"
              value={config.chatId || ""}
              onChange={(e) => setConfig({ ...config, chatId: e.target.value })}
            />
            <p className="text-xs text-muted-foreground">向 Bot 发消息后查看日志获取</p>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="webhookUrl">Webhook URL <span className="text-xs text-muted-foreground">（可选，家用留空）</span></Label>
          <Input
            id="webhookUrl"
            placeholder="https://<你的公网域名>/api/notify/webhook"
            value={config.webhookUrl || ""}
            onChange={(e) => setConfig({ ...config, webhookUrl: e.target.value })}
          />
          <p className="text-xs text-muted-foreground">留空使用轮询模式（5秒延迟），填写则使用 Webhook（毫秒级）</p>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="proxyUrl">代理 URL <span className="text-xs text-muted-foreground">（可选）</span></Label>
          <Input
            id="proxyUrl"
            placeholder="socks5://127.0.0.1:7890 或 http://127.0.0.1:7890"
            value={config.proxyUrl || ""}
            onChange={(e) => setConfig({ ...config, proxyUrl: e.target.value })}
          />
          <p className="text-xs text-muted-foreground">支持 HTTP/HTTPS/SOCKS5 协议，国内访问 Telegram 时填写</p>
        </div>

        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
          <div className="flex flex-wrap gap-x-6 gap-y-3">
            <div className="flex items-center space-x-2">
              <Checkbox
                id="enabled"
                checked={config.enabled !== false}
                onCheckedChange={(checked) => setConfig({ ...config, enabled: checked === true })}
              />
              <label htmlFor="enabled" className="text-sm font-medium leading-none cursor-pointer">
                启用通知
              </label>
            </div>
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center space-x-2">
                <Checkbox
                  id="autoPolling"
                  checked={config.autoPolling !== false}
                  onCheckedChange={(checked) => setConfig({ ...config, autoPolling: checked === true })}
                />
                <label htmlFor="autoPolling" className="text-sm font-medium leading-none cursor-pointer">
                  启动时自动轮询
                </label>
              </div>
              <p className="text-xs text-muted-foreground pl-6">
                勾选后，下次启动服务时自动开轮询，无需公网也能接收 Bot 命令和按钮回复
              </p>
            </div>
          </div>
          <div className="flex gap-2 shrink-0 flex-wrap">
            {botInfo && (
              <Button variant="outline" onClick={handleDelete} disabled={loading} size="sm">
                删除
              </Button>
            )}
            <Button onClick={handleSave} disabled={loading || !config.botToken} size="sm">
              {loading ? "保存中..." : "保存配置"}
            </Button>
          </div>
        </div>

      </section>

      {/* 机器人状态 + 测试 */}
      {botInfo ? (
        <section className="border rounded-md p-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Badge variant="outline" className="bg-muted text-muted-foreground">
                <CheckCircle className="h-3 w-3 mr-1" />
                已连接
              </Badge>
              <span className="text-sm font-medium">@{botInfo.username}</span>
              <span className="text-xs text-muted-foreground">{botInfo.first_name}</span>
            </div>
            <Button onClick={testBot} disabled={loading} size="sm" variant="outline">
              <MessageSquare className="h-3.5 w-3.5 mr-1" />
              发送测试
            </Button>
          </div>
        </section>
      ) : config.botToken ? (
        <section className="border rounded-md p-4 text-center">
          <AlertCircle className="h-6 w-6 mx-auto text-muted-foreground mb-1" />
          <p className="text-sm text-muted-foreground">保存配置后显示 Bot 状态</p>
        </section>
      ) : null}

      {/* 轮询控制 */}
      <section className="border rounded-md p-4 space-y-3">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
          <div className="flex items-center gap-2 min-w-0">
            <RefreshCw className="h-5 w-5 shrink-0" />
            <h2 className="text-base font-medium">轮询控制</h2>
            {pollingStatus && (
              <Badge variant={pollingStatus.polling ? "default" : "outline"} className="ml-1">
                {pollingStatus.polling ? "轮询中" : "Webhook"}
              </Badge>
            )}
          </div>
          <div className="flex flex-wrap gap-1.5 shrink-0">
            <Button
              onClick={startPolling}
              disabled={loading || pollingStatus?.polling === true}
              size="sm"
              variant="outline"
            >
              <Play className="h-3.5 w-3.5 mr-1" />
              启动
            </Button>
            <Button
              onClick={stopPolling}
              disabled={loading || pollingStatus?.polling === false}
              size="sm"
              variant="outline"
            >
              <Square className="h-3.5 w-3.5 mr-1" />
              停止
            </Button>
            <Button onClick={checkPollingStatus} disabled={loading} size="sm" variant="outline">
              <RefreshCw className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
        {pollingStatus?.message && (
          <p className="text-xs text-muted-foreground">{pollingStatus.message}</p>
        )}
      </section>

      {/* Webhook 信息 */}
      {webhookInfo?.url && (
        <section className="border rounded-md p-4">
          <h3 className="text-sm font-medium mb-2">Webhook 状态</h3>
          <div className="space-y-1 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">URL</span>
              <span className="font-mono text-xs">{webhookInfo.url}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">待处理</span>
              <Badge variant={webhookInfo.pending_update_count > 0 ? "destructive" : "outline"} className="text-xs">
                {webhookInfo.pending_update_count}
              </Badge>
            </div>
            {webhookInfo.last_error_message && (
              <p className="text-xs text-red-500">错误：{webhookInfo.last_error_message}</p>
            )}
          </div>
        </section>
      )}

      <Alert className="py-2.5 bg-muted/40">
        <AlertCircle className="h-4 w-4 text-muted-foreground shrink-0 mt-0.5" />
        <AlertDescription className="text-xs leading-relaxed space-y-1">
          <p className="font-medium text-foreground/90">两种接消息模式：</p>
          <p>
            · <span className="font-medium">轮询（家用推荐）</span>：WebhookURL 留空即可，每 5 秒拉取一次，无需公网 IP/域名。
          </p>
          <p>
            · <span className="font-medium">Webhook（服务器推荐）</span>：填入公网 HTTPS 地址，毫秒级延迟，需公网域名。
          </p>
          <p className="text-muted-foreground">两种模式互斥，NAS/内网用户直接用轮询。</p>
        </AlertDescription>
      </Alert>
    </div>
  );
}
