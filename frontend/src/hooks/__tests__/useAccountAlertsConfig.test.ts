// useAccountAlertsConfig 共享逻辑测试
// 覆盖：读取归一化、按字段局部保存（不覆盖另一页维护的开关）、错误回传、巡检间隔收敛。

import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";

// hoisted 供 vi.mock 工厂引用（避免 hoisting 循环依赖）
const hoisted = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}));

const mockGet = hoisted.get;
const mockPost = hoisted.post;

vi.mock("@/lib/axios", () => ({
  default: { get: hoisted.get, post: hoisted.post },
}));

import {
  clampInspectIntervalHours,
  useAccountAlertsConfig,
} from "../useAccountAlertsConfig";

describe("clampInspectIntervalHours", () => {
  it("把非法值收敛到默认值、越界值收敛到边界", () => {
    expect(clampInspectIntervalHours(Number.NaN)).toBe(6);
    expect(clampInspectIntervalHours(0)).toBe(1);
    expect(clampInspectIntervalHours(999)).toBe(168);
    expect(clampInspectIntervalHours(6.4)).toBe(6);
  });
});

describe("useAccountAlertsConfig", () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockPost.mockReset();
  });

  it("读取后归一化配置并标记 loaded", async () => {
    mockGet.mockResolvedValue({
      data: { enabled: true, onError: false, onRecover: true, cookieInspect: { enabled: false, intervalHours: 12 } },
    });

    const { result } = renderHook(() => useAccountAlertsConfig());
    await act(async () => {
      await result.current.reload();
    });

    expect(result.current.loaded).toBe(true);
    expect(result.current.config).toEqual({
      enabled: true,
      onError: false,
      onRecover: true,
      cookieInspect: { enabled: false, intervalHours: 12 },
    });
  });

  it("保存时只提交 patch 中的字段，避免覆盖另一页维护的配置", async () => {
    mockGet.mockResolvedValue({ data: { enabled: true, onError: true, onRecover: true } });
    mockPost.mockResolvedValue({ data: { success: true } });

    const { result } = renderHook(() => useAccountAlertsConfig());
    await act(async () => {
      await result.current.reload();
    });

    await act(async () => {
      await result.current.save({ cookieInspect: { enabled: false, intervalHours: 24 } });
    });

    expect(mockPost).toHaveBeenCalledWith("/api/notify/alerts", {
      cookieInspect: { enabled: false, intervalHours: 24 },
    });
    // 本地快照同步为刚提交的值，其他字段保持不变
    expect(result.current.config.cookieInspect).toEqual({ enabled: false, intervalHours: 24 });
    expect(result.current.config.enabled).toBe(true);
    expect(result.current.success).toBeTruthy();
  });

  it("保存失败时回传后端错误文案且不修改本地快照", async () => {
    mockGet.mockResolvedValue({ data: {} });
    mockPost.mockRejectedValue({ response: { data: { error: "保存失败：配置非法" } } });

    const { result } = renderHook(() => useAccountAlertsConfig());
    await act(async () => {
      await result.current.reload();
    });

    let ok = true;
    await act(async () => {
      ok = await result.current.save({ enabled: true });
    });

    expect(ok).toBe(false);
    expect(result.current.error).toBe("保存失败：配置非法");
    expect(result.current.config.enabled).toBe(false);
  });

  it("读取失败时给出默认错误提示", async () => {
    mockGet.mockRejectedValue(new Error("network down"));

    const { result } = renderHook(() => useAccountAlertsConfig());
    await act(async () => {
      await result.current.reload();
    });

    await waitFor(() => expect(result.current.error).toBe("加载账号告警配置失败"));
    expect(result.current.loaded).toBe(false);
  });
});
