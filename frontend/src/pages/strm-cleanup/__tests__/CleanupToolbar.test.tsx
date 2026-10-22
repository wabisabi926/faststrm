// CleanupToolbar 回归测试：保留“清理全部”和“补生成全部”两个主操作。
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CleanupToolbar } from "../CleanupToolbar";

vi.mock("sonner", () => {
  const toast = vi.fn() as unknown as { success: typeof vi.fn; error: typeof vi.fn };
  (toast as unknown as { success: ReturnType<typeof vi.fn> }).success = vi.fn();
  (toast as unknown as { error: ReturnType<typeof vi.fn> }).error = vi.fn();
  return { toast };
});

beforeEach(() => {
  cleanup();
});

describe("CleanupToolbar", () => {
  it("totalMissing=0 时补生成按钮禁用，清理按钮可点", () => {
    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={0}
        executing={false}
        onDeleteAll={vi.fn()}
        onRegenerate={vi.fn()}
      />
    );

    expect(screen.getByRole("button", { name: /清理全部失效/ })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: /补生成全部漏项/ })).toBeDisabled();
  });

  it("清理全部需要二次确认", async () => {
    const user = userEvent.setup();
    const onDeleteAll = vi.fn();
    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={0}
        executing={false}
        onDeleteAll={onDeleteAll}
        onRegenerate={vi.fn()}
      />
    );

    await user.click(screen.getByRole("button", { name: /清理全部失效/ }));
    expect(screen.getByText(/一键清理全部 113 个失效 STRM/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认清理" }));
    expect(onDeleteAll).toHaveBeenCalledTimes(1);
  });

  it("补生成需要二次确认", async () => {
    const user = userEvent.setup();
    const onRegenerate = vi.fn();
    render(
      <CleanupToolbar
        totalStale={0}
        totalMissing={7}
        executing={false}
        onDeleteAll={vi.fn()}
        onRegenerate={onRegenerate}
      />
    );

    await user.click(screen.getByRole("button", { name: /补生成全部漏项/ }));
    expect(screen.getByText(/确认补生成 7 个缺失 STRM/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认生成" }));
    expect(onRegenerate).toHaveBeenCalledTimes(1);
  });

  it("executing=true 时两个操作按钮都禁用", () => {
    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={5}
        executing={true}
        onDeleteAll={vi.fn()}
        onRegenerate={vi.fn()}
      />
    );

    expect(screen.getByRole("button", { name: /删除中/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /生成中/ })).toBeDisabled();
  });
});
