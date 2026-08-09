import { NextRequest, NextResponse } from "next/server";
import {
  listLifeEventLogs,
  deleteLifeEventLogs,
  cleanupLifeEventLogs,
} from "@/lib/lifeEventLogManager";

export async function GET(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const account = searchParams.get("account") || undefined;
    const eventType = searchParams.get("eventType") as
      | "create"
      | "delete"
      | "move"
      | "rename"
      | "folder-sync"
      | undefined;
    const successParam = searchParams.get("success");
    const success =
      successParam === "true"
        ? true
        : successParam === "false"
        ? false
        : undefined;
    const since = searchParams.get("since")
      ? parseInt(searchParams.get("since")!, 10)
      : undefined;
    const until = searchParams.get("until")
      ? parseInt(searchParams.get("until")!, 10)
      : undefined;
    const limit = searchParams.get("limit")
      ? parseInt(searchParams.get("limit")!, 10)
      : undefined;

    const entries = listLifeEventLogs({
      account,
      eventType,
      success,
      since,
      until,
      limit,
    });

    return NextResponse.json({ total: entries.length, items: entries });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return NextResponse.json({ error: message }, { status: 500 });
  }
}

export async function DELETE(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const id = searchParams.get("id") || undefined;
    const action = searchParams.get("action");

    if (action === "cleanup") {
      const removed = cleanupLifeEventLogs();
      return NextResponse.json({ success: true, removed });
    }

    if (action === "clear") {
      deleteLifeEventLogs();
      return NextResponse.json({ success: true });
    }

    if (id) {
      deleteLifeEventLogs(id);
      return NextResponse.json({ success: true });
    }

    return NextResponse.json(
      { error: "id or action (clear/cleanup) is required" },
      { status: 400 }
    );
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return NextResponse.json({ error: message }, { status: 500 });
  }
}
