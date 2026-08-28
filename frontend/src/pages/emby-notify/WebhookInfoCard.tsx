// Webhook 配置指引区块：URL 展示 + 复制 + 事件类型提示。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { Button } from "@/components/ui/button";
import { Copy, Check, Play } from "lucide-react";

export interface WebhookInfoCardProps {
  webhookUrl: string;
  copied: boolean;
  onCopy: () => void;
}

export function WebhookInfoCard({
  webhookUrl,
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
          <h4 className="font-medium">步骤 1：打开 Emby Webhook 设置</h4>
          <p className="text-sm text-muted-foreground">
            在 Emby 后台：<code className="bg-muted px-1 rounded">设置 → 通知 → Webhook</code>
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
        </div>

        <div className="space-y-2">
          <h4 className="font-medium">步骤 3：勾选事件类型</h4>
          <p className="text-sm text-muted-foreground">
            在 Emby Webhook 设置中勾选以下事件：
          </p>
          <div className="flex flex-wrap gap-2">
            <code className="bg-muted text-muted-foreground border border-border px-2 py-1 rounded text-xs">
              媒体入库 (library.new)
            </code>
            <code className="bg-muted text-muted-foreground border border-border px-2 py-1 rounded text-xs">
              媒体删除 (library.deleted)
            </code>
            <code className="bg-muted text-muted-foreground border border-border px-2 py-1 rounded text-xs">
              播放开始 (playback.start)
            </code>
            <code className="bg-muted text-muted-foreground border border-border px-2 py-1 rounded text-xs">
              播放暂停 (playback.pause)
            </code>
            <code className="bg-muted text-muted-foreground border border-border px-2 py-1 rounded text-xs">
              播放结束 (playback.stop)
            </code>
          </div>
        </div>

        <div className="rounded-lg bg-muted/50 p-3">
          <p className="text-sm text-muted-foreground">
            <strong>提示：</strong>配置完成后，Emby 的媒体变动（入库/删除）和播放状态将实时推送到你的 Telegram。
          </p>
        </div>
      </div>
    </section>
  );
}
