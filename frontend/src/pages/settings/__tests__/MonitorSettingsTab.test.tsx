// MonitorSettingsTab 纯展示组件测试
// 覆盖 v1.1.1 P1 核心场景：启用/禁用开关、账号列表渲染、监控状态标签、
// 保存并启动按钮禁用条件、事件类型复选框、路径映射增删。

import { describe, it, expect, vi } from "vitest";
import { render, screen, within, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MonitorSettingsTab } from "../MonitorSettingsTab";
import type { MonitorSettingsTabProps } from "../MonitorSettingsTab";
import type { PathMapping, DisplayMonitorState } from "../types";

function buildProps(overrides: Partial<MonitorSettingsTabProps> = {}): MonitorSettingsTabProps {
  const noop = vi.fn();
  return {
    monitorEnabled: true,
    setMonitorEnabled: noop,
    accounts: ["user-a", "user-b"],
    selectedAccounts: ["user-a"],
    toggleAccount: noop,
    pollInterval: 10,
    setPollInterval: noop,
    eventTypes: { create: true, remove: true, rename: false, move: false },
    setEventTypes: noop,
    removeEmptyDirs: true,
    setRemoveEmptyDirs: noop,
    minFileSizeMb: "",
    setMinFileSizeMb: noop,
    firstPullMode: "latest",
    setFirstPullMode: noop,
    moveMediaMode: "local_move",
    setMoveMediaMode: noop,
    pathMappings: [{ account: "user-a", cloudPath: "/电影", localPath: "/media/电影" }],
    setPathMappings: noop,
    newMappingAccount: "__all__",
    setNewMappingAccount: noop,
    newCloudPath: "",
    setNewCloudPath: noop,
    newLocalPath: "",
    setNewLocalPath: noop,
    openCloudPicker: noop,
    openLocalPicker: noop,
    openNewCloudPicker: noop,
    openNewLocalPicker: noop,
    addPathMapping: noop,
    removePathMapping: noop,
    verifying: false,
    verifyResult: null,
    handleVerify: vi.fn(),
    displayMonitorStates: [] as DisplayMonitorState,
    handleStopMonitor: vi.fn(),
    handleStartAccount: vi.fn(),
    handleRemoveFromMonitor: vi.fn(),
    saving: false,
    onSave: vi.fn(),
    handleStartMonitor: vi.fn(),
    ...overrides,
  };
}

