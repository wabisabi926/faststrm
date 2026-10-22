// Webhook 配置指引区块：URL 展示 + 复制 + 事件类型提示。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { Button } from "@/components/ui/button";
import { Copy, Check, Play } from "lucide-react";

export interface WebhookInfoCardProps {
  webhookUrl: string;
  authConfigured: boolean;
  telegramConfigured: boolean;
  copied: boolean;
  onCopy: () => void;
}

export function WebhookInfoCard({
  webhookUrl,
  authConfigured,
  telegramConfigured,
  copied,
  onCopy,
}: WebhookInfoCardProps) {
  return (
    <section className="border rounded-md p-3 sm:p-5 space-y-5">
      <div className="flex items-center gap-2">
        <Play className="h-5 w-5" />
        <h2 className="text-base font-medium">Webhook 配置指引</h2>
      </div>
      <p className="text-xs text-muted-foreground mt-1">
        在 Emby 后台配置 Webhook 以接收通知
      </p>
      <div className="space-y-3">
        <div className="space-y-2">
          <h4 className="font-medium">步骤 1：安装并打开 Emby Webhooks</h4>
          <p className="text-sm text-muted-foreground">
            在 Emby 后台：<code className="bg-muted px-1 rounded">设置 → 高级 → 插件</code> 安装「Webhooks」；
            新版本也可能在 <code className="bg-muted px-1 rounded">设置 → 通知</code> 中直接提供。
          </p>
        </div>

        <div className="space-y-2">
          <h4 className="font-medium">步骤 2：添加 Webhook URL</h4>
          <div className="flex flex-col sm:flex-row sm:items-center gap-2">
            <code className="flex-1 bg-muted px-3 py-2 rounded text-sm font-mono truncate break-all">
              {webhookUrl}
            </code>
            <Button
              variant="outline"
              size="sm"
              onClick={onCopy}
              disabled={!authConfigured}
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
              请先在上方填写 Webhook 鉴权密钥，否则该 URL 会被 401 拒绝。
            </p>
          )}
          <p className="text-xs text-muted-foreground">
            URL 由当前浏览器地址生成。请确认 Emby 服务器能访问该地址；如果 FastStrm 通过反代/外网访问，请把主机名替换成 Emby 可达的地址。
          </p>
        </div>

        <div className="space-y-2">
          <h4 className="font-medium">步骤 3：勾选事件类型</h4>
          <p className="text-sm text-muted-foreground">
            在 Emby Webhook 设置中勾选以下事件：
          </p>
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

        <p className="text-xs text-muted-foreground pt-1">
          {telegramConfigured
            ? "配置完成后，Emby 的媒体变动（入库/删除）和播放状态会推送到 Telegram。"
            : "通知通过 Telegram 发送：请先在「通知中心 → Telegram」启用机器人，否则 Emby 通知会被静默丢弃。"}
        </p>
      </div>
    </section>
  );
}
