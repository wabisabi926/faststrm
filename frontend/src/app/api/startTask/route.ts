import { NextRequest, NextResponse } from "next/server";
import { executeTask } from "@/lib/taskExecutor";
import { initTaskScheduler } from "@/lib/taskScheduler";

initTaskScheduler();

export async function POST(req: NextRequest) {
  try {
    const body = await req.json();
    const taskId: string = body.id;

    if (!taskId) {
      return NextResponse.json(
        { message: "Missing task id" },
        { status: 400 }
      );
    }

    const result = await executeTask(taskId, { trigger: "manual" });

    if (result.blocked) {
      const status =
        result.reason === "account_running" || result.reason === "task_running"
          ? 409
          : 423;
      return NextResponse.json(
        { message: result.message, reason: result.reason },
        { status }
      );
    }

    if (!result.success) {
      const status = result.reason === "not_found" ? 404 : 500;
      return NextResponse.json(
        { message: result.message, reason: result.reason, error: result.error },
        { status }
      );
    }

    return NextResponse.json({
      message: result.message,
      taskId: result.taskId,
      missingLocallyCount: result.missingLocallyCount,
      extraLocallyCount: result.extraLocallyCount,
      willDeleteExtraFiles: result.willDeleteExtraFiles,
    });
  } catch (error) {
    console.error("[startTask POST] fatal:", error);
    return NextResponse.json(
      {
        message: "Internal server error",
        error: error instanceof Error ? error.message : String(error),
      },
      { status: 500 }
    );
  }
}