describe("MonitorSettingsTab", () => {
  it("monitorEnabled=false 时配置区域置灰且不可交互", () => {
    const { container } = render(<MonitorSettingsTab {...buildProps({ monitorEnabled: false })} />);
    const wrapper = container.querySelector(".space-y-6 > section > .space-y-4");
    expect(wrapper).not.toBeNull();
    expect(wrapper!.className).toMatch(/opacity-50/);
    expect(wrapper!.className).toMatch(/pointer-events-none/);
  });

  it("monitorEnabled=true 时配置区域保持可交互", () => {
    const { container } = render(<MonitorSettingsTab {...buildProps({ monitorEnabled: true })} />);
    const wrapper = container.querySelector(".space-y-6 > section > .space-y-4");
    expect(wrapper!.className).not.toMatch(/opacity-50/);
  });

  it("accounts 为空时展示「暂无可用账号」提示", () => {
    render(<MonitorSettingsTab {...buildProps({ accounts: [], selectedAccounts: [] })} />);
    expect(screen.getByText(/暂无可用账号/)).toBeInTheDocument();
  });

  it("有账号时渲染每个账号的复选框和标签", () => {
    render(<MonitorSettingsTab {...buildProps({ accounts: ["alice", "bob"] })} />);
    const accountList = screen
      .getAllByText(/监控账号/)[0]
      .closest(".space-y-3")! as HTMLElement;
    expect(within(accountList).getByText("alice")).toBeInTheDocument();
    expect(within(accountList).getByText("bob")).toBeInTheDocument();
  });

  it("运行中账号渲染「运行中」标签 + 红色「停止」按钮", () => {
    const states: DisplayMonitorState = [
      { account: "alice", running: true, status: "running", eventsProcessed: 5, pending: false },
    ];
    render(<MonitorSettingsTab {...buildProps({ displayMonitorStates: states, accounts: ["alice"] })} />);
    const monitorStatusSection = screen.getByText(/监控状态/).closest(".space-y-3")! as HTMLElement;
    expect(within(monitorStatusSection).getByText("alice")).toBeInTheDocument();
    expect(screen.getByText("运行中")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "停止" })).toBeInTheDocument();
  });

  it("异常账号渲染「异常」标签 + 停止 + 从监控列表移除双按钮", () => {
    const states: DisplayMonitorState = [
      { account: "alice", running: true, status: "error", eventsProcessed: 0, lastError: "Cookie 失效" },
    ];
    render(<MonitorSettingsTab {...buildProps({ displayMonitorStates: states, accounts: ["alice"] })} />);
    expect(screen.getByText("异常")).toBeInTheDocument();
    expect(screen.getByText(/Cookie 失效/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "停止" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "从监控列表移除" })).toBeInTheDocument();
  });

  it("账号不存在渲染「账号不存在」标签 + 单个「从监控列表移除」", () => {
    const states: DisplayMonitorState = [
      { account: "ghost", running: false, status: "error", eventsProcessed: 0, lastError: "账号不存在" },
    ];
    render(<MonitorSettingsTab {...buildProps({ displayMonitorStates: states, accounts: ["alice"] })} />);
    expect(screen.getByText("账号不存在")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "从监控列表移除" }).length).toBe(1);
    expect(screen.queryByRole("button", { name: "停止" })).not.toBeInTheDocument();
  });

  it("pending 账号渲染「待保存」标签 + disabled 灰按钮", () => {
    const states: DisplayMonitorState = [
      { account: "alice", running: false, status: "pending", eventsProcessed: 0, pending: true },
    ];
    render(<MonitorSettingsTab {...buildProps({ displayMonitorStates: states, accounts: ["alice"] })} />);
    expect(screen.getByText("待保存")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "待保存" })).toBeDisabled();
  });

  it("保存并启动按钮：三项未齐备时禁用并显示对应提示，齐备后启用", () => {
    const { rerender } = render(
      <MonitorSettingsTab
        {...buildProps({ monitorEnabled: true, selectedAccounts: [], pathMappings: [] })}
      />
    );
    const startBtn = screen.getByRole("button", { name: "保存并启动监控" });
    expect(startBtn).toBeDisabled();
    expect(screen.getByText(/请至少选择一个监控账号/)).toBeInTheDocument();

    rerender(
      <MonitorSettingsTab
        {...buildProps({ monitorEnabled: true, selectedAccounts: ["alice"], pathMappings: [] })}
      />
    );
    expect(startBtn).toBeDisabled();
    expect(screen.getByText(/请至少配置一条路径映射/)).toBeInTheDocument();

    rerender(
      <MonitorSettingsTab
        {...buildProps({
          monitorEnabled: true,
          selectedAccounts: ["alice"],
          pathMappings: [{ cloudPath: "/", localPath: "/x" }],
        })}
      />
    );
    expect(startBtn).not.toBeDisabled();
  });

  it("事件类型复选框：点击 create 触发 setEventTypes 并正确 toggle", async () => {
    const user = userEvent.setup();
    const setEventTypes = vi.fn();
    render(
      <MonitorSettingsTab
        {...buildProps({
          eventTypes: { create: false, remove: false, rename: false, move: false },
          setEventTypes,
        })}
      />
    );
    const createLabel = screen.getByText("新建/上传（生成 STRM）");
    await user.click(createLabel);
    expect(setEventTypes).toHaveBeenCalled();
    const updater = setEventTypes.mock.calls[0][0];
    const result = typeof updater === "function"
      ? updater({ create: false, remove: false, rename: false, move: false })
      : updater;
    expect(result?.create).toBe(true);
  });

  it("轮询间隔 input change 触发 setPollInterval(parseInt)", () => {
    const setPollInterval = vi.fn();
    render(<MonitorSettingsTab {...buildProps({ pollInterval: 10, setPollInterval })} />);
    const inputs = screen.getAllByRole("spinbutton") as HTMLInputElement[];
    const pollInput = inputs[0]; // 轮询间隔是第一个 number input
    fireEvent.change(pollInput, { target: { value: "25" } });
    expect(setPollInterval).toHaveBeenCalledWith(25);
  });

  it("路径映射的删除按钮触发 removePathMapping(index)", async () => {
    const user = userEvent.setup();
    const removePathMapping = vi.fn();
    const mappings: PathMapping[] = [
      { account: "alice", cloudPath: "/a", localPath: "/la" },
      { account: "bob", cloudPath: "/b", localPath: "/lb" },
    ];
    render(<MonitorSettingsTab {...buildProps({ pathMappings: mappings, removePathMapping })} />);
    const deleteBtns = screen.getAllByRole("button", { name: "删除" });
    await user.click(deleteBtns[0]);
    expect(removePathMapping).toHaveBeenCalledWith(0);
  });
});

