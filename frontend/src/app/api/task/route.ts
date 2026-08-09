import { readTasks, saveTasks, readSettings, writeSettings } from "@/lib/serverUtils";
import { downloadTasks } from "@/lib/downloadTaskManager";
import { NextRequest, NextResponse } from "next/server";
import { exec } from "child_process";
import {
  initTaskScheduler,
  registerTaskSchedule,
  unregisterTaskSchedule,
  computeNextRun,
  TaskSchedule,
} from "@/lib/taskScheduler";

initTaskScheduler();

export async function GET() {
  const tasks = readTasks();
  const runningTaskIds = new Set(Object.keys(downloadTasks));

  const tasksWithStatus = tasks.map((task: any) => {
    const base = {
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

function updateMediaMountPathFor302(taskData: { enable302?: boolean; strmPrefix?: string; account?: string }) {
  if (!taskData.enable302 || !taskData.strmPrefix) return;

  const fullPath = (taskData.strmPrefix || "").replace(/\/+$/, "");
  const settings = readSettings();
  const mediaMountPath: string[] = Array.isArray(settings.mediaMountPath)
    ? (settings.mediaMountPath as string[])
    : [];

  if (!mediaMountPath.includes(fullPath)) {
    mediaMountPath.push(fullPath);
    settings.mediaMountPath = mediaMountPath;
    writeSettings(settings);
    exec("nginx -s reload", (err) => {
      if (err) console.error("nginx reload failed:", err);
      else console.log("nginx reloaded for mediaMountPath update");
    });
  }
}

export async function POST(req: NextRequest) {
  const body = await req.json();
  const tasks = readTasks();

  const newTask = { id: Date.now().toString(), ...body };
  tasks.push(newTask);
  saveTasks(tasks);
  updateMediaMountPathFor302(newTask);

  registerTaskSchedule(newTask.id);

  return NextResponse.json(newTask, { status: 201 });
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
  updateMediaMountPathFor302(tasks[index]);

  registerTaskSchedule(id);

  return NextResponse.json(tasks[index]);
}

export async function DELETE(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const id = searchParams.get("id");

  if (!id) {
    return NextResponse.json({ error: "Task ID required" }, { status: 400 });
  }

  const tasks = readTasks();
  const filtered = tasks.filter((t: { id: string }) => t.id !== id);
  if (filtered.length === tasks.length) {
    return NextResponse.json({ error: "Task not found" }, { status: 404 });
  }

  saveTasks(filtered);
  unregisterTaskSchedule(id);

  return NextResponse.json({ success: true });
}
