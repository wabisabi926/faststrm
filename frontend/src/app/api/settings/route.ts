import { NextRequest, NextResponse } from "next/server";
import { readSettings, writeSettings, AppSettings } from "@/lib/serverUtils";
import { clearRateLimiters } from "@/lib/enqueueForAccount";
import { downloadTasks } from "@/lib/downloadTaskManager";
import { syncMediaMountPaths } from "@/lib/mediaMountSync";

export async function GET() {
  const settings = readSettings();
  return NextResponse.json(settings);
}

export async function PUT(req: NextRequest) {
  const body = (await req.json()) as AppSettings;
  if (!body || typeof body !== "object") {
    return NextResponse.json({ message: "invalid payload" }, { status: 400 });
  }

  const runningTasks = Object.keys(downloadTasks);
  if (runningTasks.length > 0) {
    return NextResponse.json(
      {
        message: "有任务正在执行中，无法保存设置。请等待任务完成后再试。",
        hasRunningTasks: true,
        runningTasks: runningTasks,
      },
      { status: 409 }
    );
  }

  writeSettings(body);
  clearRateLimiters();

  const mediaMountSync = await syncMediaMountPaths({ existingSettings: body });

  return NextResponse.json({ message: "ok", mediaMountSync });
}


