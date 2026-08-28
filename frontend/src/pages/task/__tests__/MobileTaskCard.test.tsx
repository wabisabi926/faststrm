// MobileTaskCard 业务测试
// 覆盖 v1.1.1 T8 核心场景：进度条渲染、状态徽章、按钮交互、布局。

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Task } from "../types";

// ---- 子组件 mock ----
vi.mock("../components/AddTaskDialog", () => ({
  AddTaskDialog: ({ trigger }: { trigger: React.ReactNode }) => <>{trigger}</>,
}));

vi.mock("../components/TaskScheduleDialog", () => ({
  TaskScheduleDialog: ({ trigger }: { trigger: React.ReactNode }) => <>{trigger}</>,
}));

import { MobileTaskCard, type MobileTaskCardProps } from "../MobileTaskCard";

function makeTask(overrides: Partial<Task> = {}): Task {
  const base: Task = {
    id: "task-12345678",
    accountType: "115",
    account: "alice",
    originPath: "/网盘/动漫",
    targetPath: "D:/Media/Anime",
    strmType: "emby",
    strmPrefix: "anime/",
    name: "动漫目录任务",
    status: "pending",
  };
  return { ...base, ...overrides };
}

function buildProps(overrides: Partial<MobileTaskCardProps> = {}): MobileTaskCardProps {
  const task = overrides.task ?? makeTask();
  return {
    task,
    nowTs: Date.now(),
    startingTasks: new Set(),
    accounts: [{ name: "alice", accountType: "115" }],
    accountsLoading: false,
    isAccountBusy: () => false,
    isTaskDisabled: () => false,
    getTaskDisplayStatus: (t) => ({ status: t.status, label: (t.status) }),
    startTask: vi.fn(),
    cancelTask: vi.fn(),
    goToLog: vi.fn(),
    fetchTasks: vi.fn(),
    setDeleteDialogOpen: vi.fn(),
    ...overrides,
  };
}

