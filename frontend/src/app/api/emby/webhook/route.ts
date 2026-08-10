import { NextRequest, NextResponse } from 'next/server';
import { handleEmbyWebhookEvent } from '@/lib/emby/notifier';
import type { EmbyWebhookEvent } from '@/lib/emby/types';

export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const event = body as EmbyWebhookEvent;

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
