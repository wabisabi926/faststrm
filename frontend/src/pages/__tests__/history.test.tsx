// 任务记录页统计卡片测试
// 覆盖：统计卡改用服务端 /api/taskHistory/stats 的全量口径（不再只统计已加载页），
// 以及分页游标使用 createdAt。

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

const hoisted = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock("react-router-dom", () => ({
  useSearchParams: () => [new URLSearchParams()],
  Navigate: () => null,
}));

vi.mock("@/lib/axios", () => ({
  default: { get: hoisted.get },
}));

import TaskHistoryPage from "../history";

function buildItem(id: number, createdAt: number) {
  return {
    id,
    taskId: `task-${id}`,
    status: "completed",
    startedAt: createdAt + 10,
    endedAt: createdAt + 1010,
    durationMs: 1000,
    summary: { totalFiles: 10, downloadedFiles: 9 },
    createdAt,
  };
}

describe("TaskHistoryPage 统计卡片", () => {
  beforeEach(() => {
    hoisted.get.mockReset();
  });

  it("使用服务端全量统计，而不是已加载页的条数", async () => {
    hoisted.get.mockImplementation((url: string) => {
      if (url.startsWith("/api/taskHistory/stats")) {
        return Promise.resolve({
          data: {
            total: 250,
            completed: 200,
            failed: 40,
            cancelled: 10,
            running: 0,
            filesDownloaded: 1800,
            filesTotal: 2000,
            avgDurationMs: 65000,
          },
        });
      }
      return Promise.resolve({ data: { items: [buildItem(1, 1000), buildItem(2, 900)] } });
    });

    render(<TaskHistoryPage />);

    expect(await screen.findByText("记录总数")).toBeInTheDocument();
    expect(screen.getByText("250")).toBeInTheDocument();
    expect(screen.getByText("200")).toBeInTheDocument();
    expect(screen.getByText("40")).toBeInTheDocument();
    expect(screen.getByText("1800 / 2000")).toBeInTheDocument();
    expect(screen.getByText(/统计口径：全部记录/)).toBeInTheDocument();
  });

  it("统计接口失败时退回已加载页口径并标注", async () => {
    hoisted.get.mockImplementation((url: string) => {
      if (url.startsWith("/api/taskHistory/stats")) {
        return Promise.reject(new Error("stats down"));
      }
      return Promise.resolve({ data: { items: [buildItem(1, 1000)] } });
    });

    render(<TaskHistoryPage />);

    expect(await screen.findByText("记录总数")).toBeInTheDocument();
    expect(screen.getByText(/统计口径：已加载记录/)).toBeInTheDocument();
  });
});
