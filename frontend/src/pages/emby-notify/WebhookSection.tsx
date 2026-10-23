import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Check, Copy, KeyRound, Link2, Play } from "lucide-react";
import type { EmbySettings } from "./types";

export interface WebhookSectionProps {
  settings: EmbySettings;
  updateSetting: <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => void;
  webhookUrl: string;
  copied: boolean;
  onCopy: () => void;
  onSaveAuth: (auth: string) => Promise<boolean>;
}

export function WebhookSection({
  settings,
  updateSetting,
  webhookUrl,
  copied,
  onCopy,
  onSaveAuth,
}: WebhookSectionProps) {
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const authConfigured = Boolean((settings.webhookAuth || "").trim());

  const generateAuth = () => {
    const token =
      typeof crypto !== "undefined" && "randomUUID" in crypto
        ? crypto.randomUUID().replace(/-/g, "")
        : Math.random().toString(36).slice(2) + Date.now().toString(36);
    updateSetting("webhookAuth", token);
    setDirty(true);
    void saveAuth(token);
  };

  const saveAuth = async (auth: string) => {
    setSaving(true);
    try {
      const ok = await onSaveAuth(auth);
      if (ok) setDirty(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-center gap-2">
        <Play className="h-5 w-5" />
        <h2 className="text-base font-medium">Emby Webhook</h2>
      </div>
      <p className="text-xs text-muted-foreground -mt-3">
        生成鉴权密钥后，把下方 URL 填到 Emby Webhooks 插件即可。
      </p>

      <div className="space-y-2">
        <Label htmlFor="embyWebhookAuth">
          Webhook 鉴权密钥 <span className="text-xs text-muted-foreground">（必填）</span>
        </Label>
        <div className="flex flex-col sm:flex-row gap-2">
          <Input
            id="embyWebhookAuth"
            type="password"
            placeholder="必填；未设置时 Emby Webhook 会被 401 拒绝"
            value={settings.webhookAuth || ""}
            onChange={(e) => {
              updateSetting("webhookAuth", e.target.value);
              setDirty(true);
            }}
            autoComplete="off"
          />
          <Button type="button" variant="outline" className="shrink-0" onClick={generateAuth} disabled={saving}>
            <KeyRound className="h-4 w-4 mr-1" />
            生成并保存
          </Button>
          {dirty && (
            <Button
              type="button"
              variant="secondary"
              className="shrink-0"
              onClick={() => void saveAuth(settings.webhookAuth || "")}
              disabled={saving}
            >
              保存密钥
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          生成后会立即保存；手工修改后需点「保存密钥」。
        </p>
      </div>

      <div className="space-y-2">
        <Label>Webhook URL</Label>
        <div className="flex flex-col sm:flex-row sm:items-center gap-2">
          <code className="flex-1 bg-muted px-3 py-2 rounded text-sm font-mono truncate break-all">
            {webhookUrl}
          </code>
          <Button
            variant="outline"
            size="sm"
            onClick={onCopy}
            disabled={!authConfigured || dirty}
            className="shrink-0"
          >
            {copied ? (
              <>
                <Check className="h-4 w-4 mr-1" />
                已复制
              </>
            ) : (
              <>
                <Copy className="h-4 w-4 mr-1" />
                复制
              </>
            )}
          </Button>
        </div>
        {!authConfigured && (
          <p className="text-xs text-amber-600 dark:text-amber-500">
            请先设置并保存鉴权密钥，否则 Emby 调用该 URL 会被 401 拒绝。
          </p>
        )}
        {dirty && authConfigured && (
          <p className="text-xs text-amber-600 dark:text-amber-500">
            密钥尚未保存，保存后复制按钮才可用。
          </p>
        )}
        <p className="text-xs text-muted-foreground">
          URL 由当前浏览器地址生成。请确认 Emby 服务器能访问该地址；如果 FastStrm 通过反代/外网访问，请把主机名替换成 Emby 可达的地址。
        </p>
      </div>

      <details className="group border-t pt-4">
        <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-medium">
          <Link2 className="h-4 w-4 text-muted-foreground" />
          在 Emby 中配置 Webhook
          <span className="text-xs font-normal text-muted-foreground group-open:hidden">（3 步）</span>
        </summary>
        <div className="space-y-4 pt-4">
          <div className="space-y-1">
            <h4 className="font-medium text-sm">1. 安装并打开 Webhooks 插件</h4>
            <p className="text-sm text-muted-foreground">
              在 Emby 后台：<code className="bg-muted px-1 rounded">设置 → 高级 → 插件</code> 安装「Webhooks」；
              新版本也可能在 <code className="bg-muted px-1 rounded">设置 → 通知</code> 中直接提供。
            </p>
          </div>
          <div className="space-y-1">
            <h4 className="font-medium text-sm">2. 添加上方 Webhook URL</h4>
            <p className="text-sm text-muted-foreground">
              复制上方 URL，在 Webhooks 插件中新增目标并粘贴。
            </p>
          </div>
          <div className="space-y-2">
            <h4 className="font-medium text-sm">3. 勾选事件类型</h4>
            <div className="flex flex-wrap gap-2">
              {[
                "媒体入库 (library.new)",
                "媒体删除 (library.deleted)",
                "播放开始 (playback.start)",
                "播放暂停 (playback.pause)",
                "播放结束 (playback.stop)",
              ].map((label) => (
                <span
                  key={label}
                  className="inline-flex items-center text-xs font-medium text-foreground bg-background border border-border rounded-md px-2.5 py-1 shadow-sm"
                >
                  {label}
                </span>
              ))}
            </div>
          </div>
        </div>
      </details>
    </section>
  );
}
