// DownloadProgressPage 历史日志加载测试
// 覆盖 v1.2.7 修复：任务历史「查看日志」显示「历史记录不存在」的 bug
//   - 后端 id 为 int64、URL executionId 为字符串，需 String() 比较
//   - 走独立的 GET /api/taskHistory/:executionId/logs 接口拉日志

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import DownloadProgressPage from "../log-detail";

// hoisted 供 vi.mock 工厂引用（避免 hoisting 循环依赖）
const hoisted = vi.hoisted(() => ({
  executionId: "123" as string | undefined,
}));

vi.mock("react-router-dom", () => ({
  useParams: () => ({ taskId: "task-1" }),
  useSearchParams: () => [
    new URLSearchParams(hoisted.executionId ? `executionId=${hoisted.executionId}` : ""),
  ],
}));

vi.mock("@/lib/axios", () => {
  const get = vi.fn();
  const post = vi.fn();
  const axiosInstance = {
    get,
    post,
    interceptors: { request: { use: vi.fn() }, response: { use: vi.fn() } },
    defaults: {},
  };
  return {
    axios: {
      isAxiosError: (e: unknown): e is { response?: { data: unknown } } & Error =>
        e instanceof Error && "response" in e,
    },
    axiosInstance,
    // log-detail.tsx 通过 `import axiosInstance from "@/lib/axios"` 默认导入
    default: axiosInstance,
    setToken: vi.fn(),
    clearToken: vi.fn(),
    setUsername: vi.fn(),
    getUsername: vi.fn(),
    clearUsername: vi.fn(),
  };
});

let axiosGetMock: any;

beforeEach(async () => {
  const axiosMod = await import("@/lib/axios");
  axiosGetMock = axiosMod.axiosInstance.get;
  hoisted.executionId = "123";
});

describe("DownloadProgressPage 历史日志加载", () => {
  it("数字 id 与字符串 executionId 匹配，走 logs 接口并解析日志", async () => {
    axiosGetMock.mockImplementation(async (url: string) => {
      if (url === "/api/taskHistory") {
        return { data: { items: [{ id: 123, taskId: "task-1", status: "completed" }] } };
      }
      if (url === "/api/taskHistory/123/logs") {
        return { data: { logs: ['{"filePath":"/a.mp4","done":true}', "not-valid-json"] } };
      }
      throw new Error("unexpected url: " + url);
    });

    render(<DownloadProgressPage />);

    // 连接状态 = 历史记录（说明 id 匹配成功，而非「历史记录不存在」）
    await waitFor(() => {
      expect(screen.getAllByText("历史记录").length).toBeGreaterThan(0);
    });

    // 任务状态 = 已完成（status completed 正确映射）
    expect(screen.getAllByText("已完成").length).toBeGreaterThan(0);

    // 有效日志行被解析并渲染
    expect(screen.getByText("/a.mp4")).toBeInTheDocument();

    // 非法 JSON 行被跳过，不渲染
    expect(screen.queryByText("not-valid-json")).not.toBeInTheDocument();
  });

  it("找不到 execution 时显示历史记录不存在", async () => {
    hoisted.executionId = "999";
    axiosGetMock.mockImplementation(async (url: string) => {
      if (url === "/api/taskHistory") {
        return { data: { items: [] } };
      }
      if (url === "/api/taskHistory/999/logs") {
        return { data: { logs: [] } };
      }
      throw new Error("unexpected url: " + url);
    });

    render(<DownloadProgressPage />);

    await waitFor(() => {
      expect(screen.getByText("历史记录不存在")).toBeInTheDocument();
    });
    expect(screen.getByText("不存在")).toBeInTheDocument();
  });

  it("加载失败时显示加载历史记录失败", async () => {
    axiosGetMock.mockImplementation(async () => {
      throw new Error("network error");
    });

    render(<DownloadProgressPage />);

    await waitFor(() => {
      expect(screen.getByText("加载历史记录失败")).toBeInTheDocument();
    });
  });
});
