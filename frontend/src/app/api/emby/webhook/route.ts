import { NextRequest, NextResponse } from 'next/server';
import { handleEmbyWebhookEvent } from '@/lib/emby/notifier';
import type { EmbyWebhookEvent } from '@/lib/emby/types';

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: NextRequest) {
  try {
    let event: EmbyWebhookEvent;

    const contentType = request.headers.get('content-type') || '';

    if (contentType.includes('application/json')) {
      // Emby 配置了 "Send as JSON"
      event = (await request.json()) as EmbyWebhookEvent;
    } else {
      // Emby 默认 form-data 格式：data=<JSON 字符串>
      const formData = await request.formData();
      const rawData = formData.get('data');
      if (typeof rawData === 'string') {
        event = JSON.parse(rawData) as EmbyWebhookEvent;
      } else {
        // 最后兜底：尝试当 JSON 解析
        event = (await request.json()) as EmbyWebhookEvent;
      }
    }

    // 异步处理，不阻塞响应
    handleEmbyWebhookEvent(event).catch(err => {
      console.error('[Emby] 处理 Webhook 事件失败:', err);
    });

    return NextResponse.json({ ok: true });
  } catch (error) {
    console.error('[Emby] Webhook 处理错误:', error);
    return NextResponse.json({ error: 'Bad request' }, { status: 400 });
  }
}