describe("MobileTaskCard", () => {
  // ---- 基础渲染 ----

  it("渲染任务 id（前 8 位）", () => {
    render(<MobileTaskCard {...buildProps({ task: makeTask({ id: "abcdefgh1234" }) })} />);
    expect(screen.getByText("abcdefgh")).toBeInTheDocument();
  });

  it("渲染账户类型 Badge 和账户名", () => {
    render(<MobileTaskCard {...buildProps({ task: makeTask({ account: "bob", accountType: "openlist" }) })} />);
    expect(screen.getByText("openlist")).toBeInTheDocument();
    expect(screen.getByText("bob")).toBeInTheDocument();
  });

  it("渲染远程/本地路径", () => {
    render(<MobileTaskCard {...buildProps({ task: makeTask({ originPath: "/a/b", targetPath: "X:/y/z" }) })} />);
    expect(screen.getByText("/a/b")).toBeInTheDocument();
    expect(screen.getByText("X:/y/z")).toBeInTheDocument();
  });

  // ---- T8 场景 1：处理中 → 进度条 ----

  describe("任务处理中", () => {
    it("scanning 阶段 → 显示扫描 Badge + 动画进度条", () => {
      const task = makeTask({
        status: "processing",
        runtime: { status: "running", stage: "scanning", totalFiles: 100, downloadedFiles: 0 },
      });
      render(<MobileTaskCard {...buildProps({ task })} />);
      expect(screen.getByText("扫描目录")).toBeInTheDocument();
      expect(document.querySelector('[class*="bg-cyan-500"]')).toBeInTheDocument();
    });

    it("有 totalFiles + downloadedFiles → 真实进度条 + 百分比文本", () => {
      const task = makeTask({
        status: "processing",
        runtime: {
          status: "running", stage: "generating",
          startedAt: Date.now() - 60_000, totalFiles: 40, downloadedFiles: 24,
        },
      });
      render(<MobileTaskCard {...buildProps({ task, nowTs: Date.now() })} />);
      expect(screen.getByText("24 / 40 个文件 (60%)")).toBeInTheDocument();
    });

    it("只有 startedAt → 骨架进度条", () => {
      const task = makeTask({
        status: "processing",
        runtime: { status: "running", stage: "starting", startedAt: Date.now() - 10_000 },
      });
      render(<MobileTaskCard {...buildProps({ task, nowTs: Date.now() })} />);
      expect(document.querySelector('[class*="bg-blue-400"]')).toBeInTheDocument();
    });

    it("运行中显示耗时", () => {
      const startedAt = Date.now() - 125_000; // 2分05秒
      const task = makeTask({
        status: "processing",
        runtime: { status: "running", stage: "scanning", startedAt },
      });
      render(<MobileTaskCard {...buildProps({ task, nowTs: Date.now() })} />);
      expect(screen.getByText(/2分05秒/)).toBeInTheDocument();
    });
  });

  // ---- T8 场景 2：成功 ----

  describe("任务成功", () => {
    it("显示成功状态 + 绿色进度条 + 查看日志按钮", () => {
      const task = makeTask({
        status: "success",
        runtime: { totalFiles: 50, downloadedFiles: 50, startedAt: Date.now() - 60_000, endedAt: Date.now() },
      });
      render(<MobileTaskCard {...buildProps({ task })} />);
      expect(screen.getByText("50 / 50 个文件 (100%)")).toBeInTheDocument();
      expect(document.querySelector('[class*="bg-green-500"]')).toBeInTheDocument();
      expect(screen.getByTitle("查看日志")).toBeInTheDocument();
    });
  });

  // ---- T8 场景 3：失败 → 错误信息 + 重试 ----

  describe("任务失败", () => {
    it("失败进度条变红", () => {
      const task = makeTask({
        status: "failed", error: "下载中断",
        runtime: { totalFiles: 10, downloadedFiles: 3 },
      });
      render(<MobileTaskCard {...buildProps({ task })} />);
      expect(document.querySelector('[class*="bg-red-500"]')).toBeInTheDocument();
    });

    it("播放按钮始终存在（作为重试入口）", async () => {
      const startTask = vi.fn();
      const task = makeTask({ status: "failed", error: "失败" });
      render(<MobileTaskCard {...buildProps({ task, startTask })} />);
      const user = userEvent.setup();
      await user.click(screen.getByTitle("开始任务"));
      expect(startTask).toHaveBeenCalledWith(task.id);
    });
  });

  // ---- 账户忙 / 定时 / 按钮回调 / isStarting ----

  it("账户忙时显示 ● 标记", () => {
    const task = makeTask({ account: "alice" });
    render(<MobileTaskCard {...buildProps({ task, isAccountBusy: () => true })} />);
    expect(screen.getByText("●")).toBeInTheDocument();
  });

  describe("按钮回调", () => {
    let fns: {
      startTask: ReturnType<typeof vi.fn>;
      cancelTask: ReturnType<typeof vi.fn>;
      goToLog: ReturnType<typeof vi.fn>;
      setDeleteDialogOpen: ReturnType<typeof vi.fn>;
    };
    beforeEach(() => {
      fns = {
        startTask: vi.fn(), cancelTask: vi.fn(),
        goToLog: vi.fn(), setDeleteDialogOpen: vi.fn(),
      };
    });

    it("取消按钮 → cancelTask", async () => {
      render(<MobileTaskCard {...buildProps({ task: makeTask({ id: "abc" }), ...fns })} />);
      const user = userEvent.setup();
      await user.click(screen.getByTitle("取消任务"));
      expect(fns.cancelTask).toHaveBeenCalledWith("abc");
    });

    it("日志按钮 → goToLog", async () => {
      render(<MobileTaskCard {...buildProps({ task: makeTask({ id: "xyz" }), ...fns })} />);
      const user = userEvent.setup();
      await user.click(screen.getByTitle("查看日志"));
      expect(fns.goToLog).toHaveBeenCalledWith("xyz");
    });

    it("删除按钮 → setDeleteDialogOpen", async () => {
      render(<MobileTaskCard {...buildProps({ task: makeTask({ id: "del-me" }), ...fns })} />);
      const user = userEvent.setup();
      await user.click(screen.getByTitle("删除任务"));
      expect(fns.setDeleteDialogOpen).toHaveBeenCalledWith("del-me");
    });
  });

  it("schedule.enabled 时显示定时信息", () => {
    const tomorrow = new Date();
    tomorrow.setDate(tomorrow.getDate() + 1);
    const task = makeTask({
      schedule: { enabled: true, mode: "daily", time: "02:00", intervalMinutes: 0, nextRunAt: tomorrow.getTime() },
      _computedNextRunAt: tomorrow.getTime(),
    });
    render(<MobileTaskCard {...buildProps({ task })} />);
    expect(screen.getByText(/每天02:00/)).toBeInTheDocument();
  });

  it("schedule 禁用时不显示定时区块", () => {
    const task = makeTask({
      schedule: { enabled: false, mode: "daily", time: "02:00", intervalMinutes: 0 },
    });
    render(<MobileTaskCard {...buildProps({ task })} />);
    expect(screen.queryByText("定时：")).not.toBeInTheDocument();
  });

  it("isStarting → Loader2 转圈", () => {
    render(<MobileTaskCard {...buildProps({ task: makeTask({ id: "loading" }), startingTasks: new Set(["loading"]) })} />);
    expect(document.querySelector(".animate-spin")).toBeInTheDocument();
  });

  it("isDisabled → 开始按钮 disabled", () => {
    render(<MobileTaskCard {...buildProps({ task: makeTask(), isTaskDisabled: () => true })} />);
    expect(screen.getByTitle("开始任务")).toBeDisabled();
  });
});
