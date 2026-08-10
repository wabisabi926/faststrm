import { readTasks, saveTasks } from "@/lib/serverUtils";
import { downloadTasks } from "@/lib/downloadTaskManager";
import { NextRequest, NextResponse } from "next/server";
import * as fs from "fs";
import * as path from "path";
import { syncMediaMountPaths } from "@/lib/mediaMountSync";
import {
  initTaskScheduler,
  registerTaskSchedule,
  unregisterTaskSchedule,
  computeNextRun,
  TaskSchedule,
} from "@/lib/taskScheduler";

type TaskRow = {
  id: string;
  account?: string;
  accountType?: string;
  originPath?: string;
  targetPath?: string;
  schedule?: TaskSchedule;
  _computedNextRunAt?: number | null;
};

initTaskScheduler();

export async function GET() {
  const tasks = readTasks() as TaskRow[];
  const runningTaskIds = new Set(Object.keys(downloadTasks));

  const tasksWithStatus = tasks.map((task: TaskRow) => {
    const base: TaskRow & { status: string } = {
      ...task,
      status: runningTaskIds.has(task.id) ? "processing" : "pending",
    };

    if (task.schedule?.enabled) {
      const computedNext = computeNextRun(task.schedule as TaskSchedule);
      base._computedNextRunAt = computedNext ?? task.schedule.nextRunAt ?? null;
    }

    return base;
  });

  return NextResponse.json(tasksWithStatus);
}

export async function POST(req: NextRequest) {
  const body = await req.json();
  const tasks = readTasks();

  const newTask = { id: Date.now().toString(), ...body };
  tasks.push(newTask);
  saveTasks(tasks);
  const syncResult = await syncMediaMountPaths();

  registerTaskSchedule(newTask.id);

  return NextResponse.json({ ...newTask, _mediaMountSync: syncResult }, { status: 201 });
}

export async function PUT(req: NextRequest) {
  const body = await req.json();
  const { id, ...updateData } = body;

  const tasks = readTasks();
  const index = tasks.findIndex((t: { id: string }) => t.id === id);
  if (index === -1) {
    return NextResponse.json({ error: "Task not found" }, { status: 404 });
  }

  tasks[index] = { ...tasks[index], ...updateData };
  saveTasks(tasks);
  const syncResult = await syncMediaMountPaths();

  registerTaskSchedule(id);

  return NextResponse.json({ ...tasks[index], _mediaMountSync: syncResult });
}

export async function DELETE(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const id = searchParams.get("id");
  const cleanStrm = searchParams.get("cleanStrm") === "true";

  if (!id) {
    return NextResponse.json({ error: "Task ID required" }, { status: 400 });
  }

  const tasks = readTasks();
  const taskIndex = tasks.findIndex((t: { id: string }) => t.id === id);
  if (taskIndex === -1) {
    return NextResponse.json({ error: "Task not found" }, { status: 404 });
  }

  const task = tasks[taskIndex];

  // 如果要求清理 STRM 目录
  if (cleanStrm && task.targetPath) {
    try {
      const localPath = path.join(process.cwd(), "../data", task.targetPath);
      if (fs.existsSync(localPath)) {
        const stat = fs.statSync(localPath);
        if (stat.isDirectory()) {
          fs.rmSync(localPath, { recursive: true, force: true });
          console.log(`[TaskDelete] 已清理 STRM 目录: ${localPath}`);
        }
      }
    } catch (err) {
      console.error("[TaskDelete] 清理 STRM 目录失败:", err);
    }
  }

  const filtered = tasks.filter((t: { id: string }) => t.id !== id);
  saveTasks(filtered);
  unregisterTaskSchedule(id);

  const syncResult = await syncMediaMountPaths();

  return NextResponse.json({ success: true, _mediaMountSync: syncResult });
}
