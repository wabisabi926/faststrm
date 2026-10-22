import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Bot, MessageSquare, CheckCircle, XCircle, RefreshCw, Play, Square, AlertCircle, Users, UserPlus, X, Loader2, ChevronDown } from "lucide-react";
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
  hasWebhookSecret?: boolean;
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

export default function TelegramNotifyPage({ embedded = false }: { embedded?: boolean }) {
  const [config, setConfig] = useState<TelegramConfig>({});
  const [botInfo, setBotInfo] = useState<BotInfo | null>(null);
  const [webhookInfo, setWebhookInfo] = useState<WebhookInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [pollingStatus, setPollingStatus] = useState<{ polling: boolean; message: string } | null>(null);
  const [originalTokenPlain, setOriginalTokenPlain] = useState<string>("");
  const [tokenModified, setTokenModified] = useState<boolean>(false);
  // webhook secret 输入值：空串=保持现状（后端沿用旧值）；"-"=清除；其他=设置新值
  const [webhookSecretInput, setWebhookSecretInput] = useState<string>("");
  // 显式清空代理（空字符串本身无法区分“未提供”和“清空”）
  const [clearProxy, setClearProxy] = useState<boolean>(false);
  // 授权用户白名单：空列表表示允许所有 Telegram 用户
  const [allowedUsers, setAllowedUsers] = useState<Array<{ id: number }>>([]);
  const [newUserID, setNewUserID] = useState<string>("");
  const [usersLoading, setUsersLoading] = useState<boolean>(false);

  useEffect(() => {
    void loadBotInfo();
    void checkPollingStatus();
    void loadUsers();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
        setConfig((prev) => ({ ...prev, hasWebhookSecret: botResp.data.hasWebhookSecret === true }));
      }
    } catch (error) {
      logger.error("加载 Bot 信息失败:", error);
    }
  };

  const handleSave = async () => {
    try {
      if (config.webhookUrl?.trim() && !config.hasWebhookSecret && !webhookSecretInput.trim()) {
        setError("启用 Webhook 前必须设置 Webhook 密钥，否则回调会被拒绝");
        return;
      }
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
        clearProxy,
        // 留空不传=后端沿用旧值；"-"=清除；其他=设置新值
        ...(webhookSecretInput.trim() !== "" ? { webhookSecretToken: webhookSecretInput.trim() } : {}),
      });

      if (response.data.success) {
        setSuccess("配置保存成功！");
        setWebhookSecretInput("");
        const savedToken = tokenModified && config.botToken ? config.botToken : originalTokenPlain;
        setOriginalTokenPlain(savedToken);
        setTokenModified(false);
        setClearProxy(false);
        setConfig({
          botToken: maskToken(savedToken || ""),
          chatId: response.data.chatId || "",
          webhookUrl: response.data.webhook?.result?.url || "",
          enabled: config.enabled !== false,
          autoPolling: config.autoPolling !== false,
          proxyUrl: config.proxyUrl || "",
        });
        await loadBotInfo();
        await checkPollingStatus();
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

  const loadUsers = async () => {
    try {
      setUsersLoading(true);
      const response = await axiosInstance.get("/api/notify/users");
      const list = Array.isArray(response.data?.users) ? response.data.users : [];
      setAllowedUsers(
        list
          .map((u: { id?: number | string }) => Number(u?.id))
          .filter((id: number) => Number.isFinite(id) && id > 0)
          .map((id: number) => ({ id }))
      );
    } catch (error) {
      logger.error("加载授权用户失败:", error);
    } finally {
      setUsersLoading(false);
    }
  };

  const addAllowedUser = async () => {
    const raw = newUserID.trim();
    if (!raw) {
      setError("请输入 Telegram 用户 ID");
      return;
    }
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      await axiosInstance.post("/api/notify/users", { userId: raw });
      setNewUserID("");
      setSuccess(`已添加授权用户 ${raw}`);
      await loadUsers();
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "添加授权用户失败");
    } finally {
      setLoading(false);
    }
  };

  const removeAllowedUser = async (id: number) => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      await axiosInstance.delete(`/api/notify/users?userId=${encodeURIComponent(String(id))}`);
      setSuccess(`已移除授权用户 ${id}`);
      await loadUsers();
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "移除授权用户失败");
    } finally {
      setLoading(false);
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

  // 通知会话 ID 能否直接当授权用户：只有个人聊天（纯正数）才行。
  // 群/频道的 chat_id 是负数（-100…），它不是任何人的 User ID，
  // 塞进授权列表会让「列表非空 + 全员不匹配」→ Bot 直接全锁死。
  const chatIdAsUserID = (() => {
    const raw = (config.chatId || "").trim();
    if (!/^\d+$/.test(raw)) return null;
    const n = Number(raw);
    return Number.isSafeInteger(n) && n > 0 ? raw : null;
  })();
  const canAdoptChatID = chatIdAsUserID !== null && allowedUsers.length === 0;

  const adoptChatIDAsUser = async () => {
    if (!chatIdAsUserID) return;
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);
      await axiosInstance.post("/api/notify/users", { userId: chatIdAsUserID });
      setSuccess(`已将 ${chatIdAsUserID} 添加为授权用户`);
      await loadUsers();
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "添加授权用户失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className={`${embedded ? "" : "mx-auto max-w-5xl"} space-y-5 min-w-0`}>
      {!embedded && (
        <div className="break-words">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">Telegram 通知</h1>
          <p className="text-sm text-muted-foreground mt-0.5 break-words">
            配置 Bot、启动轮询，即可在 Telegram 接收通知
          </p>
        </div>
      )}

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
        <div className="grid gap-4 grid-cols-1 sm:grid-cols-2">
          <div className="space-y-1.5 min-w-0">
            <Label htmlFor="botToken">机器人令牌</Label>
            <Input
              id="botToken"
              type="password"
              placeholder="从 @BotFather 获取（形如 123456789:ABCdefGHI...）"
              value={config.botToken || ""}
              onChange={(e) => {
                setConfig({ ...config, botToken: e.target.value });
                setTokenModified(true);
              }}
            />
            <p className="text-xs text-muted-foreground">从 @BotFather 获取</p>
          </div>
          <div className="space-y-1.5 min-w-0">
            <Label htmlFor="chatId">聊天 ID（chat_id）</Label>
            <Input
              id="chatId"
              placeholder="个人填你自己的 User ID；群填 -100 开头的群 ID"
              value={config.chatId || ""}
              onChange={(e) => setConfig({ ...config, chatId: e.target.value })}
            />
            <p className="text-xs text-muted-foreground">
              通知发到这个会话。个人聊天时与你的用户 ID 相同；群/频道是 -100 开头的负数。
              向 Bot 发一条消息即可看到自己的用户 ID。
            </p>
          </div>
        </div>

        <details className="group space-y-3 border-t pt-4">
          <summary className="flex cursor-pointer list-none items-center gap-2">
            <span className="text-sm font-medium">高级连接设置</span>
            <span className="text-xs text-muted-foreground">
              {config.webhookUrl ? "Webhook 模式" : "轮询模式（默认）"}
              {config.proxyUrl ? " · 已配置代理" : ""}
            </span>
            <ChevronDown className="ml-auto h-4 w-4 text-muted-foreground transition-transform group-open:rotate-180" />
          </summary>

          <div className="space-y-3 pt-2">
            <div className="space-y-1.5 min-w-0">
              <Label htmlFor="webhookUrl">Webhook URL <span className="text-xs text-muted-foreground">（可选，家用留空）</span></Label>
              <Input
                id="webhookUrl"
                placeholder="https://<你的公网域名>/api/notify/webhook"
                value={config.webhookUrl || ""}
                onChange={(e) => {
                  const url = e.target.value;
                  setConfig({
                    ...config,
                    webhookUrl: url,
                    // Webhook 与自动轮询互斥：填写 URL 时关闭自动轮询
                    autoPolling: url.trim() ? false : config.autoPolling,
                  });
                }}
              />
              <p className="text-xs text-muted-foreground">
                留空使用轮询模式（5秒延迟），填写则使用 Webhook（毫秒级）。两者只能二选一。
              </p>
            </div>

            {config.webhookUrl && (
              <div className="space-y-1.5 min-w-0">
                <Label htmlFor="webhookSecret">
                  Webhook 密钥{" "}
                  {config.hasWebhookSecret && (
                    <span className="text-xs text-green-600">（已设置）</span>
                  )}
                  <span className="text-xs text-muted-foreground">（Webhook 模式必填，防伪造请求）</span>
                </Label>
                <Input
                  id="webhookSecret"
                  placeholder={config.hasWebhookSecret ? "已设置，留空保持不变；输入 - 清除" : "输入密钥（任意 1-256 字符随机串）"}
                  value={webhookSecretInput}
                  onChange={(e) => setWebhookSecretInput(e.target.value)}
                  autoComplete="off"
                />
                <p className="text-xs text-muted-foreground">
                  设置后把同一密钥填到反向代理/网关的 <code>X-Telegram-Bot-Api-Secret-Token</code> 头；留空保持现状
                </p>
              </div>
            )}

            <div className="space-y-1.5 min-w-0">
              <Label htmlFor="proxyUrl">代理 URL <span className="text-xs text-muted-foreground">（可选，国内必填）</span></Label>
              <div className="flex gap-1">
                <Input
                  id="proxyUrl"
                  placeholder="socks5://127.0.0.1:7890 或 http://127.0.0.1:7890"
                  value={config.proxyUrl || ""}
                  onChange={(e) => {
                    setClearProxy(false);
                    setConfig({ ...config, proxyUrl: e.target.value });
                  }}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={() => {
                    setClearProxy(true);
                    setConfig({ ...config, proxyUrl: "" });
                  }}
                >
                  清除
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">支持 HTTP/HTTPS/SOCKS5 协议，国内访问 Telegram 时填写；点「清除」保存后生效</p>
            </div>
          </div>
        </details>

        <div className="flex flex-col gap-3">
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
            <div className="flex items-center space-x-2">
              <Checkbox
                id="autoPolling"
                checked={config.autoPolling !== false}
                onCheckedChange={(checked) =>
                  setConfig({
                    ...config,
                    autoPolling: checked === true,
                    // 开启自动轮询时清空 Webhook URL，避免重启后被轮询覆盖
                    webhookUrl: checked === true ? "" : config.webhookUrl,
                  })
                }
              />
              <label htmlFor="autoPolling" className="text-sm font-medium leading-none cursor-pointer">
                启动时自动轮询
              </label>
            </div>
          </div>
          <div className="flex gap-2 flex-wrap sm:justify-end">
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
            <div className="flex items-center gap-2 min-w-0">
              <Badge variant="outline" className="bg-muted text-muted-foreground">
                <CheckCircle className="h-3 w-3 mr-1" />
                已连接
              </Badge>
              <span className="text-sm font-medium truncate">@{botInfo.username}</span>
              <span className="text-xs text-muted-foreground truncate">{botInfo.first_name}</span>
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

      {/* 授权用户白名单 */}
      <section className="border rounded-md p-4 space-y-3">
        <div className="flex items-center gap-2">
          <Users className="h-5 w-5" />
          <h2 className="text-base font-medium">授权用户</h2>
          <Badge variant="outline" className="text-[10px]">
            {allowedUsers.length === 0 ? "允许所有" : `${allowedUsers.length} 人`}
          </Badge>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            onClick={() => void loadUsers()}
            disabled={usersLoading}
          >
            {usersLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          限制哪些 Telegram 用户可以使用 Bot。<b>列表为空时允许所有用户</b>，任何搜到 Bot 的人都能执行命令；
          建议添加自己的 User ID 后立即生效。用户 ID 获取方式：向 Bot 发送任意消息，Bot 会回复你的 User ID。
        </p>
        {canAdoptChatID && (
          <div className="flex flex-col gap-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-amber-700 dark:text-amber-500">
              检测到通知会话 ID <span className="font-mono">{chatIdAsUserID}</span>
              （个人聊天），可直接作为授权用户——避免 Bot 对所有人开放。
            </p>
            <Button
              size="sm"
              variant="outline"
              className="shrink-0"
              onClick={() => void adoptChatIDAsUser()}
              disabled={loading}
            >
              <UserPlus className="mr-1 h-3.5 w-3.5" />
              添加 {chatIdAsUserID}
            </Button>
          </div>
        )}
        <div className="flex flex-col sm:flex-row gap-2">
          <Input
            placeholder="Telegram User ID（如 123456789）"
            value={newUserID}
            onChange={(e) => setNewUserID(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void addAllowedUser();
              }
            }}
            disabled={loading}
          />
          <Button onClick={() => void addAllowedUser()} disabled={loading || !newUserID.trim()}>
            <UserPlus className="mr-1 h-4 w-4" />
            添加
          </Button>
        </div>
        {usersLoading ? (
          <p className="text-xs text-muted-foreground">加载授权用户...</p>
        ) : allowedUsers.length === 0 ? (
          <p className="text-xs text-amber-600">当前未配置任何用户，Bot 对所有能发起会话的用户开放。</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {allowedUsers.map((u) => (
              <Badge key={u.id} variant="secondary" className="gap-1 font-mono text-xs">
                {u.id}
                <button
                  type="button"
                  className="rounded-full opacity-70 hover:opacity-100"
                  title="移除授权"
                  onClick={() => void removeAllowedUser(u.id)}
                  disabled={loading}
                >
                  <X className="h-3 w-3" />
                </button>
              </Badge>
            ))}
          </div>
        )}
      </section>

      {/* 轮询控制 */}
      <section className="border rounded-md p-4 space-y-3">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
          <div className="flex items-center gap-2 min-w-0">
            <RefreshCw className="h-5 w-5 shrink-0" />
            <h2 className="text-base font-medium">轮询控制</h2>
            {pollingStatus && (
              <Badge variant={pollingStatus.polling ? "default" : "outline"} className="ml-1">
                {pollingStatus.polling
                  ? "轮询中"
                  : pollingStatus.message?.includes("Webhook")
                    ? "Webhook"
                    : "未运行"}
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
    </div>
  );
}
